package requestcapture

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebSocketForkDoesNotMixAttemptsOrTerminalState(t *testing.T) {
	m, _ := testManager(t)
	task(t, m, "user", 1, false)
	parent := m.Begin(Meta{UserID: 1, RequestID: "fixture-transport", Protocol: "http"})
	require.NotNil(t, parent)
	ctx := WithSession(context.Background(), parent)
	aCtx, a := ForkWebSocket(ctx)
	bCtx, b := ForkWebSocket(ctx)
	require.NotNil(t, a)
	require.NotNil(t, b)
	require.NotSame(t, a, b)
	require.Same(t, a, FromContext(aCtx))
	require.Same(t, b, FromContext(bCtx))
	require.Nil(t, FromContext(WithoutSession(ctx)))
	a.ClientFrame([]byte(`{"type":"response.create","input":"alpha"}`))
	b.ClientFrame([]byte(`{"type":"response.create","input":"beta"}`))
	a.WSRequest(7, []byte(`{"input":"alpha"}`))
	a.WSRequest(7, []byte(`{"input":"alpha-retry"}`))
	b.WSRequest(8, []byte(`{"input":"beta"}`))
	require.Equal(t, 2, a.LastAttempt())
	require.Equal(t, 1, b.LastAttempt())
	require.Zero(t, parent.LastAttempt())
	a.Frame("client_response", 0, []byte(`{"type":"response.completed","response":{"id":"resp_alpha","output":[]}}`))
	require.True(t, a.turnEnded)
	require.False(t, b.turnEnded, "another conversation's terminal cannot end this recorder")
	a.Finish(101)
	b.Finish(101)
	parent.Finish(101)
	// A disabled recorder must not fall back to the physical parent's state.
	m.enabled.Store(false)
	disabledCtx, disabled := ForkWebSocket(ctx)
	require.Nil(t, disabled)
	require.Nil(t, FromContext(disabledCtx))
}
