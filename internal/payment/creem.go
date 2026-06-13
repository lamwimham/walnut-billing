package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"walnut-billing/internal/domain"
)

const (
	creemProviderName       = "creem"
	creemDefaultProdBaseURL = "https://api.creem.io"
	creemDefaultTestBaseURL = "https://test-api.creem.io"
)

var (
	ErrCreemInvalidConfig       = errors.New("invalid creem config")
	ErrCreemProductNotMapped    = errors.New("creem product not mapped")
	ErrCreemWebhookUnverified   = errors.New("creem webhook signature verification failed")
	ErrCreemInvalidWebhook      = errors.New("invalid creem webhook")
	ErrCreemCheckoutUnsupported = errors.New("creem legacy payment URL is unsupported; use checkout sessions")
)

type CreemConfig struct {
	APIKey         string
	WebhookSecret  string
	APIBaseURL     string
	SuccessURL     string
	CancelURL      string
	SandboxMode    bool
	ProductIDs     map[string]string
	ProductMapJSON string
	HTTPClient     *http.Client
}

// CreemAdapter keeps Creem-specific checkout and webhook behavior behind the
// payment adapter boundary. Walnut access decisions remain in fulfillment.
type CreemAdapter struct {
	apiKey        string
	webhookSecret string
	apiBaseURL    string
	successURL    string
	cancelURL     string
	sandboxMode   bool
	productIDs    map[string]string
	httpClient    *http.Client
}

var _ PaymentProvider = (*CreemAdapter)(nil)
var _ CheckoutProvider = (*CreemAdapter)(nil)
var _ WebhookVerifier = (*CreemAdapter)(nil)

func NewCreemAdapter(cfg CreemConfig) (*CreemAdapter, error) {
	products, err := creemProductMap(cfg.ProductIDs, cfg.ProductMapJSON)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	webhookSecret := strings.TrimSpace(cfg.WebhookSecret)
	if apiKey == "" || webhookSecret == "" || len(products) == 0 {
		return nil, ErrCreemInvalidConfig
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &CreemAdapter{
		apiKey:        apiKey,
		webhookSecret: webhookSecret,
		apiBaseURL:    normalizeCreemAPIBaseURL(cfg.APIBaseURL, cfg.SandboxMode),
		successURL:    strings.TrimSpace(cfg.SuccessURL),
		cancelURL:     strings.TrimSpace(cfg.CancelURL),
		sandboxMode:   cfg.SandboxMode,
		productIDs:    products,
		httpClient:    client,
	}, nil
}

func (c *CreemAdapter) Name() string {
	return creemProviderName
}

func (c *CreemAdapter) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	return "", ErrCreemCheckoutUnsupported
}

func (c *CreemAdapter) CreateCheckoutSession(ctx context.Context, req CheckoutRequest) (*CheckoutSession, error) {
	if c == nil || strings.TrimSpace(req.OutTradeNo) == "" || strings.TrimSpace(req.SKUCode) == "" {
		return nil, ErrCreemInvalidConfig
	}
	productID := strings.TrimSpace(c.productIDs[strings.TrimSpace(req.SKUCode)])
	if productID == "" {
		return nil, fmt.Errorf("%w: %s", ErrCreemProductNotMapped, req.SKUCode)
	}

	body := creemCheckoutRequest{
		ProductID:  productID,
		RequestID:  strings.TrimSpace(req.OutTradeNo),
		SuccessURL: firstNonEmpty(req.SuccessURL, c.successURL),
		Metadata:   creemCheckoutMetadata(req, c.cancelURL),
	}
	if strings.TrimSpace(req.CustomerEmail) != "" || strings.TrimSpace(req.CustomerName) != "" {
		body.Customer = &creemCheckoutCustomer{
			Email: strings.TrimSpace(req.CustomerEmail),
			Name:  strings.TrimSpace(req.CustomerName),
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.apiBaseURL, "/")+"/v1/checkouts", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("creem checkout request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("creem checkout error: status=%d body=%s", resp.StatusCode, string(respBody))
	}

	var checkout creemCheckoutResponse
	if err := json.Unmarshal(respBody, &checkout); err != nil {
		return nil, fmt.Errorf("parse creem checkout response: %w", err)
	}
	checkoutURL := firstNonEmpty(checkout.CheckoutURL, checkout.CheckoutURLCamel)
	if checkout.ID == "" || checkoutURL == "" {
		return nil, fmt.Errorf("creem checkout response missing id or checkout_url")
	}
	return &CheckoutSession{
		CheckoutURL:        checkoutURL,
		ProviderCheckoutID: checkout.ID,
		ProviderCustomerID: firstNonEmpty(checkout.CustomerID, checkout.Customer.ID),
		Status:             domain.OrderStatusCheckoutCreated,
	}, nil
}

func (c *CreemAdapter) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	return "", "", 0, ErrCreemCheckoutUnsupported
}

