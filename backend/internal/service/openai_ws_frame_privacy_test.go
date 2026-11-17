package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func TestOpenAIWSFrameIdentityAndErrorPrivacy(t *testing.T) {
	for _, raw := range []string{`{"response_id":"a","response":{"id":"b"}}`, `{"response_id":"a","response_id":"a"}`, `{"response":{},"response":{"id":"a"}}`, `{"response":{"id":"a","id":"a"}}`, `{"type":"error","type":"response.completed"}`, `{"stream_id":"a","stream_id":"b"}`, `{"response_id":null}`, `{"response_id":" a"}`, `not-json`} {
		_, ok := OpenAIWSFrameResponseID([]byte(raw))
		require.False(t, ok, raw)
	}
	for _, raw := range []string{`{"id":"event","item_id":"item"}`, `{"response":{"id":"owned"},"response_id":"owned","id":"event"}`} {
		id, ok := OpenAIWSFrameResponseID([]byte(raw))
		require.True(t, ok)
		require.Equal(t, gjson.Get(raw, "response.id").String(), id)
	}
	for _, code := range []string{"cyber_policy", "workspace_suspended", "undelivered_timeout", "private-fixture"} {
		for _, kind := range []string{"error", "response.failed"} {
			diag := map[string]string{"type": "private-fixture", "message": "private-fixture", "param": "private-fixture", "code": code}
			event := map[string]any{"type": kind, "status": 422, "error": diag, "response_id": "fixture", "metadata": "private-fixture", "response": map[string]any{"id": "fixture", "metadata": "private-fixture", "model": "private-fixture", "output": []string{"private-fixture"}, "error": diag}}
			raw, err := json.Marshal(event)
			require.NoError(t, err)
			out := publicOpenAIWSUpstreamEvent(raw, "request-model")
			require.NotContains(t, string(out), "private-fixture")
			want := code
			if code == "private-fixture" {
				want = "upstream_error"
			}
			key := "error.code"
			if kind == "response.failed" {
				key = "response.error.code"
				require.JSONEq(t, `[]`, gjson.GetBytes(out, "response.output").Raw)
				require.Positive(t, gjson.GetBytes(out, "response.created_at").Int())
				require.Equal(t, "request-model", gjson.GetBytes(out, "response.model").String())
			}
			require.Equal(t, want, gjson.GetBytes(out, key).String())
			require.Equal(t, int64(422), gjson.GetBytes(out, "status").Int())
		}
	}
	normal := []byte(`{"type":"response.output_text.delta","delta":"ordinary fixture"}`)
	require.Equal(t, normal, publicOpenAIWSUpstreamEvent(normal, ""))
}
func TestOpenAIWSHTTPBridgeHistoryToolPartBoundaries(t *testing.T) {
	good := `{"type":"input_text","text":"fixture"}`
	item := func(kind, output string) json.RawMessage {
		return json.RawMessage(`{"type":"` + kind + `","call_id":"call_fixture","output":` + output + `}`)
	}
	for _, out := range []string{`[]`, `null`, `3`, `{}`, `[{"type":"input_image","image_url":"fixture"}]`, `[{"type":"input_file","file_id":"fixture"}]`, `[{"type":"unknown","text":"x"}]`, `[` + good + `,{"type":"input_image"}]`, `[{"type":"input_text","text":3}]`, `[{"type":"input_text","text":"a","text":"b"}]`, `[{"type":"input_text","text":"a","extra":1}]`, `[` + strings.Repeat(good+",", openAIWSBridgeHistoryMaxItems) + good + `]`} {
		require.False(t, openAIWSBridgeHistoryItemsSafe([]json.RawMessage{item("function_call_output", out)}, false))
	}
	parts := "[" + good + "]"
	result := item("function_call_output", parts)
	require.True(t, openAIWSBridgeHistoryItemsSafe([]json.RawMessage{result}, false))
	require.False(t, openAIWSBridgeHistoryItemsSafe([]json.RawMessage{item("custom_tool_call_output", parts)}, false))
	require.True(t, openAIWSBridgeHistoryItemsSafe([]json.RawMessage{item("custom_tool_call_output", `"fixture"`)}, false))
	call := `{"type":"function_call","name":"lookup","arguments":"{}","call_id":"call_fixture"}`
	for _, input := range []string{string(result), call + "," + string(result) + "," + string(result)} {
		require.False(t, openAIWSCompletedToolInputs([]byte(`{"input":[`+input+`]}`)))
	}
}
