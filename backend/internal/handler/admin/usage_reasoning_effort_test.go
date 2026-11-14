package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageReasoningEffortFilters(t *testing.T) {
	for _, path := range []string{"/admin/usage", "/admin/usage/stats"} {
		for _, effort := range []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max", "unknown", " X-High "} {
			t.Run(path+"/"+effort, func(t *testing.T) {
				repo := &adminUsageRepoCapture{}
				router := newAdminUsageRequestTypeTestRouter(repo)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?reasoning_effort="+url.QueryEscape(effort)+"&user_id=42&group_id=7&platform=openai&model=fixture-model&nocache=true", nil))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				filters := repo.listFilters
				if path == "/admin/usage/stats" {
					filters = repo.statsFilters
				}
				want := effort
				if effort == " X-High " {
					want = "xhigh"
				}
				require.Equal(t, want, filters.ReasoningEffort)
				require.Equal(t, int64(42), filters.UserID)
				require.Equal(t, int64(7), filters.GroupID)
				require.Equal(t, "openai", filters.Platform)
				require.Equal(t, "fixture-model", filters.Model)
			})
		}
	}
}

func TestAdminUsageReasoningEffortValidation(t *testing.T) {
	paths := []string{"/admin/usage", "/admin/usage/stats", "/admin/dashboard/trend", "/admin/dashboard/models", "/admin/dashboard/groups", "/admin/dashboard/snapshot"}
	for _, path := range paths {
		for _, value := range []string{"unsupported", "high,low", "high' OR 1=1 --"} {
			t.Run(path+"/"+value, func(t *testing.T) {
				usageRepo := &adminUsageRepoCapture{}
				router := newAdminUsageRequestTypeTestRouter(usageRepo)
				if strings.HasPrefix(path, "/admin/dashboard/") {
					router = newDashboardRequestTypeTestRouter(&dashboardUsageRepoCapture{})
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?reasoning_effort="+url.QueryEscape(value), nil))
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Contains(t, rec.Body.String(), "reasoning_effort")
			})
		}
	}
}

func TestDashboardReasoningEffortFiltersAndCaches(t *testing.T) {
	t.Cleanup(resetDashboardReadCachesForTest)
	for _, endpoint := range []string{"trend", "models", "groups", "snapshot"} {
		t.Run(endpoint, func(t *testing.T) {
			resetDashboardReadCachesForTest()
			repo := &dashboardUsageRepoCapture{}
			router := newDashboardRequestTypeTestRouter(repo)
			for _, effort := range []string{"", "high", "low", "none", "unknown", "xhigh"} {
				query := "/admin/dashboard/" + endpoint + "?start_date=2026-11-01&end_date=2026-11-07&include_stats=false&include_group_stats=true&user_id=42&group_id=7&reasoning_effort=" + effort
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, query, nil))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Equal(t, "miss", rec.Header().Get("X-Snapshot-Cache"))
				var filters []usagestats.UsageLogFilters
				switch endpoint {
				case "trend":
					filters = append(filters, repo.trendFilters)
				case "models":
					filters = append(filters, repo.modelFilters)
				case "groups":
					filters = append(filters, repo.groupFilters)
				case "snapshot":
					filters = append(filters, repo.trendFilters, repo.modelFilters, repo.groupFilters)
				}
				for _, filter := range filters {
					require.Equal(t, effort, filter.ReasoningEffort)
					require.Equal(t, int64(42), filter.UserID)
					require.Equal(t, int64(7), filter.GroupID)
				}
				repeat := httptest.NewRecorder()
				router.ServeHTTP(repeat, httptest.NewRequest(http.MethodGet, query, nil))
				require.Equal(t, http.StatusOK, repeat.Code)
				require.Equal(t, "hit", repeat.Header().Get("X-Snapshot-Cache"))
			}
		})
	}
}

func TestUsageReasoningStatsCacheScope(t *testing.T) {
	previous := usageStatsCache
	usageStatsCache = newSnapshotCache(30 * time.Second)
	t.Cleanup(func() { usageStatsCache = previous })
	repo := &adminUsageRepoCapture{}
	router := newAdminUsageRequestTypeTestRouter(repo)
	for _, effort := range []string{"", "high", "low", "none", "unknown"} {
		query := "/admin/usage/stats?start_date=2026-11-01&end_date=2026-11-07&reasoning_effort=" + effort
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, query, nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "miss", rec.Header().Get("X-Usage-Stats-Cache"))
		require.Equal(t, effort, repo.statsFilters.ReasoningEffort)
		repeat := httptest.NewRecorder()
		router.ServeHTTP(repeat, httptest.NewRequest(http.MethodGet, query, nil))
		require.Equal(t, "hit", repeat.Header().Get("X-Usage-Stats-Cache"))
	}
}