func (c *CreemAdapter) VerifyWebhookEvent(ctx context.Context, req WebhookVerificationRequest) (*VerifiedWebhookEvent, error) {
	if c == nil || strings.TrimSpace(c.webhookSecret) == "" || len(req.RawPayload) == 0 {
		return nil, ErrCreemInvalidWebhook
	}
	signature := headerValue(req.Headers, "creem-signature")
	if signature == "" || !verifyCreemSignature(req.RawPayload, c.webhookSecret, signature) {
		return nil, ErrCreemWebhookUnverified
	}

	var payload map[string]any
	if err := json.Unmarshal(req.RawPayload, &payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCreemInvalidWebhook, err)
	}
	eventID := stringAt(payload, "id")
	eventType := stringAt(payload, "eventType")
	if eventID == "" || eventType == "" {
		return nil, ErrCreemInvalidWebhook
	}

	return &VerifiedWebhookEvent{
		ProviderEventID:   eventID,
		EventType:         mapCreemEventType(eventType, payload),
		OutTradeNo:        creemOutTradeNo(payload),
		ProviderTradeNo:   creemProviderTradeNo(payload),
		Amount:            creemAmount(payload),
		Currency:          creemCurrency(payload),
		PeriodStartAt:     creemPeriodTime(payload, "current_period_start_date", "period_start"),
		PeriodEndAt:       creemPeriodTime(payload, "current_period_end_date", "period_end"),
		SignatureVerified: true,
		RawPayload:        string(req.RawPayload),
	}, nil
}

func (c *CreemAdapter) BuildSuccessResponse() (contentType string, body string) {
	return "application/json", `{"status":"ok"}`
}

func (c *CreemAdapter) BuildFailureResponse() (contentType string, body string) {
	return "application/json", `{"status":"fail"}`
}

type creemCheckoutRequest struct {
	ProductID  string                 `json:"product_id"`
	RequestID  string                 `json:"request_id,omitempty"`
	SuccessURL string                 `json:"success_url,omitempty"`
	Customer   *creemCheckoutCustomer `json:"customer,omitempty"`
	Metadata   map[string]string      `json:"metadata,omitempty"`
}

type creemCheckoutCustomer struct {
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

type creemCheckoutResponse struct {
	ID               string `json:"id"`
	CheckoutURL      string `json:"checkout_url"`
	CheckoutURLCamel string `json:"checkoutUrl"`
	Status           string `json:"status"`
	CustomerID       string `json:"customer_id"`
	Customer         struct {
		ID string `json:"id"`
	} `json:"customer"`
}

func creemProductMap(rawMap map[string]string, rawJSON string) (map[string]string, error) {
	result := make(map[string]string)
	for sku, productID := range rawMap {
		sku = strings.TrimSpace(sku)
		productID = strings.TrimSpace(productID)
		if sku != "" && productID != "" {
			result[sku] = productID
		}
	}
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return result, nil
	}
	var object map[string]string
	if err := json.Unmarshal([]byte(rawJSON), &object); err == nil {
		for sku, productID := range object {
			sku = strings.TrimSpace(sku)
			productID = strings.TrimSpace(productID)
			if sku != "" && productID != "" {
				result[sku] = productID
			}
		}
		return result, nil
	}
	var wrapped struct {
		Products map[string]string `json:"products"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &wrapped); err == nil && len(wrapped.Products) > 0 {
		for sku, productID := range wrapped.Products {
			sku = strings.TrimSpace(sku)
			productID = strings.TrimSpace(productID)
			if sku != "" && productID != "" {
				result[sku] = productID
			}
		}
		return result, nil
	}
	var rows []struct {
		SKUCode   string `json:"sku_code"`
		ProductID string `json:"product_id"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &rows); err != nil {
		return nil, fmt.Errorf("%w: product map json", ErrCreemInvalidConfig)
	}
	for _, row := range rows {
		sku := strings.TrimSpace(row.SKUCode)
		productID := strings.TrimSpace(row.ProductID)
		if sku != "" && productID != "" {
			result[sku] = productID
		}
	}
	return result, nil
}

func normalizeCreemAPIBaseURL(baseURL string, sandbox bool) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		if sandbox {
			return creemDefaultTestBaseURL
		}
		return creemDefaultProdBaseURL
	}
	return strings.TrimRight(strings.TrimSuffix(baseURL, "/v1"), "/")
}

