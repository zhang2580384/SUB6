package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"hash"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type openAIWSBridgeHistoryFragmentKey struct {
	item, part int
	kind       string
}

type openAIWSBridgeHistoryObservedItem struct {
	shape, added [32]byte
}

// Retain bounded digests rather than another copy of every streamed delta.
// Both the delivered deltas and any done value must match the retained item.
type openAIWSBridgeHistoryFragment struct {
	itemID                         string
	digest                         hash.Hash
	size                           int64
	added, hasDelta, done, hasPart bool
	complete, completePart         [32]byte
}

func openAIWSBridgeHistoryOutputKeysSafe(item gjson.Result) bool {
	kind := item.Get("type").String()
	safe := true
	item.ForEach(func(key, value gjson.Result) bool {
		field := key.String()
		switch field {
		case "id", "type", "status":
			safe = value.Type == gjson.String
		case "role", "phase":
			safe = (kind == "message" || kind == "") && value.Type == gjson.String
		case "content":
			safe = kind == "message" || kind == ""
		case "call_id", "name":
			safe = kind == "function_call" || kind == "custom_tool_call"
		case "arguments":
			safe = kind == "function_call"
		case "input":
			safe = kind == "custom_tool_call"
		default:
			safe = false
		}
		return safe
	})
	return safe
}

func openAIWSBridgeHistoryOutputPartSafe(part gjson.Result) bool {
	kind := part.Get("type").String()
	safe := part.IsObject() && (kind == "output_text" || kind == "refusal")
	if !safe {
		return false
	}
	part.ForEach(func(key, value gjson.Result) bool {
		switch key.String() {
		case "type":
		case "text":
			safe = kind == "output_text" && value.Type == gjson.String
		case "refusal":
			safe = kind == "refusal" && value.Type == gjson.String
		case "annotations", "logprobs":
			safe = value.IsArray() && len(value.Array()) == 0
		default:
			safe = false
		}
		return safe
	})
	return safe
}

func openAIWSBridgeHistoryStreamIndex(event gjson.Result, field string) (int, bool) {
	value := event.Get(field)
	index, err := strconv.Atoi(value.Raw)
	return index, value.Type == gjson.Number && err == nil && index >= 0 && index < openAIWSBridgeHistoryMaxItems
}

func openAIWSBridgeHistoryItemShape(raw []byte) [32]byte {
	var item map[string]json.RawMessage
	_ = json.Unmarshal(raw, &item)
	delete(item, "status")
	switch gjson.GetBytes(raw, "type").String() {
	case "message", "":
		delete(item, "content")
	case "function_call":
		delete(item, "arguments")
	case "custom_tool_call":
		delete(item, "input")
	}
	encoded, _ := json.Marshal(item)
	return sha256.Sum256(normalizeOpenAIWSJSONForCompareOrRaw(encoded))
}

func (t *openAIWSBridgeHistoryTurn) reserveStreamMetadata(n int64) bool {
	if t.charge+n > openAIWSBridgeHistoryMaxBytes || !reserveOpenAIWSBridgeHistory(n) {
		t.invalidate("bridge_history_limit")
		return false
	}
	t.charge += n
	return true
}

func (t *openAIWSBridgeHistoryTurn) streamFragment(event gjson.Result, kind string, part int) *openAIWSBridgeHistoryFragment {
	index, valid := openAIWSBridgeHistoryStreamIndex(event, "output_index")
	id := event.Get("item_id")
	if !valid || (id.Exists() && (id.Type != gjson.String || id.String() == "" || len(id.String()) > 512)) {
		t.invalidate("bridge_history_unsupported")
		return nil
	}
	key := openAIWSBridgeHistoryFragmentKey{index, part, kind}
	if _, complete := t.output[index]; complete {
		t.invalidate("bridge_history_unsupported")
		return nil
	}
	f := t.fragments[key]
	if f == nil {
		// Covers the hash state, map entry and an optional bounded item ID.
		if !t.reserveStreamMetadata(1024) {
			return nil
		}
		if t.fragments == nil {
			t.fragments = make(map[openAIWSBridgeHistoryFragmentKey]*openAIWSBridgeHistoryFragment)
		}
		f = &openAIWSBridgeHistoryFragment{digest: sha256.New()}
		t.fragments[key] = f
	}
	if id.Exists() {
		if f.itemID != "" && f.itemID != id.String() {
			t.invalidate("bridge_history_unsupported")
			return nil
		}
		f.itemID = strings.Clone(id.String())
	}
	return f
}

