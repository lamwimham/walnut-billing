package service

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
)

func newSubscriptionRenewalTestService(policy SubscriptionRenewalPolicy) (SubscriptionRenewalService, FulfillmentService, *mockTxOrderRepo, *mockEntitlementUserRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockCreditTransactionRepo, *mockFulfillmentExecutionRepo) {
	fulfillmentSvc, orders, users, grants, accounts, transactions, executions := newFulfillmentTestService(editorialStudioFulfillmentRules()...)
	renewalSvc := NewSubscriptionRenewalService(SubscriptionRenewalDependencies{
		Repositories: SubscriptionRenewalRepositories{
			Orders:            orders,
			Users:             users,
			EntitlementGrants: grants,
		},
		Fulfillment:        fulfillmentSvc,
		Policy:             policy,
		EntitlementCatalog: DefaultEntitlementCatalog(),
	})
	return renewalSvc, fulfillmentSvc, orders, users, grants, accounts, transactions, executions
}

func renewalOrder(status string) *domain.Order {
	return &domain.Order{
		ID:         44,
		OutTradeNo: "RNL-1",
		UserID:     "usr_1",
		SKUCode:    "editorial_studio_monthly",
		Amount:     1900,
		Currency:   "USD",
		Status:     status,
		OrderType:  domain.OrderTypeRenewal,
	}
}

func TestSubscriptionRenewalPolicy_DefaultDecisions(t *testing.T) {
	policy := NewConfigurableSubscriptionRenewalPolicy(DefaultSubscriptionRenewalPolicyConfig())
	tests := []struct {
		name   string
		event  string
		action string
	}{
		{"paid", domain.PaymentEventTypeRenewalPaid, SubscriptionRenewalActionFulfillRenewal},
		{"failed", domain.PaymentEventTypeRenewalFailed, SubscriptionRenewalActionGrantGrace},
		{"expired", domain.PaymentEventTypeSubscriptionExpired, SubscriptionRenewalActionExpireGrace},
		{"ignored", domain.PaymentEventTypeRefunded, SubscriptionRenewalActionIgnore},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Decide(context.Background(), SubscriptionRenewalPolicyInput{Event: &domain.PaymentEventInbox{EventType: tt.event}})
			if decision.Action != tt.action {
				t.Fatalf("expected %s, got %#v", tt.action, decision)
			}
		})
	}
}

func TestSubscriptionRenewalPolicy_ConfiguresGraceAndExpiredAction(t *testing.T) {
	policy := NewConfigurableSubscriptionRenewalPolicy(SubscriptionRenewalPolicyConfig{
		GracePeriodDays: 5,
		ExpiredAction:   SubscriptionRenewalActionNaturalExpiry,
	})
	failed := policy.Decide(context.Background(), SubscriptionRenewalPolicyInput{Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRenewalFailed}})
	if failed.Action != SubscriptionRenewalActionGrantGrace || failed.GracePeriodDays != 5 {
		t.Fatalf("unexpected failed decision: %#v", failed)
	}
	expired := policy.Decide(context.Background(), SubscriptionRenewalPolicyInput{Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeSubscriptionExpired}})
	if expired.Action != SubscriptionRenewalActionNaturalExpiry {
		t.Fatalf("unexpected expired decision: %#v", expired)
	}
}

