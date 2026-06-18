package service

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type mockSubscriptionCancellationRepo struct {
	cancellations map[string]*domain.SubscriptionCancellation
}

func newMockSubscriptionCancellationRepo() *mockSubscriptionCancellationRepo {
	return &mockSubscriptionCancellationRepo{cancellations: make(map[string]*domain.SubscriptionCancellation)}
}

func (m *mockSubscriptionCancellationRepo) Create(ctx context.Context, cancellation *domain.SubscriptionCancellation) error {
	m.cancellations[cancellation.IdempotencyKey] = cancellation
	return nil
}

func (m *mockSubscriptionCancellationRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error) {
	cancellation, ok := m.cancellations[key]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return cancellation, nil
}

func (m *mockSubscriptionCancellationRepo) GetByResumeIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error) {
	if key == "" {
		return nil, repository.ErrNotFound
	}
	for _, cancellation := range m.cancellations {
		if cancellation.ResumeIdempotencyKey == key {
			return cancellation, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockSubscriptionCancellationRepo) FindActive(ctx context.Context, query repository.SubscriptionCancellationQuery) (*domain.SubscriptionCancellation, error) {
	for _, cancellation := range m.cancellations {
		if query.UserID != "" && cancellation.UserID != query.UserID {
			continue
		}
		if query.SKUCode != "" && cancellation.SKUCode != query.SKUCode {
			continue
		}
		if query.Status != "" && cancellation.Status != query.Status {
			continue
		}
		return cancellation, nil
	}
	return nil, repository.ErrNotFound
}

func (m *mockSubscriptionCancellationRepo) Update(ctx context.Context, cancellation *domain.SubscriptionCancellation) error {
	m.cancellations[cancellation.IdempotencyKey] = cancellation
	return nil
}

func TestSubscriptionCancellationService_CancelsAtPeriodEnd(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = &domain.Order{
		ID:         10,
		OutTradeNo: "CHK-1",
		UserID:     "usr_1",
		SKUCode:    domain.SKUProOwnAIMonthly,
		Status:     domain.OrderStatusFulfilled,
		OrderType:  domain.OrderTypeCheckout,
	}
	for _, entitlementID := range CurrentAdvancedEntitlements() {
		grants.grants[entitlementID] = &domain.EntitlementGrant{
			ID:            "grt_" + entitlementID,
			UserID:        "usr_1",
			EntitlementID: entitlementID,
			Status:        domain.GrantStatusActive,
			Source:        domain.GrantSourceFulfillment,
			StartsAt:      now.Add(-time.Hour),
			ExpiresAt:     &periodEnd,
		}
	}
	svc := NewSubscriptionCancellationService(SubscriptionCancellationDependencies{
		Repositories: SubscriptionCancellationRepositories{
			Orders:            orders,
			Users:             users,
			EntitlementGrants: grants,
			PaymentEvents:     newMockPaymentEventRepo(),
			Cancellations:     cancellations,
		},
		Now: func() time.Time { return now },
	})

	result, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Reason:         "user_requested",
		Source:         "settings_software_access",
		IdempotencyKey: "cancel-1",
	})
	if err != nil {
		t.Fatalf("cancel subscription: %v", err)
	}
	if result.Status != SoftwareSubscriptionStatusCancelAtPeriodEnd || !result.CancelAtPeriodEnd || result.CurrentPeriodEndsAt == "" {
		t.Fatalf("unexpected cancellation result: %#v", result)
	}
	if result.Projection.Status != SoftwareSubscriptionStatusCancelAtPeriodEnd || !result.Projection.CancelAtPeriodEnd {
		t.Fatalf("expected cancel-at-period-end projection, got %#v", result.Projection)
	}
	if orders.orders["CHK-1"].Status != domain.OrderStatusFulfilled {
		t.Fatalf("cancellation must not rewrite paid order facts, got %s", orders.orders["CHK-1"].Status)
	}
	if len(cancellations.cancellations) != 1 {
		t.Fatalf("expected one cancellation fact, got %d", len(cancellations.cancellations))
	}
	if grants.grants[domain.EntitlementEditorialStudio].Status != domain.GrantStatusActive {
		t.Fatalf("cancellation must keep current entitlement active")
	}
}

