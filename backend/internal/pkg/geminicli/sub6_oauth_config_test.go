package geminicli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	GeminiCLIOAuthClientID = "synthetic-gemini-client-id"
	_ = os.Setenv(GeminiCLIOAuthClientSecretEnv, "synthetic-gemini-secret")
	os.Exit(m.Run())
}

func TestSUB6OAuthRequiresClientID(t *testing.T) {
	old := GeminiCLIOAuthClientID
	GeminiCLIOAuthClientID = ""
	t.Cleanup(func() { GeminiCLIOAuthClientID = old })
	if _, err := EffectiveOAuthConfig(OAuthConfig{}, "code_assist"); err == nil {
		t.Fatal("missing OAuth client ID must be rejected")
	}
}
