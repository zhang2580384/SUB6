package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	openAIWSMaxNamedStreams   = 32
	openAIWSMaxSessions       = 65 // named streams plus isolated legacy threads
	openAIWSMaxActiveStreams  = 16
	openAIWSMaxPendingCreates = 8
)

type openAIWSStreamFrame struct {
	kind    coderws.MessageType
	body    []byte
	arrived time.Time
	session *openAIWSLogicalSession
}

type openAIWSLogicalSession struct {
	router             *openAIWSStreamRouter
	streamID           string
	key                string
	identity           string
	ctx                context.Context
	cancel             context.CancelFunc
	frames             chan openAIWSStreamFrame
	controls           chan openAIWSStreamFrame
	closed             chan struct{}
	drainOnce          sync.Once // marks the lane draining, without waiting for I/O
	closeOnce          sync.Once
	closeErr           error
	busy               bool // protected by router.mu
	wroteError         atomic.Bool
	started            bool
	responseID         string // protected by router.mu
	turnAccounted      bool   // protected by router.mu
	bareErrorAccounted bool
	terminalWritten    bool // protected by router.mu
	cancelled          bool // protected by router.mu
	lastArrival        atomic.Pointer[time.Time]
}

type openAIWSStreamRouter struct {
	h               *OpenAIGatewayHandler
	template        *gin.Context
	conn            *coderws.Conn
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	writeMu         sync.Mutex
	sessions        map[string]*openAIWSLogicalSession
	named           map[string]bool
	lastDefault     *openAIWSLogicalSession
	lastNamed       map[string]*openAIWSLogicalSession
	pending         []openAIWSStreamFrame
	pendingBytes    int64
	controlBytes    int64
	maxPendingBytes int64
	active          int
	wg              sync.WaitGroup
	connectionID    string
	responseOwners  map[string]string

	transportClosing atomic.Bool // a sole legacy session has claimed the physical close
}

// The physical socket has one reader. Named lanes and legacy threads have
// separate logical connections, each running the unchanged admission/forwarding
// lifecycle. A legacy default lane remains sequential across thread switches.
func (h *OpenAIGatewayHandler) serveOpenAIWSStreams(c *gin.Context, conn *coderws.Conn) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	r := &openAIWSStreamRouter{h: h, template: c, conn: conn, ctx: ctx, cancel: cancel,
		sessions: make(map[string]*openAIWSLogicalSession), named: make(map[string]bool), lastNamed: make(map[string]*openAIWSLogicalSession), responseOwners: make(map[string]string),
		connectionID: uuid.NewString(), maxPendingBytes: service.ResolveOpenAIWSClientReadLimitBytes(h.cfg)}
	defer func() {
		cancel()
		r.mu.Lock()
		for _, session := range r.sessions {
			session.stop()
		}
		r.pending = nil
		r.pendingBytes = 0
		r.mu.Unlock()
		_ = conn.CloseNow()
		r.wg.Wait()
	}()
	// Preserve the close handshake, reader join and structured diagnostics of
	// the original ingress, including a client which upgrades but sends nothing.
	// Capture belongs to logical conversations, never this shared transport.
	firstTimeout := service.ResolveOpenAIWSClientFirstMessageTimeout(h.cfg)
	readCtx := requestcapture.WithoutSession(ctx)
	kind, body, arrived, err := service.ReadOpenAIWSClientMessageWithArrival(readCtx, conn, firstTimeout, coderws.StatusPolicyViolation, "missing first response.create message")
	if err != nil {
		h.logOpenAIWSFirstReadError(c, conn, ctx, err, firstTimeout)
		return
	}
	for {
		if kind != coderws.MessageText && kind != coderws.MessageBinary || !gjson.ValidBytes(body) {
			_ = conn.Close(coderws.StatusPolicyViolation, "invalid websocket JSON payload")
			return
		}
		// All first, subsequent and control frames pass here before stream selection or rewriting.
		if _, ambiguous := requestmodel.ConflictingField("application/json", body); ambiguous {
			middleware.MarkIngressRejected(c, middleware.IngressRejectConflictingRequestFields)
			_ = conn.Close(coderws.StatusPolicyViolation, "websocket request contains ambiguous fields")
			return
		}
		if err := r.route(kind, body, arrived); err != nil {
			_ = conn.Close(coderws.StatusPolicyViolation, err.Error())
			return
		}
		kind, body, arrived, err = service.ReadOpenAIWSClientMessageWithArrival(readCtx, conn, 0, coderws.StatusPolicyViolation, "")
		if err != nil {
			return
		}
	}
}

