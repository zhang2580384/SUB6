package provider

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const qufuTestKey = "0123456789abcdef0123456789abcdef"

func qufuTestConfig(base string) map[string]string {
	return map[string]string{"apiBase": base, "mchId": "merchant-test", "merchantKey": qufuTestKey, "notifyUrl": "https://merchant.example/api/notify"}
}

func qufuTestProvider(t *testing.T, base string) *Qufu {
	t.Helper()
	q, err := NewQufu("test-instance", qufuTestConfig(base))
	if err != nil {
		t.Fatal(err)
	}
	q.now = func() time.Time { return time.Date(2026, 11, 27, 7, 30, 0, 0, time.UTC) }
	return q
}

func qufuTestRequest() payment.CreatePaymentRequest {
	return payment.CreatePaymentRequest{OrderID: "order-001", Amount: "12.34", PaymentType: payment.TypeAlipay, Subject: "SUB6 充值 & x=1+2", ClientIP: "192.0.2.1"}
}

func qufuTestCreateData() map[string]string {
	return map[string]string{"mchId": "merchant-test", "version": "1.0", "charset": "utf-8", "channelType": "10", "orderNo": "order-001", "tradeNo": "trade-001", "payData": "https://checkout.example/pay/trade-001", "signType": "MD5"}
}

func qufuTestQueryData() map[string]string {
	return map[string]string{"mchId": "merchant-test", "version": "1.0", "charset": "utf-8", "channelType": "10", "orderNo": "order-001", "tradeNo": "trade-001", "orderAmount": "12.34", "orderStatus": "2", "shareTag": "1", "shareFundList": "", "orderTime": "2026-11-27 15:30:00", "signType": "MD5"}
}

func qufuTestNotifyData() map[string]string {
	return map[string]string{"mchId": "merchant-test", "channelType": "10", "orderNo": "order-001", "tradeNo": "trade-001", "orderAmount": "12.34", "orderStatus": "2", "orderTime": "2026-11-27 15:30:00", "successTime": "2026-11-27 15:31:00", "attach": "", "signType": "MD5"}
}

func qufuTestWriteResponse(w http.ResponseWriter, fields map[string]string, order []string) {
	fields["sign"] = qufuSign(fields, order, qufuTestKey)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "msg": "Success", "data": fields})
}

func qufuTestEncodeNotify(fields map[string]string) string {
	fields["sign"] = qufuSign(fields, qufuNotificationFields, qufuTestKey)
	values := make(url.Values)
	for key, value := range fields {
		values.Set(key, value)
	}
	return values.Encode()
}

func TestQufuSignatureUsesDocumentOrderAndRawUTF8(t *testing.T) {
	fields := map[string]string{
		"mchId": "merchant-test", "version": "1.0", "service": "api.trade.order.pay", "timestamp": "20261127153000", "charset": "UTF-8", "orderNo": "order-001", "channelType": "10", "orderAmount": "12.34", "orderSubject": "SUB6 充值 & x=1+2", "notifyUrl": "https://merchant.example/api/notify", "frontUrl": "", "terminalIp": "192.0.2.1", "shareTag": "1", "shareFundList": "", "attach": "", "format": "0", "signType": "MD5",
	}
	// Independent fixed vector: raw UTF-8, empty fields retained, protocol order.
	if got := qufuSign(fields, qufuCreateFields, qufuTestKey); got != "70279d27e84bda41b8c1dc85e702b8bf" {
		t.Fatalf("unexpected signature: %s", got)
	}
	q := qufuTestProvider(t, "https://gateway.example")
	if got := qufuSign(q.baseFields("api.trade.order.query", "order-001"), qufuQueryFields, qufuTestKey); got != "842f298f9ece85fa780d8247e7123fae" {
		t.Fatalf("unexpected query signature: %s", got)
	}
	if got := qufuSign(qufuTestNotifyData(), qufuNotificationFields, qufuTestKey); got != "191a0af02f7f772eecf263320a494f62" {
		t.Fatalf("unexpected notification signature: %s", got)
	}
	if got := qufuSign(qufuTestQueryData(), qufuQueryResponseFields, qufuTestKey); got != "ba5df27c276aecee9cd2345db8c22e15" {
		t.Fatalf("unexpected query-response signature: %s", got)
	}
	sum := md5.Sum([]byte("1234567890abcdefghijklmnopqrstuvwxyz"))
	if hex.EncodeToString(sum[:]) != "928f7bcdcd08869cc44c1bf24e7abec6" {
		t.Fatal("document MD5 self-test differs")
	}
}

