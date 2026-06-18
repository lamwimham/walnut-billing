package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
)

type mockCheckoutGateway struct {
	requests []payment.CheckoutRequest
	session  *payment.CheckoutSession
	err      error
}

func (m *mockCheckoutGateway) CreateCheckoutSession(ctx context.Context, providerName string, req payment.CheckoutRequest) (*payment.CheckoutSession, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return nil, m.err
	}
	if m.session != nil {
		return m.session, nil
	}
	return &payment.CheckoutSession{
		CheckoutURL:        "mock://checkout/" + req.OutTradeNo,
		ProviderCheckoutID: "chk_" + req.OutTradeNo,
		ProviderCustomerID: "cus_" + req.UserID,
		Status:             domain.OrderStatusCheckoutCreated,
	}, nil
}

func newCheckoutTestService() (CheckoutService, *mockTxOrderRepo, *mockProductRepo, *mockEntitlementUserRepo, *mockCheckoutGateway) {
	orders := newMockTxOrderRepo()
	products := newMockProductRepo()
	users := newMockEntitlementUserRepo()
	gateway := &mockCheckoutGateway{}
	return NewCheckoutService(orders, products, users, gateway), orders, products, users, gateway
}

func newCheckoutTestServiceWithPolicies(policies ...CheckoutPolicy) (CheckoutService, *mockTxOrderRepo, *mockProductRepo, *mockEntitlementUserRepo, *mockCheckoutGateway) {
	orders := newMockTxOrderRepo()
	products := newMockProductRepo()
	users := newMockEntitlementUserRepo()
	gateway := &mockCheckoutGateway{}
	return NewCheckoutServiceWithPolicies(orders, products, users, gateway, policies...), orders, products, users, gateway
}

func TestCheckoutService_CreateCheckoutSessionCreatesCommerceOrder(t *testing.T) {
	svc, orders, products, users, gateway := newCheckoutTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["editorial_studio_monthly"] = &domain.Product{
		Code:      "editorial_studio_monthly",
		Name:      "Editorial Studio Monthly",
		Price:     1900,
		Currency:  "USD",
		Validity:  "monthly",
		IsVisible: true,
	}

	result, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "mock",
		SuccessURL:     "walnut://checkout/success",
		CancelURL:      "walnut://checkout/cancel",
		IdempotencyKey: "checkout:usr_1:editorial_studio_monthly:1",
	})
	if err != nil {
		t.Fatalf("expected checkout session, got %v", err)
	}
	if result.Order.OrderType != domain.OrderTypeCheckout {
		t.Fatalf("expected checkout order type, got %s", result.Order.OrderType)
	}
	if result.Order.UserID != "usr_1" || result.Order.SKUCode != "editorial_studio_monthly" {
		t.Fatalf("expected order user/sku to be set, got %#v", result.Order)
	}
	if result.Order.Status != domain.OrderStatusCheckoutCreated {
		t.Fatalf("expected checkout_created, got %s", result.Order.Status)
	}
	if result.Order.Currency != "USD" {
		t.Fatalf("expected checkout order currency USD, got %s", result.Order.Currency)
	}
	if result.CheckoutURL == "" || result.Order.ProviderCheckoutID == "" {
		t.Fatalf("expected provider checkout fields, got %#v", result.Order)
	}
	if len(orders.orders) != 1 || len(gateway.requests) != 1 {
		t.Fatalf("expected one order and one provider call")
	}
	if gateway.requests[0].Amount != 1900 || gateway.requests[0].Currency != "USD" {
		t.Fatalf("expected normalized amount/currency, got %#v", gateway.requests[0])
	}
}

func TestCheckoutService_CreateCheckoutSessionIsIdempotent(t *testing.T) {
	svc, _, products, users, gateway := newCheckoutTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["credits_600"] = &domain.Product{Code: "credits_600", Name: "Credits 600", Price: 990, IsVisible: true}
	input := CheckoutInput{UserID: "usr_1", SKUCode: "credits_600", Provider: "mock", IdempotencyKey: "checkout:usr_1:credits_600:1"}

	first, err := svc.CreateCheckoutSession(context.Background(), input)
	if err != nil {
		t.Fatalf("expected first checkout, got %v", err)
	}
	second, err := svc.CreateCheckoutSession(context.Background(), input)
	if err != nil {
		t.Fatalf("expected idempotent checkout, got %v", err)
	}
	if first.Order.OutTradeNo != second.Order.OutTradeNo {
		t.Fatalf("expected same order for idempotent retry")
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("expected provider to be called once, got %d", len(gateway.requests))
	}
}

