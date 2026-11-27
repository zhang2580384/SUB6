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
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func qufuServiceTestConfig() map[string]string {
	return map[string]string{
		"apiBase":       "https://gateway.example.com/pay/gateway",
		"mchId":         "test-merchant",
		"merchantKey":   "0123456789abcdef0123456789abcdef",
		"notifyUrl":     "https://merchant.example.com/api/v1/payment/webhook/qufu",
		"returnUrl":     "https://merchant.example.com/payment/result",
		"alipayChannel": "10",
		"wxpayChannel":  "11",
	}
}

func TestQufuVisibleMethodSourceAndDiscovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ method, input, wantSource, wantProvider string }{
		{payment.TypeAlipay, "qufu", VisibleMethodSourceQufuAlipay, payment.TypeQufu},
		{payment.TypeAlipay, "qufu_alipay", VisibleMethodSourceQufuAlipay, payment.TypeQufu},
		{payment.TypeWxpay, "qufu", VisibleMethodSourceQufuWechat, payment.TypeQufu},
		{payment.TypeWxpay, "qufu_wxpay", VisibleMethodSourceQufuWechat, payment.TypeQufu},
		{payment.TypeAlipay, "easypay", VisibleMethodSourceEasyPayAlipay, payment.TypeEasyPay},
		{payment.TypeWxpay, "official", VisibleMethodSourceOfficialWechat, payment.TypeWxpay},
	} {
		t.Run(tc.method+"/"+tc.input, func(t *testing.T) {
			require.Equal(t, tc.wantSource, NormalizeVisibleMethodSource(tc.method, tc.input))
			key, ok := VisibleMethodProviderKeyForSource(tc.method, tc.input)
			require.True(t, ok)
			require.Equal(t, tc.wantProvider, key)
		})
	}
	require.Empty(t, NormalizeVisibleMethodSource(payment.TypeAlipay, VisibleMethodSourceQufuWechat))
	require.Equal(t, []string{payment.TypeAlipay, payment.TypeWxpay}, enabledVisibleMethodsForProvider(payment.TypeQufu, "alipay,wxpay"))
	available := buildVisibleMethodSourceAvailability([]*dbent.PaymentProviderInstance{
		{ProviderKey: payment.TypeQufu, SupportedTypes: "alipay,wxpay", Enabled: true},
		{ProviderKey: payment.TypeEasyPay, SupportedTypes: "alipay", Enabled: true},
	})
	require.True(t, available[VisibleMethodSourceQufuAlipay])
	require.True(t, available[VisibleMethodSourceQufuWechat])
	require.True(t, available[VisibleMethodSourceEasyPayAlipay])

	ctx := context.Background()
	svc := &PaymentConfigService{settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
		SettingPaymentVisibleMethodAlipaySource: VisibleMethodSourceQufuAlipay,
	}}}
	key, err := svc.resolveVisibleMethodProviderKey(ctx, payment.TypeAlipay, []*dbent.PaymentProviderInstance{
		{ProviderKey: payment.TypeEasyPay, Enabled: true},
		{ProviderKey: payment.TypeQufu, Enabled: true},
	})
	require.NoError(t, err)
	require.Equal(t, payment.TypeQufu, key, "the explicit source wins when payment methods coexist")
}

func TestQufuConfigSecretMaskAndBlankPatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}
	config := qufuServiceTestConfig()
	instance, err := svc.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeQufu, Name: "Qufu test", Config: config,
		SupportedTypes: []string{payment.TypeAlipay, payment.TypeWxpay}, Enabled: true,
	})
	require.NoError(t, err)
	listed, err := svc.ListProviderInstancesWithConfig(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NotContains(t, listed[0].Config, "merchantKey")
	require.Equal(t, config["mchId"], listed[0].Config["mchId"])
	require.True(t, isSensitiveProviderConfigField(payment.TypeQufu, "MERCHANTKEY"))
	_, err = svc.UpdateProviderInstance(ctx, instance.ID, UpdateProviderInstanceRequest{Config: map[string]string{"merchantKey": ""}})
	require.NoError(t, err)
	saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	stored, err := svc.decryptConfig(saved.Config)
	require.NoError(t, err)
	require.Equal(t, config["merchantKey"], stored["merchantKey"])
}

