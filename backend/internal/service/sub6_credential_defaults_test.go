package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSUB6CredentialServicesRequireExplicitConfiguration(t *testing.T) {
	cfg := defaultAccountTokenGuardConfig()
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.ProbeEndpoint)
	require.Empty(t, cfg.ReloginEndpoint)
	require.NoError(t, ValidateAccountTokenGuardConfig(cfg))
	cfg.Enabled = true
	require.Error(t, ValidateAccountTokenGuardConfig(cfg))
	svc, _, _, _, _, _ := newReauthTestService("acct-1")
	svc.settings = nil
	_, err := svc.SaveCredentialConfig(context.Background(), 42, OpenAIOAuthReauthConfigInput{
		LoginEmail: "user@example.invalid", CredentialMode: OpenAIOAuthReauthModePasswordTOTP,
		Engine: OpenAIOAuthReauthEngineSessionStudio, Password: "synthetic-password",
	})
	require.Error(t, err)
}

func TestSUB6AntigravityLoginRequiresExplicitConfiguration(t *testing.T) {
	svc := NewAntigravityOAuthService(nil)
	defer svc.sessionStore.Stop()
	_, err := svc.GenerateAuthURL(context.Background(), nil)
	require.Error(t, err)
}
