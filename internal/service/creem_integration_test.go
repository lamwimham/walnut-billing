package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
)

func TestCreemCheckoutWebhookFulfillmentLoop(t *testing.T) {
	users := newMockEntitlementUserRepo()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", DisplayName: "Writer", Status: domain.UserStatusActive}
	products := newMockProductRepo()
	products.products["editorial_studio_monthly"] = &domain.Product{Code: "editorial_studio_monthly", Name: "Editorial Studio", Price: 1900, Currency: "USD", IsVisible: true}
	orders := newMockTxOrderRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	transactions := newMockCreditTransactionRepo()
	executions := newMockFulfillmentExecutionRepo()
	events := newMockPaymentEventRepo()

	var checkoutOutTradeNo string
	creemServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkouts" || r.Header.Get("x-api-key") != "creem_test_key" {
			t.Fatalf("unexpected creem checkout request path=%s apiKey=%s", r.URL.Path, r.Header.Get("x-api-key"))
		}
		checkoutOutTradeNo = ""
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode checkout payload: %v", err)
		}
		checkoutOutTradeNo, _ = payload["request_id"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"ch_%s","checkout_url":"https://checkout.creem.io/ch_%s","customer_id":"cust_1"}`, checkoutOutTradeNo, checkoutOutTradeNo)
	}))
	defer creemServer.Close()

	creemAdapter, err := payment.NewCreemAdapter(payment.CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		APIBaseURL:    creemServer.URL,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
	})
	if err != nil {
		t.Fatalf("create creem adapter: %v", err)
	}
	registry := payment.NewProviderRegistry()
	registry.Register("creem", creemAdapter, payment.ProviderStatus{SandboxMode: true})
	paymentSvc := payment.NewPaymentService(orders, nil, registry)
	checkoutSvc := NewCheckoutService(orders, products, users, paymentSvc)
	fulfillmentCatalog, err := NewStaticFulfillmentCatalog(editorialStudioFulfillmentRules()...)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	fulfillmentSvc := NewFulfillmentService(FulfillmentDependencies{
		Repositories: FulfillmentRepositories{
			Orders:                orders,
			Users:                 users,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			FulfillmentExecutions: executions,
		},
		Catalog:            fulfillmentCatalog,
		EntitlementCatalog: DefaultEntitlementCatalog(),
	})
	processor := NewPaymentFulfillmentEventProcessor(orders, NewPaymentOrderEventProcessor(orders), fulfillmentSvc)
	eventSvc := NewPaymentEventService(events, paymentSvc, processor)

	checkout, err := checkoutSvc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "creem",
		IdempotencyKey: "checkout:creem:1",
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if checkoutOutTradeNo == "" || checkout.Order.OutTradeNo != checkoutOutTradeNo || checkout.CheckoutURL == "" {
		t.Fatalf("unexpected checkout result=%#v capturedOutTradeNo=%s", checkout, checkoutOutTradeNo)
	}

	webhookPayload := []byte(fmt.Sprintf(`{"id":"evt_paid_1","eventType":"checkout.completed","object":{"id":"ch_1","request_id":"%s","order":{"id":"ord_1","amount":1900,"currency":"USD","status":"paid"},"metadata":{"walnut_out_trade_no":"%s"}}}`, checkoutOutTradeNo, checkoutOutTradeNo))
	result, err := eventSvc.ReceiveWebhook(context.Background(), PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(webhookPayload, "whsec_test")},
		RawPayload: webhookPayload,
	})
	if err != nil {
		t.Fatalf("receive webhook: %v", err)
	}
	if !result.Processed || result.Event.Status != domain.PaymentEventStatusProcessed {
		t.Fatalf("expected processed event, got %#v", result)
	}
	order := orders.orders[checkoutOutTradeNo]
	if order.Status != domain.OrderStatusFulfilled || order.PaidAt == nil || order.FulfilledAt == nil {
		t.Fatalf("expected fulfilled order, got %#v", order)
	}
	if len(grants.grants) != 1 || len(transactions.transactions) != 1 || len(executions.executions) != 2 {
		t.Fatalf("expected entitlement+credits fulfillment, grants=%d txs=%d executions=%d", len(grants.grants), len(transactions.transactions), len(executions.executions))
	}
}

func creemIntegrationSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
