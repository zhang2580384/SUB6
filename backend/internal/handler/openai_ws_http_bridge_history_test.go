//go:build unit

package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Actual handler authentication, per-turn admission, logical routing, billing,
// network HTTP and client WS are exercised with fictional data. The provider's
// fresh random answers cannot be inferred by the bridge from either question.
func TestOpenAIWSHTTPBridgeHistoryLogicalSessionsAndToolResult(t *testing.T) {
	for _, backend := range []string{"miniredis", "redis"} {
		for _, disabled := range []bool{false, true} {
			for _, named := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/owner_disabled=%v/named=%v", backend, disabled, named), func(t *testing.T) {
					cache := repository.NewGatewayCache(agentContinuationTestRedis(t, backend))
					var mu sync.Mutex
					var requests [][]byte
					codes := map[string]string{}
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							return
						}
						mu.Lock()
						defer mu.Unlock()
						requests = append(requests, append([]byte(nil), body...))
						seed := ""
						input := gjson.GetBytes(body, "input")
						if input.Type == gjson.String {
							seed = input.String()
						} else {
							seed = input.Get("0.content").String()
						}
						if seed != "fixture-alpha-first" && seed != "fixture-beta-first" {
							t.Errorf("missing isolated seed: %s", body)
							http.Error(w, "missing seed", 400)
							return
						}
						text := codes[seed]
						initial := text == ""
						if initial {
							var random [16]byte
							if _, err := rand.Read(random[:]); err != nil {
								t.Error(err)
								return
							}
							text = hex.EncodeToString(random[:])
							codes[seed] = text
						} else {
							count := 0
							for _, item := range input.Array() {
								if item.Get("role").String() == "assistant" && item.Get("content.0.text").String() == text {
									count++
								}
							}
							if count != 1 {
								text = "history_missing_or_duplicated"
							}
							if gjson.GetBytes(body, "previous_response_id").Exists() {
								t.Error("HTTP provider received a store=false response pointer")
							}
						}
						output := fmt.Sprintf(`{"type":"message","id":"msg_history_%d","role":"assistant","status":"completed","content":[{"type":"output_text","text":%q,"annotations":[]}]}`, len(requests), text)
						if initial && seed == "fixture-alpha-first" {
							output += `,{"type":"function_call","id":"fc_alpha_history","call_id":"call_alpha_history","name":"fixture_lookup","arguments":"{}","status":"completed"}`
						}
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_history_%d\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[%s],\"usage\":{\"input_tokens\":4,\"output_tokens\":3}}}\n\n", len(requests), output)
					}))
					defer up.Close()
					h := newOpenAIWSPassthroughHandlerHarnessWithKey(t, up.URL, http.Header{"Session-Id": {"fixture-shared-relay"}}, map[string]string{service.SettingKeyCyberSessionBlockEnabled: "false"}, cache,
						func(key *service.APIKey) { key.AllowLongContext = true },
						func(a *service.Account, cfg *config.Config, _ *service.HTTPUpstream, _ *service.AccountRepository) {
							a.Extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModeHTTPBridge
							a.Extra["codex_fingerprint_mode"] = "off"
							a.Concurrency = 8
							cfg.Gateway.DisableEncryptedAgentOwnerBinding = disabled
							cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = false // explicit account mode, not the payload threshold
							cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1 << 30
							cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 15
						})
					alpha, beta := "", ""
					if named {
						alpha, beta = "fixture-alpha-lane", "fixture-beta-lane"
					}
					streamIsolationSend(t, h.clientConn, streamIsolationPayload(alpha, "fixture-alpha-thread", "fixture-alpha-first", ""))
					firstAlpha := streamIsolationTerminal(t, h.clientConn)
					streamIsolationSend(t, h.clientConn, streamIsolationPayload(beta, "fixture-beta-thread", "fixture-beta-first", ""))
					firstBeta := streamIsolationTerminal(t, h.clientConn)
					alphaCode := gjson.GetBytes(firstAlpha, "response.output.0.content.0.text").String()
					betaCode := gjson.GetBytes(firstBeta, "response.output.0.content.0.text").String()
					require.Len(t, alphaCode, 32)
					require.Len(t, betaCode, 32)
					require.NotEqual(t, alphaCode, betaCode)
					follow := map[string]any{"type": "response.create", "model": "gpt-5.1", "store": false, "previous_response_id": gjson.GetBytes(firstAlpha, "response.id").String(), "input": []any{map[string]string{"type": "function_call_output", "call_id": "call_alpha_history", "output": "fixture side effect completed once"}}}
					if named {
						follow["stream_id"] = alpha
					}
					raw, _ := json.Marshal(follow)
					streamIsolationSend(t, h.clientConn, raw)
					secondAlpha := streamIsolationTerminal(t, h.clientConn)
					require.Equal(t, alphaCode, gjson.GetBytes(secondAlpha, "response.output.0.content.0.text").String(), "the first continuation on the same client connection must succeed")
					streamIsolationSend(t, h.clientConn, streamIsolationPayload(beta, "", "Repeat your code exactly once.", gjson.GetBytes(firstBeta, "response.id").String()))
					secondBeta := streamIsolationTerminal(t, h.clientConn)
					require.Equal(t, betaCode, gjson.GetBytes(secondBeta, "response.output.0.content.0.text").String())
					mu.Lock()
					observed := append([][]byte(nil), requests...)
					mu.Unlock()
					require.Len(t, observed, 4, "no hidden provider retry or regeneration")
					require.NotContains(t, string(observed[2]), betaCode)
					require.NotContains(t, string(observed[3]), alphaCode)
					require.Len(t, gjson.GetBytes(observed[2], "input").Array(), 4)
					require.Equal(t, "function_call", gjson.GetBytes(observed[2], "input.2.type").String())
					require.Equal(t, "function_call_output", gjson.GetBytes(observed[2], "input.3.type").String())
					for range 4 {
						select {
						case <-h.usageLogs:
						case <-time.After(3 * time.Second):
							t.Fatal("missing per-turn billing record")
						}
					}
					select {
					case <-h.usageLogs:
						t.Fatal("duplicate billing record")
					default:
					}
					require.NoError(t, h.clientConn.Close(coderws.StatusNormalClosure, "done"))
				})
			}
		}
	}
}

