package routes

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type trafficGateFixture struct {
	service.SettingRepository
	setting string
	err     error
}

func (r *trafficGateFixture) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{service.SettingKeyUserTrafficEnabled: r.setting}, r.err
}
func TestUserTrafficCachedPageCannotReadDisabledReport(t *testing.T) {
	r := gin.New()
	repo := &trafficGateFixture{}
	s := service.NewSettingService(repo, &config.Config{})
	queries := 0
	r.GET("/usage/traffic", userTrafficReportGuard(s), func(c *gin.Context) { queries++; c.Status(200) })
	r.GET("/usage", func(c *gin.Context) { c.Status(200) })
	request := func(path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w.Code
	}
	require.Equal(t, 200, request("/usage/traffic"))
	repo.setting = "false"
	require.Equal(t, 404, request("/usage/traffic"))
	require.Equal(t, 200, request("/usage"))
	repo.err = errors.New("fixture temporarily unavailable")
	require.Equal(t, 503, request("/usage/traffic"))
	require.Equal(t, 1, queries)
}
