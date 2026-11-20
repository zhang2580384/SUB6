package repository

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestPublicAccountIsolationTransport(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{11}, TargetAccountIDs: []int64{22}}
	request := pinRequest(t)
	request.Header.Set("Cookie", "own=fixture")
	calls := 0
	delegate := gatewayPinDelegate{call: func(req *http.Request, proxy string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.Same(t, request, req)
		require.Equal(t, "own=fixture", req.Header.Get("Cookie"))
		require.EqualValues(t, 22, id)
		require.Equal(t, "", proxy)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}}
	upstream := &astraRoutingUpstream{delegate: delegate, cfg: cfg}
	resp, err := upstream.Do(request, "", 22, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 1, calls)
	cookie, err := upstream.CodexGatewayPinWSHeader(22)
	require.NoError(t, err)
	require.Empty(t, cookie)
	legacy := &codexGatewayPinUpstream{delegate: delegate, config: cfg.Gateway.CodexGatewayPin}
	_, err = legacy.Do(request, "", 22, 1)
	require.ErrorIs(t, err, errAccountRouteIsolation)
	cookie, err = legacy.CodexGatewayPinWSHeader(22)
	require.ErrorIs(t, err, errAccountRouteIsolation)
	require.Empty(t, cookie)
	require.Equal(t, 1, calls)
}