func TestSubscriptionRenewalService_RenewalFailedCreatesGraceGrantWithoutCredits(t *testing.T) {
	renewalSvc, _, orders, users, grants, accounts, transactions, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	now := time.Now().UTC()
	paidExpiry := now.Add(-2 * time.Hour)
	grants.grants["paid"] = &domain.EntitlementGrant{
		ID:            "paid",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      paidExpiry.AddDate(0, -1, 0),
		ExpiresAt:     &paidExpiry,
	}
	orders.orders["RNL-1"] = renewalOrder(domain.OrderStatusFailed)

	result, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_past_due_1",
		EventType:       domain.PaymentEventTypeRenewalFailed,
		OutTradeNo:      "RNL-1",
		ReceivedAt:      paidExpiry,
	})
	if err != nil {
		t.Fatalf("expected grace grant, got %v", err)
	}
	if result.GraceGrant == nil || result.GraceGrant.Source != domain.GrantSourceSubscriptionGrace || result.GraceGrant.ExpiresAt == nil {
		t.Fatalf("expected subscription grace grant, got %#v", result)
	}
	expectedGraceEnd := paidExpiry.AddDate(0, 0, domain.GracePeriodDays)
	if !result.GraceGrant.ExpiresAt.Equal(expectedGraceEnd) {
		t.Fatalf("expected grace end %s, got %s", expectedGraceEnd, result.GraceGrant.ExpiresAt)
	}
	if len(transactions.transactions) != 0 {
		t.Fatalf("renewal failure must not grant credits, got %d transactions", len(transactions.transactions))
	}
	entitlementSvc := NewEntitlementServiceWithCredits(users, newMockRegistrationRepo(), grants, accounts, DefaultEntitlementCatalog())
	snapshot, err := entitlementSvc.SnapshotForUser(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] {
		t.Fatalf("expected grace entitlement to keep access, got %#v", snapshot)
	}
}

func TestSubscriptionRenewalService_DerivesRenewalOrderFromCheckoutMetadata(t *testing.T) {
	renewalSvc, _, orders, users, grants, _, transactions, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	paidAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	checkout := paidCheckoutOrder()
	checkout.Status = domain.OrderStatusFulfilled
	checkout.PaidAt = &paidAt
	orders.orders["CHK-1"] = checkout

	result, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_past_due_checkout",
		EventType:       domain.PaymentEventTypeRenewalFailed,
		OutTradeNo:      "CHK-1",
		ReceivedAt:      paidAt.AddDate(0, 1, 0),
		PeriodStartAt:   ptrTime(paidAt.AddDate(0, 1, 0)),
	})
	if err != nil {
		t.Fatalf("expected derived renewal order grace, got %v", err)
	}
	if result.Order == nil || result.Order.OutTradeNo == "CHK-1" || result.Order.OrderType != domain.OrderTypeRenewal {
		t.Fatalf("expected derived renewal order, got %#v", result.Order)
	}
	derived, err := orders.GetByIdempotencyKey(context.Background(), "subscription_renewal:CHK-1:20260712100000")
	if err != nil {
		t.Fatalf("expected derived renewal order by idempotency key: %v", err)
	}
	if derived.Status != domain.OrderStatusFailed || derived.UserID != "usr_1" || derived.SKUCode != "editorial_studio_monthly" {
		t.Fatalf("unexpected derived order: %#v", derived)
	}
	if result.GraceGrant == nil || len(transactions.transactions) != 0 {
		t.Fatalf("expected grace without credits, result=%#v tx=%d", result, len(transactions.transactions))
	}
	if len(grants.grants) != 1 {
		t.Fatalf("expected one grace grant, got %d", len(grants.grants))
	}
}

func TestSubscriptionRenewalService_RenewalFailedIsIdempotent(t *testing.T) {
	renewalSvc, _, orders, users, grants, _, transactions, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["RNL-1"] = renewalOrder(domain.OrderStatusFailed)
	event := &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_past_due_1",
		EventType:       domain.PaymentEventTypeRenewalFailed,
		OutTradeNo:      "RNL-1",
		ReceivedAt:      time.Now().UTC(),
	}

	first, err := renewalSvc.Apply(context.Background(), event)
	if err != nil {
		t.Fatalf("first failed renewal: %v", err)
	}
	second, err := renewalSvc.Apply(context.Background(), event)
	if err != nil {
		t.Fatalf("second failed renewal: %v", err)
	}
	if first.GraceGrant == nil || second.GraceGrant == nil || first.GraceGrant.ID != second.GraceGrant.ID {
		t.Fatalf("expected same idempotent grace grant, first=%#v second=%#v", first.GraceGrant, second.GraceGrant)
	}
	graceCount := 0
	for _, grant := range grants.grants {
		if grant.Source == domain.GrantSourceSubscriptionGrace {
			graceCount++
		}
	}
	if graceCount != 1 || len(transactions.transactions) != 0 {
		t.Fatalf("expected one grace grant and no credits, grace=%d tx=%d", graceCount, len(transactions.transactions))
	}
}

