package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAIWSBridgeHistoryMaxBytes        int64 = 8 << 20
	openAIWSBridgeHistoryProcessMaxBytes int64 = 128 << 20
	openAIWSBridgeHistoryMaxItems              = 2048
	openAIWSBridgeHistoryTTL                   = 5 * time.Minute
)

// This budget covers retained connection snapshots and copied output items,
// not ingress frames, HTTP buffers or total process memory. No data is persisted.
var openAIWSBridgeHistoryBytes atomic.Int64

func reserveOpenAIWSBridgeHistory(n int64) bool {
	for {
		old := openAIWSBridgeHistoryBytes.Load()
		if n < 0 || n > openAIWSBridgeHistoryProcessMaxBytes-old {
			return false
		}
		if openAIWSBridgeHistoryBytes.CompareAndSwap(old, old+n) {
			return true
		}
	}
}

type openAIWSBridgeHistoryScope struct {
	user, key, group int64
	model, execution string
}

func openAIWSBridgeHistoryScopeFor(c *gin.Context, source []byte, model string) openAIWSBridgeHistoryScope {
	key := getAPIKeyIDFromContext(c)
	execution, _ := resolveOpenAIWSExecutionScope(c, source, key)
	return openAIWSBridgeHistoryScope{openAIReadableRecoveryUser(c, key), key, getOpenAIGroupIDFromContext(c), model, execution}
}

// Owned exclusively by one serial bridge loop. A response on another socket,
// even one sharing a session ID, cannot supply this proof or these bytes.
type openAIWSHTTPBridgeHistory struct {
	service     *OpenAIGatewayService
	scope       openAIWSBridgeHistoryScope
	route       [32]byte
	response    string
	expires     time.Time
	items       []json.RawMessage
	charge      int64
	unavailable string
}

type openAIWSBridgeHistoryTurnKey struct{}

type openAIWSBridgeHistoryTurn struct {
	history                           *openAIWSHTTPBridgeHistory
	scope                             openAIWSBridgeHistoryScope
	sourceDigest, payloadDigest       [32]byte
	nativeInput                       [32]byte
	inputFrozen                       bool
	payload                           []byte
	input                             []json.RawMessage
	rebuilt                           bool
	route                             [32]byte
	sent, unsafe, completed, semantic bool
	response                          string
	output                            map[int]json.RawMessage
	charge                            int64
	unavailable                       string
	identities                        map[string]int
	observed                          map[int]openAIWSBridgeHistoryObservedItem
	fragments                         map[openAIWSBridgeHistoryFragmentKey]*openAIWSBridgeHistoryFragment
}

func (h *openAIWSHTTPBridgeHistory) close() {
	if h == nil {
		return
	}
	openAIWSBridgeHistoryBytes.Add(-h.charge)
	h.charge = 0
	h.items = nil
	h.response = ""
}

func (t *openAIWSBridgeHistoryTurn) close() {
	if t == nil {
		return
	}
	openAIWSBridgeHistoryBytes.Add(-t.charge)
	t.charge = 0
	t.output = nil
	t.identities = nil
	t.observed = nil
	t.fragments = nil
	t.input = nil
	t.payload = nil
}

func (h *openAIWSHTTPBridgeHistory) discard(reason string) {
	h.close()
	h.unavailable = reason
}

func (t *openAIWSBridgeHistoryTurn) invalidate(reason string) {
	t.unsafe = true
	t.unavailable = reason
}

func newOpenAIWSHTTPBridgeHistory(s *OpenAIGatewayService, c *gin.Context, account *Account, stateful bool) *openAIWSHTTPBridgeHistory {
	if s == nil || stateful || !openAIWSBridgeHistoryAccount(account) || openAIReadableRecoveryUser(c, getAPIKeyIDFromContext(c)) <= 0 ||
		(s.pluginManager != nil && s.pluginManager.ShouldRouteOpenAIOAuth(account)) {
		return nil
	}
	return &openAIWSHTTPBridgeHistory{service: s}
}

func openAIWSBridgeHistoryAccount(account *Account) bool {
	return account != nil && account.IsOpenAI() &&
		(account.IsOpenAIApiKey() || account.UsesOpenAICodexProtocol()) &&
		!account.HasManagedOpenAIConnections() && account.AuthConnectionID == "" &&
		!account.UsesNativeCNResponses() &&
		!account.IsOpenAIPassthroughEnabled() && !account.IsCopilotSDKEnabled() && !account.IsSIWCEnabled()
}

