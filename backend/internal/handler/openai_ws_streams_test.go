package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func streamIsolationPayload(stream, thread, text, previous string) []byte {
	v := map[string]any{"type": "response.create", "model": "gpt-5.1", "input": text, "store": false}
	if stream != "" {
		v["stream_id"] = stream
	}
	if thread != "" {
		v["client_metadata"] = map[string]string{"thread_id": thread}
	}
	if previous != "" {
		v["previous_response_id"] = previous
	}
	b, _ := json.Marshal(v)
	return b
}

func streamIsolationSend(t *testing.T, conn *coderws.Conn, b []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, b))
}
func streamIsolationRead(t *testing.T, conn *coderws.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := conn.Read(ctx)
	require.NoError(t, err)
	return b
}
func streamIsolationTerminal(t *testing.T, conn *coderws.Conn) []byte {
	t.Helper()
	for {
		b := streamIsolationRead(t, conn)
		typ := gjson.GetBytes(b, "type").String()
		require.NotEqual(t, "error", typ, string(b))
		if typ == "response.completed" {
			return b
		}
	}
}

func streamIsolationHarness(t *testing.T, upstream string, mode string, headers http.Header) *openAIWSPassthroughHandlerHarness {
	t.Helper()
	return newOpenAIWSPassthroughHandlerHarnessWithKey(t, upstream, headers, nil, nil, func(key *service.APIKey) { key.AllowLongContext = true }, func(a *service.Account, cfg *config.Config, _ *service.HTTPUpstream, _ *service.AccountRepository) {
		a.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
		a.Extra["codex_fingerprint_mode"] = "off"
		a.Concurrency = 8
		cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	})
}

// The old connection binding rejected beta locally. This goes through the real
// handler and forwarding code, and proves alpha can continue after beta with
// its own response ID, tool call, state headers and usage attribution.
func TestOpenAIWSStreamLegacyThreadsKeepIndependentHistoryAndBilling(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var requests [][]byte
			var states []string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reply := func(body []byte) []byte {
					mu.Lock()
					requests = append(requests, append([]byte(nil), body...))
					states = append(states, r.Header.Get("x-codex-turn-state"))
					n := len(requests)
					mu.Unlock()
					if gjson.GetBytes(body, "stream_id").Exists() {
						t.Error("transport stream_id reached upstream")
					}
					output := `[]`
					if n == 1 {
						output = `[{"type":"function_call","id":"fc_alpha","call_id":"call_alpha","name":"lookup","arguments":"{}","status":"completed"}]`
					}
					return []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_isolated_%d","model":"gpt-5.1","status":"completed","output":%s,"usage":{"input_tokens":2,"output_tokens":1}}}`, n, output))
				}
				if r.Header.Get("Upgrade") == "websocket" {
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					for {
						_, body, err := conn.Read(r.Context())
						if err != nil {
							return
						}
						if err = conn.Write(r.Context(), coderws.MessageText, reply(body)); err != nil {
							return
						}
					}
				}
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("x-codex-turn-state", "state-"+gjson.GetBytes(body, "client_metadata.thread_id").String())
				fmt.Fprintf(w, "data: %s\n\n", reply(body))
			}))
			defer up.Close()
			h := streamIsolationHarness(t, up.URL, mode, http.Header{"Session-Id": []string{"shared-relay-session"}})
			legacyFirst, err := sjson.DeleteBytes(streamIsolationPayload("", "thread-alpha", "alpha-first", ""), "type")
			require.NoError(t, err)
			streamIsolationSend(t, h.clientConn, legacyFirst)
			first := streamIsolationTerminal(t, h.clientConn)
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("", "thread-beta", "beta-first", ""))
			streamIsolationTerminal(t, h.clientConn)
			previous := gjson.GetBytes(first, "response.id").String()
			last := map[string]any{"type": "response.create", "model": "gpt-5.1", "previous_response_id": previous, "store": false,
				"input": []any{map[string]string{"type": "function_call_output", "call_id": "call_alpha", "output": "alpha-tool-result"}}}
			b, _ := json.Marshal(last)
			streamIsolationSend(t, h.clientConn, b)
			streamIsolationTerminal(t, h.clientConn)
			mu.Lock()
			observed := append([][]byte(nil), requests...)
			headers := append([]string(nil), states...)
			mu.Unlock()
			require.Len(t, observed, 3)
			require.NotContains(t, string(observed[1]), "alpha-first")
			require.NotContains(t, string(observed[2]), "beta-first")
			require.Contains(t, string(observed[2]), "alpha-tool-result")
			if mode == service.OpenAIWSIngressModeHTTPBridge {
				require.Empty(t, headers[1])
				require.NotContains(t, headers[2], "thread-beta")
			}
			var sessions []string
			for i := 0; i < 3; i++ {
				select {
				case usage := <-h.usageLogs:
					require.NotNil(t, usage.SessionID)
					sessions = append(sessions, *usage.SessionID)
				case <-time.After(3 * time.Second):
					t.Fatal("missing per-turn billing")
				}
			}
			require.Equal(t, []string{"thread-alpha", "thread-beta", "thread-alpha"}, sessions)
			select {
			case extra := <-h.usageLogs:
				t.Fatalf("duplicate billing: %+v", extra)
			default:
			}
			require.NoError(t, h.clientConn.Close(coderws.StatusNormalClosure, "done"))
		})
	}
}

