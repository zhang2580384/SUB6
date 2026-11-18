//go:build unit

package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// A real WebSocket client follows its tool result with a scheduled event on
// the same owned conversation. The HTTP provider is local and deterministic.
func TestCodexHostTurnThroughWSHTTPBridge(t *testing.T) {
	const task = "<codex_delegation><source_thread_id>fixture</source_thread_id><input>Count the fixture.</input></codex_delegation>"
	const tick = "<heartbeat><automation_id>fixture</automation_id></heartbeat>"
	type item = map[string]any
	host := func(name, out string) item {
		return item{"type": "function_call_output", "namespace": "codex_app", "name": name, "output": out}
	}
	observed := make(chan []byte, 6)
	var sent atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		observed <- body
		n := sent.Add(1)
		output := fmt.Sprintf(`[{"type":"message","role":"assistant","status":"completed","id":"msg_fixture_%d","content":[{"type":"output_text","text":"fixture","annotations":[]}]}]`, n)
		items := gjson.GetBytes(body, "input").Array()
		if len(items) > 0 && items[len(items)-1].Get("content.0.text").String() == task {
			output = `[{"type":"function_call","name":"read_fixture","call_id":"call_fixture","id":"fc_fixture","status":"completed","arguments":"{}"}]`
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture_%d\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":%s,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", n, output)
	}))
	defer up.Close()
	cache := repository.NewGatewayCache(agentContinuationTestRedis(t, "miniredis"))
	h := newOpenAIWSPassthroughHandlerHarnessWithKey(t, up.URL, http.Header{"Session-Id": {"fixture-session"}}, nil, cache, func(k *service.APIKey) { k.AllowLongContext = true }, func(a *service.Account, c *config.Config, _ *service.HTTPUpstream, _ *service.AccountRepository) {
		a.Extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModeHTTPBridge
		a.Extra["codex_fingerprint_mode"] = "off"
		c.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1 << 30
	})
	send := func(input []item, previous string) (gjson.Result, []gjson.Result) {
		frame := streamIsolationPayload("", "fixture-thread", "", previous)
		frame, err := sjson.SetBytes(frame, "input", input)
		require.NoError(t, err)
		frame, err = sjson.SetBytes(frame, "tools", []item{{"type": "function", "name": "read_fixture", "parameters": item{"type": "object"}}})
		require.NoError(t, err)
		streamIsolationSend(t, h.clientConn, frame)
		reply := gjson.ParseBytes(streamIsolationTerminal(t, h.clientConn))
		select {
		case b := <-observed:
			return reply, gjson.GetBytes(b, "input").Array()
		case <-time.After(time.Second):
			t.Fatal("missing upstream POST")
			return reply, nil
		}
	}
	prior := host("automation_update", tick)
	first, seed := send([]item{prior, host("send_message_to_thread", task)}, "")
	require.Len(t, seed, 2)
	priorJSON, _ := json.Marshal(prior)
	require.JSONEq(t, string(priorJSON), seed[0].Raw)
	require.Equal(t, "user", seed[1].Get("role").String())
	require.Equal(t, task, seed[1].Get("content.0.text").String())
	require.Equal(t, "call_fixture", first.Get("response.output.0.call_id").String())
	require.Equal(t, "read_fixture", first.Get("response.output.0.name").String())
	dir := t.TempDir()
	for _, name := range []string{"sample-a", "sample-b"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0600))
	}
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	second, results := send([]item{{"type": "function_call_output", "call_id": "call_fixture", "output": fmt.Sprintf("%d files", len(files))}}, first.Get("response.id").String())
	require.JSONEq(t, string(priorJSON), results[0].Raw)
	require.Equal(t, "2 files", results[len(results)-1].Get("output").String())
	third, continued := send([]item{host("automation_update", tick)}, second.Get("response.id").String())
	require.Equal(t, tick, continued[len(continued)-1].Get("content.0.text").String())
	require.Equal(t, "message", third.Get("response.output.0.type").String())
	_, replay := send([]item{host("send_message_to_thread", task), {"role": "user", "content": "Say hello."}}, "")
	want, _ := json.Marshal(host("send_message_to_thread", task))
	require.JSONEq(t, string(want), replay[0].Raw)
	require.Equal(t, "Say hello.", replay[len(replay)-1].Get("content").String())
	require.EqualValues(t, 4, sent.Load())
	// A recognized event cannot satisfy a real pending call. The rejection
	// must occur without opening another upstream request.
	pending, _ := send([]item{host("send_message_to_thread", task)}, "")
	frame := streamIsolationPayload("", "fixture-thread", "", pending.Get("response.id").String())
	frame, err = sjson.SetBytes(frame, "input", []item{prior})
	require.NoError(t, err)
	streamIsolationSend(t, h.clientConn, frame)
	failure := gjson.ParseBytes(streamIsolationRead(t, h.clientConn))
	require.Equal(t, "error", failure.Get("type").String())
	require.Equal(t, "encrypted_agent_recovery_bridge_history_unsupported", failure.Get("error.code").String())
	require.EqualValues(t, 5, sent.Load())
}

func TestCodexBridgeHostProjectionRequiresSameClientEvent(t *testing.T) {
	const tick = `<heartbeat><automation_id>fixture</automation_id></heartbeat>`
	const next = `<heartbeat><automation_id>other</automation_id></heartbeat>`
	wire := func(out string) []byte {
		b, err := json.Marshal(map[string]any{"input": []any{map[string]string{"type": "function_call_output", "namespace": "codex_app", "name": "automation_update", "output": out}}})
		require.NoError(t, err)
		return b
	}
	a, b := wire(tick), wire(next)
	for _, src := range [][]byte{b, append([]byte(`{"input":[],`), a[1:]...)} {
		got, changed := normalizeCodexBridgeHostTurn(src, a)
		require.False(t, changed)
		require.Equal(t, a, got)
	}
	claimed, err := sjson.SetBytes(a, "input.0.call_id", "call_fixture")
	require.NoError(t, err)
	got, changed := normalizeCodexBridgeHostTurn(claimed, claimed)
	require.False(t, changed)
	require.Equal(t, claimed, got)
}
