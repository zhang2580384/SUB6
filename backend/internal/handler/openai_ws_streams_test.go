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
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
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
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_valid\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
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
					if write(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`, "resp_"+text)) != nil {
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
