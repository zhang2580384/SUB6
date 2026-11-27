//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/provider"
)

// TestQufuLiveGateway is opt-in: it creates two unpaid CNY 0.01 orders. It never
// executes a payment, refund or settlement. Credentials and payment URLs stay in
// the explicitly supplied private evidence file, outside the source checkout.
func TestQufuLiveGateway(t *testing.T) {
	configPath := os.Getenv("SUB6_QUFU_LIVE_CONFIG")
	if configPath == "" || os.Getenv("SUB6_QUFU_LIVE_CREATE") != "1" {
		t.Skip("requires explicit private merchant config and unpaid-order opt-in")
	}
	evidencePath := os.Getenv("SUB6_QUFU_LIVE_EVIDENCE")
	if evidencePath == "" || !filepath.IsAbs(configPath) || !filepath.IsAbs(evidencePath) {
		t.Fatal("live tests require absolute private config and evidence paths")
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("could not read private live-test configuration")
	}
	var config map[string]string
	if json.Unmarshal(content, &config) != nil || len(config["merchantKey"]) != 32 {
		t.Fatal("invalid private live-test configuration")
	}
	var mu sync.Mutex
	var exchanges []map[string]any
	transport := http.DefaultTransport
	http.DefaultTransport = qufuLiveRecordingTransport{base: transport, record: func(status int, body []byte) {
		body = bytes.ReplaceAll(body, []byte(config["merchantKey"]), []byte("[redacted]"))
		body = bytes.ReplaceAll(body, []byte(url.QueryEscape(config["merchantKey"])), []byte("[redacted]"))
		mu.Lock()
		exchanges = append(exchanges, map[string]any{"http_status": status, "response": string(body)})
		mu.Unlock()
	}}
	defer func() { http.DefaultTransport = transport }()
	var orders []map[string]any
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		result := map[string]any{"created_at": time.Now().UTC().Format(time.RFC3339), "amount_cny": "0.01", "orders": orders, "exchanges": exchanges}
		body, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr != nil || os.WriteFile(evidencePath, body, 0600) != nil {
			t.Error("could not write private live-test evidence")
		}
	}()
	q, err := provider.NewQufu("live-test", config)
	if err != nil {
		t.Fatal("private merchant configuration rejected")
	}
	for _, method := range []string{payment.TypeAlipay, payment.TypeWxpay} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			orderNo := "SUB6QF" + time.Now().UTC().Format("20060102150405") + strings.ToUpper(method[:1])
			evidence := map[string]any{"method": method, "order_no": orderNo}
			orders = append(orders, evidence)
			created, createErr := q.CreatePayment(ctx, payment.CreatePaymentRequest{OrderID: orderNo, Amount: "0.01", PaymentType: method, Subject: "SUB6 接口联调", ClientIP: "192.0.2.1"})
			if createErr != nil {
				evidence["create_error"] = createErr.Error()
				t.Error("live create failed; inspect the private evidence file")
				return
			}
			evidence["create"] = created
			queried, queryErr := q.QueryOrder(ctx, orderNo)
			if queryErr != nil {
				evidence["query_error"] = queryErr.Error()
				t.Error("live query failed; inspect the private evidence file")
				return
			}
			evidence["query"] = queried
			if created.TradeNo != queried.TradeNo || queried.Amount != 0.01 {
				t.Error("live query does not match the unpaid test order")
			}
		})
	}
}

