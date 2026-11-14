package service

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type openAIWSShutdownPrewarmDialer struct {
	started  chan struct{}
	canceled chan struct{}
	finish   chan struct{}
	calls    atomic.Int64
	late     *openAIWSFakeConn
}

func (d *openAIWSShutdownPrewarmDialer) Dial(ctx context.Context, _ string, _ http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	if d.calls.Add(1) == 1 {
		close(d.started)
		<-ctx.Done()
		close(d.canceled)
		<-d.finish
	}
	// A transport may finish its handshake concurrently with cancellation.
	// The canceled prewarm must close this result instead of publishing it.
	return d.late, 0, nil, nil
}

func TestOpenAIWSConnPool_Close_CancelsAndJoinsPrewarm(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 3
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 3
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 4
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 30
	pool := newOpenAIWSConnPool(cfg)
	dialer := &openAIWSShutdownPrewarmDialer{
		started: make(chan struct{}), canceled: make(chan struct{}), finish: make(chan struct{}), late: &openAIWSFakeConn{},
	}
	pool.setClientDialerForTest(dialer)
	var finishOnce sync.Once
	finish := func() { finishOnce.Do(func() { close(dialer.finish) }) }
	t.Cleanup(func() { finish(); pool.Close() })
	const accountID = int64(901)
	ap := pool.getOrCreateAccountPool(accountID)
	ap.lastAcquire = &openAIWSAcquireRequest{Account: &Account{ID: accountID}, WSURL: "ws://fixture.invalid"}
	pool.ensureTargetIdleAsync(accountID)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-dialer.started:
	case <-ctx.Done():
		t.Fatal("prewarm did not start")
	}
	closed := make(chan struct{})
	go func() { pool.Close(); close(closed) }()
	select {
	case <-dialer.canceled:
	case <-ctx.Done():
		t.Fatal("Close did not cancel an in-flight prewarm")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before the prewarm dial exited")
	default:
	}
	finish()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("Close did not join the canceled prewarm")
	}
	require.EqualValues(t, 1, dialer.calls.Load(), "shutdown must cancel the remaining reserved dials")
	ap.mu.Lock()
	creating, active, conns := ap.creating, ap.prewarmActive, len(ap.conns)
	ap.mu.Unlock()
	require.Zero(t, creating)
	require.False(t, active)
	require.Zero(t, conns, "a late handshake must not enter a closed pool")
	dialer.late.mu.Lock()
	lateClosed := dialer.late.closed
	dialer.late.mu.Unlock()
	require.True(t, lateClosed)
	pool.ensureTargetIdleAsync(accountID)
	require.EqualValues(t, 1, dialer.calls.Load(), "a closed pool must not start another prewarm")
	pool.Close()
}