func TestQufuConfigValidationAndAutomaticSharing(t *testing.T) {
	for _, base := range []string{"https://gateway.example", "https://gateway.example/", "https://gateway.example/pay/gateway/"} {
		q := qufuTestProvider(t, base)
		if q.config["apiBase"] != "https://gateway.example/pay/gateway" || q.config["alipayChannel"] != "10" || q.config["wxpayChannel"] != "11" {
			t.Fatalf("unexpected normalized configuration: %s", q.config["apiBase"])
		}
	}
	for name, change := range map[string]map[string]string{
		"insecure gateway":    {"apiBase": "http://gateway.example"},
		"gateway credentials": {"apiBase": "https://user:pass@gateway.example"},
		"gateway query":       {"apiBase": "https://gateway.example/pay/gateway?key=1"},
		"gateway wrong path":  {"apiBase": "https://gateway.example/other"},
		"notify query":        {"notifyUrl": "https://merchant.example/notify?a=1"},
		"insecure notify":     {"notifyUrl": "http://merchant.example/notify"},
		"return fragment":     {"returnUrl": "https://merchant.example/result#x"},
		"empty key":           {"merchantKey": ""},
		"short key":           {"merchantKey": "short"},
		"invalid merchant":    {"mchId": "mch&x=1"},
		"invalid channel":     {"alipayChannel": "30"},
		"no sharing":          {"shareTag": "0"},
		"external receivers":  {"shareFundList": "receiver:1.00"},
	} {
		t.Run(name, func(t *testing.T) {
			config := qufuTestConfig("https://gateway.example")
			for key, value := range change {
				config[key] = value
			}
			if _, err := NewQufu("test", config); err == nil {
				t.Fatal("expected invalid configuration to be rejected")
			}
		})
	}
}

func TestQufuCreateSendsSignedFormAndVerifiesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pay/gateway" {
			t.Errorf("unexpected request method/path")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		for field, value := range map[string]string{"service": "api.trade.order.pay", "timestamp": "20261127153000", "orderNo": "order-001", "orderAmount": "12.34", "orderSubject": "SUB6 充值 & x=1+2", "shareTag": "1", "shareFundList": "", "frontUrl": "", "attach": "", "format": "0"} {
			if values, ok := r.PostForm[field]; !ok || len(values) != 1 || values[0] != value {
				t.Errorf("unexpected field %s", field)
			}
		}
		if r.PostForm.Get("sign") != "70279d27e84bda41b8c1dc85e702b8bf" {
			t.Error("request signature differs from independent vector")
		}
		qufuTestWriteResponse(w, qufuTestCreateData(), qufuCreateResponseFields)
	}))
	defer server.Close()
	result, err := qufuTestProvider(t, server.URL).CreatePayment(context.Background(), qufuTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.TradeNo != "trade-001" || result.QRCode != "https://checkout.example/pay/trade-001" || result.Currency != "CNY" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestQufuCreateRejectsResponseTamperingAndUnsafePaymentData(t *testing.T) {
	for name, change := range map[string]func(map[string]string){
		"wrong merchant":       func(f map[string]string) { f["mchId"] = "another" },
		"wrong order":          func(f map[string]string) { f["orderNo"] = "another" },
		"wrong channel":        func(f map[string]string) { f["channelType"] = "11" },
		"wrong version":        func(f map[string]string) { f["version"] = "2.0" },
		"wrong charset":        func(f map[string]string) { f["charset"] = "GBK" },
		"javascript":           func(f map[string]string) { f["payData"] = "javascript:alert(1)" },
		"credentials in URL":   func(f map[string]string) { f["payData"] = "https://u:p@checkout.example/pay" },
		"relative URL":         func(f map[string]string) { f["payData"] = "/pay/test" },
		"wrong native host":    func(f map[string]string) { f["payData"] = "alipays://other/startapp?appId=1" },
		"opaque native URL":    func(f map[string]string) { f["payData"] = "alipays:unexpected" },
		"missing signed field": func(f map[string]string) { delete(f, "charset") },
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f := qufuTestCreateData()
				change(f)
				qufuTestWriteResponse(w, f, qufuCreateResponseFields)
			}))
			defer server.Close()
			if _, err := qufuTestProvider(t, server.URL).CreatePayment(context.Background(), qufuTestRequest()); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := qufuTestCreateData()
		f["sign"] = strings.Repeat("0", 32)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": f})
	}))
	defer server.Close()
	if _, err := qufuTestProvider(t, server.URL).CreatePayment(context.Background(), qufuTestRequest()); err == nil {
		t.Fatal("expected bad signature rejection")
	}
}