func TestSubscriptionCancellationService_ResumesCancelledSubscription(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = &domain.Order{
		ID:         10,
		OutTradeNo: "CHK-1",
		UserID:     "usr_1",
		SKUCode:    domain.SKUProOwnAIMonthly,
		Status:     domain.OrderStatusFulfilled,
		OrderType:  domain.OrderTypeCheckout,
	}
	for _, entitlementID := range CurrentAdvancedEntitlements() {
		grants.grants[entitlementID] = &domain.EntitlementGrant{
			ID:            "grt_" + entitlementID,
			UserID:        "usr_1",
			EntitlementID: entitlementID,
			Status:        domain.GrantStatusActive,
			Source:        domain.GrantSourceFulfillment,
			StartsAt:      now.Add(-time.Hour),
			ExpiresAt:     &periodEnd,
		}
	}
	svc := NewSubscriptionCancellationService(SubscriptionCancellationDependencies{
		Repositories: SubscriptionCancellationRepositories{
			Orders:            orders,
			Users:             users,
			EntitlementGrants: grants,
			Cancellations:     cancellations,
		},
		Now: func() time.Time { return now },
	})

	cancelled, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Reason:         "user_requested",
		Source:         "settings_software_access",
		IdempotencyKey: "cancel-1",
	})
	if err != nil {
		t.Fatalf("cancel subscription: %v", err)
	}
	resumed, err := svc.Resume(context.Background(), SubscriptionResumeInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Source:         "settings_software_access",
		IdempotencyKey: "resume-1",
	})
	if err != nil {
		t.Fatalf("resume subscription: %v", err)
	}
	if cancelled.Status != SoftwareSubscriptionStatusCancelAtPeriodEnd || !cancelled.CancelAtPeriodEnd {
		t.Fatalf("expected cancelled-at-period-end before resume, got %#v", cancelled)
	}
	if resumed.Status != SubscriptionStatusActive || resumed.CancelAtPeriodEnd || resumed.CurrentPeriodEndsAt == "" {
		t.Fatalf("unexpected resumed subscription: %#v", resumed)
	}
	if resumed.Projection.Status != SoftwareSubscriptionStatusActive || resumed.Projection.CancelAtPeriodEnd {
		t.Fatalf("expected active projection after resume, got %#v", resumed.Projection)
	}
	stored := cancellations.cancellations["cancel-1"]
	if stored.Status != SubscriptionStatusActive || stored.CancelAtPeriodEnd || stored.ResumeIdempotencyKey != "resume-1" || stored.ResumedAt == nil {
		t.Fatalf("expected cancellation fact to be neutralized, got %#v", stored)
	}
}

func TestSubscriptionCancellationService_RecordsProviderSubscriptionControlMetadata(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	events := newMockPaymentEventRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = &domain.Order{
		ID:         10,
		OutTradeNo: "CHK-1",
		UserID:     "usr_1",
		SKUCode:    domain.SKUProOwnAIMonthly,
		Status:     domain.OrderStatusFulfilled,
		OrderType:  domain.OrderTypeCheckout,
	}
	events.events["evt_1"] = &domain.PaymentEventInbox{
		ID:          "evt_1",
		OutTradeNo:  "CHK-1",
		RawPayload:  `{"object":{"subscription":{"id":"sub_provider_1"}}}`,
		ReceivedAt:  now,
		ProcessedAt: &now,
	}
	grants.grants[domain.EntitlementEditorialStudio] = &domain.EntitlementGrant{
		ID:            "grt_editorial",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now.Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}
	svc := NewSubscriptionCancellationService(SubscriptionCancellationDependencies{
		Repositories: SubscriptionCancellationRepositories{
			Orders:            orders,
			Users:             users,
			EntitlementGrants: grants,
			PaymentEvents:     events,
			Cancellations:     cancellations,
		},
		Now: func() time.Time { return now },
	})

	if _, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Reason:         "user_requested",
		Source:         "settings_software_access",
		IdempotencyKey: "cancel-1",
	}); err != nil {
		t.Fatalf("cancel subscription: %v", err)
	}
	cancelMetadata := orderMetadataMap(orders.orders["CHK-1"].Metadata)
	if cancelMetadata["walnut_subscription_status"] != SoftwareSubscriptionStatusCancelAtPeriodEnd ||
		cancelMetadata["walnut_cancel_at_period_end"] != "true" ||
		cancelMetadata["walnut_provider_subscription_id"] != "sub_provider_1" {
		t.Fatalf("expected cancel control metadata, got %#v", cancelMetadata)
	}

	if _, err := svc.Resume(context.Background(), SubscriptionResumeInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Source:         "settings_software_access",
		IdempotencyKey: "resume-1",
	}); err != nil {
		t.Fatalf("resume subscription: %v", err)
	}
	resumeMetadata := orderMetadataMap(orders.orders["CHK-1"].Metadata)
	if resumeMetadata["walnut_subscription_status"] != SubscriptionStatusActive ||
		resumeMetadata["walnut_cancel_at_period_end"] != "false" ||
		resumeMetadata["walnut_provider_subscription_id"] != "sub_provider_1" {
		t.Fatalf("expected resume control metadata, got %#v", resumeMetadata)
	}
}

func TestSubscriptionCancellationService_RejectsLifetime(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	svc := NewSubscriptionCancellationService(SubscriptionCancellationDependencies{
		Repositories: SubscriptionCancellationRepositories{Orders: orders, Users: users, EntitlementGrants: grants, Cancellations: cancellations},
	})

	_, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{UserID: "usr_1", SKUCode: domain.SKUProOwnAILifetime})
	if err != ErrInvalidSubscriptionCancellation {
		t.Fatalf("expected invalid cancellation for lifetime, got %v", err)
	}
}
