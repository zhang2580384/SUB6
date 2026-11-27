package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const qufuMaxResponseSize = 1 << 20

var (
	qufuCreateFields         = []string{"mchId", "version", "service", "timestamp", "charset", "orderNo", "channelType", "orderAmount", "orderSubject", "notifyUrl", "frontUrl", "terminalIp", "shareTag", "shareFundList", "attach", "format", "signType"}
	qufuCreateResponseFields = []string{"mchId", "version", "charset", "channelType", "orderNo", "tradeNo", "payData", "signType"}
	qufuQueryFields          = []string{"mchId", "version", "service", "timestamp", "charset", "orderNo", "signType"}
	qufuQueryResponseFields  = []string{"mchId", "version", "charset", "channelType", "orderNo", "tradeNo", "orderAmount", "orderStatus", "shareTag", "shareFundList", "signType"}
	qufuNotificationFields   = []string{"mchId", "channelType", "orderNo", "tradeNo", "orderStatus", "orderAmount", "orderTime", "successTime", "attach", "signType"}
)

// Qufu implements the documented payment-with-sharing API. Sharing is always
// directed to the merchant's already-bound account. It does not cash out SUB6
// affiliate credits or create arbitrary third-party payment allocations.
type Qufu struct {
	config     map[string]string
	httpClient *http.Client
	now        func() time.Time
}

var _ payment.Provider = (*Qufu)(nil)
var _ payment.NotificationConfirmer = (*Qufu)(nil)
var _ payment.MerchantIdentityProvider = (*Qufu)(nil)

func NewQufu(_ string, config map[string]string) (*Qufu, error) {
	cfg := make(map[string]string, len(config))
	for key, value := range config {
		cfg[key] = strings.TrimSpace(value)
	}
	for _, key := range []string{"apiBase", "mchId", "merchantKey", "notifyUrl"} {
		if cfg[key] == "" {
			return nil, fmt.Errorf("qufu config missing required key: %s", key)
		}
	}
	if !qufuIdentifier(cfg["mchId"]) || len(cfg["merchantKey"]) != 32 {
		return nil, fmt.Errorf("qufu requires a valid merchant ID and a 32-character merchant key")
	}
	for _, value := range []byte(cfg["merchantKey"]) {
		if value < 33 || value > 126 {
			return nil, fmt.Errorf("qufu merchant key must contain printable ASCII characters")
		}
	}
	base, err := qufuURL(cfg["apiBase"], true)
	if err != nil {
		return nil, fmt.Errorf("qufu invalid apiBase")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	if base.Path == "" {
		base.Path = "/pay/gateway"
	}
	if !strings.HasSuffix(base.Path, "/pay/gateway") {
		return nil, fmt.Errorf("qufu apiBase must be the gateway origin or end in /pay/gateway")
	}
	cfg["apiBase"] = base.String()
	for _, key := range []string{"notifyUrl", "returnUrl"} {
		if cfg[key] != "" {
			if _, err := qufuURL(cfg[key], true); err != nil {
				return nil, fmt.Errorf("qufu invalid %s: expected a query-free HTTP(S) URL", key)
			}
		}
	}
	if cfg["alipayChannel"] == "" {
		cfg["alipayChannel"] = "10"
	}
	if cfg["wxpayChannel"] == "" {
		cfg["wxpayChannel"] = "11"
	}
	if cfg["alipayChannel"] != "10" && cfg["alipayChannel"] != "20" {
		return nil, fmt.Errorf("qufu alipayChannel must be 10 or 20")
	}
	if cfg["wxpayChannel"] != "11" && cfg["wxpayChannel"] != "21" {
		return nil, fmt.Errorf("qufu wxpayChannel must be 11 or 21")
	}
	if cfg["ipv6Compatibility"] == "" {
		cfg["ipv6Compatibility"] = "native"
	}
	if cfg["ipv6Compatibility"] != "native" && cfg["ipv6Compatibility"] != "pseudo_ipv4" {
		return nil, fmt.Errorf("qufu ipv6Compatibility must be native or pseudo_ipv4")
	}
	if cfg["shareTag"] != "" && cfg["shareTag"] != "1" || cfg["shareFundList"] != "" {
		return nil, fmt.Errorf("qufu supports automatic sharing to the merchant bound account only")
	}
	return &Qufu{config: cfg, now: time.Now, httpClient: &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (q *Qufu) Name() string        { return "Qufu" }
func (q *Qufu) ProviderKey() string { return "qufu" }
func (q *Qufu) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay, payment.TypeWxpay}
}
func (q *Qufu) MerchantIdentityMetadata() map[string]string {
	return map[string]string{"mch_id": q.config["mchId"], "currency": "CNY"}
}

// qufuSign follows the protocol's explicit field order, retaining empty values.
// Values are signed before application/x-www-form-urlencoded encoding.
func qufuSign(fields map[string]string, order []string, secret string) string {
	var text strings.Builder
	for _, field := range order {
		text.WriteString(field)
		text.WriteByte('=')
		text.WriteString(fields[field])
		text.WriteByte('&')
	}
	text.WriteString("key=")
	text.WriteString(secret)
	sum := md5.Sum([]byte(text.String()))
	return hex.EncodeToString(sum[:])
}

func qufuIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func qufuURL(value string, noQuery bool) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") || (noQuery && (u.RawQuery != "" || u.ForceQuery)) {
		return nil, fmt.Errorf("invalid URL")
	}
	// Public merchant credentials and payment notifications require TLS. Local
	// loopback HTTP remains available for isolated development and httptest.
	if u.Scheme == "http" && !strings.EqualFold(u.Hostname(), "localhost") {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("TLS is required")
		}
	}
	return u, nil
}