func TestQufuConfigRejectsUnsupportedMethodsAndRefunds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	svc := &PaymentConfigService{entClient: client}
	for _, req := range []CreateProviderInstanceRequest{
		{SupportedTypes: []string{payment.TypeStripe}},
		{SupportedTypes: []string{payment.TypeAlipay}, RefundEnabled: true},
		{SupportedTypes: []string{payment.TypeAlipay}, AllowUserRefund: true},
	} {
		req.ProviderKey, req.Name, req.Config = payment.TypeQufu, "Invalid Qufu", qufuServiceTestConfig()
		_, err := svc.CreateProviderInstance(ctx, req)
		require.Error(t, err)
		require.Equal(t, "VALIDATION_ERROR", infraerrors.Reason(err))
	}
	instance, err := svc.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeQufu, Name: "Qufu", Config: qufuServiceTestConfig(),
		SupportedTypes: []string{payment.TypeAlipay}, Enabled: true,
	})
	require.NoError(t, err)
	for _, req := range []UpdateProviderInstanceRequest{
		{SupportedTypes: []string{payment.TypeStripe}},
		{RefundEnabled: boolPtrValue(true)},
		{AllowUserRefund: boolPtrValue(true)},
	} {
		_, err := svc.UpdateProviderInstance(ctx, instance.ID, req)
		require.Error(t, err)
		require.Equal(t, "VALIDATION_ERROR", infraerrors.Reason(err))
	}
	saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.Equal(t, payment.TypeAlipay, saved.SupportedTypes)
	require.False(t, saved.RefundEnabled)
	require.False(t, saved.AllowUserRefund)
}

func TestQufuPendingAndRecoverableOrdersProtectMerchantConfiguration(t *testing.T) {
	t.Parallel()
	for _, status := range []string{OrderStatusPending, OrderStatusExpired} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			svc := &PaymentConfigService{entClient: client}
			config := qufuServiceTestConfig()
			instance, err := svc.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
				ProviderKey: payment.TypeQufu, Name: "Protected Qufu", Config: config,
				SupportedTypes: []string{payment.TypeAlipay, payment.TypeWxpay}, Enabled: true,
			})
			require.NoError(t, err)
			createPendingProviderConfigOrder(t, ctx, client, instance)
			order, err := client.PaymentOrder.Query().Only(ctx)
			require.NoError(t, err)
			if status == OrderStatusExpired {
				_, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(status).SetExpiresAt(time.Now().Add(-10 * time.Minute)).Save(ctx)
				require.NoError(t, err)
			}
			for key, changed := range map[string]string{
				"mchId": "other-merchant", "merchantKey": "abcdef0123456789abcdef0123456789",
				"apiBase": "https://other.example.com/pay/gateway", "alipayChannel": "20", "wxpayChannel": "21",
				"notifyUrl": "https://merchant.example.com/other-notify", "returnUrl": "https://merchant.example.com/other-return",
			} {
				t.Run(key, func(t *testing.T) {
					updated, err := svc.UpdateProviderInstance(ctx, instance.ID, UpdateProviderInstanceRequest{Config: map[string]string{key: changed}})
					require.Nil(t, updated)
					require.Error(t, err)
					require.Equal(t, "PENDING_ORDERS", infraerrors.Reason(err))
				})
			}
			saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
			require.NoError(t, err)
			stored, err := svc.decryptConfig(saved.Config)
			require.NoError(t, err)
			require.Equal(t, config, stored)
		})
	}
}