func TestOpenAIWSStreamNamedLanesRunConcurrentlyAndRemainFIFO(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			alphaStarted := make(chan struct{})
			betaStarted := make(chan struct{})
			var alphaOnce, betaOnce sync.Once
			var mu sync.Mutex
			var order []string
			process := func(body []byte) []byte {
				text := gjson.GetBytes(body, "input").String()
				mu.Lock()
				order = append(order, text)
				mu.Unlock()
				if text == "alpha-1" {
					alphaOnce.Do(func() { close(alphaStarted) })
					select {
					case <-betaStarted:
					case <-time.After(2 * time.Second):
						t.Error("beta was blocked behind alpha")
					}
				}
				if text == "beta-1" {
					betaOnce.Do(func() { close(betaStarted) })
				}
				return []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, "resp_"+text))
			}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Upgrade") == "websocket" {
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					for {
						_, b, err := conn.Read(r.Context())
						if err != nil {
							return
						}
						if err = conn.Write(r.Context(), coderws.MessageText, process(b)); err != nil {
							return
						}
					}
				}
				b, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", process(b))
			}))
			defer up.Close()
			h := streamIsolationHarness(t, up.URL, mode, http.Header{"Session-Id": []string{"shared-session"}})
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "alpha-1", ""))
			select {
			case <-alphaStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("alpha did not start")
			}
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "alpha-2", ""))
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("beta", "thread-beta", "beta-1", ""))
			var alphaIDs []string
			seen := map[string]int{}
			for i := 0; i < 3; i++ {
				e := streamIsolationTerminal(t, h.clientConn)
				lane := gjson.GetBytes(e, "stream_id").String()
				seen[lane]++
				if lane == "alpha" {
					alphaIDs = append(alphaIDs, gjson.GetBytes(e, "response.id").String())
				}
			}
			require.Equal(t, map[string]int{"alpha": 2, "beta": 1}, seen)
			require.Equal(t, []string{"resp_alpha-1", "resp_alpha-2"}, alphaIDs)
			mu.Lock()
			require.Equal(t, []string{"alpha-1", "beta-1", "alpha-2"}, order)
			mu.Unlock()
		})
	}
}

func TestOpenAIWSStreamConflictIsLaneScopedAndNeverReachesUpstream(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_valid_%d\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", n)
	}))
	defer up.Close()
	h := streamIsolationHarness(t, up.URL, service.OpenAIWSIngressModeHTTPBridge, http.Header{"Thread-Id": []string{"header-thread"}})
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("conflict", "body-thread", "must-not-run", ""))
	e := streamIsolationRead(t, h.clientConn)
	require.Equal(t, "conflict", gjson.GetBytes(e, "stream_id").String())
	require.Equal(t, "websocket_session_identity_conflict", gjson.GetBytes(e, "error.code").String())
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("valid", "header-thread", "ok", ""))
	e = streamIsolationTerminal(t, h.clientConn)
	require.Equal(t, "valid", gjson.GetBytes(e, "stream_id").String())
	// An invalid legacy/default lane must not close a healthy named lane.
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("", "body-thread", "must-not-run-either", ""))
	e = streamIsolationRead(t, h.clientConn)
	require.Equal(t, "websocket_session_identity_conflict", gjson.GetBytes(e, "error.code").String())
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("valid", "header-thread", "still-ok", ""))
	e = streamIsolationTerminal(t, h.clientConn)
	require.Equal(t, "valid", gjson.GetBytes(e, "stream_id").String())
	mu.Lock()
	require.Equal(t, 2, calls)
	mu.Unlock()
}