func qufuAmountCents(value string) (int64, error) {
	if len(value) < 4 || len(value) > 15 || value[len(value)-3] != '.' {
		return 0, fmt.Errorf("qufu amount must be a positive amount with exactly two decimal places")
	}
	whole, fraction := value[:len(value)-3], value[len(value)-2:]
	if whole == "" || len(whole) > 1 && whole[0] == '0' {
		return 0, fmt.Errorf("qufu invalid payment amount")
	}
	for _, digit := range whole + fraction {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("qufu invalid payment amount")
		}
	}
	cents, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || cents <= 0 || cents > 9999999999999 {
		return 0, fmt.Errorf("qufu invalid payment amount")
	}
	return cents, nil
}

func (q *Qufu) baseFields(service, orderNo string) map[string]string {
	return map[string]string{"mchId": q.config["mchId"], "version": "1.0", "service": service, "timestamp": q.now().UTC().Add(8 * time.Hour).Format("20060102150405"), "charset": "UTF-8", "orderNo": orderNo, "signType": "MD5"}
}

func (q *Qufu) channel(paymentType string) (string, error) {
	switch paymentType {
	case payment.TypeAlipay:
		return q.config["alipayChannel"], nil
	case payment.TypeWxpay:
		return q.config["wxpayChannel"], nil
	default:
		return "", fmt.Errorf("qufu unsupported payment method")
	}
}

// terminalIP changes only the value sent to an explicitly configured legacy
// gateway. The caller's real address remains unchanged for order and security
// records. Class E addresses are synthetic compatibility values, never identity
// or geolocation evidence; the 28-bit mapping can have collisions.
func (q *Qufu) terminalIP(address string) (string, error) {
	address = strings.TrimSpace(address)
	parsed := net.ParseIP(address)
	if parsed == nil {
		return "", fmt.Errorf("qufu requires a valid payer IP address")
	}
	if ipv4 := parsed.To4(); ipv4 != nil {
		return ipv4.String(), nil
	}
	if q.config["ipv6Compatibility"] != "pseudo_ipv4" {
		return address, nil
	}
	digest := sha256.Sum256(append([]byte("SUB6/qufu/ipv6-terminal/v1\x00"), parsed.To16()...))
	value := uint32(0xf0000000) | (binary.BigEndian.Uint32(digest[:4]) & 0x0fffffff)
	if value == 0xffffffff {
		value-- // Do not use the limited broadcast address.
	}
	return net.IPv4(byte(value>>24), byte(value>>16), byte(value>>8), byte(value)).String(), nil
}