// TestQufuLivePaidEvidence verifies captured, already-paid test orders against
// the real gateway, then replays those exact signed notifications through an
// isolated SUB6 fulfillment fixture. It never creates or pays a gateway order.
func TestQufuLivePaidEvidence(t *testing.T) {
	if os.Getenv("SUB6_QUFU_LIVE_PAID_VERIFY") != "1" {
		t.Skip("requires explicit already-paid evidence verification opt-in")
	}
	configPath, evidencePath := os.Getenv("SUB6_QUFU_LIVE_CONFIG"), os.Getenv("SUB6_QUFU_LIVE_EVIDENCE")
	if !filepath.IsAbs(configPath) || !filepath.IsAbs(evidencePath) {
		t.Fatal("paid verification requires absolute private config and evidence paths")
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("could not read private merchant configuration")
	}
	var config map[string]string
	if json.Unmarshal(configData, &config) != nil || len(config["merchantKey"]) != 32 {
		t.Fatal("invalid private merchant configuration")
	}
	var evidence struct {
		Orders []struct {
			Method  string `json:"method"`
			OrderNo string `json:"order_no"`
			TradeNo string `json:"trade_no"`
			Status  string `json:"status"`
		} `json:"orders"`
		Events []struct {
			ReceivedQuery string `json:"received_query"`
			Raw           string `json:"raw"`
			Outcome       string `json:"outcome"`
		} `json:"events"`
	}
	evidenceData, err := os.ReadFile(evidencePath)
	if err != nil || json.Unmarshal(evidenceData, &evidence) != nil || len(evidence.Orders) != 2 {
		t.Fatal("paid verification requires exactly two captured current orders")
	}
	q, err := provider.NewQufu("paid-evidence", config)
	if err != nil {
		t.Fatal("private merchant configuration rejected")
	}

	// Live identifiers and gateway payloads must remain in private evidence,
	// including if an isolated fulfillment operation encounters an error.
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(previousLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := newPaymentConfigServiceTestClient(t)
	configService := &PaymentConfigService{entClient: client}
	instance, err := configService.CreateProviderInstance(ctx, CreateProviderInstanceRequest{
		ProviderKey: payment.TypeQufu, Name: "Isolated paid evidence", Config: config,
		SupportedTypes: []string{payment.TypeAlipay, payment.TypeWxpay}, Enabled: true,
	})
	if err != nil {
		t.Fatal("could not create isolated provider fixture")
	}
	user, err := client.User.Create().SetEmail("qufu-paid-evidence@example.invalid").SetUsername("qufu-paid-evidence").SetPasswordHash("test-only").Save(ctx)
	if err != nil {
		t.Fatal("could not create isolated user fixture")
	}
	userRepo := &mockUserRepo{getByIDUser: &User{ID: user.ID, Email: user.Email, Username: user.Username}}
	userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
		if id != user.ID {
			return payment.ErrProviderNotFound
		}
		userRepo.getByIDUser.Balance += amount
		return nil
	}
	redeemRepo := &paymentOrderLifecycleRedeemRepo{codesByCode: map[string]*RedeemCode{}}
	svc := &PaymentService{
		entClient: client, configService: configService,
		loadBalancer: payment.NewDefaultLoadBalancer(client, nil), userRepo: userRepo,
		redeemService: NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil),
	}
	seenOrders, seenTrades, seenMethods := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, captured := range evidence.Orders {
		if captured.OrderNo == "" || captured.TradeNo == "" || seenOrders[captured.OrderNo] || seenTrades[captured.TradeNo] || seenMethods[captured.Method] || (captured.Method != payment.TypeAlipay && captured.Method != payment.TypeWxpay) {
			t.Fatal("captured paid orders must have unique identities and payment methods")
		}
		seenOrders[captured.OrderNo], seenTrades[captured.TradeNo], seenMethods[captured.Method] = true, true, true
		channel := "10"
		if captured.Method == payment.TypeWxpay {
			channel = "11"
		}
		queried, queryErr := q.QueryOrder(ctx, captured.OrderNo)
		if queryErr != nil || queried == nil || queried.Status != payment.ProviderStatusPaid || queried.Amount != 0.01 || queried.TradeNo != captured.TradeNo || queried.Metadata["channel_type"] != channel {
			t.Fatal("signed live query did not confirm the captured one-cent payment")
		}
		var notification *payment.PaymentNotification
		for eventIndex := len(evidence.Events) - 1; eventIndex >= 0 && notification == nil; eventIndex-- {
			event := evidence.Events[eventIndex]
			for _, raw := range []string{event.ReceivedQuery, event.Raw} {
				// net/url deliberately accepts empty trailing '&' segments, as
				// does the actual provider verifier; do not synthesize a signature.
				values, parseErr := url.ParseQuery(raw)
				if parseErr != nil || values.Get("orderNo") != captured.OrderNo {
					continue
				}
				candidate, verifyErr := q.VerifyNotification(ctx, raw, nil)
				if verifyErr == nil && candidate != nil && candidate.Status == payment.NotificationStatusSuccess && candidate.OrderID == captured.OrderNo && candidate.TradeNo == captured.TradeNo && candidate.Amount == 0.01 && candidate.Metadata["channel_type"] == channel {
					notification = candidate
					break
				}
			}
		}
		if notification == nil {
			t.Fatal("no valid actual success notification was captured for a paid order")
		}
		if svc.ConfirmNotificationUpstream(ctx, q, notification) != nil {
			t.Fatal("captured success notification failed signed live confirmation")
		}
		rechargeCode := "QUFU-PAID-EVIDENCE-" + strconv.Itoa(index+1)
		snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{InstanceID: strconv.FormatInt(instance.ID, 10), ProviderKey: payment.TypeQufu, Config: config}, CreateOrderRequest{PaymentType: captured.Method})
		order, createErr := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
			SetAmount(0.01).SetPayAmount(0.01).SetFeeRate(0).SetRechargeCode(rechargeCode).SetOutTradeNo(captured.OrderNo).
			SetPaymentType(captured.Method).SetPaymentTradeNo(captured.TradeNo).SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusPending).
			SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("192.0.2.1").SetSrcHost("test.example.invalid").
			SetProviderInstanceID(strconv.FormatInt(instance.ID, 10)).SetProviderKey(payment.TypeQufu).SetProviderSnapshot(snapshot).Save(ctx)
		if createErr != nil {
			t.Fatal("could not create isolated paid-order fixture")
		}
		redeemRepo.codesByCode[rechargeCode] = &RedeemCode{ID: int64(index + 1), Code: rechargeCode, Type: RedeemTypeBalance, Value: 0.01, Status: StatusUnused}
		if svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu) != nil || svc.HandlePaymentNotification(ctx, notification, payment.TypeQufu) != nil {
			t.Fatal("isolated fulfillment or duplicate notification replay failed")
		}
		current, loadErr := client.PaymentOrder.Get(ctx, order.ID)
		if loadErr != nil || current.Status != OrderStatusCompleted || current.PaidAt == nil || current.PaymentTradeNo != captured.TradeNo {
			t.Fatal("isolated paid-order fixture did not complete")
		}
		if len(redeemRepo.useCalls) != index+1 || math.Abs(userRepo.getByIDUser.Balance-float64(index+1)*0.01) > 1e-9 {
			t.Fatal("replayed live notification caused an incorrect isolated credit")
		}
		t.Log(captured.Method + ": actual notification, signed paid query and isolated replay verified")
	}
	if len(seenMethods) != 2 || len(redeemRepo.useCalls) != 2 || math.Abs(userRepo.getByIDUser.Balance-0.02) > 1e-9 {
		t.Fatal("isolated totals do not match exactly two one-cent payments")
	}
}

type qufuLiveRecordingTransport struct {
	base   http.RoundTripper
	record func(int, []byte)
}

func (r qufuLiveRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	r.record(response.StatusCode, body)
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