func (h *OpenAIGatewayHandler) logOpenAIWSFirstReadError(c *gin.Context, conn service.OpenAIWSDownstreamConn, ctx context.Context, err error, timeout time.Duration) {
	apiKey, _ := middleware.GetAPIKeyFromContext(c)
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	reqLog := requestLogger(c, "handler.openai_gateway.responses_ws", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", apiKey.ID), zap.Any("group_id", apiKey.GroupID), zap.Bool("openai_ws_mode", true))
	if errors.Is(context.Cause(ctx), service.ErrOpenAIWSIngressLeaseLost) {
		reqLog.Warn("openai.websocket_ingress_lease_lost_before_first_message", zap.Error(err))
		closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, "websocket ingress capacity lease lost; please reconnect")
		return
	}
	closeStatus, closeReason := summarizeWSCloseErrorForLog(err)
	var localClose *service.OpenAIWSClientCloseError
	closedByServer := errors.As(err, &localClose)
	if closedByServer {
		closeStatus = fmt.Sprintf("%d(%s)", int(localClose.StatusCode()), localClose.StatusCode().String())
		closeReason = localClose.Reason()
	}
	reason := "read_error"
	peerCloseStatus := coderws.CloseStatus(err)
	switch {
	case closedByServer && localClose.StatusCode() == coderws.StatusPolicyViolation && errors.Is(err, context.DeadlineExceeded):
		reason = "first_message_timeout"
	case errors.Is(err, io.EOF) || peerCloseStatus == coderws.StatusNormalClosure || peerCloseStatus == coderws.StatusGoingAway:
		reason = "peer_disconnected"
	case errors.Is(err, context.Canceled):
		reason = "request_canceled"
	}
	fields := []zap.Field{zap.Error(err), zap.String("client_ip", ip.GetClientIP(c)), zap.String("close_status", closeStatus), zap.String("close_reason", closeReason), zap.Duration("read_timeout", timeout), zap.String("reason", reason)}
	if reason == "peer_disconnected" || reason == "request_canceled" {
		reqLog.Info("openai.websocket_read_first_message_failed", fields...)
	} else {
		reqLog.Warn("openai.websocket_read_first_message_failed", fields...)
	}
	if !closedByServer && peerCloseStatus == -1 && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		closeOpenAIClientWS(conn, coderws.StatusPolicyViolation, "missing first response.create message")
	}
}

func openAIWSStreamID(body []byte) (string, error) {
	v := gjson.GetBytes(body, "stream_id")
	if !v.Exists() {
		if gjson.GetBytes(body, "stream_id").Type == gjson.Null && gjson.GetBytes(body, "stream_id").Raw != "" {
			return "", errors.New("invalid stream_id")
		}
		return "", nil
	}
	streamFields := 0
	gjson.ParseBytes(body).ForEach(func(key, value gjson.Result) bool {
		if key.Str == "stream_id" {
			streamFields++
		}
		return true
	})
	if streamFields != 1 {
		return "", errors.New("invalid stream_id")
	}
	if v.Type != gjson.String || len(v.Str) < 1 || len(v.Str) > 256 {
		return "", errors.New("invalid stream_id")
	}
	for _, ch := range v.Str {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.' {
			continue
		}
		return "", errors.New("invalid stream_id")
	}
	return v.Str, nil
}

