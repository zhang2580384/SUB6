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
		s.refreshUserTraffic(nil)
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
