package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"walnut-billing/internal/domain"
)

func TestCreemAdapter_CreateCheckoutSessionBuildsProviderRequest(t *testing.T) {
	var captured struct {
		Path    string
		APIKey  string
		Payload map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.APIKey = r.Header.Get("x-api-key")
		if err := json.NewDecoder(r.Body).Decode(&captured.Payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ch_test","checkout_url":"https://checkout.creem.io/ch_test","customer_id":"cust_1","status":"pending"}`))
	}))
	defer server.Close()

	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		APIBaseURL:    server.URL,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
		SuccessURL:    "https://walnut.local/success",
		CancelURL:     "https://walnut.local/cancel",
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	session, err := adapter.CreateCheckoutSession(context.Background(), CheckoutRequest{
		OutTradeNo:     "CHK-1",
		UserID:         "usr_1",
		CustomerEmail:  "writer@example.com",
		CustomerName:   "Writer",
		SKUCode:        "editorial_studio_monthly",
		IdempotencyKey: "checkout:1",
		Metadata:       map[string]string{"source": "desktop"},
	})
	if err != nil {
		t.Fatalf("create checkout: %v", err)
	}
	if session.CheckoutURL == "" || session.ProviderCheckoutID != "ch_test" || session.ProviderCustomerID != "cust_1" {
		t.Fatalf("unexpected session: %#v", session)
	}
	if captured.Path != "/v1/checkouts" || captured.APIKey != "creem_test_key" {
		t.Fatalf("unexpected request path/api key: %#v", captured)
	}
	if captured.Payload["product_id"] != "prod_studio" || captured.Payload["request_id"] != "CHK-1" || captured.Payload["success_url"] != "https://walnut.local/success" {
		t.Fatalf("unexpected checkout payload: %#v", captured.Payload)
	}
	metadata := captured.Payload["metadata"].(map[string]any)
	if metadata["walnut_out_trade_no"] != "CHK-1" || metadata["walnut_provider"] != "creem" || metadata["source"] != "desktop" || metadata["walnut_cancel_url"] == "" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	customer := captured.Payload["customer"].(map[string]any)
	if customer["email"] != "writer@example.com" || customer["name"] != "Writer" {
		t.Fatalf("unexpected customer: %#v", customer)
	}
}

func TestCreemAdapter_VerifyCheckoutCompletedWebhook(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	payload := []byte(`{"id":"evt_1","eventType":"checkout.completed","object":{"id":"ch_1","request_id":"CHK-1","order":{"id":"ord_1","amount":1900,"currency":"EUR","status":"paid"},"metadata":{"walnut_out_trade_no":"CHK-1"}}}`)
	signature := testCreemSignature(payload, "whsec_test")

	event, err := adapter.VerifyWebhookEvent(context.Background(), WebhookVerificationRequest{
		Headers:    map[string]string{"Creem-Signature": signature},
		RawPayload: payload,
	})
	if err != nil {
		t.Fatalf("verify webhook: %v", err)
	}
	if event.EventType != domain.PaymentEventTypePaid || event.ProviderEventID != "evt_1" || event.OutTradeNo != "CHK-1" || event.ProviderTradeNo != "ord_1" || event.Amount != 1900 || event.Currency != "EUR" || !event.SignatureVerified {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestCreemAdapter_VerifyDisputeWebhookMapsToPaymentDisputed(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	payload := []byte(`{"id":"evt_dispute_1","eventType":"dispute.created","object":{"id":"disp_1","dispute":{"id":"disp_1","amount":1900,"currency":"USD","metadata":{"walnut_out_trade_no":"CHK-1"}}}}`)
	signature := testCreemSignature(payload, "whsec_test")

	event, err := adapter.VerifyWebhookEvent(context.Background(), WebhookVerificationRequest{
		Headers:    map[string]string{"creem-signature": signature},
		RawPayload: payload,
	})
	if err != nil {
		t.Fatalf("verify webhook: %v", err)
	}
	if event.EventType != domain.PaymentEventTypeDisputed || event.ProviderEventID != "evt_dispute_1" || event.OutTradeNo != "CHK-1" || event.ProviderTradeNo != "disp_1" || event.Amount != 1900 || event.Currency != "USD" {
		t.Fatalf("unexpected dispute event: %#v", event)
	}
}

func TestCreemAdapter_VerifySubscriptionWebhooksMapToRenewalEvents(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	tests := []struct {
		name      string
		creemType string
		wantType  string
	}{
		{name: "paid", creemType: "subscription.paid", wantType: domain.PaymentEventTypeRenewalPaid},
		{name: "past_due", creemType: "subscription.past_due", wantType: domain.PaymentEventTypeRenewalFailed},
		{name: "expired", creemType: "subscription.expired", wantType: domain.PaymentEventTypeSubscriptionExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`{"id":"evt_` + tt.name + `","eventType":"` + tt.creemType + `","object":{"id":"sub_1","subscription":{"id":"sub_1","metadata":{"walnut_out_trade_no":"RNL-1"}},"order":{"id":"ord_renewal_1","amount":1900,"currency":"USD","period_start":1782997200000,"period_end":1785675600000},"current_period_start_date":"2026-07-02T09:00:00.000Z","current_period_end_date":"2026-08-02T09:00:00.000Z"}}`)
			event, err := adapter.VerifyWebhookEvent(context.Background(), WebhookVerificationRequest{
				Headers:    map[string]string{"creem-signature": testCreemSignature(payload, "whsec_test")},
				RawPayload: payload,
			})
			if err != nil {
				t.Fatalf("verify webhook: %v", err)
			}
			if event.EventType != tt.wantType || event.OutTradeNo != "RNL-1" || event.ProviderTradeNo != "ord_renewal_1" || event.PeriodStartAt == nil || event.PeriodEndAt == nil {
				t.Fatalf("unexpected subscription event: %#v", event)
			}
		})
	}
}

