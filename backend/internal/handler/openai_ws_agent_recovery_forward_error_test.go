//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentContinuationTestRedis(t *testing.T, backend string) *redis.Client {
	t.Helper()
	if backend == "redis" {
		return issue805Redis(t)()
	}
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestOpenAIHTTPAndWSBridgeShareAgentContinuation(t *testing.T) {
	for _, backend := range []string{"miniredis", "redis"} {
		for _, producerMode := range []string{"http", "http_bridge"} {
			t.Run(backend+"/"+producerMode, func(t *testing.T) {
				client := agentContinuationTestRedis(t, backend)
				oracle := &issue715WSOracle{}
				upstream := oracle.server(t)
				h := issue715WSNode(t, service.OpenAIWSIngressModeHTTPBridge, upstream.URL, repository.NewGatewayCache(client), false)
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), h.apiKey)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: h.apiKey.User.ID, Concurrency: 1})
					c.Next()
				})
				router.POST("/openai/v1/responses", h.handler.Responses)
				httpEntry := httptest.NewServer(router)
				defer httpEntry.Close()
				sendHTTP := func(followup bool) string {
					body := strings.Replace(issue715WSCreate("", followup), `"type":"response.create",`, `"stream":true,`, 1)
					req, err := http.NewRequest(http.MethodPost, httpEntry.URL+"/openai/v1/responses", strings.NewReader(body))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Session_id", "fixture-ws715-session")
					req.Header.Set("Thread-Id", "fixture-ws715-thread")
					response, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
					require.NoError(t, err)
					defer response.Body.Close()
					result, err := io.ReadAll(response.Body)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, response.StatusCode, string(result))
					return string(result)
				}
				if producerMode == "http" {
					require.Contains(t, sendHTTP(false), "fixture-task-reference-715")
					issue715WSSend(t, h.clientConn, issue715WSCreate("", true))
					require.Contains(t, string(issue715WSRead(t, h.clientConn)), "Same task: ledger balance 18")
				} else {
					issue715WSSend(t, h.clientConn, issue715WSCreate("", false))
					require.Contains(t, string(issue715WSRead(t, h.clientConn)), "fixture-task-reference-715")
					require.Contains(t, sendHTTP(true), "Same task: ledger balance 18")
				}
				oracle.mu.Lock()
				defer oracle.mu.Unlock()
				require.Equal(t, 2, oracle.http, "each turn must execute exactly once against the native HTTP upstream")
				require.Zero(t, oracle.ws)
				require.Equal(t, []int64{1, 1}, oracle.ids)
				require.Contains(t, string(oracle.frames[1]), "ledger balance 18; applied once")
			})
		}
	}
}

// A genuine upstream WS -> HTTP transport change remains incompatible. The
// final-send gate must report its structured cause before closing the client,
// rather than leaving a downstream relay to invent an opaque 502.
func TestOpenAIWSForwardRecoveryRefusalCarriesErrorEvent(t *testing.T) {
	for _, backend := range []string{"miniredis", "redis"} {
		t.Run(backend, func(t *testing.T) {
			client := agentContinuationTestRedis(t, backend)
			cache := repository.NewGatewayCache(client)
			oracle := &issue715WSOracle{}
			upstream := oracle.server(t)
			producer := issue715WSNode(t, service.OpenAIWSIngressModeCtxPool, upstream.URL, cache, false)
			consumer := issue715WSNode(t, service.OpenAIWSIngressModeHTTPBridge, upstream.URL, cache, false)
			issue715WSSend(t, producer.clientConn, issue715WSCreate("", false))
			first := issue715WSRead(t, producer.clientConn)
			require.Contains(t, string(first), "fixture-task-reference-715")
			issue715WSSend(t, consumer.clientConn, issue715WSCreate("", true))
			event := issue715WSRead(t, consumer.clientConn)
			require.Equal(t, "error", gjson.GetBytes(event, "type").String())
			require.EqualValues(t, http.StatusConflict, gjson.GetBytes(event, "status").Int())
			require.Equal(t, "encrypted_agent_recovery_wire_changed", gjson.GetBytes(event, "error.code").String())
			require.Contains(t, gjson.GetBytes(event, "error.message").String(), "transport changed")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _, err := consumer.clientConn.Read(ctx)
			require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
			rejections := consumer.requestLogs.FilterMessage("openai.websocket_agent_recovery_rejected").All()
			require.Len(t, rejections, 1)
			require.Equal(t, "upstream_identity", rejections[0].ContextMap()["stage"])
			require.Equal(t, "encrypted_agent_recovery_wire_changed", rejections[0].ContextMap()["error_code"])
			oracle.mu.Lock()
			defer oracle.mu.Unlock()
			require.Equal(t, 1, oracle.ws)
			require.Zero(t, oracle.http, "a refused generation/tool submission must never reach another upstream transport")
		})
	}
}
