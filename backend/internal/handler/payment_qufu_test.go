//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQufuWebhookUsesUnambiguousMerchantOrderNumber(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, want string }{
		{"normal", "mchId=test-merchant&orderNo=merchant-order&orderStatus=2", "merchant-order"},
		{"wrong field", "out_trade_no=other-provider-order", ""},
		{"duplicate", "orderNo=first&orderNo=second", ""},
		{"duplicate identical", "orderNo=first&orderNo=first", ""},
		{"malformed", "orderNo=%xx", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, extractOutTradeNo(tc.raw, payment.TypeQufu))
		})
	}
}

func TestQufuWebhookRejectsMissingOrDuplicateOrderBeforeProviderLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, raw := range []string{"", "orderNo=first&orderNo=second"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/payment/webhook/qufu?"+raw, nil)
		// A missing concrete service ensures invalid input never reaches lookup.
		(&PaymentWebhookHandler{}).QufuNotify(c)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Equal(t, "fail", w.Body.String())
	}
}

func TestQufuWebhookAcknowledgesWithExactPlainSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	writeSuccessResponse(c, payment.TypeQufu)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "success", w.Body.String())
	require.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}