func TestQufuOrderSnapshotPreservesFinancialIdentityWithoutSecret(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ method, configuredChannel, wantChannel string }{
		{payment.TypeAlipay, "", "10"}, {payment.TypeWxpay, "", "11"},
		{payment.TypeAlipay, "20", "20"}, {payment.TypeWxpay, "21", "21"},
	} {
		t.Run(tc.method+tc.wantChannel, func(t *testing.T) {
			config := qufuServiceTestConfig()
			config["alipayChannel"], config["wxpayChannel"] = tc.configuredChannel, tc.configuredChannel
			snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
				InstanceID: "42", ProviderKey: payment.TypeQufu, Config: config,
			}, CreateOrderRequest{PaymentType: tc.method})
			require.Equal(t, "test-merchant", snapshot["merchant_id"])
			require.Equal(t, "CNY", snapshot["currency"])
			require.Equal(t, tc.wantChannel, snapshot["channel_type"])
			require.Equal(t, "1", snapshot["share_tag"])
			require.Equal(t, "", snapshot["share_fund_list"])
			require.NotContains(t, snapshot, "merchantKey")
			require.NotContains(t, snapshot, "config")
		})
	}
}

func qufuServiceTestMetadata(orderNo string) map[string]string {
	return map[string]string{"mch_id": "test-merchant", "order_no": orderNo, "currency": "CNY", "channel_type": "10", "share_tag": "1", "share_fund_list": ""}
}

func TestQufuMetadataRejectsWrongMerchantOrderChannelAndSharing(t *testing.T) {
	t.Parallel()
	order := &dbent.PaymentOrder{OutTradeNo: "test-order", ProviderSnapshot: buildPaymentOrderProviderSnapshot(
		&payment.InstanceSelection{InstanceID: "42", ProviderKey: payment.TypeQufu, Config: qufuServiceTestConfig()},
		CreateOrderRequest{PaymentType: payment.TypeAlipay})}
	require.NoError(t, validateProviderSnapshotMetadata(order, payment.TypeQufu, qufuServiceTestMetadata(order.OutTradeNo)))
	require.Error(t, validateProviderSnapshotMetadata(order, payment.TypeQufu, nil))
	require.Error(t, validateProviderSnapshotMetadata(&dbent.PaymentOrder{OutTradeNo: "test-order"}, payment.TypeQufu, qufuServiceTestMetadata("test-order")))
	for key, changed := range map[string]string{
		"mch_id": "other-merchant", "order_no": "other-order", "currency": "USD", "channel_type": "11", "share_tag": "0", "share_fund_list": "partner:1.00",
	} {
		t.Run(key, func(t *testing.T) {
			metadata := qufuServiceTestMetadata(order.OutTradeNo)
			metadata[key] = changed
			require.Error(t, validateProviderSnapshotMetadata(order, payment.TypeQufu, metadata))
		})
	}
}

