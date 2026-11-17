package service

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func agentHTTPContinuationRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://native.example/v1/responses", bytes.NewBufferString(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer fixture")
	return req
}

// The upstream receives the same native HTTP request in both ingress modes.
// Exercise real admission/publication, including records from the old bridge
// namespace, instead of merely comparing newly calculated fingerprints.
func TestOpenAIAgentHTTPBridgeContinuation(t *testing.T) {
	for _, producer := range []string{"http", "bridge", "legacy_bridge"} {
		t.Run(producer, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			manager, c, account := issue715WSManager(t, svc, context.Background())
			defer manager.abort()
			if producer == "bridge" {
				require.NoError(t, manager.begin(account, []byte(issue715OpaqueBody), []byte(issue715OpaqueBody), agentHTTPContinuationRequest(t, issue715OpaqueBody), "http_bridge"))
				manager.finish(true)
				manager.finish = nil
			} else {
				issue715LearnRoute(t, svc, account, issue715OpaqueBody)
			}
			if producer == "legacy_bridge" {
				req := agentHTTPContinuationRequest(t, issue715OpaqueBody)
				req.URL.Fragment = "agent-recovery-http_bridge"
				for key, binding := range svc.agentMessageRecovery.bindings {
					binding.wire = openAIAgentRecoveryWireDigest(req.URL.String(), req.Header, "gpt-6-astra")
					svc.agentMessageRecovery.bindings[key] = binding
				}
			}
			require.Len(t, svc.agentMessageRecovery.bindings, 1)
			var original openAIAgentRecoveryBinding
			for _, binding := range svc.agentMessageRecovery.bindings {
				original = binding
			}
			originalRequest := agentHTTPContinuationRequest(t, issue715OpaqueBody)
			if producer != "http" {
				originalRequest.URL.Fragment = "agent-recovery-http_bridge"
			}
			require.Equal(t, openAIAgentRecoveryWireDigest(originalRequest.URL.String(), originalRequest.Header, "gpt-6-astra"), original.wire,
				"fresh records must remain readable by older nodes without a cache migration")
			// Repeated transitions must neither rewrite old ownership nor turn a
			// successful observation into a conflicting Redis publication.
			for _, consumer := range []string{"bridge", "http", "bridge", "http"} {
				req := agentHTTPContinuationRequest(t, issue715OpaqueBody)
				if consumer == "bridge" {
					require.NoError(t, manager.begin(account, []byte(issue715OpaqueBody), []byte(issue715OpaqueBody), req, "http_bridge"))
					manager.finish(true)
					manager.finish = nil
				} else {
					ctx := svc.WithOpenAIAgentMessageRecovery(context.Background(), c, []byte(issue715OpaqueBody), "fixture-session", "gpt-6-astra")
					finish, err := svc.prepareOpenAIAgentRecoveryAttempt(ctx, account, req)
					require.NoError(t, err)
					finish(true)
				}
				for _, binding := range svc.agentMessageRecovery.bindings {
					require.True(t, sameOpenAIAgentRecoveryRoute(original, binding))
					require.False(t, binding.conflicted)
				}
			}
		})
	}
}

func TestOpenAIAgentHTTPBridgeContinuationRejectsChangedIdentity(t *testing.T) {
	for _, mutation := range []string{"account", "credentials", "authorization", "organization", "project", "beta", "endpoint", "query", "model", "agent_item", "native_ws", "other_adapter"} {
		t.Run(mutation, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			manager, _, account := issue715WSManager(t, svc, context.Background())
			defer manager.abort()
			issue715LearnRoute(t, svc, account, issue715OpaqueBody)
			source, mode := issue715OpaqueBody, "http_bridge"
			req := agentHTTPContinuationRequest(t, source)
			switch mutation {
			case "account":
				account.ID++
			case "credentials":
				account.Credentials = map[string]any{"api_key": "fixture-rotated"}
			case "authorization":
				req.Header.Set("Authorization", "Bearer fixture-other")
			case "organization":
				req.Header.Set("OpenAI-Organization", "fixture-other")
			case "project":
				req.Header.Set("OpenAI-Project", "fixture-other")
			case "beta":
				req.Header.Set("OpenAI-Beta", "fixture-other")
			case "endpoint":
				req.URL.Host = "different.example"
			case "query":
				req.URL.RawQuery = "route=different"
			case "model":
				req = agentHTTPContinuationRequest(t, strings.Replace(source, "gpt-6-astra", "gpt-other", 1))
			case "agent_item":
				req = agentHTTPContinuationRequest(t, strings.Replace(source, "fixture envelope", "rewritten envelope", 1))
			case "native_ws":
				req.URL.Scheme = "wss"
				mode = "ctx_pool"
			case "other_adapter":
				mode = "passthrough"
			}
			err := manager.begin(account, []byte(source), []byte(source), req, mode)
			require.Error(t, err, "an ingress compatibility fix must not authorize a different upstream identity or payload")
		})
	}
}
