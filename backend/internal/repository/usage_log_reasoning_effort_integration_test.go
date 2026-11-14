//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageReasoningEffortHistoricalQueries(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "reasoning-fixture@example.invalid"})
	otherUser := mustCreateUser(t, client, &service.User{Email: "reasoning-other@example.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "synthetic-reasoning-key", Name: "Fixture"})
	otherKey := mustCreateApiKey(t, client, &service.APIKey{UserID: otherUser.ID, Key: "synthetic-other-key", Name: "Other fixture"})
	account := mustCreateAccount(t, client, &service.Account{Name: "Fixture", Platform: "openai"})
	group := mustCreateGroup(t, client, &service.Group{Name: "Fixture", Platform: "openai"})
	otherGroup := mustCreateGroup(t, client, &service.Group{Name: "Other fixture", Platform: "openai"})
	start := time.Now().UTC().Add(-time.Hour)
	end := start.Add(time.Hour)
	ptr := func(value string) *string { return &value }
	rows := []struct{ requested, forwarded *string }{
		{ptr("high"), ptr("low")}, {nil, ptr("high")}, {ptr("high"), nil}, {ptr(" \t "), ptr(" HIGH\t")},
		{ptr("low"), ptr("high")}, {ptr("none"), ptr("low")}, {ptr("minimal"), ptr("low")},
		{ptr("medium"), ptr("medium")}, {ptr("max"), ptr("max")},
		{ptr("x-high"), ptr("low")}, {ptr("extra_high"), ptr("low")},
		{nil, nil}, {ptr(" "), ptr("")}, {ptr(" "), ptr("\t")},
	}
	for i, row := range rows {
		for j, scope := range []struct{ userID, keyID, groupID int64 }{
			{user.ID, key.ID, group.ID}, {user.ID, key.ID, otherGroup.ID}, {otherUser.ID, otherKey.ID, group.ID},
		} {
			log := &service.UsageLog{UserID: scope.userID, APIKeyID: scope.keyID, AccountID: account.ID,
				GroupID: &scope.groupID, Platform: "openai", RequestID: fmt.Sprintf("reasoning-%d-%d", i, j),
				Model: "internal-route", RequestedModel: "fixture-model", UpstreamModel: ptr("upstream-fixture"),
				InputTokens: 10, OutputTokens: 20, TotalCost: 2, ActualCost: 1, CreatedAt: start.Add(time.Minute)}
			_, err := repo.Create(ctx, log)
			require.NoError(t, err)
			// Preserve NULL/blank legacy forms exactly, independently of insertion normalization.
			_, err = tx.ExecContext(ctx, "UPDATE usage_logs SET requested_reasoning_effort=$1, reasoning_effort=$2 WHERE id=$3", row.requested, row.forwarded, log.ID)
			require.NoError(t, err)
		}
	}
	for effort, count := range map[string]int64{"": 14, "high": 4, "low": 1, "none": 1, "minimal": 1, "medium": 1, "max": 1, "xhigh": 2, "unknown": 3} {
		t.Run(effort, func(t *testing.T) {
			filters := usagestats.UsageLogFilters{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID,
				GroupID: group.ID, Model: "fixture-model", ModelFilterSource: usagestats.ModelSourceRequested,
				Platform: "openai", ReasoningEffort: effort, StartTime: &start, EndTime: &end}
			listed, page, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 1}, filters)
			require.NoError(t, err)
			require.Len(t, listed, 1)
			require.Equal(t, count, page.Total, "count must reflect requested effort and fallback, not forwarded effort alone")
			stats, err := repo.GetStatsWithFilters(ctx, filters)
			require.NoError(t, err)
			require.Equal(t, count, stats.TotalRequests)
			require.Equal(t, count*30, stats.TotalTokens)
			require.Equal(t, float64(count), stats.TotalActualCost)
			filters.ExactTotal = true
			exported, exportPage, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 100}, filters)
			require.NoError(t, err)
			require.Len(t, exported, int(count))
			require.Equal(t, count, exportPage.Total)
			trend, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, "day", filters)
			require.NoError(t, err)
			require.Len(t, trend, 1)
			require.Equal(t, count, trend[0].Requests)
			for _, source := range []string{usagestats.ModelSourceRequested, usagestats.ModelSourceUpstream, usagestats.ModelSourceMapping} {
				models, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, filters, source)
				require.NoError(t, err)
				require.Len(t, models, 1)
				require.Equal(t, count, models[0].Requests)
			}
			groups, err := repo.GetGroupStatsWithUsageFilters(ctx, start, end, filters)
			require.NoError(t, err)
			require.Len(t, groups, 1)
			require.Equal(t, count, groups[0].Requests)
			filters.GroupID = 0
			_, acrossGroups, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 100}, filters)
			require.NoError(t, err)
			require.Equal(t, count*2, acrossGroups.Total, "other users must remain excluded")
		})
	}
}