// Readable means lossless replay, not a lossy summary of an unknown output.
// Opaque reasoning, references and unfamiliar item/content kinds remain on the
// existing protected route and never receive a pointer-removal exemption.
func openAIWSBridgeHistoryItemsSafe(items []json.RawMessage, output bool) bool {
	if len(items) > openAIWSBridgeHistoryMaxItems || openAIWSRawItemsContainNonPortableContext(items) {
		return false
	}
	for _, raw := range items {
		if !utf8.Valid(raw) || !openAIEncryptedJSONUnambiguous(raw) {
			return false
		}
		item := gjson.ParseBytes(raw)
		if !output && item.Type == gjson.String {
			continue
		}
		if !item.IsObject() {
			return false
		}
		if output && !openAIWSBridgeHistoryOutputKeysSafe(item) {
			return false
		}
		if kind := item.Get("type"); kind.Exists() && kind.Type != gjson.String {
			return false
		}
		if status := item.Get("status"); status.Exists() && status.String() != "completed" {
			return false
		}
		switch item.Get("type").String() {
		case "", "message":
			role := item.Get("role").String()
			if output && role != "assistant" {
				return false
			}
			if role != "user" && role != "assistant" && role != "system" && role != "developer" {
				return false
			}
			content := item.Get("content")
			if content.Type == gjson.String && !output {
				continue
			}
			if !content.IsArray() {
				return false
			}
			for _, part := range content.Array() {
				if output && !openAIWSBridgeHistoryOutputPartSafe(part) {
					return false
				}
				switch part.Get("type").String() {
				case "input_text", "output_text":
					if part.Get("text").Type != gjson.String {
						return false
					}
				case "refusal":
					if part.Get("refusal").Type != gjson.String {
						return false
					}
				default:
					return false
				}
			}
		case "function_call":
			if item.Get("call_id").Type != gjson.String || item.Get("name").Type != gjson.String || item.Get("call_id").String() == "" || item.Get("name").String() == "" || item.Get("arguments").Type != gjson.String {
				return false
			}
		case "custom_tool_call":
			if item.Get("call_id").Type != gjson.String || item.Get("name").Type != gjson.String || item.Get("call_id").String() == "" || item.Get("name").String() == "" || item.Get("input").Type != gjson.String {
				return false
			}
		case "function_call_output", "custom_tool_call_output":
			if output || item.Get("call_id").Type != gjson.String || item.Get("call_id").String() == "" || !openAIWSBridgeHistoryToolResultSafe(item) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Validate in place: preserve part order, whitespace and the original JSON value.
func openAIWSBridgeHistoryToolResultSafe(item gjson.Result) bool {
	value := item.Get("output")
	if value.Type == gjson.String {
		return true
	}
	if item.Get("type").String() != "function_call_output" || !value.IsArray() {
		return false
	}
	count, safe := 0, true
	value.ForEach(func(_, part gjson.Result) bool {
		count++
		safe = count <= openAIWSBridgeHistoryMaxItems && part.IsObject() && part.Get("type").String() == "input_text" && part.Get("text").Type == gjson.String
		if safe {
			part.ForEach(func(key, _ gjson.Result) bool {
				safe = key.String() == "type" || key.String() == "text"
				return safe
			})
		}
		return safe
	})
	return safe && count > 0
}

func openAIWSBridgeHistorySize(items []json.RawMessage) int64 {
	n := int64(0)
	for _, item := range items {
		n += int64(len(item)) + 128
	}
	return n
}

// Message id/status and an omitted message type are transport decoration. Keep
// all other fields, content, item order and tool arguments exact. A matched
// client prefix uses the retained original bytes rather than dropping metadata.
func openAIWSBridgeHistoryComparable(raw json.RawMessage) []byte {
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil {
		return normalizeOpenAIWSJSONForCompareOrRaw(raw)
	}
	kind := gjson.GetBytes(raw, "type").String()
	if kind == "" || kind == "message" {
		delete(item, "id")
		delete(item, "status")
		delete(item, "type")
		if content := gjson.GetBytes(raw, "content"); content.Type == gjson.String {
			textKind := "input_text"
			if gjson.GetBytes(raw, "role").String() == "assistant" {
				textKind = "output_text"
			}
			item["content"], _ = json.Marshal([]map[string]string{{"type": textKind, "text": content.String()}})
		} else if content.IsArray() && gjson.GetBytes(raw, "role").String() == "assistant" {
			parts := make([]json.RawMessage, 0)
			content.ForEach(func(_, part gjson.Result) bool { parts = append(parts, bridgePartComparison(part)); return true })
			item["content"], _ = json.Marshal(parts)
		}
	} else if kind == "function_call" || kind == "custom_tool_call" {
		delete(item, "id")
		delete(item, "status")
	}
	result, _ := json.Marshal(item)
	return normalizeOpenAIWSJSONForCompareOrRaw(result)
}

func openAIWSBridgeHistoryHasPrefix(items, prefix []json.RawMessage) bool {
	if len(items) < len(prefix) {
		return false
	}
	for index, prior := range prefix {
		if !bytes.Equal(normalizeOpenAIWSJSONForCompareOrRaw(items[index]), normalizeOpenAIWSJSONForCompareOrRaw(prior)) &&
			!bytes.Equal(openAIWSBridgeHistoryComparable(items[index]), openAIWSBridgeHistoryComparable(prior)) {
			return false
		}
	}
	return true
}

func (h *openAIWSHTTPBridgeHistory) prepare(c *gin.Context, account *Account, source, payload []byte, model string, protected bool) (*openAIWSBridgeHistoryTurn, error) {
	if h == nil {
		return nil, nil
	}
	if !utf8.Valid(source) || !openAIEncryptedJSONUnambiguous(source) || !gjson.ParseBytes(source).IsObject() || !utf8.Valid(payload) || !openAIEncryptedJSONUnambiguous(payload) || !gjson.ParseBytes(payload).IsObject() {
		return nil, openAIAgentRecoveryError("bridge_history_unsupported")
	}
	if isOpenAIResponsesLiteWebSocketPayload(payload) || account.IsExcelBPSEnabledForModel(model) || account.IsPrismBrowserEnabledForModel(model) {
		if h.response != "" && gjson.GetBytes(source, "previous_response_id").String() != "" {
			return nil, openAIAgentRecoveryError("route_changed")
		}
		h.discard("bridge_history_unsupported")
		return nil, nil
	}
	if protected || len(openAIAgentRecoveryItemDigests(source)) > 0 || gjson.GetBytes(source, "conversation").Exists() {
		h.discard("bridge_history_unsupported")
		return nil, nil
	}
	historyPayload := payload
	if account.IsOpenAIOAuthLike() {
		historyPayload = restoreCodexToolNamesFromContext(c, payload)
	}
	input, _, err := openAIWSExtractNormalizedInputSequence(historyPayload)
	if err != nil {
		return nil, err
	}
	// A Responses string input is shorthand for one user message. Once joined
	// into an input array, keep the valid message form rather than a bare string.
	for index, raw := range input {
		if item := gjson.ParseBytes(raw); item.Type == gjson.String {
			input[index], _ = json.Marshal(map[string]string{"role": "user", "content": item.String()})
		}
	}
	scope := openAIWSBridgeHistoryScopeFor(c, source, model)
	if scope.user <= 0 || scope.key <= 0 || len(model) > 1024 {
		return nil, openAIAgentRecoveryError("route_changed")
	}
	previousField := gjson.GetBytes(source, "previous_response_id")
	if previousField.Exists() && previousField.Type != gjson.String && previousField.Type != gjson.Null {
		return nil, openAIAgentRecoveryError("bridge_history_unsupported")
	}
	previous := strings.TrimSpace(previousField.String())
	t := &openAIWSBridgeHistoryTurn{history: h, scope: scope, sourceDigest: sha256.Sum256(source), payload: payload, input: input}
	if previous != "" {
		if h.response == "" {
			reason := h.unavailable
			if reason == "" {
				reason = "bridge_previous_response_unavailable"
			}
			return nil, openAIAgentRecoveryError(reason)
		}
		if previous != h.response {
			return nil, openAIAgentRecoveryError("bridge_previous_response_unavailable")
		}
		if !time.Now().Before(h.expires) {
			h.discard("bridge_previous_response_expired")
			return nil, openAIAgentRecoveryError("bridge_previous_response_expired")
		}
		if scope != h.scope || !openAIWSBridgeHistoryAccount(account) {
			return nil, openAIAgentRecoveryError("route_changed")
		}
		if !openAIWSBridgeHistoryItemsSafe(input, false) {
			return nil, openAIAgentRecoveryError("bridge_history_unsupported")
		}
		if openAIWSBridgeHistoryHasPrefix(input, h.items) {
			t.input = combineOpenAIWSReplayItems(h.items, input[len(h.items):])
		} else {
			for _, item := range input {
				kind := gjson.GetBytes(item, "type").String()
				if gjson.GetBytes(item, "role").String() == "assistant" || kind == "function_call" || kind == "custom_tool_call" {
					return nil, openAIAgentRecoveryError("bridge_history_unsupported")
				}
			}
			t.input = combineOpenAIWSReplayItems(h.items, input)
		}
		if len(t.input) > openAIWSBridgeHistoryMaxItems {
			return nil, openAIAgentRecoveryError("bridge_history_limit")
		}
		if !openAIWSBridgeHistoryItemsSafe(t.input, false) {
			return nil, openAIAgentRecoveryError("bridge_history_unsupported")
		}
		if openAIWSBridgeHistorySize(t.input) > openAIWSBridgeHistoryMaxBytes {
			return nil, openAIAgentRecoveryError("bridge_history_limit")
		}
		// Every previous tool call must have its actual result exactly once.
		// No unfinished call is dropped to manufacture a portable conversation.
		rebuilt, setErr := setOpenAIWSPayloadInputSequence(payload, t.input, true)
		if setErr != nil {
			return nil, setErr
		}
		if !openAIWSCompletedToolInputs(rebuilt) {
			return nil, openAIAgentRecoveryError("bridge_history_unsupported")
		}
		rebuilt, setErr = sjson.DeleteBytes(rebuilt, "previous_response_id")
		if setErr != nil {
			return nil, setErr
		}
		if account.IsOpenAIOAuthLike() {
			aliased, reverse, _, aliasErr := aliasOpenAIOAuthReservedToolNamesBody(rebuilt)
			if aliasErr != nil {
				return nil, aliasErr
			}
			rebuilt = aliased
			mergeCodexToolNameReverse(c, reverse)
		}
		t.payload, t.rebuilt = rebuilt, true
	}
	if !openAIWSBridgeHistoryItemsSafe(input, false) {
		t.invalidate("bridge_history_unsupported")
	}
	if len(input) > openAIWSBridgeHistoryMaxItems || openAIWSBridgeHistorySize(input) > openAIWSBridgeHistoryMaxBytes {
		t.invalidate("bridge_history_limit")
	}
	t.payloadDigest = sha256.Sum256(t.payload)
	return t, nil
}

func openAIWSBridgeHistoryRoute(account *Account, req *http.Request, body []byte, model, proxy string) [32]byte {
	value, _ := json.Marshal([]any{openAIAgentRecoveryConfig(account, model), req.Method, req.Host,
		openAIAgentRecoveryWireDigest(req.URL.String(), req.Header, gjson.GetBytes(body, "model").String()), account.ProxyID, proxy,
		account.ResolveOpenAIResponsesWebSocketV2Mode("")})
	return sha256.Sum256(value)
}

// Body reads are local immutable GetBody copies, never network or upstream work.
func openAIWSBridgeRequestBody(req *http.Request) ([]byte, error) {
	if req == nil || req.URL == nil || req.GetBody == nil {
		return nil, openAIAgentRecoveryWireError("bridge_body_unavailable")
	}
	reader, err := req.GetBody()
	if err != nil {
		return nil, openAIAgentRecoveryWireError("bridge_body_unavailable")
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, openAIAgentRecoveryWireError("bridge_body_read")
	}
	return body, nil
}

// Freeze after the existing client-tool projection, before request builders or
// compatibility retries. Later normalization must not discard history items.
func (t *openAIWSBridgeHistoryTurn) freezeInput(body []byte) {
	if t != nil {
		t.nativeInput = openAIAgentRecoveryInputDigest(body)
		t.inputFrozen = true
	}
}

func (t *openAIWSBridgeHistoryTurn) checkPayload(payload []byte) error {
	if t != nil && sha256.Sum256(payload) != t.payloadDigest {
		return openAIAgentRecoveryWireError("bridge_history_payload")
	}
	return nil
}

func (t *openAIWSBridgeHistoryTurn) authorize(ctx context.Context, c *gin.Context, account *Account, source, body []byte, req *http.Request, proxy string) ([]byte, error) {
	if t == nil {
		return source, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !t.inputFrozen || openAIAgentRecoveryInputDigest(body) != t.nativeInput {
		return nil, openAIAgentRecoveryWireError("bridge_history_input")
	}
	if sha256.Sum256(source) != t.sourceDigest || openAIWSBridgeHistoryScopeFor(c, source, t.scope.model) != t.scope || !openAIWSBridgeHistoryAccount(account) || account.IsExcelBPSEnabledForModel(t.scope.model) || account.IsPrismBrowserEnabledForModel(t.scope.model) ||
		(t.history.service.pluginManager != nil && t.history.service.pluginManager.ShouldRouteOpenAIOAuth(account)) {
		return nil, openAIAgentRecoveryError("route_changed")
	}
	actual, err := openAIWSBridgeRequestBody(req)
	if err != nil {
		return nil, err
	}
	if len(actual) == 0 || !utf8.Valid(actual) || !openAIEncryptedJSONUnambiguous(actual) ||
		!bytes.Equal(normalizeOpenAIWSJSONForCompareOrRaw(actual), normalizeOpenAIWSJSONForCompareOrRaw(body)) {
		return nil, openAIAgentRecoveryWireError("bridge_history_body")
	}
	if req.Method != http.MethodPost || !gjson.ParseBytes(actual).IsObject() {
		return nil, openAIAgentRecoveryWireError("bridge_history_method")
	}
	route := openAIWSBridgeHistoryRoute(account, req, actual, t.scope.model, proxy)
	if t.rebuilt {
		h := t.history
		if h.response == "" || !time.Now().Before(h.expires) || route != h.route || h.scope != t.scope || strings.TrimSpace(gjson.GetBytes(source, "previous_response_id").String()) != h.response || gjson.GetBytes(actual, "previous_response_id").Exists() {
			return nil, openAIAgentRecoveryError("route_changed")
		}
		owner, err := h.service.getOpenAIWSStateStore().GetResponseAccount(ctx, t.scope.group, h.response)
		if err != nil {
			return nil, openAIAgentRecoveryError("store_unavailable")
		}
		if owner != account.ID {
			return nil, openAIAgentRecoveryError("owner_unavailable")
		}
		// Only this per-socket, recent, fully delivered readable reconstruction
		// replaces the original pointer at the otherwise unchanged recovery gate.
		// Protected items cannot enter this proof through either source or output.
		source = actual
	}
	t.route, t.sent = route, true
	return source, nil
}

func (t *openAIWSBridgeHistoryTurn) addOutput(index int, item gjson.Result) {
	if t.unsafe {
		return
	}
	if int64(len(item.Raw))+128 > openAIWSBridgeHistoryMaxBytes {
		t.invalidate("bridge_history_limit")
		return
	}
	raw := []byte(item.Raw)
	if index < 0 || index >= openAIWSBridgeHistoryMaxItems || !openAIWSBridgeHistoryItemsSafe([]json.RawMessage{raw}, true) {
		t.invalidate("bridge_history_unsupported")
		return
	}
	if old, found := t.output[index]; found {
		if !bytes.Equal(bridgeItemComparison(old), bridgeItemComparison(raw)) {
			t.invalidate("bridge_history_unsupported")
		}
		return
	}
	charge := int64(len(raw)) + 128
	var keys []string
	for _, field := range []string{"id", "call_id"} {
		identity := item.Get(field)
		if !identity.Exists() {
			continue
		}
		if identity.Type != gjson.String || identity.String() == "" || len(identity.String()) > 512 {
			t.invalidate("bridge_history_unsupported")
			return
		}
		key := field + ":" + identity.String()
		if old, ok := t.identities[key]; ok && old != index {
			t.invalidate("bridge_history_unsupported")
			return
		}
		keys = append(keys, key)
		charge += int64(len(key))
	}
	if t.charge+charge > openAIWSBridgeHistoryMaxBytes || !reserveOpenAIWSBridgeHistory(charge) {
		t.invalidate("bridge_history_limit")
		return
	}
	if t.output == nil {
		t.output = make(map[int]json.RawMessage)
	}
	if t.identities == nil {
		t.identities = make(map[string]int)
	}
	for _, key := range keys {
		t.identities[key] = index
	}
	t.output[index] = slices.Clone(raw)
	t.charge += charge
}

// Called only after the original downstream writer succeeds. A queued frame,
// item.done alone, cancellation, partial stream or failed write grants nothing.
func (t *openAIWSBridgeHistoryTurn) delivered(message []byte) {
	if t == nil || t.unsafe {
		return
	}
	if !utf8.Valid(message) || !openAIEncryptedJSONUnambiguous(message) || openAIWSRawItemsContainNonPortableContext([]json.RawMessage{message}) {
		t.invalidate("bridge_history_unsupported")
		return
	}
	event := gjson.GetBytes(message, "type").String()
	if !t.observeStreamEvent(message, event) {
		return
	}
	switch event {
	case "response.output_item.done":
		index := gjson.GetBytes(message, "output_index")
		if index.Type != gjson.Number {
			t.invalidate("bridge_history_unsupported")
			return
		}
		value, err := strconv.Atoi(index.Raw)
		if err != nil {
			t.invalidate("bridge_history_unsupported")
			return
		}
		t.addOutput(value, gjson.GetBytes(message, "item"))
	case "response.completed", "response.done":
		if !openAIAgentCompletedResponse(message, true) {
			t.invalidate("bridge_delivery_unconfirmed")
			return
		}
		output := gjson.GetBytes(message, "response.output")
		if !output.IsArray() {
			t.invalidate("bridge_delivery_unconfirmed")
			return
		}
		items := output.Array()
		if len(items) > 0 {
			if len(t.output) > len(items) {
				t.invalidate("bridge_history_unsupported")
				return
			}
			for index, item := range items {
				t.addOutput(index, item)
			}
		}
		if len(t.output) == 0 && t.semantic {
			t.invalidate("bridge_delivery_unconfirmed")
			return
		}
		for index := range len(t.output) {
			if _, ok := t.output[index]; !ok {
				t.invalidate("bridge_delivery_unconfirmed")
				return
			}
		}
		if !t.streamMatchesOutput() {
			t.invalidate("bridge_history_unsupported")
			return
		}
		response := gjson.GetBytes(message, "response.id").String()
		if t.response != "" && t.response != response {
			t.invalidate("bridge_delivery_unconfirmed")
			return
		}
		t.response = response
		t.completed = gjson.GetBytes(message, "response.id").Type == gjson.String && t.response != "" && len(t.response) <= 512
	case "error", "response.failed", "response.incomplete", "response.cancelled":
		t.invalidate("bridge_delivery_unconfirmed")
	}
}

func (t *openAIWSBridgeHistoryTurn) commit(ctx context.Context, success bool) {
	if t == nil {
		return
	}
	defer t.close()
	h := t.history
	if !success || ctx.Err() != nil || !t.sent || !t.completed || t.unsafe || !openAIWSBridgeHistoryItemsSafe(t.input, false) {
		reason := t.unavailable
		if reason == "" {
			reason = "bridge_delivery_unconfirmed"
		}
		h.discard(reason)
		return
	}
	items := make([]json.RawMessage, 0, len(t.input)+len(t.output))
	for _, raw := range t.input {
		if item := gjson.ParseBytes(raw); item.Type == gjson.String {
			raw, _ = json.Marshal(map[string]any{"role": "user", "content": item.String()})
		}
		items = append(items, raw)
	}
	for index := range len(t.output) {
		items = append(items, t.output[index])
	}
	charge := openAIWSBridgeHistorySize(items)
	if len(items) > openAIWSBridgeHistoryMaxItems || charge > openAIWSBridgeHistoryMaxBytes || !reserveOpenAIWSBridgeHistory(charge) {
		h.discard("bridge_history_limit")
		return
	}
	for index, item := range items {
		items[index] = slices.Clone(item)
	}
	h.close()
	h.items, h.charge, h.scope, h.route, h.response = items, charge, t.scope, t.route, t.response
	h.unavailable = ""
	h.expires = time.Now().Add(min(openAIWSBridgeHistoryTTL, h.service.openAIWSSessionStickyTTL()))
}
