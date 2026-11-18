package service

import (
	"bytes"
	"math"
	"strconv"

	"github.com/tidwall/gjson"
)

const bridgeItemInternalMetadata = "internal_chat_message_metadata_passthrough"

func bridgeHasItemMetadata(item gjson.Result) bool {
	return item.Get("metadata").Exists() || item.Get(bridgeItemInternalMetadata).Exists()
}

// Only these two numeric fields may be added while an assistant item streams.
// All previously delivered fields (including either number, if present) stay fixed.
type bridgeAddedNumbers uint8

const (
	bridgeCreateTime bridgeAddedNumbers = 1 << iota
	bridgeContentCount
)

func bridgeItemNumbers(item gjson.Result) (bits bridgeAddedNumbers) {
	internal := item.Get(bridgeItemInternalMetadata)
	if internal.Get("create_time").Exists() {
		bits |= bridgeCreateTime
	}
	if internal.Get("content_token_count").Exists() {
		bits |= bridgeContentCount
	}
	return bits
}

func bridgeKnownItemMetadata(item gjson.Result) bool {
	if item.Get("type").Str != "message" || item.Get("role").Str != "assistant" || item.Get("id").Str == "" {
		return false
	}
	meta, internal := item.Get("metadata"), item.Get(bridgeItemInternalMetadata)
	if !meta.IsObject() || !internal.IsObject() || len(meta.Raw) > 1024 || len(internal.Raw) > 2048 {
		return false
	}
	id := meta.Get("turn_id")
	if id.Type != gjson.String || len(id.Str) == 0 || len(id.Str) > 128 || internal.Get("turn_id").Type != gjson.String || internal.Get("turn_id").Str != id.Str {
		return false
	}
	for _, b := range id.Str {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	ok := true
	meta.ForEach(func(k, _ gjson.Result) bool { ok = k.Str == "turn_id"; return ok })
	if !ok {
		return false
	}
	internal.ForEach(func(k, v gjson.Result) bool {
		switch k.Str {
		case "turn_id":
		case "create_time":
			n, e := strconv.ParseFloat(v.Raw, 64)
			ok = v.Type == gjson.Number && e == nil && n >= 0 && !math.IsInf(n, 0) && !math.IsNaN(n)
		case "content_token_count":
			_, e := strconv.ParseUint(v.Raw, 10, 63)
			ok = v.Type == gjson.Number && e == nil
		default:
			ok = false
		}
		return ok
	})
	return ok
}

// This provider notice describes moderation of the current response. It is
// delivered normally, never added to model input, and cannot prove completion
// or override a denial. Accept only its scoped, non-output protocol envelope.
func (t *openAIWSBridgeHistoryTurn) acceptsModerationNotice(event gjson.Result) bool {
	if !t.apiKeyMetadata || t.response == "" || t.completed || len(event.Raw) > 16<<10 {
		return false
	}
	id, seq, meta := event.Get("response_id"), event.Get("sequence_number"), event.Get("metadata")
	_, e := strconv.ParseUint(seq.Raw, 10, 63)
	if id.Type != gjson.String || id.Str != t.response || seq.Type != gjson.Number || e != nil || !meta.IsObject() {
		return false
	}
	allowed := func(obj gjson.Result, fn func(string, gjson.Result) bool) bool {
		ok := obj.IsObject()
		if ok {
			obj.ForEach(func(k, v gjson.Result) bool { ok = fn(k.Str, v); return ok })
		}
		return ok
	}
	if !allowed(event, func(k string, _ gjson.Result) bool {
		return k == "type" || k == "sequence_number" || k == "response_id" || k == "metadata"
	}) {
		return false
	}
	report := meta.Get("openai_chatgpt_moderation_metadata")
	return report.IsObject() && allowed(meta, func(k string, _ gjson.Result) bool { return k == "openai_chatgpt_moderation_metadata" }) && allowed(report, func(k string, v gjson.Result) bool {
		switch k {
		case "prompt", "generation", "tool_call", "tool_response":
			return allowed(v, func(engine string, info gjson.Result) bool { return engine == "omnimod" && info.IsObject() })
		default:
			return false
		}
	})
}

// The compatibility adapter runs before freezeInput. Verify the exact historic
// decorated messages again on the actual outgoing body, not just its projection.
func (h *openAIWSHTTPBridgeHistory) metadataSurvives(body []byte) bool {
	if !h.hasDecorated {
		return true
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	list := input.Array()
	if len(list) < len(h.items) {
		return false
	}
	for i, raw := range h.items {
		item := gjson.ParseBytes(raw)
		if item.Get("role").Str == "assistant" && bridgeHasItemMetadata(item) && !bytes.Equal(normalizeOpenAIWSJSONForCompareOrRaw(raw), normalizeOpenAIWSJSONForCompareOrRaw([]byte(list[i].Raw))) {
			return false
		}
	}
	return true
}
