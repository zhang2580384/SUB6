package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// This fictional provider creates its own unpredictable answer. A continuation
// can repeat it only if the bridge actually sends the delivered assistant item.
type bridgeHistoryUpstream struct {
	mu        sync.Mutex
	bodies    [][]byte
	code      string
	tool      bool
	streamed  bool
	toolName  string
	namespace bool
	rejectAt  int
}

func (u *bridgeHistoryUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.bodies = append(u.bodies, body)
	turn := len(u.bodies)
	if turn == u.rejectAt {
		return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"prompt_cache_breakpoint","message":"prompt_cache_breakpoint is not supported on this model"}}`))}, nil
	}
	if u.rejectAt > 0 && turn > u.rejectAt {
		turn--
	}
	if turn == 1 && u.namespace {
		u.toolName = gjson.GetBytes(body, "tools.0.name").String()
		if u.toolName == "" {
			return nil, fmt.Errorf("fixture namespace tool was not projected")
		}
	}
	text := u.code
	if turn > 1 {
		text = "history_missing"
		for _, item := range gjson.GetBytes(body, "input").Array() {
			if !item.IsObject() {
				return nil, fmt.Errorf("fixture provider refuses non-message input array items")
			}
			if item.Get("role").String() == "assistant" && (item.Get("content.0.text").String() == u.code || item.Get("content").String() == u.code) {
				text = u.code
			}
		}
	}
	message := fmt.Sprintf(`{"id":"msg_fixture_%d","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q,"annotations":[]}]}`, turn, text)
	if turn == 1 && u.tool {
		message += fmt.Sprintf(`,{"type":"function_call","id":"fc_fixture","call_id":"call_fixture","name":%q,"arguments":"{}","status":"completed"}`, u.toolName)
	}
	event := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_fixture_%d","status":"completed","output":[%s],"usage":{"input_tokens":3,"output_tokens":2}}}`, turn, message)
	if u.streamed {
		var items []json.RawMessage
		if err := json.Unmarshal([]byte("["+message+"]"), &items); err != nil {
			return nil, err
		}
		var events strings.Builder
		for index, item := range items {
			kind := gjson.GetBytes(item, "type").String()
			added, _ := sjson.SetBytes(item, "status", "in_progress")
			if kind == "message" {
				added, _ = sjson.SetRawBytes(added, "content", []byte("[]"))
			} else {
				added, _ = sjson.SetBytes(added, "arguments", "")
			}
			fmt.Fprintf(&events, "data: {\"type\":\"response.output_item.added\",\"output_index\":%d,\"item\":%s}\n\n", index, added)
			if kind == "message" {
				fmt.Fprintf(&events, "data: {\"type\":\"response.content_part.added\",\"output_index\":%d,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n", index)
				fmt.Fprintf(&events, "data: {\"type\":\"response.output_text.delta\",\"output_index\":%d,\"content_index\":0,\"delta\":%q}\n\n", index, text)
				fmt.Fprintf(&events, "data: {\"type\":\"response.output_text.done\",\"output_index\":%d,\"content_index\":0,\"text\":%q}\n\n", index, text)
				fmt.Fprintf(&events, "data: {\"type\":\"response.content_part.done\",\"output_index\":%d,\"content_index\":0,\"part\":%s}\n\n", index, gjson.GetBytes(item, "content.0").Raw)
			} else {
				fmt.Fprintf(&events, "data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":%d,\"delta\":\"{}\"}\n\n", index)
				fmt.Fprintf(&events, "data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":%d,\"arguments\":\"{}\"}\n\n", index)
			}
			fmt.Fprintf(&events, "data: {\"type\":\"response.output_item.done\",\"output_index\":%d,\"item\":%s}\n\n", index, item)
		}
		event, _ = sjson.SetRaw(event, "response.output", "[]")
		fmt.Fprintf(&events, "data: %s\n\n", event)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(events.String()))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + event + "\n\n"))}, nil
}