func TestQufuCreateValidatesAmountAndNoQueryReturnBeforeSending(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	q := qufuTestProvider(t, server.URL)
	for _, amount := range []string{"NaN", "Inf", "-1.00", "0.00", "1", "1.0", "1.001", "01.00", "1e2", " 1.00", "100000000000.00"} {
		request := qufuTestRequest()
		request.Amount = amount
		if _, err := q.CreatePayment(context.Background(), request); err == nil {
			t.Fatalf("accepted amount %q", amount)
		}
	}
	request := qufuTestRequest()
	request.ReturnURL = "https://merchant.example/payment/result?resume_token=test"
	if _, err := q.CreatePayment(context.Background(), request); err == nil {
		t.Fatal("accepted query-bearing return URL")
	}
	request = qufuTestRequest()
	request.ClientIP = "not-an-ip"
	if _, err := q.CreatePayment(context.Background(), request); err == nil {
		t.Fatal("accepted invalid payer IP")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid payment input reached upstream")
	}
}

func TestQufuCreateNativeQRAndH5ModesUseOnlySupportedLaunchTargets(t *testing.T) {
	for _, tc := range []struct {
		name, method, channel, payData string
		wantQR                         bool
	}{
		{"alipay native QR", payment.TypeAlipay, "10", "alipays://platformapi/startapp?appId=10000007&qrcode=example", true},
		{"document HTTP checkout", payment.TypeAlipay, "10", "http://checkout.example:8020/order/pay/trade-001", true},
		{"wechat native QR", payment.TypeWxpay, "11", "weixin://wxpay/bizpayurl?pr=example", true},
		{"alipay H5", payment.TypeAlipay, "20", "https://checkout.example/h5/pay", false},
		{"wechat H5", payment.TypeWxpay, "21", "https://checkout.example/h5/pay", false},
		{"HTTP H5 checkout", payment.TypeAlipay, "20", "http://checkout.example:8020/order/pay/trade-001", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("channelType") != tc.channel || r.Form.Get("frontUrl") != "https://merchant.example/payment/result" {
					t.Error("wrong channel or canonical return URL")
				}
				data := qufuTestCreateData()
				data["channelType"] = tc.channel
				data["payData"] = tc.payData
				qufuTestWriteResponse(w, data, qufuCreateResponseFields)
			}))
			defer server.Close()
			cfg := qufuTestConfig(server.URL)
			if tc.method == payment.TypeAlipay {
				cfg["alipayChannel"] = tc.channel
			} else {
				cfg["wxpayChannel"] = tc.channel
			}
			q, err := NewQufu("test", cfg)
			if err != nil {
				t.Fatal(err)
			}
			request := qufuTestRequest()
			request.PaymentType = tc.method
			request.ReturnURL = "https://merchant.example/payment/result"
			result, err := q.CreatePayment(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantQR {
				wantPayURL := ""
				if strings.HasPrefix(tc.payData, "https://") || strings.HasPrefix(tc.payData, "http://") {
					wantPayURL = tc.payData
				}
				if result.QRCode != tc.payData || result.PayURL != wantPayURL {
					t.Fatal("QR launch result differs: web checkout may redirect, native QR must not")
				}
			} else if result.PayURL != tc.payData || result.QRCode != "" {
				t.Fatal("H5 launch result differs")
			}
		})
	}
}

