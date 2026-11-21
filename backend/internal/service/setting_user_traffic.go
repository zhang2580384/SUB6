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
			go s.refreshUserTraffic(context.Background(), snapshot)
		}
		// An expired permission cannot authorize a new traffic sample.
		return false
	}
	return snapshot.enabled
}

func (s *SettingService) refreshUserTraffic(parent context.Context, previous *userTrafficSnapshot) {
	defer s.userTrafficRefreshing.Store(false)
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	enabled, err := s.UserTrafficReportsEnabled(ctx)
	if parent.Err() != nil {
		return
	}
	ttl := userTrafficTTL
	if err != nil {
		enabled = false
		ttl = 5 * time.Second
	}
	// A settings save that completes during the lookup must win.
	s.userTrafficCache.CompareAndSwap(previous, &userTrafficSnapshot{enabled: enabled, expires: time.Now().Add(ttl)})
}

// StartUserTrafficRefresh keeps the capture setting fresh even on an idle gateway.
// Report requests still read the setting directly. Draining stops this background read.
func (s *SettingService) StartUserTrafficRefresh() func() {
	ctx, cancel := context.WithCancel(context.Background())
	ticker := time.NewTicker(userTrafficTTL / 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ticker.Stop()
		s.runUserTrafficRefresh(ctx, ticker.C)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *SettingService) runUserTrafficRefresh(ctx context.Context, ticks <-chan time.Time) {
	for ctx.Err() == nil {
		if s.userTrafficRefreshing.CompareAndSwap(false, true) {
			s.refreshUserTraffic(ctx, s.userTrafficCache.Load())
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
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