func (u *bridgeHistoryUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

type bridgeHistoryFixtureOptions struct {
	explicitBridge, strictOwnership, oauth bool
	streamed                               bool
	reservedTool                           bool
	namespace                              bool
	rejectAt                               int
}

func bridgeHistoryFixture(t *testing.T, withRecovery bool, tool bool, options ...bridgeHistoryFixtureOptions) (*websocket.Conn, *bridgeHistoryUpstream, context.Context, <-chan error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var nonce [16]byte
	_, err := rand.Read(nonce[:])
	require.NoError(t, err)
	upstream := &bridgeHistoryUpstream{code: hex.EncodeToString(nonce[:]), tool: tool, toolName: "fixture_lookup"}
	cfg := &config.Config{}
	cfg.Gateway.DisableEncryptedAgentOwnerBinding = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 5
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg)}
	account := &Account{ID: 910, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture-upstream"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	account.GroupIDs = []int64{912}
	if len(options) > 0 {
		option := options[0]
		upstream.streamed = option.streamed
		upstream.namespace = option.namespace
		upstream.rejectAt = option.rejectAt
		if option.reservedTool {
			upstream.toolName = codexPythonToolAlias
		}
		cfg.Gateway.DisableEncryptedAgentOwnerBinding = !option.strictOwnership
		if option.explicitBridge {
			cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = false
			cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1 << 30
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
			account.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
		}
		if option.oauth {
			account.Type = AccountTypeOAuth
			account.Credentials = map[string]any{"access_token": "fixture-upstream"}
		}
	}
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer conn.CloseNow()
		_, first, readErr := conn.Read(ctx)
		if readErr != nil {
			done <- readErr
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(ctx)
		c.Set("api_key", &APIKey{ID: 911, UserID: 913, GroupID: int64Ptr(912)})
		SetOpenAIWSRecoveryPrincipal(c, 913, 911)
		forwardCtx := ctx
		if withRecovery {
			forwardCtx = svc.WithOpenAIWSAgentRecovery(ctx, c, first, "fixture-history-session", "gpt-fixture")
		}
		done <- svc.ProxyResponsesWebSocketFromClient(forwardCtx, c, conn, account, "fixture-upstream", first, nil)
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, upstream, ctx, done
}

func bridgeHistoryCreate(input any, previous string, store bool) []byte {
	body := map[string]any{"type": "response.create", "model": "gpt-fixture", "store": store, "input": input}
	if previous != "" {
		body["previous_response_id"] = previous
	}
	raw, _ := json.Marshal(body)
	return raw
}

func TestOpenAIWSHTTPBridgeHistoryExplicitModeAndOwnership(t *testing.T) {
	for _, store := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			for _, oauth := range []bool{false, true} {
				t.Run(fmt.Sprintf("store=%v/strict=%v/oauth=%v", store, strict, oauth), func(t *testing.T) {
					conn, u, ctx, done := bridgeHistoryFixture(t, true, false, bridgeHistoryFixtureOptions{explicitBridge: true, strictOwnership: strict, oauth: oauth})
					require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate("Generate a fresh code.", "", store)))
					first := bridgeHistoryReadCompleted(t, ctx, conn)
					followup, err := sjson.DeleteBytes(bridgeHistoryCreate("Repeat the code exactly once.", gjson.GetBytes(first, "response.id").String(), store), "model")
					require.NoError(t, err)
					require.NoError(t, conn.Write(ctx, websocket.MessageText, followup))
					second := bridgeHistoryReadCompleted(t, ctx, conn)
					require.Equal(t, u.code, gjson.GetBytes(second, "response.output.0.content.0.text").String())
					require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
					require.NoError(t, <-done)
					require.Len(t, u.bodies, 2)
					require.False(t, gjson.GetBytes(u.bodies[1], "previous_response_id").Exists())
					require.Equal(t, store, gjson.GetBytes(u.bodies[1], "store").Bool(), "storage policy must not be silently changed")
					require.Len(t, gjson.GetBytes(u.bodies[1], "input").Array(), 3)
				})
			}
		}
	}
}

func TestOpenAIWSHTTPBridgeHistorySeedCompatibilityRetryAndRebuiltRefusal(t *testing.T) {
	for _, rebuilt := range []bool{false, true} {
		for _, oauth := range []bool{false, true} {
			t.Run(fmt.Sprintf("rebuilt=%v/oauth=%v", rebuilt, oauth), func(t *testing.T) {
				rejectAt := 1
				if rebuilt {
					rejectAt = 2
				}
				conn, upstream, ctx, done := bridgeHistoryFixture(t, true, false,
					bridgeHistoryFixtureOptions{explicitBridge: true, oauth: oauth, rejectAt: rejectAt})
				seed, err := sjson.SetBytes(bridgeHistoryCreate("Generate a fresh code.", "", false), "prompt_cache_breakpoint", "fixture-break")
				require.NoError(t, err)
				require.NoError(t, conn.Write(ctx, websocket.MessageText, seed))
				first := bridgeHistoryReadCompleted(t, ctx, conn)
				require.Equal(t, upstream.code, gjson.GetBytes(first, "response.output.0.content.0.text").String())
				followup, err := sjson.SetBytes(bridgeHistoryCreate("Repeat the code.", gjson.GetBytes(first, "response.id").String(), false), "prompt_cache_breakpoint", "fixture-break")
				require.NoError(t, err)
				require.NoError(t, conn.Write(ctx, websocket.MessageText, followup))
				if rebuilt {
					_, refusal, readErr := conn.Read(ctx)
					require.NoError(t, readErr)
					require.Equal(t, "error", gjson.GetBytes(refusal, "type").String())
					require.Error(t, <-done)
					require.Len(t, upstream.bodies, 2, "a rejected reconstructed history turn must never be automatically sent again")
					return
				}
				second := bridgeHistoryReadCompleted(t, ctx, conn)
				require.Equal(t, upstream.code, gjson.GetBytes(second, "response.output.0.content.0.text").String(), "the successful seed retry must still retain delivered history")
				require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
				require.NoError(t, <-done)
				require.Len(t, upstream.bodies, 3, "one rejected fresh request, its compatibility retry, and exactly one continuation")
				require.True(t, gjson.GetBytes(upstream.bodies[0], "prompt_cache_breakpoint").Exists())
				require.False(t, gjson.GetBytes(upstream.bodies[1], "prompt_cache_breakpoint").Exists())
				require.False(t, gjson.GetBytes(upstream.bodies[2], "previous_response_id").Exists())
			})
		}
	}
}

