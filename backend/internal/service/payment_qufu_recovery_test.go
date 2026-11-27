//go:build unit

package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/stretchr/testify/require"
)

type qufuRecoveryFixture struct {
	svc      *PaymentService
	client   *dbent.Client
	order    *dbent.PaymentOrder
	provider *provider.Qufu
	userRepo *mockUserRepo
	redeems  *paymentOrderLifecycleRedeemRepo
}

func qufuRecoveryDigest(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

// All evidence is fictional. The real adapter verifies a signed callback and
// independently signed query from an isolated loopback gateway before ordinary
// order validation and balance fulfillment run against SQLite.
func newQufuRecoveryFixture(t *testing.T, status string, expires, updated time.Time) *qufuRecoveryFixture {
	t.Helper()
	ctx := context.Background()
	f := &qufuRecoveryFixture{client: newPaymentConfigServiceTestClient(t)}
	const secret = "0123456789abcdef0123456789abcdef"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pay/gateway" || r.ParseForm() != nil {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
		fields := r.PostForm
		canonical := fmt.Sprintf("mchId=%s&version=%s&service=%s&timestamp=%s&charset=%s&orderNo=%s&signType=%s&key=%s", fields.Get("mchId"), fields.Get("version"), fields.Get("service"), fields.Get("timestamp"), fields.Get("charset"), fields.Get("orderNo"), fields.Get("signType"), secret)
		if fields.Get("service") != "api.trade.order.query" || fields.Get("mchId") != "test-merchant" || fields.Get("orderNo") != f.order.OutTradeNo || fields.Get("sign") != qufuRecoveryDigest(canonical) {
			http.Error(w, "invalid query identity or signature", http.StatusBadRequest)
			return
		}
		response := map[string]string{"mchId": "test-merchant", "version": "1.0", "charset": "UTF-8", "channelType": "10", "orderNo": f.order.OutTradeNo, "tradeNo": "recovery-transaction", "orderAmount": "88.00", "orderStatus": "2", "shareTag": "1", "shareFundList": "", "signType": "MD5"}
		response["sign"] = qufuRecoveryDigest(fmt.Sprintf("mchId=test-merchant&version=1.0&charset=UTF-8&channelType=10&orderNo=%s&tradeNo=recovery-transaction&orderAmount=88.00&orderStatus=2&shareTag=1&shareFundList=&signType=MD5&key=%s", f.order.OutTradeNo, secret))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": response})
	}))
	t.Cleanup(gateway.Close)
	config := qufuServiceTestConfig()
	config["apiBase"] = gateway.URL + "/pay/gateway"
	configService := &PaymentConfigService{entClient: f.client}
	instance, err := configService.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeQufu, Name: "Recovery fixture", Config: config,
		SupportedTypes: []string{payment.TypeAlipay}, Enabled: true,
	})
	require.NoError(t, err)
	createPendingProviderConfigOrder(t, ctx, f.client, instance)
	order, err := f.client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	instanceID := strconv.FormatInt(instance.ID, 10)
	snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{InstanceID: instanceID, ProviderKey: payment.TypeQufu, Config: config}, CreateOrderRequest{PaymentType: payment.TypeAlipay})
	f.order, err = f.client.PaymentOrder.UpdateOneID(order.ID).SetProviderSnapshot(snapshot).
		SetStatus(status).SetExpiresAt(expires).SetUpdatedAt(updated).Save(ctx)
	require.NoError(t, err)
	f.provider, err = provider.NewQufu(instanceID, config)
	require.NoError(t, err)
	f.userRepo = &mockUserRepo{getByIDUser: &User{ID: order.UserID, Email: order.UserEmail, Username: order.UserName}}
	f.userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
		require.Equal(t, order.UserID, id)
		f.userRepo.getByIDUser.Balance += amount
		return nil
	}
	f.redeems = &paymentOrderLifecycleRedeemRepo{codesByCode: map[string]*RedeemCode{
		order.RechargeCode: {ID: 1, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUnused},
	}}
	f.svc = &PaymentService{entClient: f.client, configService: configService,
		loadBalancer: payment.NewDefaultLoadBalancer(f.client, nil), userRepo: f.userRepo,
		redeemService: NewRedeemService(f.redeems, f.userRepo, nil, nil, nil, f.client, nil, nil)}
	return f
}

func (f *qufuRecoveryFixture) confirmedNotification(t *testing.T) *payment.PaymentNotification {
	t.Helper()
	ctx := context.Background()
	fields := url.Values{"mchId": {"test-merchant"}, "channelType": {"10"}, "orderNo": {f.order.OutTradeNo}, "tradeNo": {"recovery-transaction"}, "orderStatus": {"2"}, "orderAmount": {"88.00"}, "orderTime": {"2026-11-27 12:00:00"}, "successTime": {"2026-11-27 12:01:00"}, "attach": {""}, "signType": {"MD5"}}
	fields.Set("sign", qufuRecoveryDigest(fmt.Sprintf("mchId=test-merchant&channelType=10&orderNo=%s&tradeNo=recovery-transaction&orderStatus=2&orderAmount=88.00&orderTime=2026-11-27 12:00:00&successTime=2026-11-27 12:01:00&attach=&signType=MD5&key=0123456789abcdef0123456789abcdef", f.order.OutTradeNo)))
	notification, err := f.provider.VerifyNotification(ctx, fields.Encode(), nil)
	require.NoError(t, err)
	require.NoError(t, f.svc.ConfirmNotificationUpstream(ctx, f.provider, notification))
	return notification
}

