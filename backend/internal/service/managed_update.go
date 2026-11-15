package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManagedUpdateClient talks only to an operator-installed local controller.
// No Docker socket, shell command, download URL or host path comes from HTTP input.
type ManagedUpdateClient struct {
	client  *http.Client
	token   string
	initErr error
}

type ManagedUpdateStatus struct {
	Managed        bool              `json:"managed"`
	OperationID    string            `json:"operation_id,omitempty"`
	Phase          string            `json:"phase"`
	TargetVersion  string            `json:"target_version,omitempty"`
	CurrentVersion string            `json:"current_version,omitempty"`
	Error          string            `json:"error,omitempty"`
	Versions       []RollbackVersion `json:"versions,omitempty"`
	Allowed        bool              `json:"allowed"`
	ReasonCode     string            `json:"reason_code,omitempty"`
	SchemaMode     string            `json:"schema_mode,omitempty"`
}

// A configured but broken controller fails closed; it never falls back to
// replacing one container's binary or exiting the serving process.
func NewManagedUpdateClientFromEnvironment() *ManagedUpdateClient {
	socket := strings.TrimSpace(os.Getenv("SUB6_UPDATE_CONTROLLER_SOCKET"))
	tokenFile := strings.TrimSpace(os.Getenv("SUB6_UPDATE_CONTROLLER_TOKEN_FILE"))
	if socket == "" && tokenFile == "" {
		return nil
	}
	c := &ManagedUpdateClient{}
	if !filepath.IsAbs(socket) || !filepath.IsAbs(tokenFile) || len(socket) > 100 {
		c.initErr = fmt.Errorf("managed updater requires absolute socket and token file paths")
		return c
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil || len(bytes.TrimSpace(data)) < 32 || len(data) > 4096 {
		c.initErr = fmt.Errorf("managed updater authentication is unavailable")
		return c
	}
	c.token = strings.TrimSpace(string(data))
	c.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
			},
			MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c
}

func (c *ManagedUpdateClient) Status(ctx context.Context) (*ManagedUpdateStatus, error) {
	return c.call(ctx, http.MethodGet, "/v1/status", nil)
}

// Preflight is read-only. A missing or unreachable controller never grants installation.
func (c *ManagedUpdateClient) Preflight(ctx context.Context, version string) (*ManagedUpdateStatus, error) {
	return c.call(ctx, http.MethodGet, "/v1/preflight?version="+url.QueryEscape(version), nil)
}

func (c *ManagedUpdateClient) Start(ctx context.Context, action, version string) (*ManagedUpdateStatus, error) {
	return c.call(ctx, http.MethodPost, "/v1/operations", map[string]string{"action": action, "version": version})
}

func (c *ManagedUpdateClient) call(ctx context.Context, method, path string, payload any) (*ManagedUpdateStatus, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://local-updater"+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("managed update controller is unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	var result ManagedUpdateStatus
	if err := json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid managed update response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("managed update rejected: %s", result.Error)
	}
	result.Managed = true
	return &result, nil
}