func TestOpenAIWSHTTPBridgeHistoryFreshSeedAfterCompletedReconstruction(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		t.Run(fmt.Sprintf("oauth=%v", oauth), func(t *testing.T) {
			conn, upstream, ctx, done := bridgeHistoryFixture(t, true, false,
				bridgeHistoryFixtureOptions{explicitBridge: true, oauth: oauth, rejectAt: 3})
			require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate("Generate a fresh code.", "", false)))
			first := bridgeHistoryReadCompleted(t, ctx, conn)
			require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate("Repeat the code.", gjson.GetBytes(first, "response.id").String(), false)))
			second := bridgeHistoryReadCompleted(t, ctx, conn)
			require.Equal(t, upstream.code, gjson.GetBytes(second, "response.output.0.content.0.text").String())
			freshInput := []any{map[string]string{"role": "user", "content": "Start another independent request."}}
			fresh, err := sjson.SetBytes(bridgeHistoryCreate(freshInput, "", false), "prompt_cache_breakpoint", "fixture-break")
			require.NoError(t, err)
			require.NoError(t, conn.Write(ctx, websocket.MessageText, fresh))
			bridgeHistoryReadCompleted(t, ctx, conn)
			require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
			require.NoError(t, <-done)
			require.Len(t, upstream.bodies, 4, "a completed reconstruction must not disable a later fresh request's compatibility retry")
			require.NotContains(t, string(upstream.bodies[3]), upstream.code, "a fresh seed must not inherit the prior assistant answer")
			require.False(t, gjson.GetBytes(upstream.bodies[3], "previous_response_id").Exists())
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryFullClientHistoryIsNotDuplicated(t *testing.T) {
	for _, pointer := range []bool{false, true} {
		t.Run(fmt.Sprint(pointer), func(t *testing.T) {
			conn, u, ctx, done := bridgeHistoryFixture(t, true, false, bridgeHistoryFixtureOptions{explicitBridge: true})
			seed := map[string]string{"role": "user", "content": "Generate a fresh code."}
			require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate([]any{seed}, "", false)))
			first := bridgeHistoryReadCompleted(t, ctx, conn)
			previous := ""
			if pointer {
				previous = gjson.GetBytes(first, "response.id").String()
			}
			// Real clients may omit the completed item's transport id/status.
			input := []any{seed, map[string]string{"role": "assistant", "content": u.code}, map[string]string{"role": "user", "content": "Repeat the code exactly once."}}
			require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate(input, previous, false)))
			second := bridgeHistoryReadCompleted(t, ctx, conn)
			require.Equal(t, u.code, gjson.GetBytes(second, "response.output.0.content.0.text").String())
			require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
			require.NoError(t, <-done)
			require.Len(t, u.bodies, 2)
			require.Len(t, gjson.GetBytes(u.bodies[1], "input").Array(), 3)
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryExplicitToolResultIsNotReexecuted(t *testing.T) {
	conn, u, ctx, done := bridgeHistoryFixture(t, true, true, bridgeHistoryFixtureOptions{explicitBridge: true})
	require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate([]any{map[string]string{"role": "user", "content": "Explain and call the fixture tool."}}, "", false)))
	first := bridgeHistoryReadCompleted(t, ctx, conn)
	result := []any{map[string]string{"type": "function_call_output", "call_id": "call_fixture", "output": "fixture result applied once"}}
	require.NoError(t, conn.Write(ctx, websocket.MessageText, bridgeHistoryCreate(result, gjson.GetBytes(first, "response.id").String(), false)))
	second := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, u.code, gjson.GetBytes(second, "response.output.0.content.0.text").String())
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
	require.NoError(t, <-done)
	require.Len(t, u.bodies, 2, "the provider must not be asked to regenerate the first turn")
	items := gjson.GetBytes(u.bodies[1], "input").Array()
	require.Len(t, items, 4)
	require.Equal(t, "assistant", items[1].Get("role").String())
	require.Equal(t, u.code, items[1].Get("content.0.text").String())
	require.Equal(t, "function_call", items[2].Get("type").String())
	require.Equal(t, "function_call_output", items[3].Get("type").String())
	require.Equal(t, "fixture result applied once", items[3].Get("output").String())
}