func (r *openAIWSStreamRouter) route(kind coderws.MessageType, body []byte, arrived time.Time) error {
	if r.transportClosing.Load() {
		return nil
	}
	streamID, err := openAIWSStreamID(body)
	if err != nil {
		return r.writeError("", "invalid_stream_id", "stream_id must contain 1-256 letters, numbers, underscores, hyphens or periods")
	}
	if _, err := service.OpenAIWSGenerateMode(body); err != nil {
		return r.writeError(streamID, "invalid_generate", err.Error())
	}
	frameType := strings.TrimSpace(gjson.GetBytes(body, "type").String())
	if frameType == "" {
		frameType = "response.create" // preserve the existing legacy parser default
	}
	// Metadata must be inspected before any namespace/protocol transformation.
	apiKey, _ := middleware.GetAPIKeyFromContext(r.template)
	identity := service.ResolveCyberSessionIdentity(apiKey.ID, r.template, body)
	r.mu.Lock()
	if r.transportClosing.Load() { // the close may have started during frame validation
		r.mu.Unlock()
		return nil
	}
	base := "named:" + streamID
	last := r.lastNamed[streamID]
	if streamID == "" {
		base = "default"
		last = r.lastDefault
	}
	key := base
	if identity.Resolved() {
		key += ":" + identity.BlockKey
	} else if last != nil {
		key = last.key
	}
	// A static session header on a follow-up may omit the body thread. Keep
	// that frame in its current lane so the binding can inherit safely.
	if identity.Metadata.Source == service.OpenAIClientSessionIdentitySourceHeader && last != nil {
		key = last.key
	}
	previous := gjson.GetBytes(body, "previous_response_id").String()
	if previous == "" {
		previous = gjson.GetBytes(body, "response.previous_response_id").String()
	}
	if owner := r.responseOwners[previous]; previous != "" && owner != "" && owner != key {
		// An omitted body identity can inherit the actual response's legacy
		// lane, but an explicit change must never move tool results across it.
		ownerSession := r.sessions[owner]
		if ownerSession != nil && ownerSession.streamID == streamID && identity.Metadata.Source != service.OpenAIClientSessionIdentitySourceBody && identity.Metadata.Source != service.OpenAIClientSessionIdentitySourceHeaderBody {
			key = owner
		} else {
			r.mu.Unlock()
			return r.writeError(streamID, "previous_response_not_found", "This response belongs to a different logical conversation; replay the full context with previous_response_id omitted")
		}
	}
	session := r.sessions[key]
	if frameType != "response.create" {
		if identity.Metadata.Status == service.OpenAIClientSessionIdentityConflict || identity.Metadata.Status == service.OpenAIClientSessionIdentityInvalid {
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_session_identity_conflict", "Control event has invalid or conflicting session identity")
		}
		for _, candidate := range r.sessions {
			if candidate.streamID == streamID && candidate.busy {
				if identity.Resolved() && candidate.identity != "" && identity.BlockKey != candidate.identity && (identity.Metadata.Source == service.OpenAIClientSessionIdentitySourceBody || identity.Metadata.Source == service.OpenAIClientSessionIdentitySourceHeaderBody) {
					r.mu.Unlock()
					return r.writeError(streamID, "response_not_active", "This control event belongs to a different logical conversation")
				}
				session = candidate
				break
			}
		}
		if session == nil {
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_session_not_found", "Send response.create before a control event")
		}
		if frameType == "response.cancel" && (streamID != "" || len(r.sessions) > 1) {
			r.mu.Unlock()
			return r.cancelSession(session, gjson.GetBytes(body, "response_id").String())
		}
		if session.isStopped() {
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_session_draining", "This conversation is closing; retry after its terminal event")
		}
		// Control frames are never broadcast. A cancel can only affect its lane.
		if r.pendingBytes+r.controlBytes+int64(len(body)) > r.maxPendingBytes {
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_queue_full", "Too many pending WebSocket control events")
		}
		select {
		case session.controls <- openAIWSStreamFrame{kind: kind, body: body, session: session, arrived: arrived}:
			r.controlBytes += int64(len(body))
			r.mu.Unlock()
			return nil
		default:
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_queue_full", "Too many pending WebSocket control events")
		}
	}
	if session != nil && session.isStopped() {
		r.mu.Unlock()
		return r.writeError(streamID, "websocket_session_draining", "This conversation is closing; retry after its terminal event")
	}
	if len(r.pending) >= openAIWSMaxPendingCreates || r.pendingBytes+r.controlBytes+int64(len(body)) > r.maxPendingBytes {
		r.mu.Unlock()
		return r.writeError(streamID, "websocket_queue_full", "Too many pending WebSocket requests; wait for a response before retrying")
	}
	if session == nil {
		if streamID != "" && !r.named[streamID] && len(r.named) >= openAIWSMaxNamedStreams {
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_stream_limit_reached", "Reuse an existing stream_id or open a new WebSocket connection")
		}
		if len(r.sessions) >= openAIWSMaxSessions {
			r.mu.Unlock()
			return r.writeError(streamID, "websocket_session_limit_reached", "Open a new WebSocket connection for additional conversations")
		}
		ctx, cancel := context.WithCancel(r.ctx)
		ctx = service.WithOpenAIWSLogicalIdentity(ctx, r.template, body)
		// Stable identified legacy conversations keep their existing reconnect
		// ownership/preemption scope. Only named lanes add a transport scope.
		if streamID != "" {
			scope := streamID
			if !identity.Resolved() {
				scope = r.connectionID + "|" + streamID
			}
			ctx = service.WithOpenAIWSLogicalStream(ctx, scope)
		}
		session = &openAIWSLogicalSession{router: r, streamID: streamID, key: key, identity: identity.BlockKey,
			ctx: ctx, cancel: cancel, frames: make(chan openAIWSStreamFrame, 1), controls: make(chan openAIWSStreamFrame, 8), closed: make(chan struct{})}
		session.ctx = service.WithOpenAIWSFrameOwner(ctx, uuid.NewString(), session.checkFrameOwner)
		r.sessions[key] = session
		if streamID != "" {
			r.named[streamID] = true
		}
	}
	if streamID == "" {
		r.lastDefault = session
	} else {
		r.lastNamed[streamID] = session
	}
	r.pending = append(r.pending, openAIWSStreamFrame{kind: kind, body: body, session: session, arrived: arrived})
	r.pendingBytes += int64(len(body))
	r.dispatchLocked()
	r.mu.Unlock()
	return nil
}

