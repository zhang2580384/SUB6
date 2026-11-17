package service

import (
	"errors"
	"net/http"
	"time"
)

var errOpenAIAgentRecoveryUnavailable = errors.New("encrypted agent continuation has no unchanged compatible route")

// OpenAIAgentRecoveryError is a local compatibility refusal, not a failure of
// the entire account pool. Reasons contain no route IDs, credentials or input.
type OpenAIAgentRecoveryError struct {
	Reason  string
	RetryAt time.Time
	Stage   string // bounded internal diagnostic; never contains request data
}

func (e *OpenAIAgentRecoveryError) Error() string { return e.ClientMessage() }
func (e *OpenAIAgentRecoveryError) Unwrap() error { return errOpenAIAgentRecoveryUnavailable }
func (e *OpenAIAgentRecoveryError) ClientCode() string {
	return "encrypted_agent_recovery_" + e.Reason
}
func (e *OpenAIAgentRecoveryError) HTTPStatus() int {
	switch e.Reason {
	case "readable_intent_missing", "readable_history_unsupported", "bridge_history_unsupported", "bridge_history_limit":
		return http.StatusUnprocessableEntity
	case "owner_rate_limited":
		return http.StatusTooManyRequests
	case "owner_overloaded":
		return http.StatusServiceUnavailable
	default:
		return http.StatusConflict
	}
}
func (e *OpenAIAgentRecoveryError) ClientType() string {
	switch e.HTTPStatus() {
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}
func (e *OpenAIAgentRecoveryError) ClientMessage() string {
	var reason, action string
	switch e.Reason {
	case "ws_previous_response_unavailable", "bridge_previous_response_unavailable":
		return "The response pointer is not the latest fully delivered response on this WebSocket connection. Preserve the task and verify the prior outcome; no generation or tool was replayed."
	case "bridge_previous_response_expired":
		return "The completed response context on this WebSocket connection has expired. Preserve the task and verify the prior outcome; no generation or tool was replayed."
	case "bridge_delivery_unconfirmed":
		return "The prior response was not confirmed as completely delivered. Check its outcome before continuing; do not automatically resend a generation or tool result."
	case "bridge_history_unsupported":
		return "The HTTP bridge cannot preserve this history losslessly. Its response pointer was not rebuilt; no generation or tool was replayed."
	case "bridge_history_limit":
		return "The connection history exceeds the HTTP bridge retention limit. Its response pointer was not rebuilt; no generation or tool was replayed."
	case "readable_intent_missing":
		return "The current request has no readable task or summary from which a new provider conversation can be built. The opaque contents are unavailable; no generation or tool was replayed."
	case "readable_history_unsupported":
		return "The supplied history has an unsupported readable representation. No provider state was transferred and no generation or tool was replayed."
	case "http_previous_response_unsupported":
		reason = "HTTP previous_response_id continuation is unsupported for this encrypted agent owner."
		action = "For HTTP continuation, send the complete original history without previous_response_id; current permissions and route compatibility still apply. Waiting alone will not make this request supported. Do not replay tool results while an earlier delivery is uncertain."
	case "owner_rate_limited", "owner_overloaded":
		reason = "The proven encrypted agent owner is temporarily rate limited."
		if e.Reason == "owner_overloaded" {
			reason = "The proven encrypted agent owner is temporarily overloaded."
		}
		action = "Wait until " + e.RetryAt.UTC().Format(time.RFC3339) + " (estimated), then resume this same conversation with its complete history. Compatibility records are retained through that window; current permissions and route compatibility will be checked again. Do not resubmit tool results while an earlier delivery is uncertain."
	case "owner_conflict":
		reason = "The encrypted agent items have conflicting compatible owners."
		action = "Ask the administrator to verify the original compatible owner; do not switch accounts or replay tool results."
	case "route_changed":
		reason = "The proven encrypted agent route or credentials changed."
		action = "Ask the administrator to verify the original compatible route and current authorized credentials before continuing."
	case "wire_changed":
		reason = "The proven encrypted agent transport changed."
		action = "Ask the administrator to verify the original endpoint, adapter and model before continuing."
	case "delivery_pending":
		reason = "The encrypted agent producer has not confirmed delivery yet."
		action = "Wait for the producer's current request to finish and check its outcome before continuing; do not resend tool results while delivery is uncertain."
	case "store_unavailable":
		reason = "Encrypted agent compatibility records are temporarily unavailable."
		action = "Ask the administrator to check the shared compatibility store; do not clear its records or switch accounts to bypass this refusal."
	default:
		reason = "The proven encrypted agent owner is currently unavailable."
		action = "Ask the administrator to check the original account's access and availability; ordinary account switching cannot establish compatibility."
	}
	return reason + " Preserve this task and context. " + action
}

func openAIAgentRecoveryError(reason string) error {
	return &OpenAIAgentRecoveryError{Reason: reason}
}

func openAIAgentRecoveryWireError(stage string) error {
	return &OpenAIAgentRecoveryError{Reason: "wire_changed", Stage: stage}
}

// Preserve completed upstream accounting while propagating downstream failure.
// This is never eligible for retry/failover, even if no bytes were delivered.
type openAINonStreamingDeliveryError struct{ err error }

func (e *openAINonStreamingDeliveryError) Error() string { return e.err.Error() }
func (e *openAINonStreamingDeliveryError) Unwrap() error { return e.err }