func TestOpenAIWSStreamIDValidation(t *testing.T) {
	for _, bad := range []string{`""`, `null`, `3`, `"a/b"`, `"a b"`, `"中文"`, `"` + strings.Repeat("a", 257) + `"`} {
		_, err := openAIWSStreamID([]byte(`{"stream_id":` + bad + `}`))
		require.Error(t, err, bad)
	}
	for _, good := range []string{`{}`, `{"stream_id":"a-Z_1.2"}`} {
		_, err := openAIWSStreamID([]byte(good))
		require.NoError(t, err)
	}
	_, err := openAIWSStreamID([]byte(`{"stream_id":"alpha","stream_id":"beta"}`))
	require.Error(t, err, "ambiguous duplicate stream keys must be rejected")
}

func TestOpenAIWSStreamCancelIsLaneScopedAndQueuedWorkIsNotReplayed(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var mu sync.Mutex
			var calls []string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var conn *coderws.Conn
				if r.Header.Get("Upgrade") == "websocket" {
					var err error
					conn, err = coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				write := func(event string) error {
					if conn != nil {
						return conn.Write(r.Context(), coderws.MessageText, []byte(event))
					}
					_, err := fmt.Fprintf(w, "data: %s\n\n", event)
					w.(http.Flusher).Flush()
					return err
				}
				for {
					var b []byte
					var err error
					if conn != nil {
						_, b, err = conn.Read(r.Context())
					} else {
						b, err = io.ReadAll(r.Body)
					}
					if err != nil {
						return
					}
					text := gjson.GetBytes(b, "input").String()
					mu.Lock()
					calls = append(calls, text)
					mu.Unlock()
					if strings.Contains(string(b), "must-not-execute") {
						t.Error("queued work executed after cancellation")
					}
					if text == "alpha-active" || text == "beta-active" {
						if write(fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":"gpt-5.1","status":"in_progress","output":[]}}`, "resp_"+strings.ReplaceAll(text, "-", "_"))) != nil {
							return
						}
						// Bridge staging deliberately holds lifecycle-only events until
						// semantic output commits the attempt; exercise real output.
						if write(`{"type":"response.output_text.delta","delta":"fixture"}`) != nil {
							return
						}
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					id := "resp_" + text
					if text == "alpha-active" || text == "beta-active" {
						id = strings.ReplaceAll(id, "-", "_")
					}
					if write(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, id)) != nil {
						return
					}
					if conn == nil {
						return
					}
				}
			}))
			defer up.Close()
			// Close the synthetic blocked writer before httptest server cleanup,
			// including an assertion failure in the middle of the scenario.
			defer releaseOnce.Do(func() { close(release) })
			h := streamIsolationHarness(t, up.URL, mode, http.Header{"Session-Id": []string{"fixture-shared-session"}})
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "alpha-active", ""))
			created := streamIsolationRead(t, h.clientConn)
			require.Equal(t, "response.created", gjson.GetBytes(created, "type").String())
			require.Equal(t, "response.output_text.delta", gjson.GetBytes(streamIsolationRead(t, h.clientConn), "type").String())
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("beta", "thread-beta", "beta-active", ""))
			created = streamIsolationRead(t, h.clientConn)
			require.Equal(t, "beta", gjson.GetBytes(created, "stream_id").String())
			require.Equal(t, "response.output_text.delta", gjson.GetBytes(streamIsolationRead(t, h.clientConn), "type").String())
			for i := 0; i < openAIWSMaxPendingCreates; i++ {
				streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", fmt.Sprintf("must-not-execute-%d", i), ""))
			}
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "queue-overflow", ""))
			e := streamIsolationRead(t, h.clientConn)
			require.Equal(t, "websocket_queue_full", gjson.GetBytes(e, "error.code").String())
			// A response from another stream cannot be cancelled by guessing its ID.
			streamIsolationSend(t, h.clientConn, []byte(`{"type":"response.cancel","stream_id":"alpha","response_id":"resp_beta_active"}`))
			e = streamIsolationRead(t, h.clientConn)
			require.Equal(t, "response_not_active", gjson.GetBytes(e, "error.code").String())
			streamIsolationSend(t, h.clientConn, []byte(`{"type":"response.cancel","stream_id":"alpha","response_id":"resp_alpha_active"}`))
			cancelled, unexecuted := 0, 0
			for i := 0; i < openAIWSMaxPendingCreates+1; i++ {
				e = streamIsolationRead(t, h.clientConn)
				require.Equal(t, "alpha", gjson.GetBytes(e, "stream_id").String())
				if gjson.GetBytes(e, "type").String() == "response.cancelled" {
					require.Equal(t, openAIWSMaxPendingCreates, i, "cancellation terminal follows every queued-request notification")
					cancelled++
				} else {
					require.Equal(t, "websocket_request_not_executed", gjson.GetBytes(e, "error.code").String())
					unexecuted++
				}
			}
			require.Equal(t, 1, cancelled)
			require.Equal(t, openAIWSMaxPendingCreates, unexecuted)
			releaseOnce.Do(func() { close(release) })
			e = streamIsolationTerminal(t, h.clientConn)
			require.Equal(t, "beta", gjson.GetBytes(e, "stream_id").String(), "alpha cancellation must leave beta's active response alive")
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "alpha-fresh", ""))
			streamIsolationTerminal(t, h.clientConn)
			// Cancelling a completed turn does not stop the lane or its next turn.
			streamIsolationSend(t, h.clientConn, []byte(`{"type":"response.cancel","stream_id":"alpha","response_id":"resp_alpha-fresh"}`))
			e = streamIsolationRead(t, h.clientConn)
			require.Equal(t, "response_not_active", gjson.GetBytes(e, "error.code").String())
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "alpha-next", ""))
			streamIsolationTerminal(t, h.clientConn)
			mu.Lock()
			observed := append([]string(nil), calls...)
			mu.Unlock()
			require.Equal(t, []string{"alpha-active", "beta-active", "alpha-fresh", "alpha-next"}, observed)
		})
	}
}