func TestOpenAIWSHTTPBridgeFirstFrameCustomToolHistory(t *testing.T) {
	const parts = `[{"type":"input_text","text":"one"},{"type":"input_text","text":"two"},{"type":"input_text","text":"three"}]`
	for _, tc := range []struct{ name, tools string }{
		{"still_declared", `[{"type":"custom","name":"fixture_note"}]`},
		{"removed", `[{"type":"function","name":"fixture_other","parameters":{"type":"object"}}]`},
		{"same_name_now_function", `[{"type":"function","name":"fixture_note","parameters":{"type":"object"}}]`},
		{"empty", `[]`},
		{"omitted", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, u, ctx, done := bridgeHistoryFixture(t, true, false, bridgeHistoryFixtureOptions{explicitBridge: true, strictOwnership: true})
			frame := []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"input":[
				{"type":"custom_tool_call","id":"ctc_item_a","call_id":"call_pair_a","name":"fixture_note","input":"past note"},
				{"type":"function_call","id":"fc_item_b","call_id":"call_pair_b","name":"fixture_lookup","arguments":"{}"},
				{"type":"function_call_output","call_id":"call_pair_b","output":"other result"},
				{"type":"custom_tool_call_output","id":"ctco_item_c","call_id":"call_pair_a","output":` + parts + `},
				{"role":"user","content":"Continue."}]}`)
			if tc.tools != "" {
				var err error
				frame, err = sjson.SetRawBytes(frame, "tools", []byte(tc.tools))
				require.NoError(t, err)
			}
			require.NoError(t, conn.Write(ctx, websocket.MessageText, frame))
			bridgeHistoryReadCompleted(t, ctx, conn)
			require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
			require.NoError(t, <-done)
			require.Len(t, u.bodies, 1)
			wire := u.bodies[0]
			require.False(t, gjson.GetBytes(wire, "previous_response_id").Exists())
			items := gjson.GetBytes(wire, "input").Array()
			require.Len(t, items, 5)
			require.Equal(t, "function_call", items[0].Get("type").String(), "wire types: %s / %s", items[0].Get("type"), items[3].Get("type"))
			require.Equal(t, "fc_item_a", items[0].Get("id").String())
			require.JSONEq(t, `{"input":"past note"}`, items[0].Get("arguments").String())
			require.Equal(t, "function_call_output", items[3].Get("type").String())
			require.False(t, items[3].Get("id").Exists())
			require.JSONEq(t, parts, items[3].Get("output").Raw)
			for _, i := range []int{0, 3} {
				require.Equal(t, "call_pair_a", items[i].Get("call_id").String())
			}
			for _, i := range []int{1, 2} {
				require.Equal(t, "call_pair_b", items[i].Get("call_id").String())
			}
			require.Equal(t, "other result", items[2].Get("output").String())
			if tc.name == "still_declared" {
				require.Equal(t, "function", gjson.GetBytes(wire, "tools.0.type").String())
			} else if tc.tools == "" {
				require.False(t, gjson.GetBytes(wire, "tools").Exists())
			} else {
				require.JSONEq(t, tc.tools, gjson.GetBytes(wire, "tools").Raw)
			}
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryExistingLiteSeedPath(t *testing.T) {
	conn, u, ctx, done := bridgeHistoryFixture(t, true, false, bridgeHistoryFixtureOptions{explicitBridge: true, oauth: true})
	seed := bridgeHistoryCreate("Generate a fresh code.", "", false)
	seed, err := sjson.SetBytes(seed, "client_metadata."+responsesLiteWSMetadataKey, "true")
	require.NoError(t, err)
	seed, err = sjson.SetRawBytes(seed, "tools", []byte(`[{"type":"namespace","name":"fixture_tools","tools":[{"type":"function","name":"fixture_lookup","parameters":{"type":"object","properties":{}}}]}]`))
	require.NoError(t, err)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, seed))
	first := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, u.code, gjson.GetBytes(first, "response.output.0.content.0.text").String())
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
	require.NoError(t, <-done)
	require.Len(t, u.bodies, 1)
	require.Equal(t, "additional_tools", gjson.GetBytes(u.bodies[0], "input.1.type").String(), "existing Lite namespace projection must remain effective")
}

func TestOpenAIWSHTTPBridgeHistoryNamespacedToolResult(t *testing.T) {
	conn, u, ctx, done := bridgeHistoryFixture(t, true, true, bridgeHistoryFixtureOptions{explicitBridge: true, namespace: true})
	seed := bridgeHistoryCreate("Explain and call the namespace fixture tool.", "", false)
	seed, err := sjson.SetRawBytes(seed, "tools", []byte(`[{"type":"namespace","name":"fixture_tools","tools":[{"type":"function","name":"fixture_lookup","parameters":{"type":"object","properties":{}}}]}]`))
	require.NoError(t, err)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, seed))
	first := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, "fixture_tools", gjson.GetBytes(first, "response.output.1.namespace").String())
	require.Equal(t, "fixture_lookup", gjson.GetBytes(first, "response.output.1.name").String())
	follow := bridgeHistoryCreate([]any{map[string]string{"type": "function_call_output", "call_id": "call_fixture", "output": "fixture result applied once"}}, gjson.GetBytes(first, "response.id").String(), false)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, follow))
	second := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, u.code, gjson.GetBytes(second, "response.output.0.content.0.text").String())
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
	require.NoError(t, <-done)
	require.Len(t, u.bodies, 2)
	input := gjson.GetBytes(u.bodies[1], "input").Array()
	require.Len(t, input, 4)
	require.Equal(t, u.toolName, input[2].Get("name").String(), "retained client namespace must use the existing provider projection")
	require.Equal(t, "function_call_output", input[3].Get("type").String())
}

func TestOpenAIWSHTTPBridgeHistoryStreamedToolResultAndOAuthNames(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		t.Run(fmt.Sprintf("oauth=%v", oauth), func(t *testing.T) {
			conn, u, ctx, done := bridgeHistoryFixture(t, true, true, bridgeHistoryFixtureOptions{explicitBridge: true, strictOwnership: true, oauth: oauth, streamed: true, reservedTool: oauth})
			seed := bridgeHistoryCreate("Explain and call the fixture tool.", "", false)
			if oauth {
				seed, _ = sjson.SetRawBytes(seed, "tools", []byte(`[{"type":"function","name":"python","parameters":{"type":"object","properties":{}}}]`))
			}
			require.NoError(t, conn.Write(ctx, websocket.MessageText, seed))
			read := func() []byte {
				t.Helper()
				var text strings.Builder
				for {
					_, event, err := conn.Read(ctx)
					require.NoError(t, err)
					switch gjson.GetBytes(event, "type").String() {
					case "response.output_text.delta":
						text.WriteString(gjson.GetBytes(event, "delta").String())
					case "response.output_item.done":
						item := gjson.GetBytes(event, "item")
						if item.Get("type").String() == "message" {
							require.Equal(t, u.code, item.Get("content.0.text").String())
						}
						if oauth && item.Get("type").String() == "function_call" {
							require.Equal(t, "python", item.Get("name").String())
						}
					case "response.completed":
						require.Empty(t, gjson.GetBytes(event, "response.output").Array(), "the terminal must not supply the missing stream history")
						require.Equal(t, u.code, text.String(), "repeat the provider's unpredictable code exactly once")
						return event
					case "error":
						t.Fatalf("unexpected refusal: %s", event)
					}
				}
			}
			first := read()
			follow := bridgeHistoryCreate([]any{map[string]string{"type": "function_call_output", "call_id": "call_fixture", "output": "fixture result applied once"}}, gjson.GetBytes(first, "response.id").String(), false)
			follow, err := sjson.DeleteBytes(follow, "model")
			require.NoError(t, err)
			require.NoError(t, conn.Write(ctx, websocket.MessageText, follow))
			read()
			require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
			require.NoError(t, <-done)
			require.Len(t, u.bodies, 2, "the same socket must continue without regeneration or a hidden retry")
			items := gjson.GetBytes(u.bodies[1], "input").Array()
			require.Len(t, items, 4)
			require.Equal(t, u.code, items[1].Get("content.0.text").String())
			require.Equal(t, u.toolName, items[2].Get("name").String())
			require.Equal(t, "function_call_output", items[3].Get("type").String())
			require.False(t, gjson.GetBytes(u.bodies[1], "previous_response_id").Exists())
		})
	}
}

const bridgeHistoryUnitOutput = `{"id":"msg_unit","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"fictional answer","annotations":[]}]}`

func bridgeHistoryTerminal(output string) []byte {
	return []byte(`{"type":"response.completed","response":{"id":"resp_unit","status":"completed","output":` + output + `}}`)
}

func bridgeHistoryUnit(t *testing.T) (*openAIWSHTTPBridgeHistory, *gin.Context, *Account) {
	t.Helper()
	baseline := openAIWSBridgeHistoryBytes.Load()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set("api_key", &APIKey{ID: 911, UserID: 913, GroupID: int64Ptr(912)})
	SetOpenAIWSRecoveryPrincipal(c, 913, 911)
	a := &Account{ID: 910, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-upstream"}, Extra: map[string]any{"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeHTTPBridge}}
	h := newOpenAIWSHTTPBridgeHistory(&OpenAIGatewayService{cfg: &config.Config{}}, c, a, false)
	require.NotNil(t, h)
	t.Cleanup(func() {
		h.close()
		require.Equal(t, baseline, openAIWSBridgeHistoryBytes.Load(), "retained history budget must be released")
	})
	return h, c, a
}

func bridgeHistoryUnitRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://bridge-provider.example/v1/responses", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer fixture-upstream")
	return req
}

func bridgeHistorySeedUnit(t *testing.T, h *openAIWSHTTPBridgeHistory, c *gin.Context, a *Account) {
	t.Helper()
	source := []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"client_metadata":{"thread_id":"fixture-alpha"},"input":[{"role":"user","content":"fictional task"}]}`)
	turn, err := h.prepare(c, a, source, source, "gpt-fixture", false)
	require.NoError(t, err)
	body, err := prepareOpenAIWSHTTPBridgeBody(a, turn.payload)
	require.NoError(t, err)
	turn.freezeInput(body)
	_, err = turn.authorize(context.Background(), c, a, source, body, bridgeHistoryUnitRequest(t, body), "")
	require.NoError(t, err)
	turn.delivered(bridgeHistoryTerminal("[" + bridgeHistoryUnitOutput + "]"))
	turn.commit(context.Background(), true)
	require.Equal(t, "resp_unit", h.response)
	require.NoError(t, h.service.getOpenAIWSStateStore().BindResponseAccount(context.Background(), 912, h.response, a.ID, time.Minute))
}

func bridgeHistoryUnitFollowup() []byte {
	return []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"client_metadata":{"thread_id":"fixture-alpha"},"previous_response_id":"resp_unit","input":[{"role":"user","content":"continue"}]}`)
}