func TestQufuInvalidNotificationsNeverAdvanceOrCreditOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	configService := &PaymentConfigService{entClient: client}
	instance, err := configService.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeQufu, Name: "Qufu order", Config: qufuServiceTestConfig(), SupportedTypes: []string{payment.TypeAlipay}, Enabled: true,
	})
	require.NoError(t, err)
	createPendingProviderConfigOrder(t, ctx, client, instance)
	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{InstanceID: strconv.FormatInt(instance.ID, 10), ProviderKey: payment.TypeQufu, Config: qufuServiceTestConfig()}, CreateOrderRequest{PaymentType: payment.TypeAlipay})
	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetProviderSnapshot(snapshot).SetPaymentTradeNo("test-upstream-trade").Save(ctx)
	require.NoError(t, err)
	userBefore, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client, configService: configService}
	for _, tc := range []struct {
		name     string
		amount   float64
		metadata map[string]string
		tradeNo  string
	}{
		{"missing metadata", order.PayAmount, nil, "test-upstream-trade"},
		{"wrong merchant", order.PayAmount, qufuServiceTestMetadata(order.OutTradeNo), "test-upstream-trade"},
		{"wrong channel", order.PayAmount, qufuServiceTestMetadata(order.OutTradeNo), "test-upstream-trade"},
		{"wrong amount", order.PayAmount + 1, qufuServiceTestMetadata(order.OutTradeNo), "test-upstream-trade"},
		{"one cent underpaid", order.PayAmount - 0.01, qufuServiceTestMetadata(order.OutTradeNo), "test-upstream-trade"},
		{"different transaction", order.PayAmount, qufuServiceTestMetadata(order.OutTradeNo), "different-upstream-trade"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "wrong merchant" {
				tc.metadata["mch_id"] = "other-merchant"
			}
			if tc.name == "wrong channel" {
				tc.metadata["channel_type"] = "11"
			}
			err := svc.HandlePaymentNotification(ctx, &payment.PaymentNotification{OrderID: order.OutTradeNo, TradeNo: tc.tradeNo, Amount: tc.amount, Status: payment.NotificationStatusSuccess, Metadata: tc.metadata}, payment.TypeQufu)
			require.Error(t, err)
			current, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, current.Status)
			require.Nil(t, current.PaidAt)
			require.Equal(t, "test-upstream-trade", current.PaymentTradeNo)
			userAfter, err := client.User.Get(ctx, order.UserID)
			require.NoError(t, err)
			require.Equal(t, userBefore.Balance, userAfter.Balance)
		})
	}

	// A valid delivery replay for an already completed order must not re-credit it.
	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).SetPaymentTradeNo("test-upstream-trade").Save(ctx)
	require.NoError(t, err)
	notification := &payment.PaymentNotification{OrderID: order.OutTradeNo, TradeNo: "test-upstream-trade", Amount: order.PayAmount, Status: payment.NotificationStatusSuccess, Metadata: qufuServiceTestMetadata(order.OutTradeNo)}
	require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu))
	require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu))
	userAfter, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, userBefore.Balance, userAfter.Balance)
}

func TestQufuQueryUsesMerchantOrderNumber(t *testing.T) {
	t.Parallel()
	key := payment.TypeQufu
	order := &dbent.PaymentOrder{OutTradeNo: "merchant-order", PaymentTradeNo: "gateway-trade", ProviderKey: &key, PaymentType: payment.TypeAlipay}
	require.Equal(t, "merchant-order", paymentOrderQueryReference(order, nil))
	require.Equal(t, "merchant-order", paymentOrderQueryReference(order, paymentFulfillmentTestProvider{key: payment.TypeQufu}))
}