func TestOpenAIWSStreamForeignContinuationIsRejectedBeforeUpstream(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture_%d\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", n)
	}))
	defer up.Close()
	h := streamIsolationHarness(t, up.URL, service.OpenAIWSIngressModeHTTPBridge, nil)
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("alpha", "thread-alpha", "alpha", ""))
	streamIsolationTerminal(t, h.clientConn)
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("beta", "thread-beta", "foreign-tool-output", "resp_fixture_1"))
	e := streamIsolationRead(t, h.clientConn)
	require.Equal(t, "previous_response_not_found", gjson.GetBytes(e, "error.code").String())
	require.Equal(t, "beta", gjson.GetBytes(e, "stream_id").String())
	// An unnamed request cannot silently borrow a named conversation either.
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("", "", "foreign-unnamed-output", "resp_fixture_1"))
	e = streamIsolationRead(t, h.clientConn)
	require.Equal(t, "previous_response_not_found", gjson.GetBytes(e, "error.code").String())
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("beta", "thread-beta", "beta", ""))
	streamIsolationTerminal(t, h.clientConn)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 2, calls)
}

func TestOpenAIWSStreamSameLaneThreadSwitchesKeepHistoryAndFIFO(t *testing.T) {
	betaStarted, releaseBeta := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var mu sync.Mutex
	var requests [][]byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, append([]byte(nil), body...))
		n := len(requests)
		mu.Unlock()
		if gjson.GetBytes(body, "input").String() == "beta" {
			close(betaStarted)
			select {
			case <-releaseBeta:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_same_lane_%d\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", n)
	}))
	defer up.Close()
	defer releaseOnce.Do(func() { close(releaseBeta) })
	h := streamIsolationHarness(t, up.URL, service.OpenAIWSIngressModeHTTPBridge, http.Header{"Session-Id": []string{"fixture-shared"}})
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("shared", "thread-alpha", "alpha-first", ""))
	streamIsolationTerminal(t, h.clientConn)
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("shared", "thread-beta", "beta", ""))
	select {
	case <-betaStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("beta did not start")
	}
	streamIsolationSend(t, h.clientConn, streamIsolationPayload("shared", "", "alpha-second", "resp_same_lane_1"))
	// A different thread on this named lane still waits for beta's terminal.
	select {
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	count := len(requests)
	mu.Unlock()
	require.Equal(t, 2, count, "same lane must remain FIFO across a thread change")
	releaseOnce.Do(func() { close(releaseBeta) })
	streamIsolationTerminal(t, h.clientConn)
	streamIsolationTerminal(t, h.clientConn)
	mu.Lock()
	observed := append([][]byte(nil), requests...)
	mu.Unlock()
	require.Len(t, observed, 3)
	require.NotContains(t, string(observed[1]), "alpha-first")
	require.NotContains(t, string(observed[2]), "beta")
	require.Contains(t, string(observed[2]), "alpha-second")
	for _, thread := range []string{"thread-alpha", "thread-beta", "thread-alpha"} {
		select {
		case usage := <-h.usageLogs:
			require.NotNil(t, usage.SessionID)
			require.Equal(t, thread, *usage.SessionID)
		case <-time.After(3 * time.Second):
			t.Fatal("missing isolated billing")
		}
	}
}