func (t *openAIWSBridgeHistoryTurn) streamValue(event gjson.Result, kind string, part int, value gjson.Result, action string) {
	if value.Type != gjson.String {
		t.invalidate("bridge_history_unsupported")
		return
	}
	f := t.streamFragment(event, kind, part)
	if f == nil {
		return
	}
	text := value.String()
	switch action {
	case "added":
		if f.added || f.hasDelta || f.done {
			t.invalidate("bridge_history_unsupported")
			return
		}
		f.added = true
		if text == "" {
			return
		}
		fallthrough
	case "delta":
		if f.done {
			t.invalidate("bridge_history_unsupported")
			return
		}
		f.size += int64(len(text))
		if f.size > openAIWSBridgeHistoryMaxBytes {
			t.invalidate("bridge_history_limit")
			return
		}
		_, _ = f.digest.Write([]byte(text))
		f.hasDelta = true
	case "done":
		sum := sha256.Sum256([]byte(text))
		if (f.done && f.complete != sum) || (f.hasDelta && (f.size != int64(len(text)) || !bytes.Equal(f.digest.Sum(nil), sum[:]))) {
			t.invalidate("bridge_history_unsupported")
			return
		}
		f.done, f.complete = true, sum
	}
}

func (t *openAIWSBridgeHistoryTurn) streamPart(event gjson.Result, part gjson.Result, action string) {
	index, valid := openAIWSBridgeHistoryStreamIndex(event, "content_index")
	kind := part.Get("type").String()
	field := "text"
	if kind == "refusal" {
		field = "refusal"
	}
	if !valid || !openAIWSBridgeHistoryOutputPartSafe(part) {
		t.invalidate("bridge_history_unsupported")
		return
	}
	t.streamValue(event, kind, index, part.Get(field), action)
	if action == "done" && !t.unsafe {
		f := t.streamFragment(event, kind, index)
		if f == nil {
			return
		}
		sum := sha256.Sum256(normalizeOpenAIWSJSONForCompareOrRaw([]byte(part.Raw)))
		if f.hasPart && f.completePart != sum {
			t.invalidate("bridge_history_unsupported")
			return
		}
		f.hasPart, f.completePart = true, sum
	}
}