func TestQufuIPv6CompatibilityPreservesOriginalOrderAddress(t *testing.T) {
	for _, tc := range []struct{ method, channel string }{{payment.TypeAlipay, "10"}, {payment.TypeWxpay, "11"}} {
		t.Run(tc.method, func(t *testing.T) {
			ctx := context.Background()
			const originalIP = "2001:db8:1234:5678::abcd"
			// Independent test vector for the documented compatibility mapping.
			// The synthetic address is only for the legacy gateway wire field.
			const gatewayIP = "254.55.132.37"
			const secret = "0123456789abcdef0123456789abcdef"
			digest := func(text string) string {
				sum := md5.Sum([]byte(text))
				return hex.EncodeToString(sum[:])
			}
			createdRequests, queriedRequests := make(chan url.Values, 1), make(chan url.Values, 1)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/pay/gateway" || r.ParseForm() != nil {
					t.Error("invalid compatibility gateway request")
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				f := r.PostForm
				var canonical string
				var response map[string]string
				switch f.Get("service") {
				case "api.trade.order.pay":
					canonical = fmt.Sprintf("mchId=%s&version=%s&service=%s&timestamp=%s&charset=%s&orderNo=%s&channelType=%s&orderAmount=%s&orderSubject=%s&notifyUrl=%s&frontUrl=%s&terminalIp=%s&shareTag=%s&shareFundList=%s&attach=%s&format=%s&signType=%s&key=%s", f.Get("mchId"), f.Get("version"), f.Get("service"), f.Get("timestamp"), f.Get("charset"), f.Get("orderNo"), f.Get("channelType"), f.Get("orderAmount"), f.Get("orderSubject"), f.Get("notifyUrl"), f.Get("frontUrl"), f.Get("terminalIp"), f.Get("shareTag"), f.Get("shareFundList"), f.Get("attach"), f.Get("format"), f.Get("signType"), secret)
					if f.Get("terminalIp") != gatewayIP || f.Get("channelType") != tc.channel || f.Get("orderAmount") != "88.00" || f.Get("shareTag") != "1" || f.Get("shareFundList") != "" {
						t.Error("gateway did not receive the expected compatible IP, channel, cents or sharing policy")
						http.Error(w, "incorrect fields", http.StatusBadRequest)
						return
					}
					createdRequests <- f
					response = map[string]string{"mchId": "test-merchant", "version": "1.0", "charset": "UTF-8", "channelType": tc.channel, "orderNo": f.Get("orderNo"), "tradeNo": "ipv6-compatible-trade", "payData": "https://checkout.example.com/pay/ipv6", "signType": "MD5"}
					response["sign"] = digest(fmt.Sprintf("mchId=test-merchant&version=1.0&charset=UTF-8&channelType=%s&orderNo=%s&tradeNo=ipv6-compatible-trade&payData=https://checkout.example.com/pay/ipv6&signType=MD5&key=%s", tc.channel, f.Get("orderNo"), secret))
				case "api.trade.order.query":
					canonical = fmt.Sprintf("mchId=%s&version=%s&service=%s&timestamp=%s&charset=%s&orderNo=%s&signType=%s&key=%s", f.Get("mchId"), f.Get("version"), f.Get("service"), f.Get("timestamp"), f.Get("charset"), f.Get("orderNo"), f.Get("signType"), secret)
					queriedRequests <- f
					response = map[string]string{"mchId": "test-merchant", "version": "1.0", "charset": "UTF-8", "channelType": tc.channel, "orderNo": f.Get("orderNo"), "tradeNo": "ipv6-compatible-trade", "orderAmount": "88.00", "orderStatus": "1", "shareTag": "1", "shareFundList": "", "signType": "MD5"}
					response["sign"] = digest(fmt.Sprintf("mchId=test-merchant&version=1.0&charset=UTF-8&channelType=%s&orderNo=%s&tradeNo=ipv6-compatible-trade&orderAmount=88.00&orderStatus=1&shareTag=1&shareFundList=&signType=MD5&key=%s", tc.channel, f.Get("orderNo"), secret))
				default:
					t.Error("unexpected compatibility gateway service")
					http.Error(w, "invalid service", http.StatusBadRequest)
					return
				}
				if f.Get("sign") != digest(canonical) {
					t.Error("gateway rejected signature over the final compatibility address")
					http.Error(w, "invalid signature", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"code": "000000", "msg": "Success", "data": response}); err != nil {
					t.Error(err)
				}
			}))
			defer gateway.Close()
			client := newPaymentConfigServiceTestClient(t)
			settings := &paymentFulfillmentSettingRepoStub{values: map[string]string{
				SettingPaymentEnabled: "true", SettingEnabledPaymentTypes: payment.TypeQufu,
				SettingPaymentVisibleMethodAlipaySource: VisibleMethodSourceQufuAlipay,
				SettingPaymentVisibleMethodWxpaySource:  VisibleMethodSourceQufuWechat,
			}}
			configService := NewPaymentConfigService(client, settings, nil)
			config := qufuServiceTestConfig()
			config["apiBase"], config["ipv6Compatibility"] = gateway.URL+"/pay/gateway", "pseudo_ipv4"
			instance, err := configService.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
				ProviderKey: payment.TypeQufu, Name: "IPv6 compatible gateway", Config: config,
				SupportedTypes: []string{payment.TypeAlipay, payment.TypeWxpay}, Enabled: true,
			})
			require.NoError(t, err)
			user, err := client.User.Create().SetEmail("ipv6-" + tc.method + "@example.invalid").SetUsername("ipv6-" + tc.method).SetPasswordHash("test-only").Save(ctx)
			require.NoError(t, err)
			svc := &PaymentService{entClient: client, configService: configService,
				loadBalancer: payment.NewDefaultLoadBalancer(client, nil), registry: payment.NewRegistry(),
				userRepo: &mockUserRepo{getByIDUser: &User{ID: user.ID, Email: user.Email, Username: user.Username, Status: StatusActive}},
			}
			req := CreateOrderRequest{UserID: user.ID, Amount: 88, PaymentType: tc.method, OrderType: payment.OrderTypeBalance,
				ClientIP: originalIP, SrcHost: "merchant.example.com", ReturnURL: "https://merchant.example.com/payment/result"}
			result, err := svc.CreateOrder(ctx, req)
			require.NoError(t, err)
			require.Equal(t, originalIP, req.ClientIP, "compatibility must not rewrite the caller's original address")
			created := <-createdRequests
			require.Equal(t, result.OutTradeNo, created.Get("orderNo"))
			require.Equal(t, gatewayIP, created.Get("terminalIp"))
			order, err := client.PaymentOrder.Get(ctx, result.OrderID)
			require.NoError(t, err)
			require.Equal(t, originalIP, order.ClientIP, "order audit data must retain the real IPv6")
			require.Equal(t, OrderStatusPending, order.Status)
			require.Nil(t, order.PaidAt)
			require.Equal(t, "ipv6-compatible-trade", order.PaymentTradeNo)
			require.Equal(t, strconv.FormatInt(instance.ID, 10), *order.ProviderInstanceID)
			originalProvider, err := svc.GetWebhookProvider(ctx, payment.TypeQufu, result.OutTradeNo)
			require.NoError(t, err)
			queried, err := originalProvider.QueryOrder(ctx, result.OutTradeNo)
			require.NoError(t, err)
			require.Equal(t, payment.ProviderStatusPending, queried.Status)
			require.Equal(t, 88.0, queried.Amount)
			require.Equal(t, order.PaymentTradeNo, queried.TradeNo)
			require.Equal(t, tc.channel, queried.Metadata["channel_type"])
			require.Equal(t, result.OutTradeNo, (<-queriedRequests).Get("orderNo"))
		})
	}
}

