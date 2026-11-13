package antigravity

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	ClientID = "synthetic-antigravity-client-id"
	defaultClientSecret = "synthetic-antigravity-secret"
	os.Exit(m.Run())
}

func TestSUB6OAuthRequiresClientID(t *testing.T) {
	old := ClientID
	ClientID = ""
	t.Cleanup(func() { ClientID = old })
	if _, err := getClientSecret(); err == nil {
		t.Fatal("missing OAuth client ID must be rejected")
	}
}