// A fabricated stale terminal is never forwarded or counted on a sibling lane.
func TestOpenAIWSStreamRejectsForeignProviderTerminalBeforeUsage(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		for _, named := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/named=%v", mode, named), func(t *testing.T) {
				var calls atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var conn *coderws.Conn
					if r.Header.Get("Upgrade") == "websocket" {
						var err error
						conn, err = coderws.Accept(w, r, nil)
						if err != nil {
							return
						}
						defer conn.CloseNow()
						if _, _, err = conn.Read(r.Context()); err != nil {
							return
						}
					} else {
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("Content-Type", "text/event-stream")
					}
					n := calls.Add(1)
					text := "ordinary-seed"
					if n > 1 {
						text = "late-fixture-marker"
					}
					raw := fmt.Sprintf(`{"type":"response.completed","stream_id":"untrusted-tag","response":{"id":"resp_first","model":"gpt-5.1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":%d,"output_tokens":1}}}`, text, n)
					if conn != nil {
						_ = conn.Write(r.Context(), coderws.MessageText, []byte(raw))
						_, _, _ = conn.Read(r.Context())
					} else {
						fmt.Fprintf(w, "data: %s\n\n", raw)
					}
				}))
				defer up.Close()
				h := streamIsolationHarness(t, up.URL, mode, nil)
				defer h.clientConn.CloseNow()
				a, b := "", ""
				if named {
					a, b = "alpha", "beta"
				}
				streamIsolationSend(t, h.clientConn, streamIsolationPayload(a, "thread-one", "seed", ""))
				first := streamIsolationTerminal(t, h.clientConn)
				require.Equal(t, a, gjson.GetBytes(first, "stream_id").String())
				require.Contains(t, string(first), "ordinary-seed")
				select {
				case row := <-h.usageLogs:
					require.Equal(t, 1, row.InputTokens)
				case <-time.After(3 * time.Second):
					t.Fatal("seed usage missing")
				}
				streamIsolationSend(t, h.clientConn, streamIsolationPayload(b, "thread-two", "follow", ""))
				frame := streamIsolationRead(t, h.clientConn)
				require.Equal(t, "error", gjson.GetBytes(frame, "type").String())
				require.NotContains(t, string(frame), "late-fixture-marker")
				require.Equal(t, b, gjson.GetBytes(frame, "stream_id").String())
				require.Contains(t, gjson.GetBytes(frame, "error.message").String(), "identity")
				require.EqualValues(t, 2, calls.Load())
				select {
				case row := <-h.usageLogs:
					t.Fatalf("unexpected usage: %v", row.InputTokens)
				case <-time.After(50 * time.Millisecond):
				}
			})
		}
	}
}