func (r *openAIWSStreamRouter) dispatchLocked() {
	busyLanes := make(map[string]bool)
	for _, s := range r.sessions {
		if s.busy {
			busyLanes[s.streamID] = true
		}
	}
	for i := 0; i < len(r.pending) && r.active < openAIWSMaxActiveStreams; {
		frame := r.pending[i]
		s := frame.session
		if s.busy || busyLanes[s.streamID] {
			i++
			continue
		}
		if s.isStopped() {
			i++ // runSession reports every unexecuted queued request on cleanup.
			continue
		}
		s.busy = true
		s.wroteError.Store(false)
		s.turnAccounted, s.terminalWritten, s.bareErrorAccounted = false, false, false
		s.responseID = ""
		r.active++
		busyLanes[s.streamID] = true
		r.removePendingLocked(i)
		s.frames <- frame // dedicated create slot; control frames cannot fill it
		if !s.started {
			s.started = true
			r.wg.Add(1)
			go r.runSession(s)
		}
	}
}

func (r *openAIWSStreamRouter) runSession(s *openAIWSLogicalSession) {
	defer r.wg.Done()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().Error("openai.websocket_session_panic", zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()))
			_ = s.Close(coderws.StatusInternalError, "websocket session failed")
		}
		s.stop()
		// Keep final notifications ahead of any replacement session's output.
		// The lock order matches Write/cancelSession: writeMu, then mu.
		r.writeMu.Lock()
		defer r.writeMu.Unlock()
		r.mu.Lock()
		if s.busy {
			s.busy = false
			r.active--
		}
		unexecuted := 0
		for i := 0; i < len(r.pending); {
			if r.pending[i].session == s {
				r.removePendingLocked(i)
				unexecuted++
			} else {
				i++
			}
		}
		for {
			select {
			case frame := <-s.controls:
				r.controlBytes -= int64(len(frame.body))
			default:
				goto drained
			}
		}
	drained:
		delete(r.sessions, s.key)
		if r.lastDefault == s {
			r.lastDefault = nil
		}
		if r.lastNamed[s.streamID] == s {
			delete(r.lastNamed, s.streamID)
		}
		r.dispatchLocked()
		cancelled, responseID := s.cancelled, s.responseID
		r.mu.Unlock()
		for i := 0; i < unexecuted; i++ {
			_ = r.writeEventLocked(openAIWSStreamErrorPayload(s.streamID, "websocket_request_not_executed", "Queued request was not sent upstream because its conversation closed; retry with full context"))
		}
		if cancelled {
			// Cleanup, reservations and queue notifications precede the terminal.
			payload := map[string]any{"type": "response.cancelled", "response": map[string]any{"status": "cancelled"}}
			if s.streamID != "" {
				payload["stream_id"] = s.streamID
			}
			if responseID != "" {
				payload["response"].(map[string]any)["id"] = responseID
			}
			_ = r.writeEventLocked(payload)
		}
	}()
	// Copy only the authenticated pre-forwarding context. A separate HTTP
	// writer prevents concurrent lanes from racing on the hijacked writer.
	c := r.template.Copy()
	c.Writer = &openAIWSIsolatedWriter{header: r.template.Writer.Header().Clone()}
	// Auth snapshots and the immutable revalidator can be shared, but their
	// mutable permission result and capacity cancellation belong to this lane.
	logicalCtx, cancelAdmission := service.ForkAPIKeyAdmissionOwner(s.ctx)
	defer cancelAdmission()
	logicalCtx = service.WithAPIKeyQueueImagePermission(logicalCtx, service.NewAPIKeyQueueImagePermission())
	c.Request = r.template.Request.Clone(logicalCtx)
	c.Request.Header = r.template.Request.Header.Clone()
	captureCtx, capture := requestcapture.ForkWebSocket(c.Request.Context())
	defer capture.Finish(http.StatusSwitchingProtocols)
	c.Request = c.Request.WithContext(requesttiming.With(captureCtx, requesttiming.New(time.Now(), 0)))
	r.h.serveOpenAIWSSession(c, s)
	if r.h.opsService != nil {
		logOpsStreamError(c, r.h.opsService, http.StatusSwitchingProtocols)
	}
}