func TestCreemAdapter_RejectsBadWebhookSignature(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		ProductIDs:    map[string]string{"credits_600": "prod_credits"},
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	_, err = adapter.VerifyWebhookEvent(context.Background(), WebhookVerificationRequest{
		Headers:    map[string]string{"creem-signature": "bad"},
		RawPayload: []byte(`{"id":"evt_1","eventType":"checkout.completed"}`),
	})
	if !errors.Is(err, ErrWebhookSignatureVerificationFailed) || !errors.Is(err, ErrCreemWebhookUnverified) {
		t.Fatalf("expected wrapped bad signature error, got %v", err)
	}
}

func TestCreemAdapter_ValidatesRequiredProductMappings(t *testing.T) {
	_, err := NewCreemAdapter(CreemConfig{
		APIKey:           "creem_test_key",
		WebhookSecret:    "whsec_test",
		SandboxMode:      true,
		ProductIDs:       map[string]string{"pro_own_ai_monthly": "prod_monthly"},
		RequiredSKUCodes: []string{"pro_own_ai_monthly", "pro_own_ai_lifetime"},
	})
	if !errors.Is(err, ErrCreemProductNotMapped) {
		t.Fatalf("expected missing required sku mapping error, got %v", err)
	}
}

func TestCreemAdapter_RejectsEnvironmentMixing(t *testing.T) {
	tests := []struct {
		name string
		cfg  CreemConfig
	}{
		{
			name: "sandbox with production endpoint",
			cfg: CreemConfig{
				APIKey:        "creem_test_key",
				WebhookSecret: "whsec_test",
				SandboxMode:   true,
				APIBaseURL:    "https://api.creem.io",
				ProductIDs:    map[string]string{"pro_own_ai_monthly": "prod_monthly"},
			},
		},
		{
			name: "production with test endpoint",
			cfg: CreemConfig{
				APIKey:        "creem_live_key",
				WebhookSecret: "whsec_live",
				SandboxMode:   false,
				APIBaseURL:    "https://test-api.creem.io",
				ProductIDs:    map[string]string{"pro_own_ai_monthly": "prod_monthly"},
			},
		},
		{
			name: "production with test key",
			cfg: CreemConfig{
				APIKey:        "creem_test_key",
				WebhookSecret: "whsec_live",
				SandboxMode:   false,
				ProductIDs:    map[string]string{"pro_own_ai_monthly": "prod_monthly"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCreemAdapter(tt.cfg)
			if !errors.Is(err, ErrCreemEnvironmentMismatch) {
				t.Fatalf("expected environment mismatch, got %v", err)
			}
		})
	}
}

func TestCreemAdapter_AcceptsTestModeDefaults(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:           "creem_test_key",
		WebhookSecret:    "whsec_test",
		SandboxMode:      true,
		ProductIDs:       map[string]string{"pro_own_ai_monthly": "prod_monthly"},
		RequiredSKUCodes: []string{"pro_own_ai_monthly"},
	})
	if err != nil {
		t.Fatalf("expected test mode config to pass: %v", err)
	}
	if adapter.apiBaseURL != creemDefaultTestBaseURL {
		t.Fatalf("expected default test endpoint %s, got %s", creemDefaultTestBaseURL, adapter.apiBaseURL)
	}
}

func TestCreemProductMapSupportsObjectWrappedAndRows(t *testing.T) {
	objectMap, err := creemProductMap(nil, `{"credits_600":"prod_credits"}`)
	if err != nil || objectMap["credits_600"] != "prod_credits" {
		t.Fatalf("unexpected object map=%#v err=%v", objectMap, err)
	}
	wrappedMap, err := creemProductMap(nil, `{"products":{"editorial_studio_monthly":"prod_studio"}}`)
	if err != nil || wrappedMap["editorial_studio_monthly"] != "prod_studio" {
		t.Fatalf("unexpected wrapped map=%#v err=%v", wrappedMap, err)
	}
	rowsMap, err := creemProductMap(nil, `[{"sku_code":"credits_1200","product_id":"prod_1200"}]`)
	if err != nil || rowsMap["credits_1200"] != "prod_1200" {
		t.Fatalf("unexpected rows map=%#v err=%v", rowsMap, err)
	}
}

func testCreemSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
