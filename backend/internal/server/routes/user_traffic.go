package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func userTrafficReportGuard(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		enabled, err := settings.UserTrafficReportsEnabled(ctx)
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "Traffic settings temporarily unavailable")
			c.Abort()
			return
		}
		if !enabled {
			response.NotFound(c, "Traffic reporting is disabled")
			c.Abort()
			return
		}
		c.Next()
	}
}