func TestQufuQueryUsesMerchantOrderAndVerifiedSharingPolicy(t *testing.T) {
	for _, status := range []string{"1", "2", "3"} {
		t.Run(status, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.Form.Get("service") != "api.trade.order.query" || r.Form.Get("orderNo") != "order-001" || r.Form.Get("tradeNo") != "" {
					t.Error("query did not use merchant order ID")
				}
				f := qufuTestQueryData()
				f["orderStatus"] = status
				qufuTestWriteResponse(w, f, qufuQueryResponseFields)
			}))
			defer server.Close()
			result, err := qufuTestProvider(t, server.URL).QueryOrder(context.Background(), "order-001")
			if err != nil {
				t.Fatal(err)
			}
			want, _ := qufuStatus(status)
			if result.Status != want || result.Amount != 12.34 || result.Metadata["mch_id"] != "merchant-test" || result.Metadata["share_tag"] != "1" || result.Metadata["share_fund_list"] != "" {
				t.Fatalf("unexpected query result: %+v", result)
			}
		})
	}
	for name, change := range map[string]map[string]string{
		"wrong order": {"orderNo": "another"}, "wrong merchant": {"mchId": "another"}, "unknown status": {"orderStatus": "4"}, "invalid amount": {"orderAmount": "NaN"}, "zero amount": {"orderAmount": "0.00"}, "non-cent amount": {"orderAmount": "1.001"}, "sharing disabled": {"shareTag": "0"}, "external receivers": {"shareFundList": "someone:1.00"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f := qufuTestQueryData()
				for key, value := range change {
					f[key] = value
				}
				qufuTestWriteResponse(w, f, qufuQueryResponseFields)
			}))
			defer server.Close()
			if _, err := qufuTestProvider(t, server.URL).QueryOrder(context.Background(), "order-001"); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestQufuNotificationVerificationAndNonSuccessAcknowledgement(t *testing.T) {
	q := qufuTestProvider(t, "https://gateway.example")
	for _, status := range []string{"1", "2", "3"} {
		f := qufuTestNotifyData()
		f["orderStatus"] = status
		n, err := q.VerifyNotification(context.Background(), qufuTestEncodeNotify(f), nil)
		if err != nil {
			t.Fatal(err)
		}
		if status != "2" && n != nil {
			t.Fatal("non-success notification must not credit an order")
		}
		if status == "2" && (n == nil || n.OrderID != "order-001" || n.TradeNo != "trade-001" || n.Amount != 12.34 || n.Metadata["channel_type"] != "10") {
			t.Fatalf("unexpected paid notification: %+v", n)
		}
	}
	valid := qufuTestEncodeNotify(qufuTestNotifyData())
	for name, raw := range map[string]string{
		"tampered amount":  strings.Replace(valid, "12.34", "99.99", 1),
		"duplicate amount": valid + "&orderAmount=99.99",
		"duplicate sign":   valid + "&sign=" + strings.Repeat("0", 32),
		"malformed escape": valid + "&extra=%zz",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := q.VerifyNotification(context.Background(), raw, nil); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	for name, change := range map[string]func(map[string]string){
		"wrong merchant":         func(f map[string]string) { f["mchId"] = "another" },
		"unknown channel":        func(f map[string]string) { f["channelType"] = "99" },
		"missing attach":         func(f map[string]string) { delete(f, "attach") },
		"zero amount":            func(f map[string]string) { f["orderAmount"] = "0.00" },
		"missing paid timestamp": func(f map[string]string) { f["successTime"] = "" },
		"changed attachment":     func(f map[string]string) { f["attach"] = "another" },
		"injected time fields":   func(f map[string]string) { f["orderTime"] = "2026-11-27 15:30:00&orderAmount=99.99" },
	} {
		t.Run(name, func(t *testing.T) {
			f := qufuTestNotifyData()
			change(f)
			if _, err := q.VerifyNotification(context.Background(), qufuTestEncodeNotify(f), nil); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	n, err := q.VerifyNotification(context.Background(), valid+"&merchantKey=must-not-be-audited", nil)
	if err != nil || n == nil || strings.Contains(n.RawData, "merchantKey") || strings.Contains(n.RawData, "must-not-be-audited") {
		t.Fatalf("unexpected notification audit contents or error: %v", err)
	}
}

func TestQufuConfirmationRequiresSamePaidOrderTradeAmountAndChannel(t *testing.T) {
	for name, change := range map[string]map[string]string{
		"same paid order": {}, "pending": {"orderStatus": "1"}, "wrong trade": {"tradeNo": "different"}, "wrong amount": {"orderAmount": "12.35"}, "wrong channel": {"channelType": "11"}, "different sharing": {"shareTag": "0"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f := qufuTestQueryData()
				for key, value := range change {
					f[key] = value
				}
				qufuTestWriteResponse(w, f, qufuQueryResponseFields)
			}))
			defer server.Close()
			q := qufuTestProvider(t, server.URL)
			n, err := q.VerifyNotification(context.Background(), qufuTestEncodeNotify(qufuTestNotifyData()), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = q.ConfirmNotification(context.Background(), n)
			if name == "same paid order" {
				if err != nil || n.Metadata["share_tag"] != "1" {
					t.Fatalf("valid confirmation rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("mismatched confirmation accepted")
			}
		})
	}
}

func TestQufuRejectsRedirectsOversizedAndAmbiguousJSON(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	for name, handler := range map[string]http.HandlerFunc{
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
		},
		"too large": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", qufuMaxResponseSize+1)))
		},
		"duplicate envelope": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":"000000","code":"000000","data":{}}`))
		},
		"duplicate data": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":"000000","data":{"mchId":"a","mchId":"b"}}`))
		},
		"trailing JSON": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"code":"000000","data":{}} {}`)) },
		"null field": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"code":"000000","data":{"mchId":null}}`))
		},
		"gateway rejection": func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"code":"999999","msg":"sensitive gateway diagnostics"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			_, err := qufuTestProvider(t, server.URL).QueryOrder(context.Background(), "order-001")
			if err == nil {
				t.Fatal("expected rejection")
			}
			if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), qufuTestKey) {
				t.Fatal("error exposed gateway diagnostics or merchant secret")
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("credential-bearing request followed redirect")
	}
}