func TestSubscriptionRenewalService_InitialSubscriptionPaidUsesCheckoutFulfillmentIdempotency(t *testing.T) {
	renewalSvc, _, orders, users, grants, _, transactions, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	paidAt := time.Now().UTC().Add(-time.Minute)
	checkout := paidCheckoutOrder()
	checkout.Status = domain.OrderStatusPaid
	checkout.PaidAt = &paidAt
	orders.orders["CHK-1"] = checkout

	result, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_initial_subscription_paid",
		EventType:       domain.PaymentEventTypeRenewalPaid,
		OutTradeNo:      "CHK-1",
		ReceivedAt:      paidAt,
		PeriodStartAt:   ptrTime(paidAt.Add(30 * time.Second)),
	})
	if err != nil {
		t.Fatalf("expected initial subscription.paid to fulfill checkout, got %v", err)
	}
	if result.PolicyDecision.Action != SubscriptionRenewalActionFulfillCheckout || orders.orders["CHK-1"].Status != domain.OrderStatusFulfilled {
		t.Fatalf("expected checkout fulfillment decision, result=%#v order=%#v", result, orders.orders["CHK-1"])
	}
	if len(grants.grants) != 1 || len(transactions.transactions) != 1 || len(orders.orders) != 1 {
		t.Fatalf("expected checkout fulfillment without derived renewal order, grants=%d tx=%d orders=%d", len(grants.grants), len(transactions.transactions), len(orders.orders))
	}

	replay, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_initial_subscription_paid_replay",
		EventType:       domain.PaymentEventTypeRenewalPaid,
		OutTradeNo:      "CHK-1",
		ReceivedAt:      paidAt,
		PeriodStartAt:   ptrTime(paidAt.Add(30 * time.Second)),
	})
	if err != nil {
		t.Fatalf("expected replay to stay idempotent, got %v", err)
	}
	if replay.Fulfillment == nil || !replay.Fulfillment.AlreadyFulfilled || len(grants.grants) != 1 || len(transactions.transactions) != 1 {
		t.Fatalf("expected already fulfilled replay with no duplicate side effects, replay=%#v grants=%d tx=%d", replay, len(grants.grants), len(transactions.transactions))
	}
}

func TestSubscriptionRenewalService_SubscriptionExpiredExpiresGraceGrant(t *testing.T) {
	renewalSvc, _, orders, users, grants, accounts, _, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	now := time.Now().UTC()
	paidExpiry := now.AddDate(0, 0, -domain.GracePeriodDays).Add(-2 * time.Hour)
	grants.grants["paid"] = &domain.EntitlementGrant{
		ID:            "paid",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      paidExpiry.AddDate(0, -1, 0),
		ExpiresAt:     &paidExpiry,
	}
	orders.orders["RNL-1"] = renewalOrder(domain.OrderStatusFailed)
	if _, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_past_due_1",
		EventType:       domain.PaymentEventTypeRenewalFailed,
		OutTradeNo:      "RNL-1",
		ReceivedAt:      paidExpiry,
	}); err != nil {
		t.Fatalf("failed renewal: %v", err)
	}

	expired, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_expired_1",
		EventType:       domain.PaymentEventTypeSubscriptionExpired,
		OutTradeNo:      "RNL-1",
		ReceivedAt:      now,
	})
	if err != nil {
		t.Fatalf("subscription expired: %v", err)
	}
	if len(expired.ExpiredGrantIDs) != 1 {
		t.Fatalf("expected one expired grace grant, got %#v", expired)
	}
	entitlementSvc := NewEntitlementServiceWithCredits(users, newMockRegistrationRepo(), grants, accounts, DefaultEntitlementCatalog())
	snapshot, err := entitlementSvc.SnapshotForUser(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Entitlements[domain.EntitlementEditorialStudio] {
		t.Fatalf("expected no access after grace expiry, got %#v", snapshot)
	}
}

