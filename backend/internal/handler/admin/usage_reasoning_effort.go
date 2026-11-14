package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/gin-gonic/gin"
)

func parseUsageReasoningEffort(c *gin.Context) (string, error) {
	return usagestats.ParseReasoningEffortFilter(c.Query("reasoning_effort"))
}