func (t *openAIWSBridgeHistoryTurn) observeStreamEvent(message []byte, kind string) bool {
	event := gjson.ParseBytes(message)
	switch kind {
	case "response.completed", "response.done", "error", "response.failed", "response.incomplete", "response.cancelled":
		return true
	case "response.created", "response.in_progress", "response.queued":
		id := event.Get("response.id")
		output := event.Get("response.output")
		if !event.Get("response").IsObject() || (id.Exists() && (id.Type != gjson.String || id.String() == "" || len(id.String()) > 512)) ||
			(output.Exists() && !output.IsArray()) || len(output.Array()) != 0 || event.Get("response.error").Type != gjson.Null {
			t.invalidate("bridge_history_unsupported")
			return false
		}
		if id.Exists() {
			if t.response != "" && t.response != id.String() {
				t.invalidate("bridge_delivery_unconfirmed")
				return false
			}
			t.response = strings.Clone(id.String())
		}
		return true
	case "rate_limits.updated":
		return true
	case "response.output_item.done":
		t.semantic = true
		return true
	case "response.output_item.added":
		t.semantic = true
		index, valid := openAIWSBridgeHistoryStreamIndex(event, "output_index")
		item := event.Get("item")
		status := item.Get("status")
		_, alreadyComplete := t.output[index]
		if !valid || alreadyComplete || (status.Exists() && (status.Type != gjson.String || (status.String() != "in_progress" && status.String() != "completed"))) {
			t.invalidate("bridge_history_unsupported")
			return false
		}
		completed, err := sjson.SetBytes([]byte(item.Raw), "status", "completed")
		if err != nil || !openAIWSBridgeHistoryItemsSafe([]json.RawMessage{completed}, true) {
			t.invalidate("bridge_history_unsupported")
			return false
		}
		shape := openAIWSBridgeHistoryObservedItem{openAIWSBridgeHistoryItemShape(completed), sha256.Sum256(normalizeOpenAIWSJSONForCompareOrRaw([]byte(item.Raw)))}
		if old, ok := t.observed[index]; ok {
			if old != shape {
				t.invalidate("bridge_history_unsupported")
			}
			return !t.unsafe
		}
		if !t.reserveStreamMetadata(128) {
			return false
		}
		if t.observed == nil {
			t.observed = make(map[int]openAIWSBridgeHistoryObservedItem)
		}
		t.observed[index] = shape
		switch item.Get("type").String() {
		case "message", "":
			for part, value := range item.Get("content").Array() {
				indexed, _ := sjson.SetBytes(message, "content_index", part)
				t.streamPart(gjson.ParseBytes(indexed), value, "added")
			}
		case "function_call":
			t.streamValue(event, "function_call", -1, item.Get("arguments"), "added")
		case "custom_tool_call":
			t.streamValue(event, "custom_tool_call", -1, item.Get("input"), "added")
		}
	case "response.content_part.added", "response.content_part.done":
		t.semantic = true
		action := "added"
		if strings.HasSuffix(kind, ".done") {
			action = "done"
		}
		t.streamPart(event, event.Get("part"), action)
	case "response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done":
		t.semantic = true
		index, valid := openAIWSBridgeHistoryStreamIndex(event, "content_index")
		if !valid {
			t.invalidate("bridge_history_unsupported")
			return false
		}
		partKind, field := "output_text", "text"
		if strings.Contains(kind, ".refusal.") {
			partKind, field = "refusal", "refusal"
		}
		action := "done"
		if strings.HasSuffix(kind, ".delta") {
			action, field = "delta", "delta"
		}
		t.streamValue(event, partKind, index, event.Get(field), action)
	case "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
		t.semantic = true
		partKind, field := "function_call", "arguments"
		if strings.Contains(kind, ".custom_tool_call_input.") {
			partKind, field = "custom_tool_call", "input"
		}
		action := "done"
		if strings.HasSuffix(kind, ".delta") {
			action, field = "delta", "delta"
		}
		t.streamValue(event, partKind, -1, event.Get(field), action)
	default:
		t.invalidate("bridge_history_unsupported")
	}
	return !t.unsafe
}

func (t *openAIWSBridgeHistoryTurn) streamMatchesOutput() bool {
	for index, observed := range t.observed {
		if item, ok := t.output[index]; !ok || openAIWSBridgeHistoryItemShape(item) != observed.shape {
			return false
		}
	}
	for key, f := range t.fragments {
		raw, ok := t.output[key.item]
		if !ok {
			return false
		}
		item := gjson.ParseBytes(raw)
		if f.itemID != "" && item.Get("id").String() != f.itemID {
			return false
		}
		var value gjson.Result
		if key.part >= 0 {
			part := item.Get("content").Get(strconv.Itoa(key.part))
			if (item.Get("type").String() != "message" && item.Get("type").String() != "") || part.Get("type").String() != key.kind {
				return false
			}
			field := "text"
			if key.kind == "refusal" {
				field = "refusal"
			}
			value = part.Get(field)
			if f.hasPart && sha256.Sum256(normalizeOpenAIWSJSONForCompareOrRaw([]byte(part.Raw))) != f.completePart {
				return false
			}
		} else {
			if item.Get("type").String() != key.kind {
				return false
			}
			field := "arguments"
			if key.kind == "custom_tool_call" {
				field = "input"
			}
			value = item.Get(field)
		}
		if value.Type != gjson.String {
			return false
		}
		text := value.String()
		sum := sha256.Sum256([]byte(text))
		if (f.hasDelta && (f.size != int64(len(text)) || !bytes.Equal(f.digest.Sum(nil), sum[:]))) || (f.done && f.complete != sum) {
			return false
		}
	}
	return true
}
