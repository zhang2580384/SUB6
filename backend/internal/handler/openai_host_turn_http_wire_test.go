//go:build unit

package handler

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A deterministic upstream and a read-only client tool exercise the actual
// Responses handler and its SSE transport without a live credential or model.
type fixtureHostUpstream struct {
	service.HTTPUpstream
	bodies [][]byte
}

func (u *fixtureHostUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.bodies = append(u.bodies, body)
	items := gjson.GetBytes(body, "input").Array()
	last := items[len(items)-1]
	output := "[]"
	if last.Get("role").String() == "user" && strings.Contains(last.Get("content").Raw, "Count the fixture") {
		output = `[{"type":"function_call","name":"count_fixture","call_id":"call_fixture_count","arguments":"{}"}]`
	}
	resp := fmt.Sprintf(`{"id":"resp_fixture_host_%d","object":"response","status":"completed","model":"gpt-5.1","output":%s,"usage":{"input_tokens":4,"output_tokens":1}}`, len(u.bodies), output)
	payload := "event: response.completed\ndata: " + `{"type":"response.completed","response":` + resp + "}\n\ndata: [DONE]\n\n"
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
}

func TestHTTPCurrentHostTurnThroughGateway(t *testing.T) {
	accounts := issue715AccountsOfType(service.AccountTypeAPIKey)
	up := &fixtureHostUpstream{}
	h := newOpenAIResponsesFailoverTestHandler(t, up, accounts[0])
	const handoff = "<codex_delegation><source_thread_id>fixture</source_thread_id><input>Count the fixture.</input></codex_delegation>"
	const clock = "<heartbeat><automation_id>example</automation_id><current_time_iso>2026-11-10T01:00:00Z</current_time_iso><instructions>Count the fixture.</instructions></heartbeat>"
	host := func(name, out string) any {
		return map[string]any{"type": "function_call_output", "namespace": "codex_app", "name": name, "output": out, "id": "fixture-event", "internal_chat_message_metadata_passthrough": map[string]any{"turn_id": "00000000-0000-4000-8000-000000000001", "create_time": 1.25}}
	}
	deleg, sched := host("send_message_to_thread", handoff), host("automation_update", clock)
	tool := map[string]any{"type": "function", "name": "count_fixture", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}
	fixtureDir := t.TempDir()
	for _, name := range []string{"sample-a", "sample-b"} {
		require.NoError(t, os.WriteFile(filepath.Join(fixtureDir, name), nil, 0600))
	}
	reads := 0
	for _, tc := range []struct {
		name  string
		input []any
		read  bool
	}{
		{"direct", []any{map[string]any{"role": "user", "content": "Count the fixture."}}, true},
		{"new delegation", []any{sched, deleg}, true}, {"new heartbeat", []any{deleg, sched}, true},
		{"new human unrelated to old task", []any{sched, deleg, map[string]any{"role": "user", "content": "Say hello."}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sent := len(up.bodies)
			status, ans := issue715Request(t, h, httpRebuildJSON(map[string]any{"model": "gpt-5.1", "stream": true, "tools": []any{tool}, "input": tc.input}), 99, "fixture-host-session", false)
			require.Equal(t, 200, status, ans)
			require.Len(t, up.bodies, sent+1)
			before := reads
			completed := false
			for _, line := range strings.Split(ans, "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				ev := gjson.Parse(strings.TrimPrefix(line, "data: "))
				if ev.Get("type").String() != "response.completed" {
					continue
				}
				completed = true
				for _, call := range ev.Get("response.output").Array() {
					if call.Get("type").String() == "function_call" {
						require.Equal(t, "count_fixture", call.Get("name").String())
						files, err := os.ReadDir(fixtureDir)
						require.NoError(t, err)
						require.Len(t, files, 2)
						reads++
					}
				}
			}
			require.True(t, completed)
			if tc.read {
				require.Equal(t, before+1, reads)
			} else {
				require.Equal(t, before, reads)
			}
		})
	}
}
