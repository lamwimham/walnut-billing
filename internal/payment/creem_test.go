package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestCreemAdapter_RejectsBadWebhookSignature(t *testing.T) {
	adapter, err := NewCreemAdapter(CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		ProductIDs:    map[string]string{"credits_600": "prod_credits"},
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	_, err = adapter.VerifyWebhookEvent(context.Background(), WebhookVerificationRequest{
		Headers:    map[string]string{"creem-signature": "bad"},
		RawPayload: []byte(`{"id":"evt_1","eventType":"checkout.completed"}`),
	})
	if err == nil {
		t.Fatalf("expected bad signature error")
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