func TestCheckoutService_RetriesIncompleteIdempotentOrder(t *testing.T) {
	svc, _, products, users, gateway := newCheckoutTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["credits_600"] = &domain.Product{Code: "credits_600", Name: "Credits 600", Price: 990, IsVisible: true}
	input := CheckoutInput{UserID: "usr_1", SKUCode: "credits_600", Provider: "mock", IdempotencyKey: "checkout:retry"}
	gateway.err = errors.New("provider unavailable")

	_, err := svc.CreateCheckoutSession(context.Background(), input)
	if !errors.Is(err, ErrCheckoutProviderFailed) {
		t.Fatalf("expected first provider failure, got %v", err)
	}
	gateway.err = nil

	result, err := svc.CreateCheckoutSession(context.Background(), input)
	if err != nil {
		t.Fatalf("expected retry to complete existing order, got %v", err)
	}
	if result.Order.Status != domain.OrderStatusCheckoutCreated || result.Order.CheckoutURL == "" {
		t.Fatalf("expected retry to complete checkout order, got %#v", result.Order)
	}
	if len(gateway.requests) != 2 {
		t.Fatalf("expected provider retry, got %d calls", len(gateway.requests))
	}
}

func TestCheckoutService_RejectsIdempotencyKeyReuseAcrossSKU(t *testing.T) {
	svc, _, products, users, _ := newCheckoutTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["credits_600"] = &domain.Product{Code: "credits_600", Name: "Credits 600", Price: 990, IsVisible: true}
	products.products["credits_1200"] = &domain.Product{Code: "credits_1200", Name: "Credits 1200", Price: 1800, IsVisible: true}

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_1", SKUCode: "credits_600", Provider: "mock", IdempotencyKey: "checkout:reuse"})
	if err != nil {
		t.Fatalf("expected first checkout, got %v", err)
	}
	_, err = svc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_1", SKUCode: "credits_1200", Provider: "mock", IdempotencyKey: "checkout:reuse"})
	if !errors.Is(err, ErrInvalidCheckoutRequest) {
		t.Fatalf("expected invalid checkout request, got %v", err)
	}
}

func TestCheckoutService_ProviderFailureMarksOrderFailed(t *testing.T) {
	svc, orders, products, users, gateway := newCheckoutTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["credits_600"] = &domain.Product{Code: "credits_600", Name: "Credits 600", Price: 990, IsVisible: true}
	gateway.err = errors.New("provider unavailable")

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_1", SKUCode: "credits_600", Provider: "mock", IdempotencyKey: "checkout:provider-fail"})
	if !errors.Is(err, ErrCheckoutProviderFailed) {
		t.Fatalf("expected provider failure, got %v", err)
	}
	var stored *domain.Order
	for _, order := range orders.orders {
		stored = order
	}
	if stored == nil || stored.Status != domain.OrderStatusFailed {
		t.Fatalf("expected failed order to be stored, got %#v", stored)
	}
}

func TestCheckoutService_RejectsMissingUser(t *testing.T) {
	svc, _, products, _, _ := newCheckoutTestService()
	products.products["credits_600"] = &domain.Product{Code: "credits_600", Name: "Credits 600", Price: 990, IsVisible: true}
	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_missing", SKUCode: "credits_600", Provider: "mock", IdempotencyKey: "checkout:missing-user"})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected user not found, got %v", err)
	}
}

func TestCheckoutService_RejectsHiddenSKU(t *testing.T) {
	svc, _, products, users, _ := newCheckoutTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["hidden"] = &domain.Product{Code: "hidden", Name: "Hidden", Price: 990, IsVisible: false}
	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{UserID: "usr_1", SKUCode: "hidden", Provider: "mock", IdempotencyKey: "checkout:hidden"})
	if err == nil {
		t.Fatalf("expected hidden SKU error")
	}
}

func TestCheckoutService_AllowsCheckoutWhenRiskPolicyHasNoOpenRisk(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	policy := NewPaymentRiskCheckoutPolicy(risks, DefaultCheckoutRiskPolicyConfig())
	svc, _, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	seedCheckoutUserAndProduct(users, products)

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "mock",
		IdempotencyKey: "checkout:risk-allow",
	})
	if err != nil {
		t.Fatalf("expected checkout to be allowed, got %v", err)
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("expected provider call when risk policy allows checkout, got %d", len(gateway.requests))
	}
}