// Two independently authenticated sockets use the same gateway, upstream and cache.
func TestOpenAIWSStreamTwoPrincipalsSameThreadAndLane(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{}, 2)
			release := make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body []byte
				var conn *coderws.Conn
				var err error
				if r.Header.Get("Upgrade") == "websocket" {
					conn, err = coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					_, body, err = conn.Read(r.Context())
				} else {
					w.Header().Set("Content-Type", "text/event-stream")
					body, err = io.ReadAll(r.Body)
				}
				if err != nil {
					return
				}
				marker := gjson.GetBytes(body, "input").String()
				if marker != "one" && marker != "two" {
					t.Error("unknown fixture input")
					return
				}
				calls.Add(1)
				entered <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				raw := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%s","model":"gpt-5.1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":2,"output_tokens":1}}}`, marker, marker)
				if conn != nil {
					_ = conn.Write(r.Context(), coderws.MessageText, []byte(raw))
				} else {
					fmt.Fprintf(w, "data: %s\n\n", raw)
				}
			}))
			defer up.Close()
			defer finish()
			h := streamIsolationHarness(t, up.URL, mode, nil)
			gateway := gin.New()
			keys := map[string]*service.APIKey{}
			for i, tag := range []string{"one", "two"} {
				key := *h.apiKey
				user := *h.apiKey.User
				user.ID += int64(i + 1)
				key.UserID = user.ID
				key.ID += int64(i + 1)
				key.User = &user
				key.Key = "fixture-key-" + tag
				keys[key.Key] = &key
			}
			gateway.GET("/v1/responses", func(c *gin.Context) {
				key := keys[strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")]
				if key == nil {
					c.AbortWithStatus(401)
					return
				}
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID, Concurrency: 2})
				h.handler.ResponsesWebSocket(c)
			})
			server := httptest.NewServer(gateway)
			defer server.Close()
			var clients []*coderws.Conn
			for _, tag := range []string{"one", "two"} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				c, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer fixture-key-" + tag}, "Session-Id": {"same-session"}}})
				cancel()
				require.NoError(t, err)
				defer c.CloseNow()
				clients = append(clients, c)
				streamIsolationSend(t, c, streamIsolationPayload("same-lane", "same-thread", tag, ""))
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(4 * time.Second):
					t.Fatal("both principals must have concurrent upstream turns")
				}
			}
			finish()
			for i, tag := range []string{"one", "two"} {
				frame := streamIsolationTerminal(t, clients[i])
				require.Equal(t, "same-lane", gjson.GetBytes(frame, "stream_id").String())
				require.Equal(t, tag, gjson.GetBytes(frame, "response.output.0.content.0.text").String())
			}
			seen := map[string]bool{}
			for range 2 {
				select {
				case row := <-h.usageLogs:
					tag := strings.TrimPrefix(row.RequestID, "resp_")
					key := keys["fixture-key-"+tag]
					require.NotNil(t, key, row.RequestID)
					require.Equal(t, key.UserID, row.UserID)
					require.Equal(t, key.ID, row.APIKeyID)
					require.False(t, seen[tag])
					seen[tag] = true
				case <-time.After(3 * time.Second):
					t.Fatal("missing usage")
				}
			}
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestOpenAIWSStreamProviderDenialDoesNotDiscloseContent(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool, "http_status"} {
		t.Run(mode, func(t *testing.T) {
			const private = "fixture-private-detail"
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				errDetail := `{"code":"invalid_request_error","type":"invalid_request_error","message":"fixture-private-detail","param":"fixture-private-detail"}`
				raw := `{"type":"response.failed","sequence_number":2,"response":{"id":"resp_refused","status":"failed","created_at":123,"model":"fixture-private-detail","output":[{"text":"fixture-private-detail"}],"metadata":{"x":"fixture-private-detail"},"error":` + errDetail + `}}`
				if r.Header.Get("Upgrade") == "websocket" {
					c, err := coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer c.CloseNow()
					if _, _, err = c.Read(r.Context()); err == nil {
						_ = c.Write(r.Context(), coderws.MessageText, []byte(raw))
					}
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "http_status" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnprocessableEntity)
					fmt.Fprintf(w, `{"error":%s}`, errDetail)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\n", raw)
			}))
			defer up.Close()
			actual := mode
			if mode == "http_status" {
				actual = service.OpenAIWSIngressModeHTTPBridge
			}
			h := streamIsolationHarness(t, up.URL, actual, nil)
			defer h.clientConn.CloseNow()
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("lane", "fixture-thread", "unremarkable request", ""))
			frame := streamIsolationRead(t, h.clientConn)
			require.NotContains(t, string(frame), private)
			require.Equal(t, "lane", gjson.GetBytes(frame, "stream_id").String())
			if mode == "http_status" {
				require.Equal(t, int64(422), gjson.GetBytes(frame, "status").Int())
				require.Equal(t, "invalid_request_error", gjson.GetBytes(frame, "error.code").String())
			} else {
				require.Equal(t, "response.failed", gjson.GetBytes(frame, "type").String())
				require.Equal(t, "invalid_request_error", gjson.GetBytes(frame, "response.error.code").String())
				require.JSONEq(t, `[]`, gjson.GetBytes(frame, "response.output").Raw)
			}
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

