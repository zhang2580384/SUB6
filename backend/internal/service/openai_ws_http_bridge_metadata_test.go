package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestBridgeScopedMessageMetadata(t *testing.T) {
	for _, scenario := range []string{"valid", "valid_existing_number", "notice_only", "item_only", "adapter_strips_field", "oauth", "notice_before_created", "foreign_notice", "extra_notice_field", "unknown_notice", "unknown_item_field", "turn_changed", "numeric_changed", "missing_at_done", "missing_added", "missing_done", "missing_terminal_output", "different_terminal", "policy_denial", "wrong_pointer"} {
		t.Run(scenario, func(t *testing.T) {
			h, c, a := bridgeHistoryUnit(t)
			ctx := context.Background()
			if scenario == "oauth" {
				a.Type = AccountTypeOAuth
			}
			source := bridgeHistoryCreate("fictional task", "", false)
			turn, err := h.prepare(c, a, source, source, "gpt-fixture", false)
			require.NoError(t, err)
			defer turn.close()
			body, err := prepareOpenAIWSHTTPBridgeBody(a, turn.payload)
			require.NoError(t, err)
			turn.freezeInput(body)
			_, err = turn.authorize(ctx, c, a, source, body, bridgeHistoryUnitRequest(t, body), "")
			require.NoError(t, err)
			send := func(v any) { b, e := json.Marshal(v); require.NoError(t, e); turn.delivered(b) }
			item := func(stage int) map[string]any {
				m := map[string]any{}
				require.NoError(t, json.Unmarshal([]byte(bridgeHistoryUnitOutput), &m))
				if stage == 0 {
					m["status"] = "in_progress"
					m["content"] = []any{}
				}
				if scenario == "notice_only" {
					return m
				}
				id := "fictional-turn"
				if scenario == "turn_changed" && stage > 0 {
					id = "another-turn"
				}
				m["metadata"] = map[string]any{"turn_id": id}
				d := map[string]any{"turn_id": id}
				m[bridgeItemInternalMetadata] = d
				if stage > 0 || scenario == "valid_existing_number" || scenario == "numeric_changed" || scenario == "missing_at_done" {
					d["create_time"] = 12.25
				}
				if stage > 0 {
					d["content_token_count"] = 2
				}
				if scenario == "numeric_changed" && stage == 0 {
					d["create_time"] = 1
				}
				if scenario == "missing_at_done" && stage > 0 {
					delete(d, "create_time")
				}
				if scenario == "unknown_item_field" {
					d["unrecognized"] = true
				}
				if scenario == "different_terminal" && stage == 2 {
					d["content_token_count"] = 3
				}
				return m
			}
			notice := map[string]any{"type": "response.metadata", "response_id": "resp_unit", "sequence_number": 3, "metadata": map[string]any{"openai_chatgpt_moderation_metadata": map[string]any{"prompt": map[string]any{"omnimod": map[string]any{}}}}}
			if scenario == "foreign_notice" {
				notice["response_id"] = "resp_other"
			}
			if scenario == "extra_notice_field" {
				notice["output"] = []any{}
			}
			if scenario == "unknown_notice" {
				notice["metadata"] = map[string]any{"unknown": true}
			}
			if scenario == "notice_before_created" {
				send(notice)
			}
			send(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_unit", "output": []any{}}})
			if scenario != "missing_added" {
				send(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item(0)})
			}
			if scenario != "item_only" {
				send(notice)
			}
			if scenario != "missing_done" {
				send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item(1)})
			}
			output := []any{item(2)}
			if scenario == "missing_terminal_output" {
				output = nil
			}
			if scenario == "policy_denial" {
				send(map[string]any{"type": "response.failed", "response": map[string]any{"id": "resp_unit", "status": "failed"}})
			} else if output == nil {
				turn.delivered(bridgeHistoryTerminal("[]"))
			} else {
				b, e := json.Marshal(output)
				require.NoError(t, e)
				turn.delivered(bridgeHistoryTerminal(string(b)))
			}
			turn.commit(ctx, true)
			id := "resp_unit"
			if scenario == "wrong_pointer" {
				id = "resp_other"
			}
			next := bridgeHistoryCreate("repeat the code", id, false)
			follow, err := h.prepare(c, a, next, next, "gpt-fixture", false)
			switch scenario {
			case "valid", "valid_existing_number", "notice_only", "item_only", "adapter_strips_field":
			default:
				require.Error(t, err)
				require.Nil(t, follow)
				return
			}
			require.NoError(t, err)
			defer follow.close()
			require.NoError(t, h.service.getOpenAIWSStateStore().BindResponseAccount(ctx, 912, h.response, a.ID, time.Minute))
			second, e := prepareOpenAIWSHTTPBridgeBody(a, follow.payload)
			require.NoError(t, e)
			expected, e := json.Marshal(item(2))
			require.NoError(t, e)
			require.JSONEq(t, string(expected), gjson.GetBytes(second, "input.1").Raw)
			if scenario == "adapter_strips_field" {
				second, e = sjson.DeleteBytes(second, "input.1."+bridgeItemInternalMetadata)
				require.NoError(t, e)
			}
			follow.freezeInput(second)
			_, e = follow.authorize(ctx, c, a, next, second, bridgeHistoryUnitRequest(t, second), "")
			if scenario == "adapter_strips_field" {
				require.Error(t, e)
				require.False(t, follow.sent)
			} else {
				require.NoError(t, e)
				require.True(t, follow.sent)
			}
		})
	}
}