func (q *Qufu) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if !qufuIdentifier(req.OrderID) {
		return nil, fmt.Errorf("qufu invalid merchant order ID")
	}
	if _, err := qufuAmountCents(req.Amount); err != nil {
		return nil, err
	}
	channel, err := q.channel(req.PaymentType)
	if err != nil {
		return nil, err
	}
	terminalIP, err := q.terminalIP(req.ClientIP)
	if err != nil {
		return nil, err
	}
	if len(req.Subject) > 2048 {
		return nil, fmt.Errorf("qufu order subject is too long")
	}
	notifyURL := q.config["notifyUrl"]
	if req.NotifyURL != "" {
		notifyURL = req.NotifyURL
	}
	if _, err := qufuURL(notifyURL, true); err != nil {
		return nil, fmt.Errorf("qufu invalid notification URL")
	}
	returnURL := q.config["returnUrl"]
	if req.ReturnURL != "" {
		returnURL = req.ReturnURL
	}
	if returnURL != "" {
		if _, err := qufuURL(returnURL, true); err != nil {
			return nil, fmt.Errorf("qufu invalid return URL: query parameters are not supported")
		}
	}
	fields := q.baseFields("api.trade.order.pay", req.OrderID)
	for key, value := range map[string]string{"channelType": channel, "orderAmount": req.Amount, "orderSubject": req.Subject, "notifyUrl": notifyURL, "frontUrl": returnURL, "terminalIp": terminalIP, "shareTag": "1", "shareFundList": "", "attach": "", "format": "0"} {
		fields[key] = value
	}
	fields["sign"] = qufuSign(fields, qufuCreateFields, q.config["merchantKey"])
	result, err := q.post(ctx, fields)
	if err != nil {
		return nil, err
	}
	if err := q.verify(result, qufuCreateResponseFields); err != nil {
		return nil, err
	}
	if err := q.validateIdentity(result, req.OrderID); err != nil {
		return nil, err
	}
	if result["channelType"] != channel {
		return nil, fmt.Errorf("qufu payment channel mismatch")
	}
	payData := result["payData"]
	if payData == "" || len(payData) > 8192 {
		return nil, fmt.Errorf("qufu invalid payment URL")
	}
	response := &payment.CreatePaymentResponse{TradeNo: result["tradeNo"], Currency: "CNY"}
	parsed, parseErr := url.Parse(payData)
	if parseErr != nil || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("qufu invalid payment URL")
	}
	// The protocol permits an HTTP checkout link in the signed payData.
	// Keep its validation separate from the TLS requirement for requests that
	// carry merchant credentials and for configured notification destinations.
	webURL := (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Hostname() != "" && parsed.Opaque == ""
	if channel == "10" || channel == "11" {
		nativeURL := parsed.Opaque == "" && parsed.RawQuery != "" && (channel == "10" && parsed.Scheme == "alipays" && parsed.Host == "platformapi" && parsed.Path == "/startapp" || channel == "11" && parsed.Scheme == "weixin" && parsed.Host == "wxpay" && parsed.Path == "/bizpayurl")
		if !webURL && !nativeURL {
			return nil, fmt.Errorf("qufu unsupported QR payment scheme")
		}
		response.QRCode = payData
		if webURL {
			response.PayURL = payData
		}
	} else {
		if !webURL {
			return nil, fmt.Errorf("qufu H5 payment requires an HTTP(S) URL")
		}
		response.PayURL = payData
	}
	return response, nil
}

