package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func TestHTTPCurrentCodexHostTurn(t *testing.T) {
	type item = map[string]any
	const task = "<codex_delegation><source_thread_id>fixture-source</source_thread_id><input>Count the sample files.</input></codex_delegation>"
	const clock = "<heartbeat><automation_id>example</automation_id></heartbeat>"
	host := func(name, text string) item {
		return item{"type": "function_call_output", "namespace": "codex_app", "name": name, "output": text}
	}
	delegation, schedule := host("send_message_to_thread", task), host("automation_update", clock)
	human := item{"role": "user", "content": "Show the sample count."}
	call := item{"type": "function_call", "name": "read_fixture", "call_id": "call_fixture"}
	result := item{"type": "function_call_output", "output": "fixture", "call_id": "call_fixture"}
	for _, tt := range []struct {
		name     string
		input    []item
		previous bool
		want     string
		valid    bool
	}{
		{"schedule then delegation", []item{schedule, human, delegation}, false, task, true},
		{"delegation then schedule", []item{delegation, human, schedule}, false, clock, true},
		{"continued schedule", []item{schedule}, true, clock, true},
		{"history of same kind", []item{delegation, human, delegation}, false, task, true},
		{"actual tool history and new task", []item{delegation, call, result, schedule}, false, clock, true},
		{"human after both", []item{delegation, schedule, human}, false, "", true},
		{"call result after delegation", []item{delegation, call, result}, false, "", true},
		{"ordinary tool only", []item{host("read_fixture", task)}, false, "", false},
		{"mixed role envelope", []item{{"type": "function_call_output", "role": "user", "namespace": "codex_app", "name": "send_message_to_thread", "output": task}}, false, "", false},
		{"malformed history is not authorization", []item{host("read_fixture", task), schedule}, false, "", false},
		{"real tool id", []item{call, {"type": "function_call_output", "call_id": "call_fixture", "namespace": "codex_app", "name": "send_message_to_thread", "output": task}}, false, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := item{"model": "fixture", "input": tt.input}
			if tt.previous {
				req["previous_response_id"] = "resp_fixture"
			}
			body, err := json.Marshal(req)
			require.NoError(t, err)
			got, changed := normalizeCodexCurrentHostTurn(body)
			require.Equal(t, tt.want != "", changed)
			items := gjson.GetBytes(got, "input").Array()
			require.Len(t, items, len(tt.input))
			for i := range tt.input {
				if i == len(items)-1 && changed {
					require.Equal(t, "user", items[i].Get("role").String())
					require.Equal(t, tt.want, items[i].Get("content.0.text").String())
				} else {
					prior, _ := json.Marshal(tt.input[i])
					require.JSONEq(t, string(prior), items[i].Raw)
				}
			}
			second, changedAgain := normalizeCodexCurrentHostTurn(got)
			require.False(t, changedAgain)
			require.Equal(t, got, second)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			require.Equal(t, tt.valid, (&OpenAIGatewayHandler{}).validateFunctionCallOutputRequest(c, got, zap.NewNop()))
		})
	}
}