func TestOpenAIWSHTTPBridgeHistoryUnknownPointerIsStructured(t *testing.T) {
	for _, backend := range []string{"miniredis", "redis"} {
		t.Run(backend, func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()
				http.Error(w, "must not reach provider", 500)
			}))
			defer up.Close()
			cache := repository.NewGatewayCache(agentContinuationTestRedis(t, backend))
			h := newOpenAIWSPassthroughHandlerHarnessWithKey(t, up.URL, nil, nil, cache, func(key *service.APIKey) { key.AllowLongContext = true }, func(a *service.Account, cfg *config.Config, _ *service.HTTPUpstream, _ *service.AccountRepository) {
				a.Extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModeHTTPBridge
				cfg.Gateway.DisableEncryptedAgentOwnerBinding = true
			})
			streamIsolationSend(t, h.clientConn, streamIsolationPayload("", "fixture-thread", "continue", "resp_external_unknown"))
			event := streamIsolationRead(t, h.clientConn)
			require.Equal(t, "error", gjson.GetBytes(event, "type").String(), string(event))
			require.Equal(t, "invalid_request_error", gjson.GetBytes(event, "error.type").String())
			require.Equal(t, "encrypted_agent_recovery_ws_previous_response_unavailable", gjson.GetBytes(event, "error.code").String())
			require.EqualValues(t, http.StatusConflict, gjson.GetBytes(event, "status").Int())
			require.Contains(t, gjson.GetBytes(event, "error.message").String(), "no generation or tool was replayed")
			mu.Lock()
			defer mu.Unlock()
			require.Zero(t, requests)
		})
	}
}