func (s *openAIWSLogicalSession) Read(ctx context.Context) (coderws.MessageType, []byte, error) {
	// A just-dispatched create always precedes its controls, including when
	// the client cancels before this logical reader has started.
	var frame openAIWSStreamFrame
	select {
	case frame = <-s.frames:
	default:
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		case <-s.closed:
			return 0, nil, io.EOF
		case frame = <-s.frames:
		case frame = <-s.controls:
			s.router.mu.Lock()
			s.router.controlBytes -= int64(len(frame.body))
			s.router.mu.Unlock()
		}
	}
	if s.isStopped() {
		return 0, nil, io.EOF
	}
	s.lastArrival.Store(&frame.arrived)
	// stream_id is transport metadata. Older upstream WS implementations
	// and HTTP /responses must never receive it.
	body, err := sjson.DeleteBytes(frame.body, "stream_id")
	return frame.kind, body, err
}

func (r *openAIWSStreamRouter) removePendingLocked(i int) {
	r.pendingBytes -= int64(len(r.pending[i].body))
	copy(r.pending[i:], r.pending[i+1:])
	r.pending[len(r.pending)-1] = openAIWSStreamFrame{}
	r.pending = r.pending[:len(r.pending)-1]
}

func (s *openAIWSLogicalSession) LastFrameArrival() time.Time {
	if t := s.lastArrival.Load(); t != nil {
		return *t
	}
	return time.Time{}
}

