//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPublicAccountIsolationRejectsLegacySettingsAndSessionState(t *testing.T) {
	ctx := t.Context()
	legacy := config.AstraRoutingSettings{CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2}}, WSSession: config.CodexWSAnchorConfig{Enabled: true, AccountIDs: []int64{2}}, AccountScheduling: true, TicketPoolScheduling: true}
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = legacy.CookiePool
	cfg.Gateway.CodexWSAnchor = legacy.WSSession
	require.False(t, cfg.AstraRouting(ctx).CookiePool.Enabled)
	cfg.SetAstraRoutingLoader(func(_ context.Context) config.AstraRoutingSettings { return legacy })
	effective := cfg.AstraRouting(ctx)
	require.False(t, effective.CookiePool.Enabled)
	require.False(t, effective.WSSession.Enabled)
	require.False(t, effective.TicketPoolScheduling)
	svc := NewSettingService(&settingRepoStub{values: map[string]string{astraRoutingSettingKey: `{"cookie_pool":{"enabled":true},"ws_session":{"enabled":true}}`}}, cfg)
	settings, err := svc.GetAstraRouting(ctx)
	require.NoError(t, err)
	require.False(t, settings.CookiePool.Enabled)
	require.False(t, settings.WSSession.Enabled)
	_, err = svc.SetAstraRouting(ctx, legacy)
	require.Error(t, err)
	gateway := &OpenAIGatewayService{cfg: cfg}
	account := &Account{ID: 2}
	attempted, _ := gateway.tryBorrowStoredAstraTicket(ctx, account, "", time.Second, func() bool { t.Fatal("borrow must not consume a request"); return false })
	require.False(t, attempted)
	ticket := &openAICodexTicket{AccountID: 2, SourceAccountID: 1, Identity: ticketIdentity(account)}
	require.False(t, ticketIdentityMatches(account, ticket))
	ticket.SourceAccountID = 0
	ticket.Acquisition = "reuse"
	require.False(t, ticketIdentityMatches(account, ticket))
	ticket.Acquisition = "self"
	require.True(t, ticketIdentityMatches(account, ticket))
}