func (q *Qufu) QueryOrder(ctx context.Context, orderNo string) (*payment.QueryOrderResponse, error) {
	if !qufuIdentifier(orderNo) {
		return nil, fmt.Errorf("qufu invalid merchant order ID")
	}
	fields := q.baseFields("api.trade.order.query", orderNo)
	fields["sign"] = qufuSign(fields, qufuQueryFields, q.config["merchantKey"])
	result, err := q.post(ctx, fields)
	if err != nil {
		return nil, err
	}
	if err := q.verify(result, qufuQueryResponseFields); err != nil {
		return nil, err
	}
	if err := q.validateIdentity(result, orderNo); err != nil {
		return nil, err
	}
	if result["shareTag"] != "1" || result["shareFundList"] != "" {
		return nil, fmt.Errorf("qufu order sharing policy mismatch")
	}
	cents, err := qufuAmountCents(result["orderAmount"])
	if err != nil {
		return nil, err
	}
	status, err := qufuStatus(result["orderStatus"])
	if err != nil {
		return nil, err
	}
	return &payment.QueryOrderResponse{TradeNo: result["tradeNo"], Status: status, Amount: float64(cents) / 100, Metadata: qufuMetadata(result)}, nil
}

func qufuStatus(value string) (string, error) {
	switch value {
	case "1":
		return payment.ProviderStatusPending, nil
	case "2":
		return payment.ProviderStatusPaid, nil
	case "3":
		return payment.ProviderStatusFailed, nil
	default:
		return "", fmt.Errorf("qufu invalid payment status")
	}
}

func qufuMetadata(fields map[string]string) map[string]string {
	return map[string]string{"mch_id": fields["mchId"], "order_no": fields["orderNo"], "channel_type": fields["channelType"], "currency": "CNY", "share_tag": fields["shareTag"], "share_fund_list": fields["shareFundList"]}
}

func (q *Qufu) VerifyNotification(_ context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	if len(rawBody) > qufuMaxResponseSize {
		return nil, fmt.Errorf("qufu notification exceeds size limit")
	}
	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("qufu malformed notification")
	}
	fields := make(map[string]string, len(values))
	for key, value := range values {
		if len(value) != 1 {
			return nil, fmt.Errorf("qufu duplicate notification parameter")
		}
		fields[key] = value[0]
	}
	if err := q.verify(fields, qufuNotificationFields); err != nil {
		return nil, err
	}
	if fields["mchId"] != q.config["mchId"] || !qufuIdentifier(fields["orderNo"]) || !qufuIdentifier(fields["tradeNo"]) || !q.knownChannel(fields["channelType"]) {
		return nil, fmt.Errorf("qufu notification identity mismatch")
	}
	if fields["attach"] != "" {
		return nil, fmt.Errorf("qufu notification attachment mismatch")
	}
	cents, err := qufuAmountCents(fields["orderAmount"])
	if err != nil {
		return nil, err
	}
	status, err := qufuStatus(fields["orderStatus"])
	if err != nil {
		return nil, err
	}
	if status != payment.ProviderStatusPaid {
		return nil, nil
	}
	if _, err := time.Parse("2006-01-02 15:04:05", fields["orderTime"]); err != nil {
		return nil, fmt.Errorf("qufu successful notification has an invalid order timestamp")
	}
	if _, err := time.Parse("2006-01-02 15:04:05", fields["successTime"]); err != nil {
		return nil, fmt.Errorf("qufu successful notification is missing payment timestamps")
	}
	// Keep the verified protocol fields for audit, excluding arbitrary extra
	// parameters that may contain credentials or untrusted diagnostic payloads.
	audit := make(url.Values, len(qufuNotificationFields)+1)
	for _, key := range qufuNotificationFields {
		audit.Set(key, fields[key])
	}
	audit.Set("sign", fields["sign"])
	return &payment.PaymentNotification{TradeNo: fields["tradeNo"], OrderID: fields["orderNo"], Amount: float64(cents) / 100, Status: payment.NotificationStatusSuccess, RawData: audit.Encode(), Metadata: qufuMetadata(fields)}, nil
}

func (q *Qufu) ConfirmNotification(ctx context.Context, n *payment.PaymentNotification) error {
	if n == nil || n.Status != payment.NotificationStatusSuccess || n.Metadata["mch_id"] != q.config["mchId"] || n.Metadata["order_no"] != n.OrderID {
		return fmt.Errorf("qufu invalid notification for confirmation")
	}
	order, err := q.QueryOrder(ctx, n.OrderID)
	if err != nil {
		return err
	}
	if order.Status != payment.ProviderStatusPaid || order.TradeNo != n.TradeNo || order.Amount != n.Amount || order.Metadata["channel_type"] != n.Metadata["channel_type"] {
		return fmt.Errorf("qufu notification does not match confirmed payment")
	}
	n.Metadata = order.Metadata
	return nil
}

