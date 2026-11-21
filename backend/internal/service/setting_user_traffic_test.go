//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestUserTrafficVisibilityMatchesCollectionPolicy(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{{"", true}, {"true", true}, {"false", false}} {
		repo := &settingPublicRepoStub{values: map[string]string{}}
		if tt.value != "" {
			repo.values[SettingKeyUserTrafficEnabled] = tt.value
		}
		s := NewSettingService(repo, &config.Config{})
		public, err := s.GetPublicSettings(t.Context())
		require.NoError(t, err)
		require.Equal(t, tt.want, public.UserTrafficEnabled)
		read, err := s.UserTrafficReportsEnabled(t.Context())
		require.NoError(t, err)
		require.Equal(t, tt.want, read)
		s.refreshUserTraffic(t.Context(), nil)
		require.Equal(t, tt.want, s.UserTrafficCaptureEnabled())
	}
}

type blockedTrafficSettings struct {
	SettingRepository
	started, release chan struct{}
}

func (r *blockedTrafficSettings) GetMultiple(ctx context.Context, _ []string) (map[string]string, error) {
	close(r.started)
	select {
	case <-r.release:
		return map[string]string{SettingKeyUserTrafficEnabled: "true"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestUserTrafficSlowRefreshDoesNotBlockOrUndoSave(t *testing.T) {
	r := &blockedTrafficSettings{started: make(chan struct{}), release: make(chan struct{})}
	s := NewSettingService(r, &config.Config{})
	returned := make(chan bool, 1)
	go func() { returned <- s.UserTrafficCaptureEnabled() }()
	select {
	case enabled := <-returned:
		require.False(t, enabled)
	case <-time.After(time.Second):
		t.Fatal("model request blocked on optional telemetry")
	}
	<-r.started
	s.refreshCachedSettings(&SystemSettings{UserTrafficEnabled: false})
	close(r.release)
	require.Eventually(t, func() bool { return !s.userTrafficRefreshing.Load() }, time.Second, time.Millisecond)
	require.False(t, s.UserTrafficCaptureEnabled())
}

func TestUserTrafficExpiredSwitchSkipsCaptureDuringRefresh(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{SettingKeyUserTrafficEnabled: "true"}}
	s := NewSettingService(repo, &config.Config{})
	expired := &userTrafficSnapshot{enabled: true, expires: time.Now().Add(-time.Hour)}
	s.userTrafficCache.Store(expired)
	s.userTrafficRefreshing.Store(true)

	require.False(t, s.UserTrafficCaptureEnabled())
	s.refreshUserTraffic(t.Context(), expired)
	require.True(t, s.UserTrafficCaptureEnabled())
}

// The refresh can start before any model request arrives and observe edits while idle.
func TestUserTrafficIdleGatewayTracksChangesBeforeNextRequest(t *testing.T) {
	repo := &clockedTrafficSetting{reads: make(chan chan string)}
	s := NewSettingService(repo, &config.Config{})
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runUserTrafficRefresh(ctx, ticks)
	}()
	t.Cleanup(func() { cancel(); <-done })
	respond := func(value string) {
		t.Helper()
		select {
		case reply := <-repo.reads:
			reply <- value
		case <-time.After(time.Second):
			t.Fatal("idle gateway did not refresh the traffic setting")
		}
		require.Eventually(t, func() bool {
			v := s.userTrafficCache.Load()
			return v != nil && !s.userTrafficRefreshing.Load() && v.enabled == (value == "true")
		}, time.Second, time.Millisecond)
	}
	respond("true")
	ticks <- time.Now()
	respond("true")
	require.True(t, s.UserTrafficCaptureEnabled())
	ticks <- time.Now()
	respond("false")
	require.False(t, s.UserTrafficCaptureEnabled())
}

type clockedTrafficSetting struct {
	SettingRepository
	reads chan chan string
}

func (r *clockedTrafficSetting) GetMultiple(ctx context.Context, _ []string) (map[string]string, error) {
	reply := make(chan string, 1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r.reads <- reply:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case value := <-reply:
		return map[string]string{SettingKeyUserTrafficEnabled: value}, nil
	}
}

func TestUserTrafficDrainCancelsAnIdleRefresh(t *testing.T) {
	repo := &clockedTrafficSetting{reads: make(chan chan string)}
	s := NewSettingService(repo, &config.Config{})
	stop := s.StartUserTrafficRefresh()
	t.Cleanup(stop)
	select {
	case <-repo.reads: // Keep the lookup pending until shutdown.
	case <-time.After(time.Second):
		t.Fatal("background settings read did not start")
	}
	stop()
	require.False(t, s.userTrafficRefreshing.Load())
	require.Nil(t, s.userTrafficCache.Load())
}