func (s *openAIWSLogicalSession) Write(ctx context.Context, kind coderws.MessageType, body []byte) error {
	id, valid := service.OpenAIWSFrameResponseID(body)
	if service.IsOpenAIWSLocalPrewarmAck(ctx, body) {
		id, valid = "", true
	}
	// Only the router may attach its downstream lane identifier.
	var err error
	if s.streamID != "" {
		body, err = sjson.SetBytes(body, "stream_id", s.streamID)
	} else if gjson.GetBytes(body, "stream_id").Exists() {
		body, err = sjson.DeleteBytes(body, "stream_id")
	}
	if err != nil {
		return err
	}
	eventType := gjson.GetBytes(body, "type").String()
	r := s.router
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if service.IsOpenAIWSLocalNotice(ctx) {
		// A policy refusal or a solicited session.update acknowledgment is not a
		// provider response. Still require this exact live logical session.
		r.mu.Lock()
		live := !s.cancelled && !s.isStopped() && s.ctx.Err() == nil && r.sessions[s.key] == s
		r.mu.Unlock()
		if !live || kind != coderws.MessageText || (eventType != "response.failed" && eventType != "session.updated") || id != "" || !valid {
			return context.Canceled
		}
		return r.conn.Write(ctx, kind, body)
	}
	if err := s.checkFrameOwner(id, valid); err != nil {
		return err
	}
	err = r.conn.Write(ctx, kind, body)
	if err == nil && eventType == "error" {
		s.wroteError.Store(true)
	}
	if err == nil && openAIWSStreamTerminal(eventType) {
		r.mu.Lock()
		if eventType != "error" || s.bareErrorAccounted {
			s.terminalWritten = true
		}
		s.finishTurnLocked()
		r.mu.Unlock()
	}
	return err
}

// Reserve a response on its live logical turn before observing its usage or content.
func (s *openAIWSLogicalSession) checkFrameOwner(id string, valid bool) error {
	r := s.router
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.cancelled || s.isStopped() || s.ctx.Err() != nil || r.sessions[s.key] != s {
		return context.Canceled
	}
	if !s.busy || s.terminalWritten {
		return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "response turn already ended", nil)
	}
	owner, known := r.responseOwners[id]
	if !valid || (id != "" && ((s.responseID != "" && id != s.responseID) || (known && (owner != s.key || s.responseID != id)))) {
		return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "upstream response identity conflict", nil)
	}
	if id != "" && !known {
		// Do not forget an old response and later mistake it for a new identity.
		if len(r.responseOwners) >= 1024 {
			return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "response tracking limit reached; reconnect", nil)
		}
		s.responseID = id
		r.responseOwners[id] = s.key
	}
	return nil
}

func openAIWSStreamTerminal(eventType string) bool {
	switch eventType {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
		return true
	}
	return false
}

func (s *openAIWSLogicalSession) TurnComplete(terminal string) {
	r := s.router
	r.mu.Lock()
	s.turnAccounted = true
	s.bareErrorAccounted = terminal == "" || terminal == "error"
	if s.bareErrorAccounted && s.wroteError.Load() {
		s.terminalWritten = true
	}
	s.finishTurnLocked()
	r.mu.Unlock()
}

func (s *openAIWSLogicalSession) finishTurnLocked() {
	r := s.router
	if s.busy && s.turnAccounted && s.terminalWritten && !s.cancelled {
		s.busy = false
		r.active--
		r.dispatchLocked()
	}
}

func (r *openAIWSStreamRouter) cancelSession(s *openAIWSLogicalSession, responseID string) error {
	r.writeMu.Lock()
	r.mu.Lock()
	active := s.busy && !s.terminalWritten && !s.isStopped()
	if responseID != "" && responseID != s.responseID {
		active = false
	}
	if active {
		s.cancelled = true
		s.wroteError.Store(true)
		s.stop()
	}
	r.mu.Unlock()
	r.writeMu.Unlock()
	if !active {
		return r.writeError(s.streamID, "response_not_active", "No matching active response exists in this stream")
	}
	return nil
}

