//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManagedUpdaterUsesAuthenticatedUnixSocketAndNoFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("production controller uses Linux Unix sockets")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "ctl.sock")
	token := strings.Repeat("a", 64)
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(token), 0600))
	listener, err := net.Listen("unix", sock)
	require.NoError(t, err)
	received := make(chan map[string]string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		if r.Method == http.MethodPost {
			require.Equal(t, "/v1/operations", r.URL.Path)
			var payload map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			received <- payload
		}
		_ = json.NewEncoder(w).Encode(ManagedUpdateStatus{Phase: "draining", OperationID: "controlled"})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	t.Setenv("SUB6_UPDATE_CONTROLLER_SOCKET", sock)
	t.Setenv("SUB6_UPDATE_CONTROLLER_TOKEN_FILE", tokenFile)
	client := NewManagedUpdateClientFromEnvironment()
	require.NotNil(t, client)
	status, err := client.Start(context.Background(), "update", "0.1.3")
	require.NoError(t, err)
	require.True(t, status.Managed)
	require.Equal(t, map[string]string{"action": "update", "version": "0.1.3"}, <-received)
	_, err = client.Status(context.Background())
	require.NoError(t, err)
	require.NoError(t, server.Close())
	_, err = client.Start(context.Background(), "restart", "")
	require.ErrorContains(t, err, "unavailable")
}

func TestManagedUpdaterConfiguredInvalidlyDoesNotBecomeStandalone(t *testing.T) {
	t.Setenv("SUB6_UPDATE_CONTROLLER_SOCKET", "relative.sock")
	t.Setenv("SUB6_UPDATE_CONTROLLER_TOKEN_FILE", "")
	client := NewManagedUpdateClientFromEnvironment()
	require.NotNil(t, client)
	_, err := client.Status(context.Background())
	require.Error(t, err)
	t.Setenv("SUB6_UPDATE_CONTROLLER_SOCKET", "")
	require.Nil(t, NewManagedUpdateClientFromEnvironment())
}