func TestQufuResponsesIgnoreUnusedUnsignedExtensions(t *testing.T) {
	for _, service := range []string{"api.trade.order.pay", "api.trade.order.query"} {
		t.Run(service, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fields, order := qufuTestCreateData(), qufuCreateResponseFields
				if service == "api.trade.order.query" {
					fields, order = qufuTestQueryData(), qufuQueryResponseFields
				}
				fields["sign"] = qufuSign(fields, order, qufuTestKey)
				data := make(map[string]any, len(fields)+4)
				for key, value := range fields {
					data[key] = value
				}
				data["extensionNumber"] = 123
				data["extensionNull"] = nil
				data["extensionObject"] = map[string]any{"flag": true}
				data["extensionArray"] = []any{1, nil}
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": data})
			}))
			defer server.Close()
			q := qufuTestProvider(t, server.URL)
			if service == "api.trade.order.pay" {
				if _, err := q.CreatePayment(context.Background(), qufuTestRequest()); err != nil {
					t.Fatalf("unused unsigned response extensions rejected: %v", err)
				}
			} else if _, err := q.QueryOrder(context.Background(), "order-001"); err != nil {
				t.Fatalf("unused unsigned response extensions rejected: %v", err)
			}
		})
	}
}

func TestQufuResponsesRejectNonStringSignedFields(t *testing.T) {
	for _, tc := range []struct {
		name, service, field string
		value                any
	}{
		{"numeric merchant", "api.trade.order.pay", "mchId", 100000},
		{"array checkout", "api.trade.order.pay", "payData", []any{"https://checkout.example/pay"}},
		{"null signature", "api.trade.order.pay", "sign", nil},
		{"numeric amount", "api.trade.order.query", "orderAmount", 12.34},
		{"numeric status", "api.trade.order.query", "orderStatus", 2},
		{"null sharing list", "api.trade.order.query", "shareFundList", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fields, order := qufuTestCreateData(), qufuCreateResponseFields
				if tc.service == "api.trade.order.query" {
					fields, order = qufuTestQueryData(), qufuQueryResponseFields
				}
				fields["sign"] = qufuSign(fields, order, qufuTestKey)
				data := make(map[string]any, len(fields))
				for key, value := range fields {
					data[key] = value
				}
				data[tc.field] = tc.value
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": data})
			}))
			defer server.Close()
			q := qufuTestProvider(t, server.URL)
			var err error
			if tc.service == "api.trade.order.pay" {
				_, err = q.CreatePayment(context.Background(), qufuTestRequest())
			} else {
				_, err = q.QueryOrder(context.Background(), "order-001")
			}
			if err == nil || !strings.Contains(err.Error(), "expected string response fields") {
				t.Fatalf("expected strict signed-field type rejection, got %v", err)
			}
		})
	}
}

func TestQufuResponsesRejectDuplicateUnsignedExtensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fields := qufuTestQueryData()
		fields["sign"] = qufuSign(fields, qufuQueryResponseFields, qufuTestKey)
		data, err := json.Marshal(fields)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, `{"code":"000000","data":%s,"extension":1,"extension":null}}`, data[:len(data)-1])
	}))
	defer server.Close()
	_, err := qufuTestProvider(t, server.URL).QueryOrder(context.Background(), "order-001")
	if err == nil || !strings.Contains(err.Error(), "duplicate JSON response field") {
		t.Fatalf("duplicate unsigned extension was accepted: %v", err)
	}
}

func TestQufuCancellationAndUnsupportedRefundDoNotIssueRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	q := qufuTestProvider(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.QueryOrder(ctx, "order-001"); err == nil {
		t.Fatal("expected canceled request to fail")
	}
	if _, err := q.Refund(context.Background(), payment.RefundRequest{OrderID: "order-001", Amount: "1.00"}); err == nil {
		t.Fatal("refund should be unsupported")
	}
	if calls.Load() != 0 {
		t.Fatal("canceled query or unsupported refund reached upstream")
	}
}