func TestQufuRecoveryExpiredPaymentSettlesExactlyOnce(t *testing.T) {
	for _, age := range []time.Duration{6 * time.Minute, 2 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			f := newQufuRecoveryFixture(t, OrderStatusExpired, now.Add(-age), now.Add(-4*time.Hour))
			notification := f.confirmedNotification(t)
			require.NoError(t, f.svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu))
			require.NoError(t, f.svc.HandlePaymentNotification(ctx, f.confirmedNotification(t), payment.TypeQufu))
			current, err := f.client.PaymentOrder.Get(ctx, f.order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusCompleted, current.Status)
			require.NotNil(t, current.PaidAt)
			require.Equal(t, notification.TradeNo, current.PaymentTradeNo)
			require.True(t, f.order.ExpiresAt.Equal(current.ExpiresAt))
			require.Equal(t, f.order.Amount, f.userRepo.getByIDUser.Balance)
			require.Len(t, f.redeems.useCalls, 1)
		})
	}
}

func TestQufuRecoveryPastDeadlineRequiresManualReconciliation(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	f := newQufuRecoveryFixture(t, OrderStatusExpired, now.Add(-qufuOrderRecoveryWindow-time.Minute), now)
	err := f.svc.HandlePaymentNotification(ctx, f.confirmedNotification(t), payment.TypeQufu)
	require.ErrorContains(t, err, "manual reconciliation required")
	require.Empty(t, f.svc.reconcilePaid(ctx, f.order), "confirmed upstream payment must not report recovery outside the fixed window")
	current, err := f.client.PaymentOrder.Get(ctx, f.order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusExpired, current.Status)
	require.True(t, f.order.UpdatedAt.Equal(current.UpdatedAt), "retry cannot refresh the recovery window")
	require.True(t, f.order.ExpiresAt.Equal(current.ExpiresAt))
	require.Nil(t, current.PaidAt)
	require.Empty(t, current.PaymentTradeNo)
	require.Zero(t, f.userRepo.getByIDUser.Balance)
	require.Empty(t, f.redeems.useCalls)
	audits, err := f.svc.GetOrderAuditLogs(ctx, f.order.ID)
	require.NoError(t, err)
	require.NotEmpty(t, audits)
	for _, audit := range audits {
		require.Equal(t, "PAYMENT_AFTER_EXPIRY", audit.Action)
		require.Contains(t, audit.Detail, "manual reconciliation required")
		require.Contains(t, audit.Detail, "recovery_until")
	}
}

func TestQufuRecoveryDoesNotResurrectExcludedStates(t *testing.T) {
	for _, status := range []string{OrderStatusCancelled, OrderStatusFailed, OrderStatusRefunded} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			f := newQufuRecoveryFixture(t, status, now.Add(-time.Minute), now)
			err := f.svc.HandlePaymentNotification(ctx, f.confirmedNotification(t), payment.TypeQufu)
			if status == OrderStatusRefunded {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "manual reconciliation")
			}
			current, err := f.client.PaymentOrder.Get(ctx, f.order.ID)
			require.NoError(t, err)
			require.Equal(t, status, current.Status)
			require.Nil(t, current.PaidAt)
			require.Empty(t, current.PaymentTradeNo)
			require.Zero(t, f.userRepo.getByIDUser.Balance)
			require.Empty(t, f.redeems.useCalls)
		})
	}
}

func TestQufuRecoveryRetainsFinancialValidation(t *testing.T) {
	for _, invalid := range []string{"merchant", "channel", "amount", "sharing"} {
		t.Run(invalid, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			f := newQufuRecoveryFixture(t, OrderStatusExpired, now.Add(-2*time.Hour), now)
			notification := f.confirmedNotification(t)
			switch invalid {
			case "merchant":
				notification.Metadata["mch_id"] = "another-merchant"
			case "channel":
				notification.Metadata["channel_type"] = "11"
			case "amount":
				notification.Amount -= 0.01
			case "sharing":
				notification.Metadata["share_tag"] = "0"
			}
			require.Error(t, f.svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu))
			current, err := f.client.PaymentOrder.Get(ctx, f.order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusExpired, current.Status)
			require.Nil(t, current.PaidAt)
			require.Zero(t, f.userRepo.getByIDUser.Balance)
			require.Empty(t, f.redeems.useCalls)
		})
	}
}

func TestQufuRecoveryRetriesPreviouslyPaidFulfillmentFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	f := newQufuRecoveryFixture(t, OrderStatusFailed, now.Add(-4*time.Hour), now)
	paidAt := now.Add(-2 * time.Hour)
	_, err := f.client.PaymentOrder.UpdateOneID(f.order.ID).
		SetPaidAt(paidAt).SetPaymentTradeNo("recovery-transaction").
		SetFailedAt(now).SetFailedReason("temporary fixture fulfillment failure").Save(ctx)
	require.NoError(t, err)
	require.NoError(t, f.svc.HandlePaymentNotification(ctx, f.confirmedNotification(t), payment.TypeQufu))
	require.NoError(t, f.svc.HandlePaymentNotification(ctx, f.confirmedNotification(t), payment.TypeQufu))
	current, err := f.client.PaymentOrder.Get(ctx, f.order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, current.Status)
	require.NotNil(t, current.PaidAt)
	require.True(t, paidAt.Equal(*current.PaidAt), "retry must preserve the previous verified payment time")
	require.Equal(t, f.order.Amount, f.userRepo.getByIDUser.Balance)
	require.Len(t, f.redeems.useCalls, 1)
}