func TestOpenAIWSHTTPBridgeHistoryScopeAndExpiry(t *testing.T) {
	for _, field := range []string{"user", "key", "group", "model", "thread", "unknown_id", "expired", "new_socket", "ambiguous_json", "invalid_utf8"} {
		t.Run(field, func(t *testing.T) {
			h, c, a := bridgeHistoryUnit(t)
			bridgeHistorySeedUnit(t, h, c, a)
			source := bridgeHistoryUnitFollowup()
			model := "gpt-fixture"
			reason := "route_changed"
			switch field {
			case "user":
				SetOpenAIWSRecoveryPrincipal(c, 914, 911)
			case "key":
				c.Set("api_key", &APIKey{ID: 915, UserID: 913, GroupID: int64Ptr(912)})
				SetOpenAIWSRecoveryPrincipal(c, 913, 915)
			case "group":
				c.Set("api_key", &APIKey{ID: 911, UserID: 913, GroupID: int64Ptr(916)})
			case "model":
				model = "gpt-other"
				source, _ = sjson.SetBytes(source, "model", model)
			case "thread":
				source, _ = sjson.SetBytes(source, "client_metadata.thread_id", "fixture-beta")
			case "unknown_id":
				source, _ = sjson.SetBytes(source, "previous_response_id", "resp_external")
				reason = "bridge_previous_response_unavailable"
			case "expired":
				h.expires = time.Now().Add(-time.Second)
				reason = "bridge_previous_response_expired"
			case "new_socket":
				h.close()
				h = newOpenAIWSHTTPBridgeHistory(h.service, c, a, false)
				reason = "bridge_previous_response_unavailable"
			case "ambiguous_json":
				source = append([]byte(`{"previous_response_id":"resp_other",`), source[1:]...)
				reason = "bridge_history_unsupported"
			case "invalid_utf8":
				source = append(source, 0xff)
				reason = "bridge_history_unsupported"
			}
			turn, err := h.prepare(c, a, source, source, model, false)
			require.Nil(t, turn)
			var refusal *OpenAIAgentRecoveryError
			require.ErrorAs(t, err, &refusal)
			require.Equal(t, reason, refusal.Reason)
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryFinalSendIntegrity(t *testing.T) {
	for _, field := range []string{"account", "credential", "base_url", "mapped_model", "proxy", "authorization", "organization", "project", "responses_lite", "request_url", "input", "source", "payload", "missing_body", "unreadable_body", "binding_changed"} {
		t.Run(field, func(t *testing.T) {
			h, c, a := bridgeHistoryUnit(t)
			bridgeHistorySeedUnit(t, h, c, a)
			source := bridgeHistoryUnitFollowup()
			turn, err := h.prepare(c, a, source, source, "gpt-fixture", false)
			require.NoError(t, err)
			defer turn.close()
			body, err := prepareOpenAIWSHTTPBridgeBody(a, turn.payload)
			require.NoError(t, err)
			turn.freezeInput(body)
			req := bridgeHistoryUnitRequest(t, body)
			switch field {
			case "binding_changed":
				require.NoError(t, h.service.getOpenAIWSStateStore().BindResponseAccount(context.Background(), 912, h.response, 918, time.Minute))
			case "account":
				a.ID++
			case "credential":
				a.Credentials["api_key"] = "fixture-rotated"
			case "base_url":
				a.Credentials["base_url"] = "https://changed-provider.example"
			case "mapped_model":
				a.Credentials["model_mapping"] = map[string]any{"gpt-fixture": "gpt-other"}
			case "proxy":
				a.ProxyID = int64Ptr(917)
			case "authorization":
				req.Header.Set("Authorization", "Bearer fixture-changed")
			case "organization":
				req.Header.Set("OpenAI-Organization", "fixture-changed")
			case "project":
				req.Header.Set("OpenAI-Project", "fixture-changed")
			case "responses_lite":
				req.Header.Set(responsesLiteHeader, "true")
			case "request_url":
				req.URL.RawQuery = "revision=changed"
			case "input":
				body, _ = sjson.SetBytes(body, "input.0.content", "changed task")
				req = bridgeHistoryUnitRequest(t, body)
			case "source":
				source, _ = sjson.SetBytes(source, "input.0.content", "changed task")
			case "payload":
				changed, _ := sjson.SetBytes(turn.payload, "input.0.content", "changed task")
				require.Error(t, turn.checkPayload(changed))
				return
			case "missing_body":
				req.GetBody = nil
			case "unreadable_body":
				req.GetBody = func() (io.ReadCloser, error) { return nil, fmt.Errorf("fixture read denied") }
			}
			_, err = turn.authorize(context.Background(), c, a, source, body, req, "")
			require.Error(t, err)
			require.False(t, turn.sent, "the refusal must precede the physical HTTP send")
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryDeliveryEvidence(t *testing.T) {
	done := func(index string, item string) []byte {
		return []byte(`{"type":"response.output_item.done","output_index":` + index + `,"item":` + item + `}`)
	}
	completed := bridgeHistoryTerminal("[" + bridgeHistoryUnitOutput + "]")
	function := `{"id":"fc_unit","type":"function_call","call_id":"call_unit","name":"fixture_tool","arguments":"{}","status":"completed"}`
	custom := `{"id":"custom_unit","type":"custom_tool_call","call_id":"call_custom","name":"fixture_custom","input":"fixture input","status":"completed"}`
	refusal := `{"id":"msg_unit","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"fictional refusal"}]}`
	other := strings.ReplaceAll(bridgeHistoryUnitOutput, "fictional answer", "conflicting answer")
	cases := []struct {
		name          string
		events        [][]byte
		success, want bool
	}{
		{"nonempty_logprobs", [][]byte{bridgeHistoryTerminal("[" + strings.Replace(bridgeHistoryUnitOutput, `"annotations":[]`, `"logprobs":[{}]`, 1) + "]")}, true, false},
		{"null_annotations", [][]byte{bridgeHistoryTerminal("[" + strings.Replace(bridgeHistoryUnitOutput, `"annotations":[]`, `"annotations":null`, 1) + "]")}, true, false},
		{"refusal_optional_empty", [][]byte{done("0", strings.Replace(refusal, `"refusal":"fictional refusal"`, `"refusal":"fictional refusal","logprobs":[],"annotations":[]`, 1)), bridgeHistoryTerminal("[" + refusal + "]")}, true, true},
		{"refusal_different_text", [][]byte{done("0", refusal), bridgeHistoryTerminal("[" + strings.ReplaceAll(refusal, "fictional refusal", "different") + "]")}, true, false},
		{"completed", [][]byte{completed}, true, true},
		{"done_then_completed", [][]byte{done("0", bridgeHistoryUnitOutput), completed}, true, true},
		{"done_empty_terminal", [][]byte{done("0", bridgeHistoryUnitOutput), bridgeHistoryTerminal("[]")}, true, true},
		{"same_done_duplicate", [][]byte{done("0", bridgeHistoryUnitOutput), done("0", bridgeHistoryUnitOutput), completed}, true, true},
		{"split_text_delta", [][]byte{[]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_unit","delta":"fictional "}`), []byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"answer"}`), completed}, true, true},
		{"function_arguments_delta", [][]byte{[]byte(`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_unit","delta":"{"}`), []byte(`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{"}`), bridgeHistoryTerminal("[" + function + "]")}, true, false},
		{"function_arguments_complete", [][]byte{[]byte(`{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_unit","delta":"{}"}`), []byte(`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{}"}`), bridgeHistoryTerminal("[" + function + "]")}, true, true},
		{"custom_input_complete", [][]byte{[]byte(`{"type":"response.custom_tool_call_input.delta","output_index":0,"item_id":"custom_unit","delta":"fixture input"}`), bridgeHistoryTerminal("[" + custom + "]")}, true, true},
		{"refusal_complete", [][]byte{[]byte(`{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"fictional refusal"}`), bridgeHistoryTerminal("[" + refusal + "]")}, true, true},
		{"conflicting_done", [][]byte{done("0", bridgeHistoryUnitOutput), done("0", other), completed}, true, false},
		{"conflicting_terminal", [][]byte{done("0", bridgeHistoryUnitOutput), bridgeHistoryTerminal("[" + other + "]")}, true, false},
		{"sparse_indices", [][]byte{done("1", bridgeHistoryUnitOutput), bridgeHistoryTerminal("[]")}, true, false},
		{"duplicate_item_id", [][]byte{bridgeHistoryTerminal("[" + bridgeHistoryUnitOutput + "," + bridgeHistoryUnitOutput + "]")}, true, false},
		{"string_index", [][]byte{done(`"0"`, bridgeHistoryUnitOutput), completed}, true, false},
		{"fractional_index", [][]byte{done("0.5", bridgeHistoryUnitOutput), completed}, true, false},
		{"missing_terminal", [][]byte{done("0", bridgeHistoryUnitOutput)}, true, false},
		{"failed_write_or_proxy", [][]byte{completed}, false, false},
		{"unknown_output", [][]byte{bridgeHistoryTerminal(`[{"type":"unfamiliar_provider_state","payload":"opaque"}]`)}, true, false},
		{"encrypted_output", [][]byte{bridgeHistoryTerminal(`[{"type":"agent_message","encrypted_content":"fixture-ciphertext"}]`)}, true, false},
		{"incomplete_output", [][]byte{bridgeHistoryTerminal("[" + strings.ReplaceAll(bridgeHistoryUnitOutput, "completed", "in_progress") + "]")}, true, false},
		{"delta_without_item", [][]byte{[]byte(`{"type":"response.output_text.delta","delta":"fictional fragment"}`), bridgeHistoryTerminal("[]")}, true, false},
		{"text_done_without_item", [][]byte{[]byte(`{"type":"response.output_text.done","text":"fictional fragment"}`), bridgeHistoryTerminal("[]")}, true, false},
		{"unknown_semantic_delta", [][]byte{[]byte(`{"type":"response.unfamiliar.delta","delta":"opaque"}`), completed}, true, false},
		{"missing_added_item", [][]byte{[]byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"message"}}`), completed}, true, false},
		{"opaque_added_item", [][]byte{[]byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"reasoning"}}`), completed}, true, false},
		{"opaque_content_part", [][]byte{[]byte(`{"type":"response.content_part.added","output_index":0,"content_index":1,"part":{"type":"reasoning_text","text":"unreadable state"}}`), completed}, true, false},
		{"missing_content_part", [][]byte{[]byte(`{"type":"response.content_part.added","output_index":0,"content_index":1,"part":{"type":"output_text","text":""}}`), completed}, true, false},
		{"conflicting_delta", [][]byte{[]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"different delivered text"}`), completed}, true, false},
		{"conflicting_text_done", [][]byte{[]byte(`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"different delivered text"}`), completed}, true, false},
		{"conflicting_item_identity", [][]byte{[]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_changed","type":"message","role":"assistant","status":"in_progress","content":[]}}`), completed}, true, false},
		{"unknown_added_event", [][]byte{[]byte(`{"type":"response.unfamiliar.added","output_index":0,"payload":"opaque"}`), completed}, true, false},
		{"conflicting_delta_id", [][]byte{[]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg_changed","delta":"fictional answer"}`), completed}, true, false},
		{"fractional_content_index", [][]byte{[]byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0.5,"delta":"fictional answer"}`), completed}, true, false},
		{"delta_after_item_done", [][]byte{done("0", bridgeHistoryUnitOutput), []byte(`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"fictional answer"}`), completed}, true, false},
		{"conflicting_added_content", [][]byte{[]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_unit","type":"message","role":"assistant","status":"in_progress","content":[]}}`), []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_unit","type":"message","role":"assistant","status":"in_progress","content":[{"type":"output_text","text":"different content"}]}}`), completed}, true, false},
		{"created_response_id_changed", [][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_other","status":"in_progress","output":[]}}`), completed}, true, false},
		{"unknown_item_field", [][]byte{bridgeHistoryTerminal("[" + strings.Replace(bridgeHistoryUnitOutput, `"role":"assistant"`, `"role":"assistant","opaque_state":"unfamiliar"`, 1) + "]")}, true, false},
		{"unknown_part_field", [][]byte{bridgeHistoryTerminal("[" + strings.Replace(bridgeHistoryUnitOutput, `"annotations":[]`, `"annotations":[],"opaque_state":"unfamiliar"`, 1) + "]")}, true, false},
		{"failed_response", [][]byte{[]byte(`{"type":"response.failed","response":{"id":"resp_unit","status":"failed"}}`)}, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h, c, a := bridgeHistoryUnit(t)
			source := bridgeHistoryCreate("fictional task", "", false)
			turn, err := h.prepare(c, a, source, source, "gpt-fixture", false)
			require.NoError(t, err)
			turn.sent = true
			for _, event := range test.events {
				turn.delivered(event)
			}
			turn.commit(context.Background(), test.success)
			if test.want {
				require.Equal(t, "resp_unit", h.response)
			} else {
				require.Empty(t, h.response)
				require.NotEmpty(t, h.unavailable)
			}
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryLimitsAndCancellation(t *testing.T) {
	for _, scenario := range []string{"bytes", "items", "process_budget", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			h, c, a := bridgeHistoryUnit(t)
			input := any("fictional task")
			if scenario == "bytes" {
				input = strings.Repeat("x", int(openAIWSBridgeHistoryMaxBytes))
			}
			if scenario == "items" {
				items := make([]any, openAIWSBridgeHistoryMaxItems+1)
				for i := range items {
					items[i] = map[string]string{"role": "user", "content": "fictional task"}
				}
				input = items
			}
			source := bridgeHistoryCreate(input, "", false)
			turn, err := h.prepare(c, a, source, source, "gpt-fixture", false)
			require.NoError(t, err)
			turn.sent = true
			var reserved int64
			if scenario == "process_budget" {
				reserved = openAIWSBridgeHistoryProcessMaxBytes - openAIWSBridgeHistoryBytes.Load()
				require.True(t, reserveOpenAIWSBridgeHistory(reserved))
			}
			turn.delivered(bridgeHistoryTerminal("[" + bridgeHistoryUnitOutput + "]"))
			ctx := context.Background()
			if scenario == "cancelled" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			turn.commit(ctx, true)
			if reserved > 0 {
				openAIWSBridgeHistoryBytes.Add(-reserved)
			}
			require.Empty(t, h.response)
			follow := bridgeHistoryUnitFollowup()
			_, err = h.prepare(c, a, follow, follow, "gpt-fixture", false)
			var refusal *OpenAIAgentRecoveryError
			require.ErrorAs(t, err, &refusal)
			reason := "bridge_history_limit"
			if scenario == "cancelled" {
				reason = "bridge_delivery_unconfirmed"
			}
			require.Equal(t, reason, refusal.Reason)
		})
	}
}

func TestOpenAIWSHTTPBridgeHistoryProtectedInputKeepsExistingGate(t *testing.T) {
	h, c, a := bridgeHistoryUnit(t)
	bridgeHistorySeedUnit(t, h, c, a)
	source := []byte(`{"type":"response.create","model":"gpt-fixture","previous_response_id":"resp_unit","input":[{"type":"agent_message","encrypted_content":"fixture-protected-content"}]}`)
	before := append([]byte(nil), source...)
	turn, err := h.prepare(c, a, source, source, "gpt-fixture", false)
	require.NoError(t, err)
	require.Nil(t, turn)
	require.Equal(t, before, source)
	require.Empty(t, h.response)
}

func TestOpenAIWSHTTPBridgeHistoryRetentionBudget(t *testing.T) {
	baseline := openAIWSBridgeHistoryBytes.Load()
	require.False(t, reserveOpenAIWSBridgeHistory(-1))
	const available int64 = 64 << 10
	held := openAIWSBridgeHistoryProcessMaxBytes - baseline - available
	require.True(t, reserveOpenAIWSBridgeHistory(held))
	defer openAIWSBridgeHistoryBytes.Add(-held)
	var ready, finished sync.WaitGroup
	release := make(chan struct{})
	var admitted atomic.Int64
	for range 192 {
		ready.Add(1)
		finished.Add(1)
		go func() {
			defer finished.Done()
			ok := reserveOpenAIWSBridgeHistory(4096)
			if ok {
				admitted.Add(1)
			}
			ready.Done()
			<-release
			if ok {
				openAIWSBridgeHistoryBytes.Add(-4096)
			}
		}()
	}
	ready.Wait()
	count, total := admitted.Load(), openAIWSBridgeHistoryBytes.Load()
	overflow := reserveOpenAIWSBridgeHistory(1)
	if overflow {
		openAIWSBridgeHistoryBytes.Add(-1)
	}
	close(release)
	finished.Wait()
	require.EqualValues(t, 16, count)
	require.Equal(t, openAIWSBridgeHistoryProcessMaxBytes, total)
	require.False(t, overflow)
	require.Equal(t, baseline+held, openAIWSBridgeHistoryBytes.Load())
}

func bridgeHistoryReadCompleted(t *testing.T, ctx context.Context, conn *websocket.Conn) []byte {
	t.Helper()
	_, event, err := conn.Read(ctx)
	require.NoError(t, err, "the first continuation must succeed on the existing socket")
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String(), "%s", event)
	return event
}

func TestOpenAIWSHTTPBridgeHistoryPointerFirstContinuation(t *testing.T) {
	conn, upstream, ctx, done := bridgeHistoryFixture(t, true, false)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"input":[{"role":"user","content":"Generate a fresh short code."}]}`)))
	first := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, upstream.code, gjson.GetBytes(first, "response.output.0.content.0.text").String())
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"previous_response_id":"resp_fixture_1","input":[{"role":"user","content":"Repeat your code exactly once."}]}`)))
	second := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, upstream.code, gjson.GetBytes(second, "response.output.0.content.0.text").String())
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
	require.NoError(t, <-done)
	require.Len(t, upstream.bodies, 2, "no retry or second handshake may be needed")
}

func TestOpenAIWSHTTPBridgeHistoryRetainsDeliveredAssistantText(t *testing.T) {
	// Isolate the independent output collector defect from the ownership gate.
	conn, upstream, ctx, done := bridgeHistoryFixture(t, false, true)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"input":[{"role":"user","content":"Explain and then call the fixture tool."}]}`)))
	bridgeHistoryReadCompleted(t, ctx, conn)
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-fixture","store":false,"previous_response_id":"resp_fixture_1","input":[{"type":"function_call_output","call_id":"call_fixture","output":"fixture result"}]}`)))
	second := bridgeHistoryReadCompleted(t, ctx, conn)
	require.Equal(t, upstream.code, gjson.GetBytes(second, "response.output.0.content.0.text").String(), "assistant text and the completed tool call must both survive")
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, "done"))
	require.NoError(t, <-done)
	require.Len(t, upstream.bodies, 2)
	input := gjson.GetBytes(upstream.bodies[1], "input").Array()
	require.Len(t, input, 4)
	var decoded []any
	require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(upstream.bodies[1], "input").Raw), &decoded))
}
