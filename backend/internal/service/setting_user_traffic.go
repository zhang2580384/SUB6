package service

import (
	"context"
	"time"
)

const userTrafficTTL = 30 * time.Second

type userTrafficSnapshot struct {
	enabled bool
	expires time.Time
}

// Metering is auxiliary: never wait for settings I/O on a model request.
// Prime the cache at route setup. A cold or unavailable store skips collection.
func (s *SettingService) UserTrafficCaptureEnabled() bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	snapshot := s.userTrafficCache.Load()
	if snapshot == nil || !time.Now().Before(snapshot.expires) {
		if s.userTrafficRefreshing.CompareAndSwap(false, true) {
			go s.refreshUserTraffic(snapshot)
		}
	}
	return snapshot != nil && snapshot.enabled
}

func (s *SettingService) refreshUserTraffic(previous *userTrafficSnapshot) {
	defer s.userTrafficRefreshing.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	enabled, err := s.UserTrafficReportsEnabled(ctx)
	ttl := userTrafficTTL
	if err != nil {
		enabled = false
		ttl = 5 * time.Second
	}
	// A settings save that completes during the lookup must win.
	s.userTrafficCache.CompareAndSwap(previous, &userTrafficSnapshot{enabled: enabled, expires: time.Now().Add(ttl)})
}

// The report routes check storage directly so a stale browser cannot query a
// disabled report on another instance. This runs only when opening a report.
func (s *SettingService) UserTrafficReportsEnabled(ctx context.Context) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return false, nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyUserTrafficEnabled})
	if err != nil {
		return false, err
	}
	return values[SettingKeyUserTrafficEnabled] != "false", nil
}