func TestCheckoutService_BlocksCheckoutForOpenCriticalRisk(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	risks.flags["risk_1"] = &domain.PaymentRiskFlag{
		ID:       "risk_1",
		UserID:   "usr_1",
		Severity: domain.PaymentRiskSeverityCritical,
		Status:   domain.PaymentRiskStatusOpen,
	}
	policy := NewPaymentRiskCheckoutPolicy(risks, DefaultCheckoutRiskPolicyConfig())
	svc, orders, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	seedCheckoutUserAndProduct(users, products)

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "mock",
		IdempotencyKey: "checkout:risk-block",
	})
	if !errors.Is(err, ErrCheckoutBlockedByRisk) {
		t.Fatalf("expected checkout risk block, got %v", err)
	}
	decision, ok := CheckoutPolicyDecisionFromError(err)
	if !ok || decision.Reason != CheckoutPolicyReasonOpenPaymentRisk || decision.Action != CheckoutPolicyActionManualReview {
		t.Fatalf("expected risk policy decision, got %#v ok=%v", decision, ok)
	}
	if len(orders.orders) != 0 {
		t.Fatalf("expected blocked checkout to avoid order creation, got %d orders", len(orders.orders))
	}
	if len(gateway.requests) != 0 {
		t.Fatalf("expected blocked checkout to avoid provider call, got %d calls", len(gateway.requests))
	}
}

func TestCheckoutService_UsesConfiguredRiskSeverities(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	risks.flags["risk_high"] = &domain.PaymentRiskFlag{
		ID:       "risk_high",
		UserID:   "usr_1",
		Severity: domain.PaymentRiskSeverityHigh,
		Status:   domain.PaymentRiskStatusOpen,
	}
	config := DefaultCheckoutRiskPolicyConfig()
	config.BlockSeverities = []string{domain.PaymentRiskSeverityCritical}
	policy := NewPaymentRiskCheckoutPolicy(risks, config)
	svc, _, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	seedCheckoutUserAndProduct(users, products)

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "mock",
		IdempotencyKey: "checkout:risk-config",
	})
	if err != nil {
		t.Fatalf("expected high risk to be allowed when only critical is configured, got %v", err)
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("expected provider call after configured allow, got %d", len(gateway.requests))
	}
}

func TestCheckoutService_AllowsCheckoutWhenCriticalRiskResolved(t *testing.T) {
	risks := newMockPaymentRiskFlagRepo()
	risks.flags["risk_1"] = &domain.PaymentRiskFlag{
		ID:       "risk_1",
		UserID:   "usr_1",
		Severity: domain.PaymentRiskSeverityCritical,
		Status:   domain.PaymentRiskStatusResolved,
	}
	policy := NewPaymentRiskCheckoutPolicy(risks, DefaultCheckoutRiskPolicyConfig())
	svc, _, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	seedCheckoutUserAndProduct(users, products)

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		Provider:       "mock",
		IdempotencyKey: "checkout:risk-resolved",
	})
	if err != nil {
		t.Fatalf("expected resolved risk to allow checkout, got %v", err)
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("expected provider call after resolved risk, got %d", len(gateway.requests))
	}
}

func TestSoftwareAccessPlanCheckoutPolicy_BlocksDuplicateMonthlySubscription(t *testing.T) {
	grants := newMockGrantRepo()
	periodEnd := time.Now().UTC().AddDate(0, 1, 0)
	grants.grants["monthly"] = &domain.EntitlementGrant{
		ID:            "monthly",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      time.Now().UTC().Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}
	policy := NewSoftwareAccessPlanCheckoutPolicy(grants, nil, func() time.Time { return time.Now().UTC() })
	svc, orders, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products[domain.SKUProOwnAIMonthly] = &domain.Product{
		Code:      domain.SKUProOwnAIMonthly,
		Name:      "Walnut Pro Own AI Monthly",
		Price:     500,
		Currency:  "USD",
		Validity:  "monthly",
		IsVisible: true,
	}

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Provider:       "mock",
		IdempotencyKey: "checkout:duplicate-monthly",
	})
	if !errors.Is(err, ErrCheckoutBlockedByPlan) {
		t.Fatalf("expected plan policy block, got %v", err)
	}
	decision, ok := CheckoutPolicyDecisionFromError(err)
	if !ok || decision.Reason != CheckoutPolicyReasonDuplicateActiveSubscription {
		t.Fatalf("expected duplicate subscription decision, got %#v ok=%v", decision, ok)
	}
	if len(orders.orders) != 0 || len(gateway.requests) != 0 {
		t.Fatalf("blocked checkout must not create order or provider session")
	}
}

