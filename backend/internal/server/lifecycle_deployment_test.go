package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploymentProbeCountsActiveHandlersWithoutExposingThem(t *testing.T) {
	token := strings.Repeat("b", 64)
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte(token), 0600))
	t.Setenv("SUB6_UPDATE_CONTROLLER_TOKEN_FILE", path)
	lifecycle := NewLifecycle()
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := lifecycle.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-finish
		w.WriteHeader(http.StatusOK)
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	}()
	<-entered
	defer func() { close(finish); <-done }()
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/internal/deployment/status", nil))
	require.Equal(t, http.StatusNotFound, unauthorized.Code)
	request := httptest.NewRequest(http.MethodGet, "/internal/deployment/status", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var status struct {
		Active   int  `json:"active_requests"`
		Draining bool `json:"draining"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))
	require.Equal(t, 1, status.Active)
	require.False(t, status.Draining)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}
