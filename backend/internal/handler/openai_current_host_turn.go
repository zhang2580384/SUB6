package handler

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// These are standalone client inputs, not responses to model-issued calls.
// Preserve the host envelope (including its origin), never extract its prompt
// or synthesize human approval. Transport adaptation does not execute tools.
func codexHostEnvelope(item gjson.Result) bool {
	if item.Get("type").String() != "function_call_output" {
		return false
	}
	id := item.Get("call_id")
	if id.Exists() && (id.Type != gjson.String || strings.TrimSpace(id.String()) != "") {
		return false
	}
	// Do not reinterpret a payload that mixes the host wire shape with
	// a message role, arguments, or other tool metadata.
	fieldsOK := true
	item.ForEach(func(k, v gjson.Result) bool {
		switch k.String() {
		case "type", "namespace", "name", "output", "call_id":
		case "id":
			fieldsOK = v.Type == gjson.String
		default:
			fieldsOK = false
		}
		return fieldsOK
	})
	if !fieldsOK {
		return false
	}
	ns, name := item.Get("namespace").String(), item.Get("name").String()
	out := item.Get("output")
	if out.Type != gjson.String {
		return false
	}
	if isCodexDelegationTool(ns, name) {
		return validCodexDelegationEnvelope(out.String())
	}
	return ns == "codex_app" && name == "automation_update" && (validCodexAutomationBootstrap(out.String()) || validCodexAutomationHeartbeat(out.String()))
}

func mayContainCodexHostTurn(body []byte) bool {
	if bytes.Contains(body, []byte(`\u`)) {
		return true
	}
	return bytes.Contains(body, []byte("function_call_output")) && (bytes.Contains(body, []byte("codex_delegation")) || bytes.Contains(body, []byte("<heartbeat")) || bytes.Contains(body, []byte("Automation: ")))
}

// Only the last input can start the current turn. Replaying the output of
// this function is a no-op, even when another host item precedes the last one.
func normalizeCodexCurrentHostTurn(body []byte) ([]byte, bool) {
	if !mayContainCodexHostTurn(body) {
		return body, false
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false
	}
	var last gjson.Result
	n := 0
	input.ForEach(func(_, v gjson.Result) bool { last = v; n++; return true })
	if !codexHostEnvelope(last) || !hasUniqueJSONMembers(body) {
		return body, false
	}
	previous := gjson.GetBytes(body, "previous_response_id")
	if previous.Exists() && previous.Type != gjson.String {
		return body, false
	}
	ambiguous := false
	input.ForEach(func(_, v gjson.Result) bool {
		typ := v.Get("type").String()
		switch {
		case typ == "item_reference":
			ambiguous = !nonemptyHostWireID(v.Get("id"))
		case strings.HasSuffix(typ, "_call") || isResponsesCallOutputType(typ):
			ambiguous = !nonemptyHostWireID(v.Get("call_id")) && !codexHostEnvelope(v)
		}
		return !ambiguous
	})
	if ambiguous {
		return body, false
	}
	message := map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": last.Get("output").String()}}}
	updated, err := sjson.SetBytes(body, "input."+strconv.Itoa(n-1), message)
	if err != nil {
		return body, false
	}
	return updated, true
}

func nonemptyHostWireID(v gjson.Result) bool {
	return v.Type == gjson.String && strings.TrimSpace(v.String()) != ""
}

// Only the HTTP pairing validator sees this view. The original transcript,
// including historical host envelopes, still goes upstream byte-for-byte.
func codexHistoryPairingView(body []byte) []byte {
	if !mayContainCodexHostTurn(body) {
		return nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return nil
	}
	items := input.Array()
	trimmed := false
	for i := 0; i+1 < len(items); i++ {
		if codexHostEnvelope(items[i]) {
			trimmed = true
			break
		}
	}
	if !trimmed || !hasUniqueJSONMembers(body) {
		return nil
	}
	raw := make([]json.RawMessage, 0, len(items))
	for i, v := range items {
		if i+1 < len(items) && codexHostEnvelope(v) {
			continue
		}
		raw = append(raw, json.RawMessage(v.Raw))
	}
	view, err := json.Marshal(struct {
		Input []json.RawMessage `json:"input"`
	}{raw})
	if err != nil {
		return nil
	}
	return view
}
