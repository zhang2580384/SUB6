package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// adminUserQuotaDefaults distinguishes an absent policy from unreadable settings.
// Only configured limits need a database row. An explicit zero is a limit.
func (s *SettingService) adminUserQuotaDefaults(ctx context.Context) (map[string]*DefaultPlatformQuotaSetting, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("default platform quota settings unavailable")
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultPlatformQuotas)
	if errors.Is(err, ErrSettingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read default platform quotas: %w", err)
	}
	var quotas map[string]*DefaultPlatformQuotaSetting
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if !json.Valid([]byte(raw)) || decoder.Decode(&quotas) != nil || quotas == nil {
		return nil, errors.New("invalid default platform quota settings")
	}
	if err := validateDefaultPlatformQuotaMap(quotas); err != nil {
		return nil, fmt.Errorf("invalid default platform quotas: %w", err)
	}
	for platform, quota := range quotas {
		if !quota.HasAnyLimit() {
			delete(quotas, platform)
		}
	}
	return quotas, nil
}

// snapshotUserPlatformQuotaDefaults initializes configured limits for a new user.
// The caller chooses the transaction and how to handle failures.
func snapshotUserPlatformQuotaDefaults(ctx context.Context, repo UserPlatformQuotaRepository, userID int64, quotas map[string]*DefaultPlatformQuotaSetting) error {
	if repo == nil || userID <= 0 || len(quotas) == 0 {
		return nil
	}
	// Missing rows mean unlimited; explicit zero is a configured limit.
	records := make([]UserPlatformQuotaRecord, 0, len(quotas))
	for platform, quota := range quotas {
		if !quota.HasAnyLimit() {
			continue
		}
		records = append(records, UserPlatformQuotaRecord{
			UserID:          userID,
			Platform:        platform,
			DailyLimitUSD:   quota.DailyLimitUSD,
			WeeklyLimitUSD:  quota.WeeklyLimitUSD,
			MonthlyLimitUSD: quota.MonthlyLimitUSD,
		})
	}
	if len(records) == 0 {
		return nil
	}
	return repo.BulkInsertInitial(ctx, records)
}