// This gateway uses the published wire strings independently of the provider's
// unexported signing helpers, and drives the same provider/config/order layers
// as the real application without contacting a payment network.
func TestQufuPaymentGatewayRoundTrip(t *testing.T) {
	ctx := context.Background()
	const secret = "0123456789abcdef0123456789abcdef"
	digest := func(text string) string {
		sum := md5.Sum([]byte(text))
		return hex.EncodeToString(sum[:])
	}
	var state struct {
		sync.Mutex
		amount, channel, status string
		createCalls, queryCalls int
	}
	state.amount, state.channel, state.status = "88.00", "10", "2"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pay/gateway" {
			t.Errorf("unexpected gateway request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		f := r.PostForm
		var canonical string
		var response map[string]string
		switch f.Get("service") {
		case "api.trade.order.pay":
			canonical = fmt.Sprintf("mchId=%s&version=%s&service=%s&timestamp=%s&charset=%s&orderNo=%s&channelType=%s&orderAmount=%s&orderSubject=%s&notifyUrl=%s&frontUrl=%s&terminalIp=%s&shareTag=%s&shareFundList=%s&attach=%s&format=%s&signType=%s&key=%s", f.Get("mchId"), f.Get("version"), f.Get("service"), f.Get("timestamp"), f.Get("charset"), f.Get("orderNo"), f.Get("channelType"), f.Get("orderAmount"), f.Get("orderSubject"), f.Get("notifyUrl"), f.Get("frontUrl"), f.Get("terminalIp"), f.Get("shareTag"), f.Get("shareFundList"), f.Get("attach"), f.Get("format"), f.Get("signType"), secret)
			if f.Get("orderSubject") != "SUB6 充值 & 余额+测试" || f.Get("shareTag") != "1" || f.Get("shareFundList") != "" || f.Get("orderAmount") != "88.00" {
				t.Error("gateway received incorrect raw UTF-8 subject, cents or sharing policy")
			}
			state.Lock()
			state.createCalls++
			state.Unlock()
			response = map[string]string{"mchId": f.Get("mchId"), "version": "1.0", "charset": "UTF-8", "channelType": "10", "orderNo": f.Get("orderNo"), "tradeNo": "gateway-trade", "payData": "http://checkout.example.com/pay/test", "signType": "MD5"}
			response["sign"] = digest(fmt.Sprintf("mchId=%s&version=1.0&charset=UTF-8&channelType=10&orderNo=%s&tradeNo=gateway-trade&payData=http://checkout.example.com/pay/test&signType=MD5&key=%s", f.Get("mchId"), f.Get("orderNo"), secret))
		case "api.trade.order.query":
			canonical = fmt.Sprintf("mchId=%s&version=%s&service=%s&timestamp=%s&charset=%s&orderNo=%s&signType=%s&key=%s", f.Get("mchId"), f.Get("version"), f.Get("service"), f.Get("timestamp"), f.Get("charset"), f.Get("orderNo"), f.Get("signType"), secret)
			state.Lock()
			amount, channel, status := state.amount, state.channel, state.status
			state.queryCalls++
			state.Unlock()
			response = map[string]string{"mchId": f.Get("mchId"), "version": "1.0", "charset": "UTF-8", "channelType": channel, "orderNo": f.Get("orderNo"), "tradeNo": "gateway-trade", "orderAmount": amount, "orderStatus": status, "shareTag": "1", "shareFundList": "", "signType": "MD5"}
			response["sign"] = digest(fmt.Sprintf("mchId=%s&version=1.0&charset=UTF-8&channelType=%s&orderNo=%s&tradeNo=gateway-trade&orderAmount=%s&orderStatus=%s&shareTag=1&shareFundList=&signType=MD5&key=%s", f.Get("mchId"), channel, f.Get("orderNo"), amount, status, secret))
		default:
			t.Errorf("unexpected service: %q", f.Get("service"))
			http.Error(w, "invalid service", http.StatusBadRequest)
			return
		}
		if got, want := f.Get("sign"), digest(canonical); got != want {
			t.Error("gateway rejected request signature")
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		data := make(map[string]any, len(response)+3)
		for key, value := range response {
			data[key] = value
		}
		data["unsigned_number"], data["unsigned_null"], data["unsigned_object"] = 7, nil, map[string]any{"unused": true}
		if err := json.NewEncoder(w).Encode(map[string]any{"code": "000000", "msg": "Success", "data": data}); err != nil {
			t.Error(err)
		}
	}))
	defer gateway.Close()
	config := qufuServiceTestConfig()
	config["apiBase"] = gateway.URL + "/pay/gateway"
	client := newPaymentConfigServiceTestClient(t)
	configService := &PaymentConfigService{entClient: client}
	instance, err := configService.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeQufu, Name: "Local gateway", Config: config, SupportedTypes: []string{payment.TypeAlipay, payment.TypeWxpay}, Enabled: true,
	})
	require.NoError(t, err)
	createPendingProviderConfigOrder(t, ctx, client, instance)
	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{InstanceID: strconv.FormatInt(instance.ID, 10), ProviderKey: payment.TypeQufu, Config: config}, CreateOrderRequest{PaymentType: payment.TypeAlipay})
	order, err = client.PaymentOrder.UpdateOneID(order.ID).SetProviderSnapshot(snapshot).SetPaymentTradeNo("gateway-trade").Save(ctx)
	require.NoError(t, err)
	q, err := provider.NewQufu(strconv.FormatInt(instance.ID, 10), config)
	require.NoError(t, err)
	created, err := q.CreatePayment(ctx, payment.CreatePaymentRequest{OrderID: order.OutTradeNo, Amount: "88.00", PaymentType: payment.TypeAlipay, Subject: "SUB6 充值 & 余额+测试", ClientIP: "127.0.0.1"})
	require.NoError(t, err)
	require.Equal(t, "gateway-trade", created.TradeNo)
	require.Equal(t, "http://checkout.example.com/pay/test", created.QRCode)

	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID, Email: order.UserEmail, Username: order.UserName}}
	userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
		require.Equal(t, order.UserID, id)
		userRepo.getByIDUser.Balance += amount
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{codesByCode: map[string]*RedeemCode{
		order.RechargeCode: {ID: 1, Code: order.RechargeCode, Type: RedeemTypeBalance, Value: order.Amount, Status: StatusUnused},
	}}
	svc := &PaymentService{
		entClient: client, configService: configService,
		loadBalancer: payment.NewDefaultLoadBalancer(client, nil),
		userRepo:     userRepo, redeemService: NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil),
	}
	for _, tc := range []struct{ name, amount, channel string }{
		{"one cent underpaid", "87.99", "10"}, {"wrong channel", "88.00", "11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state.Lock()
			state.amount, state.channel = tc.amount, tc.channel
			state.Unlock()
			require.Empty(t, svc.reconcilePaid(ctx, order), "rejected query must not report a recovered payment")
			current, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, OrderStatusPending, current.Status)
			require.Equal(t, "gateway-trade", current.PaymentTradeNo)
			require.Nil(t, current.PaidAt)
			require.Zero(t, userRepo.getByIDUser.Balance)
			require.Empty(t, redeemRepo.useCalls)
		})
	}
	state.Lock()
	state.amount, state.channel, state.status = "88.00", "10", "1"
	state.Unlock()
	callback := url.Values{"mchId": {"test-merchant"}, "channelType": {"10"}, "orderNo": {order.OutTradeNo}, "tradeNo": {"gateway-trade"}, "orderStatus": {"2"}, "orderAmount": {"88.00"}, "orderTime": {"2026-11-27 12:00:00"}, "successTime": {"2026-11-27 12:01:00"}, "attach": {""}, "signType": {"MD5"}}
	callback.Set("sign", digest(fmt.Sprintf("mchId=test-merchant&channelType=10&orderNo=%s&tradeNo=gateway-trade&orderStatus=2&orderAmount=88.00&orderTime=2026-11-27 12:00:00&successTime=2026-11-27 12:01:00&attach=&signType=MD5&key=%s", order.OutTradeNo, secret)))
	notification, err := q.VerifyNotification(ctx, callback.Encode(), nil)
	require.NoError(t, err)
	require.Error(t, svc.ConfirmNotificationUpstream(ctx, q, notification), "signed callback cannot override unpaid upstream state")
	callback.Set("orderAmount", "0.01")
	_, err = q.VerifyNotification(ctx, callback.Encode(), nil)
	require.Error(t, err, "tampered callback cannot reach fulfillment")
	callback.Set("orderAmount", "88.00")
	state.Lock()
	state.status = "2"
	state.Unlock()
	notification, err = q.VerifyNotification(ctx, callback.Encode(), nil)
	require.NoError(t, err)
	require.NoError(t, svc.ConfirmNotificationUpstream(ctx, q, notification))
	require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu))
	require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu))
	current, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, current.Status)
	require.Equal(t, 88.0, userRepo.getByIDUser.Balance)
	require.Len(t, redeemRepo.useCalls, 1, "successful callback replay cannot credit twice")
	state.Lock()
	require.Equal(t, 1, state.createCalls)
	require.GreaterOrEqual(t, state.queryCalls, 4)
	state.Unlock()
}
