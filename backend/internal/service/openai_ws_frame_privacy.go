package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// Only response identity fields bind a downstream frame. An event or item ID
// names a different object and cannot confer response ownership.
func OpenAIWSFrameResponseID(body []byte) (string, bool) {
	if !gjson.ValidBytes(body) || !utf8.Valid(body) {
		return "", false
	}
	obj := gjson.ParseBytes(body)
	if !obj.IsObject() {
		return "", false
	}
	var seen uint8
	id := ""
	valid := true
	accept := func(v gjson.Result) bool {
		if v.Type != gjson.String || v.Str == "" || len(v.Str) > 512 || strings.TrimSpace(v.Str) != v.Str || (id != "" && id != v.Str) {
			return false
		}
		id = v.Str
		return true
	}
	obj.ForEach(func(k, v gjson.Result) bool {
		var bit uint8
		switch k.Str {
		case "type":
			bit = 1
			valid = v.Type == gjson.String && v.Str != ""
		case "stream_id":
			bit = 2
		case "response_id":
			bit = 4
			valid = accept(v)
		case "response":
			bit = 8
			if !v.IsObject() {
				valid = false
				break
			}
			nested := false
			v.ForEach(func(key, value gjson.Result) bool {
				if key.Str == "id" {
					valid = !nested && accept(value)
					nested = true
				}
				return valid
			})
		default:
			return true
		}
		valid = valid && seen&bit == 0
		seen |= bit
		return valid
	})
	return id, valid
}

type openAIWSFrameOwnerKey struct{}
type openAIWSFrameOwner struct {
	lease string
	check func(string, bool) error
}

// WithOpenAIWSFrameOwner binds only the local transport; it adds no wire header.
func WithOpenAIWSFrameOwner(ctx context.Context, lease string, check func(string, bool) error) context.Context {
	return context.WithValue(ctx, openAIWSFrameOwnerKey{}, openAIWSFrameOwner{lease, check})
}
func wsFrameOwner(ctx context.Context) openAIWSFrameOwner {
	o, _ := ctx.Value(openAIWSFrameOwnerKey{}).(openAIWSFrameOwner)
	return o
}
func checkUpstreamWSIdentity(ctx context.Context, current, id string, valid bool) error {
	var err error
	if !valid || (current != "" && id != "" && current != id) {
		err = NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "upstream response identity changed", nil)
	} else if o := wsFrameOwner(ctx); o.check != nil {
		err = o.check(id, true)
	}
	if err != nil {
		if recovery := openAIWSAgentRecoveryFromContext(ctx); recovery != nil {
			recovery.preventReplay()
		}
	}
	return err
}

func upstreamWSDiagnostic(value string) string {
	switch value {
	case "cyber_policy", "undelivered_timeout", "workspace_suspended", "upstream_capacity_exhausted", "invalid_request_error", "invalid_parameter", "invalid_value", "invalid_prompt", "invalid_encrypted_content", "previous_response_not_found", "context_length_exceeded", "model_not_found", "rate_limit_error", "rate_limit_exceeded", "insufficient_quota", "server_error", "server_is_overloaded", "slow_down", "authentication_error", "invalid_api_key", "permission_error", "permission_denied", "content_policy_violation", "content_filter", "bad_request", "upstream_error", "upstream_stream_incomplete", "upgrade_required", "websocket_not_supported", "websocket_unsupported", "websocket_connection_limit_reached", "unsupported_parameter", "insufficient_permissions", "invalid_prompt_template", "invalid_image", "invalid_model", "request_timeout":
		return value
	}
	return "upstream_error"
}

// Public copy only. Preserve operational classifications on the original frame.
// Provider free text, arbitrary fields, partial output and metadata do not
// belong in a public error envelope. Local policy errors do not pass here.
func publicOpenAIWSUpstreamEvent(raw []byte, requestModel string) []byte {
	kind := gjson.GetBytes(raw, "type").String()
	if kind != "error" && kind != "response.failed" {
		return raw
	}
	e := gjson.GetBytes(raw, "error")
	if kind == "response.failed" {
		e = gjson.GetBytes(raw, "response.error")
	}
	code := upstreamWSDiagnostic(e.Get("code").String())
	if code == "upstream_error" && kind == "error" && !e.Get("code").Exists() {
		code = upstreamWSDiagnostic(gjson.GetBytes(raw, "code").String())
	}
	message := "Upstream request failed"
	switch code {
	case "workspace_suspended":
		message = "The workspace is suspended"
	case "server_error":
		message = "Our servers are currently overloaded or unavailable"
	case "cyber_policy":
		message = "Request blocked by upstream policy"
	}
	diag := map[string]any{"type": upstreamWSDiagnostic(e.Get("type").String()), "code": code, "message": message}
	body := map[string]any{"type": kind, "sequence_number": int64(0)}
	if seq := gjson.GetBytes(raw, "sequence_number"); seq.Type == gjson.Number && seq.Int() >= 0 {
		body["sequence_number"] = seq.Int()
	}
	if status := gjson.GetBytes(raw, "status"); status.Type == gjson.Number && status.Int() >= 400 && status.Int() <= 599 {
		body["status"] = status.Int()
	}
	// Keep both claimed IDs until the final writer has checked them against
	// each other and its own active turn. Never use an event or item ID here.
	if v := gjson.GetBytes(raw, "response_id"); v.Type == gjson.String {
		body["response_id"] = v.String()
	}
	if kind == "error" {
		body["error"] = diag
		if v := gjson.GetBytes(raw, "response.id"); v.Type == gjson.String && v.Str != "" {
			body["response"] = map[string]string{"id": v.String()}
		}
	} else {
		resp := map[string]any{"object": "response", "status": "failed", "created_at": time.Now().Unix(), "output": []any{}, "error": diag}
		if v := gjson.GetBytes(raw, "response.id"); v.Type == gjson.String && v.Str != "" {
			resp["id"] = v.String()
		}
		if requestModel != "" {
			resp["model"] = requestModel
		}
		if v := gjson.GetBytes(raw, "response.created_at"); v.Type == gjson.Number && v.Int() > 0 {
			resp["created_at"] = v.Int()
		}
		body["response"] = resp
	}
	result, _ := json.Marshal(body)
	return result
}