func (q *Qufu) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("qufu refunds are not supported by the supplied API")
}

func (q *Qufu) knownChannel(value string) bool {
	return value == q.config["alipayChannel"] || value == q.config["wxpayChannel"]
}

func (q *Qufu) validateIdentity(fields map[string]string, orderNo string) error {
	if fields["mchId"] != q.config["mchId"] || fields["version"] != "1.0" || !strings.EqualFold(fields["charset"], "UTF-8") || fields["orderNo"] != orderNo || !qufuIdentifier(fields["tradeNo"]) || !q.knownChannel(fields["channelType"]) {
		return fmt.Errorf("qufu gateway response identity mismatch")
	}
	return nil
}

func (q *Qufu) verify(fields map[string]string, order []string) error {
	for _, key := range order {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("qufu signed response missing required field")
		}
	}
	if !strings.EqualFold(fields["signType"], "MD5") {
		return fmt.Errorf("qufu unsupported signature type")
	}
	signature := fields["sign"]
	if len(signature) != 32 || signature != strings.ToLower(signature) || !hmac.Equal([]byte(qufuSign(fields, order, q.config["merchantKey"])), []byte(signature)) {
		return fmt.Errorf("qufu signature verification failed")
	}
	return nil
}

func qufuJSONObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, fmt.Errorf("qufu expected a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("qufu malformed JSON response")
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("qufu malformed JSON response")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("qufu duplicate JSON response field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("qufu malformed JSON response")
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("qufu malformed JSON response")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("qufu trailing JSON response")
	}
	return fields, nil
}

func (q *Qufu) post(ctx context.Context, fields map[string]string) (map[string]string, error) {
	form := make(url.Values, len(fields))
	for key, value := range fields {
		form.Set(key, value)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, q.config["apiBase"], strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("qufu could not construct gateway request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("Accept", "application/json")
	response, err := q.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("qufu gateway request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qufu gateway returned HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, qufuMaxResponseSize+1))
	if err != nil || len(body) > qufuMaxResponseSize {
		return nil, fmt.Errorf("qufu invalid gateway response size")
	}
	envelope, err := qufuJSONObject(body)
	if err != nil {
		return nil, err
	}
	var code string
	if err := json.Unmarshal(envelope["code"], &code); err != nil {
		return nil, fmt.Errorf("qufu gateway rejected request")
	}
	if code != "000000" {
		// Only the documented six-digit code is safe diagnostic data. Never
		// propagate gateway messages or payloads: they can echo credentials.
		if len(code) == 6 && strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
			if code == "400005" {
				return nil, fmt.Errorf("qufu gateway rejected request (code 400005): check payer IP and IPv6 compatibility configuration")
			}
			return nil, fmt.Errorf("qufu gateway rejected request (code %s)", code)
		}
		return nil, fmt.Errorf("qufu gateway rejected request")
	}
	data, err := qufuJSONObject(envelope["data"])
	if err != nil {
		return nil, err
	}
	var signedFields []string
	switch fields["service"] {
	case "api.trade.order.pay":
		signedFields = qufuCreateResponseFields
	case "api.trade.order.query":
		signedFields = qufuQueryResponseFields
	default:
		return nil, fmt.Errorf("qufu unsupported gateway service")
	}
	// Decode only fields used by this response's signature and identity checks.
	// Unsigned extensions (for example optional channel diagnostics) do not
	// participate in payment decisions and may have arbitrary JSON types.
	// qufuJSONObject has already rejected duplicate keys, including extensions.
	keys := append(append([]string(nil), signedFields...), "sign")
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		raw, exists := data[key]
		if !exists {
			continue // verify rejects missing signed fields and missing signatures.
		}
		var value string
		if len(raw) == 0 || raw[0] != '"' {
			return nil, fmt.Errorf("qufu expected string response fields")
		}
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("qufu expected string response fields")
		}
		result[key] = value
	}
	return result, nil
}