func creemCheckoutMetadata(req CheckoutRequest, defaultCancelURL string) map[string]string {
	metadata := make(map[string]string, len(req.Metadata)+6)
	for key, value := range req.Metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		metadata[key] = strings.TrimSpace(value)
	}
	metadata["walnut_out_trade_no"] = strings.TrimSpace(req.OutTradeNo)
	metadata["walnut_user_id"] = strings.TrimSpace(req.UserID)
	metadata["walnut_sku_code"] = strings.TrimSpace(req.SKUCode)
	metadata["walnut_idempotency_key"] = strings.TrimSpace(req.IdempotencyKey)
	metadata["walnut_provider"] = creemProviderName
	if cancelURL := firstNonEmpty(req.CancelURL, defaultCancelURL); cancelURL != "" {
		metadata["walnut_cancel_url"] = cancelURL
	}
	for key, value := range metadata {
		if strings.TrimSpace(value) == "" {
			delete(metadata, key)
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func verifyCreemSignature(payload []byte, secret string, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)
	provided, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return false
	}
	return hmac.Equal(expected, provided)
}

func headerValue(headers map[string]string, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	for headerKey, value := range headers {
		if strings.ToLower(strings.TrimSpace(headerKey)) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mapCreemEventType(eventType string, payload map[string]any) string {
	switch strings.TrimSpace(eventType) {
	case "checkout.completed":
		return domain.PaymentEventTypePaid
	case "refund.created":
		if creemOutTradeNo(payload) != "" {
			return domain.PaymentEventTypeRefunded
		}
	case "dispute.created", "chargeback.created":
		if creemOutTradeNo(payload) != "" {
			return domain.PaymentEventTypeDisputed
		}
	case "subscription.paid":
		if creemOutTradeNo(payload) != "" {
			return domain.PaymentEventTypeRenewalPaid
		}
	case "subscription.past_due":
		if creemOutTradeNo(payload) != "" {
			return domain.PaymentEventTypeRenewalFailed
		}
	case "subscription.expired":
		if creemOutTradeNo(payload) != "" {
			return domain.PaymentEventTypeSubscriptionExpired
		}
	}
	return strings.TrimSpace(eventType)
}

func creemOutTradeNo(payload map[string]any) string {
	return firstNonEmpty(
		stringAt(payload, "object", "request_id"),
		stringAt(payload, "object", "checkout", "request_id"),
		stringAt(payload, "object", "metadata", "walnut_out_trade_no"),
		stringAt(payload, "object", "checkout", "metadata", "walnut_out_trade_no"),
		stringAt(payload, "object", "subscription", "metadata", "walnut_out_trade_no"),
		stringAt(payload, "object", "dispute", "metadata", "walnut_out_trade_no"),
		stringAt(payload, "object", "chargeback", "metadata", "walnut_out_trade_no"),
		stringAt(payload, "object", "metadata", "request_id"),
		stringAt(payload, "object", "metadata", "order_id"),
	)
}

func creemProviderTradeNo(payload map[string]any) string {
	return firstNonEmpty(
		stringAt(payload, "object", "order", "id"),
		stringAt(payload, "object", "transaction", "id"),
		stringAt(payload, "object", "dispute", "id"),
		stringAt(payload, "object", "chargeback", "id"),
		stringAt(payload, "object", "order"),
		stringAt(payload, "object", "last_transaction_id"),
		stringAt(payload, "object", "id"),
	)
}

func creemAmount(payload map[string]any) int64 {
	return firstPositiveInt64(
		intAt(payload, "object", "order", "amount"),
		intAt(payload, "object", "transaction", "amount"),
		intAt(payload, "object", "refund_amount"),
		intAt(payload, "object", "dispute", "amount"),
		intAt(payload, "object", "chargeback", "amount"),
		intAt(payload, "object", "amount"),
		intAt(payload, "object", "product", "price"),
	)
}

func creemCurrency(payload map[string]any) string {
	return firstNonEmpty(
		stringAt(payload, "object", "order", "currency"),
		stringAt(payload, "object", "transaction", "currency"),
		stringAt(payload, "object", "refund_currency"),
		stringAt(payload, "object", "dispute", "currency"),
		stringAt(payload, "object", "chargeback", "currency"),
		stringAt(payload, "object", "currency"),
		stringAt(payload, "object", "product", "currency"),
	)
}

func creemPeriodTime(payload map[string]any, subscriptionField string, transactionField string) *time.Time {
	if value := stringAt(payload, "object", subscriptionField); value != "" {
		return parseCreemTime(value)
	}
	if value := stringAt(payload, "object", "subscription", subscriptionField); value != "" {
		return parseCreemTime(value)
	}
	if value := intAt(payload, "object", transactionField); value > 0 {
		return creemUnixMillis(value)
	}
	if value := intAt(payload, "object", "order", transactionField); value > 0 {
		return creemUnixMillis(value)
	}
	return nil
}

func parseCreemTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	return nil
}

func creemUnixMillis(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	parsed := time.UnixMilli(value).UTC()
	return &parsed
}

func stringAt(value any, path ...string) string {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	switch typed := current.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%f", typed)
	default:
		return ""
	}
}

func intAt(value any, path ...string) int64 {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return 0
		}
		current = object[key]
	}
	switch typed := current.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
