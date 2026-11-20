package middleware

import (
	"errors"
	"net/http"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"

	"github.com/gin-gonic/gin"
)

const requestBodyRouteLimitKey = "gateway_request_body_limit"

// RequestBodyLimit 使用 MaxBytesReader 限制请求体大小。
func RequestBodyLimit(maxBytes int64, budgets ...*pkghttputil.BodyMemoryBudget) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(requestBodyRouteLimitKey, maxBytes)
		if len(budgets) > 0 {
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(c.Request.Context(), budgets[0])
			defer release()
			c.Request = c.Request.WithContext(ctx)
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

// ReadGatewayRequestBody reads with the matched route's decoded size limit and
// writes a protocol-specific error on failure. A caller that continues the chain
// restores the original or rewritten body for the handler.
func ReadGatewayRequestBody(c *gin.Context) ([]byte, bool) {
	limit := c.GetInt64(requestBodyRouteLimitKey)
	body, err := pkghttputil.ReadRequestBodyWithPreallocLimit(c.Request, limit)
	// An earlier middleware may already have buffered this body.
	if err == nil && limit > 0 && int64(len(body)) > limit {
		err = &http.MaxBytesError{Limit: limit}
	}
	if err == nil {
		return body, true
	}
	if RejectRequestBodyReadError(c, err) {
		return nil, false
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeGatewayInvalidRequest(c, http.StatusRequestEntityTooLarge, pkghttputil.BodyTooLargeMessage(maxErr.Limit))
	} else {
		writeGatewayInvalidRequest(c, http.StatusBadRequest, "Failed to read request body")
	}
	c.Abort()
	return nil, false
}