// Exercise an actual client socket, handler, billing hook and HTTP provider. A
// non-empty prewarm is intentional: input length cannot distinguish generation.
func TestOpenAIWSStreamHTTPBridgePrewarmDoesNotGenerate(t *testing.T) {
	for _, lane := range []string{"", "named"} {
		t.Run("lane_"+lane, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				calls = append(calls, string(body))
				n := len(calls)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, `data: {"type":"response.completed","response":{"id":"resp_fixture_%d","status":"completed","model":"gpt-5.1","output":[{"type":"message","id":"msg_fixture_%d","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fixture answer"}]}],"usage":{"input_tokens":4,"output_tokens":1,"total_tokens":5}}}`+"\n\n", n, n)
			}))
			defer up.Close()
			h := streamIsolationHarness(t, up.URL, service.OpenAIWSIngressModeHTTPBridge, nil)
			warm := streamIsolationPayload(lane, "warmup-thread", "", "")
			warm, _ = sjson.SetBytes(warm, "generate", false)
			warm, _ = sjson.SetRawBytes(warm, "include", []byte(`["reasoning.encrypted_content"]`))
			// The real client includes an item id even on its first plaintext
			// developer message. It is not a previous response or a history reference.
			const warmItemID = "msg_00000000-0000-4000-8000-000000000001"
			warm, _ = sjson.SetRawBytes(warm, "input", []byte(`[{"type":"message","id":"`+warmItemID+`","role":"developer","content":[{"type":"input_text","text":"prewarm only marker"}]}]`))
			warm, _ = sjson.SetRawBytes(warm, "tools", []byte(`[{"type":"function","name":"fixture_warm_only","parameters":{"type":"object","properties":{}}}]`))
			streamIsolationSend(t, h.clientConn, warm)
			ack := streamIsolationRead(t, h.clientConn)
			mu.Lock()
			count := len(calls)
			mu.Unlock()
			require.Zero(t, count, "prewarm reached provider")
			require.Equal(t, "response.completed", gjson.GetBytes(ack, "type").String())
			require.Equal(t, lane, gjson.GetBytes(ack, "stream_id").String())
			require.Equal(t, `""`, gjson.GetBytes(ack, "response.id").Raw)
			require.NotContains(t, string(ack), warmItemID)
			require.Equal(t, "[]", gjson.GetBytes(ack, "response.output").Raw)
			for _, f := range []string{"input_tokens", "output_tokens", "total_tokens"} {
				require.Equal(t, "0", gjson.GetBytes(ack, "response.usage."+f).Raw)
			}
			streamIsolationSend(t, h.clientConn, streamIsolationPayload(lane, "warmup-thread", "first real prompt", ""))
			first := streamIsolationTerminal(t, h.clientConn)
			require.Equal(t, "resp_fixture_1", gjson.GetBytes(first, "response.id").String())
			// An out-of-place prewarm has no authority to reset the completed conversation.
			streamIsolationSend(t, h.clientConn, warm)
			reject := streamIsolationRead(t, h.clientConn)
			require.Equal(t, "error", gjson.GetBytes(reject, "type").String())
			require.Equal(t, "invalid_prewarm", gjson.GetBytes(reject, "error.code").String())
			require.Equal(t, lane, gjson.GetBytes(reject, "stream_id").String())
			streamIsolationSend(t, h.clientConn, streamIsolationPayload(lane, "warmup-thread", "second real prompt", "resp_fixture_1"))
			second := streamIsolationTerminal(t, h.clientConn)
			require.Equal(t, "resp_fixture_2", gjson.GetBytes(second, "response.id").String())
			mu.Lock()
			sent := append([]string(nil), calls...)
			mu.Unlock()
			require.Len(t, sent, 2)
			require.Contains(t, sent[0], "first real prompt")
			require.Contains(t, sent[1], "second real prompt")
			for _, body := range sent {
				require.NotContains(t, body, "prewarm only marker")
				require.NotContains(t, body, warmItemID)
				require.NotContains(t, body, "fixture_warm_only")
				require.False(t, gjson.Get(body, "generate").Exists())
			}
			var ids []string
			for range 2 {
				select {
				case row := <-h.usageLogs:
					ids = append(ids, row.RequestID)
					require.EqualValues(t, 1, row.OutputTokens)
				case <-time.After(3 * time.Second):
					t.Fatal("missing real generation usage")
				}
			}
			require.ElementsMatch(t, []string{"resp_fixture_1", "resp_fixture_2"}, ids)
			select {
			case row := <-h.usageLogs:
				t.Fatalf("unexpected usage %s", row.RequestID)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestOpenAIWSStreamHTTPBridgePrewarmInvalid(t *testing.T) {
	for name, extra := range map[string]string{"pointer": `"previous_response_id":"resp_fixture"`, "conversation": `"conversation":"conv_fixture"`, "empty_id": `"input":[{"type":"message","role":"developer","content":"fixture","id":""}]`, "long_id": `"input":[{"type":"message","role":"developer","content":"fixture","id":"` + strings.Repeat("x", 513) + `"}]`, "control_id": `"input":[{"type":"message","role":"developer","content":"fixture","id":"msg_\nfixture"}]`, "id_reference": `"input":[{"type":"item_reference","id":"msg_fixture"}]`, "non_string_id": `"input":[{"type":"message","role":"developer","content":"fixture","id":{"id":"resp_fixture"}}]`, "tool_result": `"input":[{"type":"function_call_output","call_id":"call_fixture","output":"fixture"}]`, "opaque": `"input":[{"type":"reasoning","encrypted_content":"fixture"}]`, "unknown_include": `"include":["unknown"]`, "image": `"input":[{"role":"user","content":[{"type":"input_image","image_url":"fixture"}]}]`, "duplicate": `"generate":true`, "nonboolean": `"generate":"false"`} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "unexpected generation", 400)
			}))
			defer up.Close()
			h := streamIsolationHarness(t, up.URL, service.OpenAIWSIngressModeHTTPBridge, nil)
			frame := []byte(`{"type":"response.create","model":"gpt-5.1","stream_id":"warm","generate":false,` + extra + `}`)
			// A single malformed generate value is a lane error. A repeated key is
			// ambiguous before the physical connection can select a lane.
			if name == "nonboolean" {
				frame = []byte(`{"type":"response.create","model":"gpt-5.1","stream_id":"warm",` + extra + `}`)
			}
			streamIsolationSend(t, h.clientConn, frame)
			if name == "duplicate" {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, _, err := h.clientConn.Read(ctx)
				var closed coderws.CloseError
				require.ErrorAs(t, err, &closed)
				require.Equal(t, coderws.StatusPolicyViolation, closed.Code)
				require.Equal(t, "websocket request contains ambiguous fields", closed.Reason)
			} else {
				reject := streamIsolationRead(t, h.clientConn)
				require.Equal(t, "error", gjson.GetBytes(reject, "type").Str)
				require.Equal(t, "warm", gjson.GetBytes(reject, "stream_id").Str)
				if name == "nonboolean" {
					require.Equal(t, "invalid_generate", gjson.GetBytes(reject, "error.code").Str)
				}
			}
			require.Zero(t, calls.Load())
			select {
			case <-h.usageLogs:
				t.Fatal("unexpected prewarm billing")
			case <-time.After(25 * time.Millisecond):
			}
		})
	}
}

func TestStreamIsolationValidatesOriginalFrames(t *testing.T) {
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	h := streamIsolationHarness(t, upstream.URL, service.OpenAIWSIngressModeHTTPBridge, nil)
	streamIsolationSend(t, h.clientConn, []byte(`{"type":"session.update","session":{"label":1,"label":1}}`))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := h.clientConn.Read(ctx)
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
	require.Zero(t, forwarded.Load())
}