func TestSoftwareAccessPlanCheckoutPolicy_BlocksLifetimeFromActiveSubscriptionBeforeCancellation(t *testing.T) {
	grants := newMockGrantRepo()
	periodEnd := time.Now().UTC().AddDate(0, 1, 0)
	grants.grants["monthly"] = &domain.EntitlementGrant{
		ID:            "monthly",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      time.Now().UTC().Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}
	policy := NewSoftwareAccessPlanCheckoutPolicy(grants, nil, func() time.Time { return time.Now().UTC() })
	svc, orders, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products[domain.SKUProOwnAILifetime] = &domain.Product{
		Code:      domain.SKUProOwnAILifetime,
		Name:      "Walnut Pro Own AI Lifetime",
		Price:     9900,
		Currency:  "USD",
		Validity:  "lifetime",
		IsVisible: true,
	}

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAILifetime,
		Provider:       "mock",
		IdempotencyKey: "checkout:lifetime-from-monthly",
	})
	if !errors.Is(err, ErrCheckoutBlockedByPlan) {
		t.Fatalf("expected plan policy block, got %v", err)
	}
	decision, ok := CheckoutPolicyDecisionFromError(err)
	if !ok || decision.Reason != CheckoutPolicyReasonActiveSubscriptionRequiresCancellation {
		t.Fatalf("expected active subscription decision, got %#v ok=%v", decision, ok)
	}
	if len(orders.orders) != 0 || len(gateway.requests) != 0 {
		t.Fatalf("blocked checkout must not create order or provider session")
	}
}

func TestSoftwareAccessPlanCheckoutPolicy_AllowsLifetimeAfterCancelAtPeriodEnd(t *testing.T) {
	grants := newMockGrantRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	periodEnd := time.Now().UTC().AddDate(0, 1, 0)
	grants.grants["monthly"] = &domain.EntitlementGrant{
		ID:            "monthly",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      time.Now().UTC().Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}
	cancellations.cancellations["cancel-1"] = &domain.SubscriptionCancellation{
		ID:                  "sub_cancel_1",
		UserID:              "usr_1",
		SKUCode:             domain.SKUProOwnAIMonthly,
		Status:              SubscriptionCancellationStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:   true,
		CurrentPeriodEndsAt: periodEnd,
		IdempotencyKey:      "cancel-1",
	}
	policy := NewSoftwareAccessPlanCheckoutPolicy(grants, cancellations, func() time.Time { return time.Now().UTC() })
	svc, _, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products[domain.SKUProOwnAILifetime] = &domain.Product{
		Code:      domain.SKUProOwnAILifetime,
		Name:      "Walnut Pro Own AI Lifetime",
		Price:     9900,
		Currency:  "USD",
		Validity:  "lifetime",
		IsVisible: true,
	}

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAILifetime,
		Provider:       "mock",
		IdempotencyKey: "checkout:lifetime-after-cancel",
	})
	if err != nil {
		t.Fatalf("expected lifetime checkout after cancellation, got %v", err)
	}
	if len(gateway.requests) != 1 {
		t.Fatalf("expected provider call for lifetime after cancellation, got %d", len(gateway.requests))
	}
}

func TestSoftwareAccessPlanCheckoutPolicy_BlocksCheckoutWhenLifetimeAlreadyActive(t *testing.T) {
	grants := newMockGrantRepo()
	grants.grants["lifetime"] = &domain.EntitlementGrant{
		ID:            "lifetime",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      time.Now().UTC().Add(-time.Hour),
	}
	policy := NewSoftwareAccessPlanCheckoutPolicy(grants, nil, nil)
	svc, orders, products, users, gateway := newCheckoutTestServiceWithPolicies(policy)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products[domain.SKUProOwnAILifetime] = &domain.Product{
		Code:      domain.SKUProOwnAILifetime,
		Name:      "Walnut Pro Own AI Lifetime",
		Price:     9900,
		Currency:  "USD",
		Validity:  "lifetime",
		IsVisible: true,
	}

	_, err := svc.CreateCheckoutSession(context.Background(), CheckoutInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAILifetime,
		Provider:       "mock",
		IdempotencyKey: "checkout:lifetime-again",
	})
	if !errors.Is(err, ErrCheckoutBlockedByPlan) {
		t.Fatalf("expected lifetime policy block, got %v", err)
	}
	decision, ok := CheckoutPolicyDecisionFromError(err)
	if !ok || decision.Reason != CheckoutPolicyReasonLifetimeAlreadyActive {
		t.Fatalf("expected lifetime-active decision, got %#v ok=%v", decision, ok)
	}
	if len(orders.orders) != 0 || len(gateway.requests) != 0 {
		t.Fatalf("blocked checkout must not create order or provider session")
	}
}

func seedCheckoutUserAndProduct(users *mockEntitlementUserRepo, products *mockProductRepo) {
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	products.products["editorial_studio_monthly"] = &domain.Product{
		Code:      "editorial_studio_monthly",
		Name:      "Editorial Studio Monthly",
		Price:     1900,
		Currency:  "USD",
		Validity:  "monthly",
		IsVisible: true,
	}
}

var _ repository.OrderRepository = (*mockTxOrderRepo)(nil)
