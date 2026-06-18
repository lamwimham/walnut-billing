package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
)

type commerceFlowHarness struct {
	users          *mockEntitlementUserRepo
	products       *mockProductRepo
	orders         *mockTxOrderRepo
	grants         *mockGrantRepo
	accounts       *mockCreditAccountRepo
	transactions   *mockCreditTransactionRepo
	executions     *mockFulfillmentExecutionRepo
	events         *mockPaymentEventRepo
	risks          *mockPaymentRiskFlagRepo
	checkoutSvc    CheckoutService
	eventSvc       PaymentEventService
	entitlementSvc EntitlementService
	checkoutCalls  *atomic.Int64
	webhookSecret  string
}

func newCommerceFlowHarness(t *testing.T) (*commerceFlowHarness, func()) {
	t.Helper()
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
	risks := newMockPaymentRiskFlagRepo()
	checkoutCalls := &atomic.Int64{}

	creemServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkoutCalls.Add(1)
		if r.URL.Path != "/v1/checkouts" || r.Header.Get("x-api-key") != "creem_test_key" {
			t.Fatalf("unexpected creem checkout request path=%s apiKey=%s", r.URL.Path, r.Header.Get("x-api-key"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode checkout payload: %v", err)
		}
		outTradeNo, _ := payload["request_id"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"ch_%s","checkout_url":"https://checkout.creem.io/ch_%s","customer_id":"cust_1"}`, outTradeNo, outTradeNo)
	}))

	creemAdapter, err := payment.NewCreemAdapter(payment.CreemConfig{
		APIKey:        "creem_test_key",
		WebhookSecret: "whsec_test",
		SandboxMode:   true,
		APIBaseURL:    creemServer.URL,
		ProductIDs:    map[string]string{"editorial_studio_monthly": "prod_studio"},
	})
	if err != nil {
		creemServer.Close()
		t.Fatalf("create creem adapter: %v", err)
	}
	registry := payment.NewProviderRegistry()
	registry.Register("creem", creemAdapter, payment.ProviderStatus{SandboxMode: true})
	paymentSvc := payment.NewPaymentService(orders, nil, registry)
	checkoutSvc := NewCheckoutServiceWithPolicies(
		orders,
		products,
		users,
		paymentSvc,
		NewPaymentRiskCheckoutPolicy(risks, DefaultCheckoutRiskPolicyConfig()),
	)
	fulfillmentCatalog, err := NewStaticFulfillmentCatalog(editorialStudioFulfillmentRules()...)
	if err != nil {
		creemServer.Close()
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
	adjustmentSvc := NewPaymentAdjustmentService(PaymentAdjustmentDependencies{
		Repositories: PaymentAdjustmentRepositories{
			Orders:                orders,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			FulfillmentExecutions: executions,
			PaymentRiskFlags:      risks,
		},
	})
	processor := NewPaymentFulfillmentEventProcessorWithAdjustments(orders, NewPaymentOrderEventProcessor(orders), fulfillmentSvc, adjustmentSvc)
	eventSvc := NewPaymentEventService(events, paymentSvc, processor)
	entitlementSvc := NewEntitlementServiceWithCredits(users, newMockRegistrationRepo(), grants, accounts, DefaultEntitlementCatalog())

	return &commerceFlowHarness{
		users:          users,
		products:       products,
		orders:         orders,
		grants:         grants,
		accounts:       accounts,
		transactions:   transactions,
		executions:     executions,
		events:         events,
		risks:          risks,
		checkoutSvc:    checkoutSvc,
		eventSvc:       eventSvc,
		entitlementSvc: entitlementSvc,
		checkoutCalls:  checkoutCalls,
		webhookSecret:  "whsec_test",
	}, creemServer.Close
}

func (h *commerceFlowHarness) createCheckout(t *testing.T, idempotencyKey string) *CheckoutResult {
	t.Helper()
	checkout, err := h.checkoutSvc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "creem",
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if checkout.Order.OutTradeNo == "" || checkout.CheckoutURL == "" {
		t.Fatalf("unexpected checkout result=%#v", checkout)
	}
	return checkout
}

func (h *commerceFlowHarness) receiveCreemWebhook(t *testing.T, payload []byte) *PaymentEventProcessResult {
	t.Helper()
	result, err := h.eventSvc.ReceiveWebhook(context.Background(), PaymentWebhookInput{
		Provider:   "creem",
		Headers:    map[string]string{"creem-signature": creemIntegrationSignature(payload, h.webhookSecret)},
		RawPayload: payload,
	})
	if err != nil {
		t.Fatalf("receive webhook: %v", err)
	}
	return result
}

func TestCreemCheckoutWebhookFulfillmentLoop(t *testing.T) {
	harness, cleanup := newCommerceFlowHarness(t)
	defer cleanup()

	checkout := harness.createCheckout(t, "checkout:creem:1")
	result := harness.receiveCreemWebhook(t, creemPaidWebhookPayload(checkout.Order.OutTradeNo, "evt_paid_1"))
	if !result.Processed || result.Event.Status != domain.PaymentEventStatusProcessed {
		t.Fatalf("expected processed event, got %#v", result)
	}
	order := harness.orders.orders[checkout.Order.OutTradeNo]
	if order.Status != domain.OrderStatusFulfilled || order.PaidAt == nil || order.FulfilledAt == nil {
		t.Fatalf("expected fulfilled order, got %#v", order)
	}
	if len(harness.grants.grants) != 1 || len(harness.transactions.transactions) != 1 || len(harness.executions.executions) != 2 {
		t.Fatalf("expected entitlement+credits fulfillment, grants=%d txs=%d executions=%d", len(harness.grants.grants), len(harness.transactions.transactions), len(harness.executions.executions))
	}
	if harness.checkoutCalls.Load() != 1 {
		t.Fatalf("expected one provider checkout call, got %d", harness.checkoutCalls.Load())
	}
}

func TestCommerceCheckoutWebhookSnapshotDisputeRiskHoldLoop(t *testing.T) {
	harness, cleanup := newCommerceFlowHarness(t)
	defer cleanup()

	checkout := harness.createCheckout(t, "checkout:flow:1")
	paidPayload := creemPaidWebhookPayload(checkout.Order.OutTradeNo, "evt_flow_paid_1")
	paid := harness.receiveCreemWebhook(t, paidPayload)
	if !paid.Processed || paid.Duplicate {
		t.Fatalf("expected first paid webhook to process, got %#v", paid)
	}

	snapshot, err := harness.entitlementSvc.SnapshotForUser(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("snapshot after paid: %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] || snapshot.Credits[domain.CreditMetricBalance] != 600 {
		t.Fatalf("expected fulfilled snapshot entitlement+credits, got %#v", snapshot)
	}

	duplicatePaid := harness.receiveCreemWebhook(t, paidPayload)
	if !duplicatePaid.Duplicate || !duplicatePaid.Processed {
		t.Fatalf("expected duplicate paid webhook to be idempotent, got %#v", duplicatePaid)
	}
	if len(harness.grants.grants) != 1 || len(harness.transactions.transactions) != 1 || len(harness.executions.executions) != 2 {
		t.Fatalf("duplicate paid webhook must not duplicate fulfillment, grants=%d txs=%d executions=%d", len(harness.grants.grants), len(harness.transactions.transactions), len(harness.executions.executions))
	}

	dispute := harness.receiveCreemWebhook(t, creemDisputeWebhookPayload(checkout.Order.OutTradeNo, "evt_flow_dispute_1"))
	if !dispute.Processed || dispute.Event.EventType != domain.PaymentEventTypeDisputed {
		t.Fatalf("expected dispute webhook to process, got %#v", dispute)
	}

	afterDispute, err := harness.entitlementSvc.SnapshotForUser(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("snapshot after dispute: %v", err)
	}
	if afterDispute.Entitlements[domain.EntitlementEditorialStudio] || afterDispute.Credits[domain.CreditMetricBalance] != 0 {
		t.Fatalf("expected disputed order to revoke entitlement and claw back credits, got %#v", afterDispute)
	}
	if len(harness.risks.flags) != 1 {
		t.Fatalf("expected one payment risk flag, got %d", len(harness.risks.flags))
	}
	for _, flag := range harness.risks.flags {
		if flag.Status != domain.PaymentRiskStatusOpen || flag.Severity != domain.PaymentRiskSeverityCritical || flag.UserID != "usr_1" {
			t.Fatalf("unexpected risk flag: %#v", flag)
		}
	}

	_, err = harness.checkoutSvc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "creem",
		IdempotencyKey: "checkout:flow:blocked",
	})
	if !errors.Is(err, ErrCheckoutBlockedByRisk) {
		t.Fatalf("expected checkout to be blocked by open risk flag, got %v", err)
	}
	if harness.checkoutCalls.Load() != 1 {
		t.Fatalf("blocked checkout must not call provider, got %d checkout calls", harness.checkoutCalls.Load())
	}
}

func creemPaidWebhookPayload(outTradeNo string, eventID string) []byte {
	return []byte(fmt.Sprintf(`{"id":"%s","eventType":"checkout.completed","object":{"id":"ch_1","request_id":"%s","order":{"id":"ord_1","amount":1900,"currency":"USD","status":"paid"},"metadata":{"walnut_out_trade_no":"%s"}}}`, eventID, outTradeNo, outTradeNo))
}

func creemDisputeWebhookPayload(outTradeNo string, eventID string) []byte {
	return []byte(fmt.Sprintf(`{"id":"%s","eventType":"dispute.created","object":{"id":"disp_1","dispute":{"id":"disp_1","amount":1900,"currency":"USD","metadata":{"walnut_out_trade_no":"%s"}}}}`, eventID, outTradeNo))
}

func creemIntegrationSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