func TestSubscriptionRenewalService_SubscriptionExpiredBeforeGraceEndPreservesGrant(t *testing.T) {
	renewalSvc, _, orders, users, grants, accounts, _, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	now := time.Now().UTC()
	paidExpiry := now.Add(-2 * time.Hour)
	orders.orders["RNL-1"] = renewalOrder(domain.OrderStatusFailed)
	if _, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_past_due_1",
		EventType:       domain.PaymentEventTypeRenewalFailed,
		OutTradeNo:      "RNL-1",
		ReceivedAt:      paidExpiry,
	}); err != nil {
		t.Fatalf("failed renewal: %v", err)
	}

	expired, err := renewalSvc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_expired_early_1",
		EventType:       domain.PaymentEventTypeSubscriptionExpired,
		OutTradeNo:      "RNL-1",
		ReceivedAt:      now,
	})
	if err != nil {
		t.Fatalf("subscription expired: %v", err)
	}
	if len(expired.ExpiredGrantIDs) != 0 {
		t.Fatalf("expected early subscription.expired to preserve grace, got %#v", expired)
	}
	entitlementSvc := NewEntitlementServiceWithCredits(users, newMockRegistrationRepo(), grants, accounts, DefaultEntitlementCatalog())
	snapshot, err := entitlementSvc.SnapshotForUser(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snapshot.Entitlements[domain.EntitlementEditorialStudio] {
		t.Fatalf("expected grace access until grace expires_at, got %#v", snapshot)
	}
}

func TestPaymentFulfillmentEventProcessor_RoutesRenewalPaidThroughSubscriptionService(t *testing.T) {
	renewalSvc, fulfillmentSvc, orders, users, grants, _, transactions, _ := newSubscriptionRenewalTestService(nil)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	paidAt := time.Now().UTC().AddDate(0, -1, 0)
	existingExpiry := paidAt.AddDate(0, 1, 0)
	grants.grants["existing"] = &domain.EntitlementGrant{
		ID:            "existing",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      paidAt,
		ExpiresAt:     &existingExpiry,
	}
	orders.orders["RNL-1"] = renewalOrder(domain.OrderStatusFailed)
	processor := NewPaymentFulfillmentEventProcessorWithPolicies(orders, NewPaymentOrderEventProcessor(orders), fulfillmentSvc, nil, renewalSvc)

	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_renewal_paid_1",
		EventType:       domain.PaymentEventTypeRenewalPaid,
		OutTradeNo:      "RNL-1",
		ProviderTradeNo: "txn_renewal_1",
		Amount:          1900,
		Currency:        "USD",
		ReceivedAt:      existingExpiry,
	})
	if err != nil {
		t.Fatalf("expected renewal paid processing, got %v", err)
	}
	if orders.orders["RNL-1"].Status != domain.OrderStatusFulfilled {
		t.Fatalf("expected fulfilled renewal order, got %s", orders.orders["RNL-1"].Status)
	}
	if len(transactions.transactions) != 1 {
		t.Fatalf("expected renewal credits, got %d", len(transactions.transactions))
	}
}

func TestPaymentOrderEventProcessor_MarksRenewalFailureAndSubscriptionExpired(t *testing.T) {
	orders := newMockTxOrderRepo()
	orders.orders["RNL-1"] = renewalOrder(domain.OrderStatusPending)
	processor := NewPaymentOrderEventProcessor(orders)
	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRenewalFailed,
		OutTradeNo: "RNL-1",
	})
	if err != nil {
		t.Fatalf("expected renewal failed order update, got %v", err)
	}
	if orders.orders["RNL-1"].Status != domain.OrderStatusFailed {
		t.Fatalf("expected failed order, got %s", orders.orders["RNL-1"].Status)
	}

	orders.orders["RNL-2"] = renewalOrder(domain.OrderStatusCheckoutCreated)
	orders.orders["RNL-2"].OutTradeNo = "RNL-2"
	err = processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeSubscriptionExpired,
		OutTradeNo: "RNL-2",
	})
	if err != nil {
		t.Fatalf("expected subscription expired order update, got %v", err)
	}
	if orders.orders["RNL-2"].Status != domain.OrderStatusFailed {
		t.Fatalf("expected failed order after subscription expired, got %s", orders.orders["RNL-2"].Status)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