func (s *openAIWSLogicalSession) isStopped() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func (s *openAIWSLogicalSession) stop()           { s.drainOnce.Do(func() { s.cancel(); close(s.closed) }) }
func (s *openAIWSLogicalSession) CloseNow() error { s.stop(); return nil }
func (s *openAIWSLogicalSession) Close(code coderws.StatusCode, reason string) error {
	s.closeOnce.Do(func() {
		// Reject new work immediately. Canceling the lane while coder/websocket
		// is writing its last accepted frame would hard-close the shared socket.
		s.drainOnce.Do(func() { close(s.closed) })
		defer s.cancel()

		r := s.router
		r.writeMu.Lock() // let an already accepted write finish on its own deadline
		r.mu.Lock()
		physical := s.streamID == "" && len(r.sessions) <= 1
		if physical {
			r.transportClosing.Store(true)
		}
		r.mu.Unlock()
		if physical {
			r.writeMu.Unlock() // never hold a router lock while awaiting the peer's close
			s.closeErr = r.conn.Close(code, reason)
			return
		}
		defer r.writeMu.Unlock()
		if code != coderws.StatusNormalClosure && code != coderws.StatusGoingAway && !s.wroteError.Load() {
			s.closeErr = r.writeEventLocked(openAIWSStreamErrorPayload(s.streamID, "websocket_session_closed", reason))
			if s.closeErr == nil {
				s.wroteError.Store(true)
			}
		}
	})
	return s.closeErr
}

func openAIWSStreamErrorPayload(stream, code, message string) map[string]any {
	payload := map[string]any{"type": "error", "status": 400, "error": map[string]string{"type": "invalid_request_error", "code": code, "message": message}}
	if stream != "" {
		payload["stream_id"] = stream
	}
	return payload
}

func (r *openAIWSStreamRouter) writeError(stream, code, message string) error {
	return r.writeEvent(openAIWSStreamErrorPayload(stream, code, message))
}

func (r *openAIWSStreamRouter) writeEvent(payload any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.writeEventLocked(payload)
}

func (r *openAIWSStreamRouter) writeEventLocked(payload any) error {
	if r.transportClosing.Load() {
		return nil // the physical close already owns the client-visible outcome
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(r.ctx, 2*time.Second)
	defer cancel()
	return r.conn.Write(ctx, coderws.MessageText, body)
}

// A logical lane cannot write an HTTP response after the physical upgrade.
// It still needs a private writer for headers and the existing Ops helpers.
type openAIWSIsolatedWriter struct {
	header http.Header
	status int
	size   int
}

func (w *openAIWSIsolatedWriter) Header() http.Header               { return w.header }
func (w *openAIWSIsolatedWriter) WriteHeader(status int)            { w.status = status }
func (w *openAIWSIsolatedWriter) Write(p []byte) (int, error)       { w.size += len(p); return len(p), nil }
func (w *openAIWSIsolatedWriter) WriteString(p string) (int, error) { return w.Write([]byte(p)) }
func (w *openAIWSIsolatedWriter) Status() int {
	if w.status == 0 {
		return http.StatusSwitchingProtocols
	}
	return w.status
}
func (w *openAIWSIsolatedWriter) Size() int                { return w.size }
func (w *openAIWSIsolatedWriter) Written() bool            { return w.status != 0 || w.size > 0 }
func (w *openAIWSIsolatedWriter) WriteHeaderNow()          { w.status = w.Status() }
func (w *openAIWSIsolatedWriter) Flush()                   {}
func (w *openAIWSIsolatedWriter) CloseNotify() <-chan bool { return nil }
func (w *openAIWSIsolatedWriter) Pusher() http.Pusher      { return nil }
func (w *openAIWSIsolatedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("logical websocket is already upgraded")
}

var _ service.OpenAIWSDownstreamConn = (*openAIWSLogicalSession)(nil)
var _ gin.ResponseWriter = (*openAIWSIsolatedWriter)(nil)
