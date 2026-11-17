package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// OpenAIWSGenerateMode reads the wire control before routing or a JSON map can
// erase a conflicting spelling. No control means ordinary generation.
func OpenAIWSGenerateMode(body []byte) (prewarm bool, err error) {
	seen := false
	gjson.ParseBytes(body).ForEach(func(k, v gjson.Result) bool {
		if !strings.EqualFold(k.Str, "generate") {
			return true
		}
		if seen || k.Str != "generate" || (v.Type != gjson.True && v.Type != gjson.False) {
			err = errors.New("generate must be one boolean")
			return false
		}
		seen = true
		prewarm = v.Type == gjson.False
		return true
	})
	if prewarm && err == nil && (!utf8.Valid(body) || !openAIEncryptedJSONUnambiguous(body) || gjson.GetBytes(body, "type").Str != "response.create") {
		return false, errors.New("invalid prewarm request")
	}
	return prewarm, err
}

func openAIWSHTTPPrewarmSafe(body []byte) bool {
	root := gjson.ParseBytes(body)
	for _, key := range []string{"previous_response_id", "conversation", "response", "item_reference", "encrypted_content"} {
		if root.Get(key).Exists() {
			return false
		}
	}
	// Codex asks for encrypted reasoning *output* even when its input is
	// plaintext. The local acknowledgment returns no output of any kind.
	if v := root.Get("include"); v.Exists() {
		if !v.IsArray() {
			return false
		}
		for _, item := range v.Array() {
			if item.Type != gjson.String || item.Str != "reasoning.encrypted_content" {
				return false
			}
		}
	}
	input := root.Get("input")
	if !input.Exists() || input.Type == gjson.String {
		return true
	}
	if !input.IsArray() {
		return false
	}
	safe := true
	input.ForEach(func(_, item gjson.Result) bool {
		typ := item.Get("type")
		role := item.Get("role").Str
		safe = item.IsObject() && (!typ.Exists() || typ.Str == "message") && (role == "system" || role == "developer" || role == "user" || role == "assistant")
		if !safe {
			return false
		}
		item.ForEach(func(k, _ gjson.Result) bool {
			safe = k.Str == "role" || k.Str == "type" || k.Str == "content"
			return safe
		})
		if !safe {
			return false
		}
		v := item.Get("content")
		if v.Type == gjson.String {
			return true
		}
		if !v.IsArray() {
			safe = false
			return false
		}
		v.ForEach(func(_, part gjson.Result) bool {
			safe = part.IsObject() && part.Get("type").Str == "input_text" && part.Get("text").Type == gjson.String
			if safe {
				part.ForEach(func(k, _ gjson.Result) bool { safe = k.Str == "type" || k.Str == "text"; return safe })
			}
			return safe
		})
		return safe
	})
	return safe
}

// Codex requires a string id for Completed and sends the first full request
// when that string is empty. No fake provider id, snapshot or ownership entry.
const openAIWSHTTPPrewarmAck = `{"type":"response.completed","sequence_number":0,"response":{"id":"","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`

type openAIWSLocalPrewarmKey struct{}

// IsOpenAIWSLocalPrewarmAck identifies only the fixed frame written by the local
// HTTP bridge. Provider frames still require normal ownership validation.
func IsOpenAIWSLocalPrewarmAck(ctx context.Context, body []byte) bool {
	return ctx.Value(openAIWSLocalPrewarmKey{}) == true && string(body) == openAIWSHTTPPrewarmAck
}

func (s *OpenAIGatewayService) completeOpenAIWSHTTPPrewarm(ctx context.Context, c *gin.Context, a *Account, payload []byte, conn OpenAIWSDownstreamConn, hooks *OpenAIWSIngressHooks) error {
	model := extractOpenAICodexTicketModel(payload)
	latest, err := s.admitOpenAITurn(ctx, c, a, model, payload)
	if err == nil && (latest.ID != a.ID || latest.AuthConnectionID != a.AuthConnectionID || latest.AuthConnectionRevision != a.AuthConnectionRevision || openAITurnRouteFingerprint(latest) != openAITurnRouteFingerprint(a)) {
		err = denyOpenAITurn("account_binding_changed")
	}
	if err == nil {
		err = s.applyOpenAICodexTicket(ctx, latest, model, http.Header{})
	}
	if err != nil {
		return err
	}
	writeCtx, cancel := newOpenAIWSDownstreamWriteContext(ctx, hooks, s.openAIWSWriteTimeout())
	defer cancel()
	return WriteCapturedWSClient(context.WithValue(writeCtx, openAIWSLocalPrewarmKey{}, true), conn, coderws.MessageText, []byte(openAIWSHTTPPrewarmAck))
}
