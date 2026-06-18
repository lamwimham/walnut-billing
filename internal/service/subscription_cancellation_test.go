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

type mockSubscriptionControlGateway struct {
	cancelCalls  int
	resumeCalls  int
	cancelReq    payment.SubscriptionControlRequest
	resumeReq    payment.SubscriptionControlRequest
	provider     string
	cancelErr    error
	resumeErr    error
	cancelResult *payment.SubscriptionControlResult
	resumeResult *payment.SubscriptionControlResult
}

func (m *mockSubscriptionControlGateway) CancelSubscription(ctx context.Context, providerName string, req payment.SubscriptionControlRequest) (*payment.SubscriptionControlResult, error) {
	m.cancelCalls++
	m.provider = providerName
	m.cancelReq = req
	if m.cancelErr != nil {
		return nil, m.cancelErr
	}
	if m.cancelResult != nil {
		return m.cancelResult, nil
	}
	return &payment.SubscriptionControlResult{
		ProviderSubscriptionID: req.ProviderSubscriptionID,
		Status:                 SoftwareSubscriptionStatusCancelAtPeriodEnd,
		RawStatus:              "scheduled_cancel",
		CancelAtPeriodEnd:      true,
	}, nil
}

func (m *mockSubscriptionControlGateway) ResumeSubscription(ctx context.Context, providerName string, req payment.SubscriptionControlRequest) (*payment.SubscriptionControlResult, error) {
	m.resumeCalls++
	m.provider = providerName
	m.resumeReq = req
	if m.resumeErr != nil {
		return nil, m.resumeErr
	}
	if m.resumeResult != nil {
		return m.resumeResult, nil
	}
	return &payment.SubscriptionControlResult{
		ProviderSubscriptionID: req.ProviderSubscriptionID,
		Status:                 SubscriptionStatusActive,
		RawStatus:              "active",
		CancelAtPeriodEnd:      false,
	}, nil
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

func TestProviderSubscriptionIDFromRawPayloadSupportsFormEncodedMockPayload(t *testing.T) {
	raw := "currency=USD&event_type=payment.paid&out_trade_no=CHK-1&provider_event_id=evt_paid_CHK_1&subscription_id=sub_mock_CHK_1"
	if got := providerSubscriptionIDFromRawPayload(raw); got != "sub_mock_CHK_1" {
		t.Fatalf("expected form encoded subscription id, got %q", got)
	}
}

func TestSubscriptionCancellationService_CallsProviderControlBeforeWritingCancellation(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	events := newMockPaymentEventRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	gateway := &mockSubscriptionControlGateway{}
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
		Provider:   "creem",
	}
	events.events["evt_1"] = &domain.PaymentEventInbox{
		ID:          "evt_1",
		Provider:    "creem",
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
		ProviderControl: gateway,
		Now:             func() time.Time { return now },
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
	if gateway.cancelCalls != 1 || gateway.provider != "creem" {
		t.Fatalf("expected one creem provider cancel call, gateway=%#v", gateway)
	}
	if gateway.cancelReq.ProviderSubscriptionID != "sub_provider_1" || !gateway.cancelReq.CancelAtPeriodEnd || gateway.cancelReq.Metadata["walnut_action"] != "subscription_cancel" {
		t.Fatalf("unexpected provider cancel request: %#v", gateway.cancelReq)
	}
	metadata := orderMetadataMap(orders.orders["CHK-1"].Metadata)
	if metadata["walnut_provider_subscription_status"] != SoftwareSubscriptionStatusCancelAtPeriodEnd ||
		metadata["walnut_provider_subscription_raw_status"] != "scheduled_cancel" {
		t.Fatalf("expected provider control metadata, got %#v", metadata)
	}
	if len(cancellations.cancellations) != 1 {
		t.Fatalf("expected Walnut cancellation fact after provider success, got %d", len(cancellations.cancellations))
	}
}

func TestSubscriptionCancellationService_DoesNotWriteCancellationWhenProviderFails(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	events := newMockPaymentEventRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	gateway := &mockSubscriptionControlGateway{cancelErr: errors.New("provider down")}
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
		Provider:   "creem",
	}
	events.events["evt_1"] = &domain.PaymentEventInbox{ID: "evt_1", OutTradeNo: "CHK-1", RawPayload: `{"object":{"subscription":{"id":"sub_provider_1"}}}`, ReceivedAt: now}
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
		ProviderControl: gateway,
		Now:             func() time.Time { return now },
	})

	_, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		IdempotencyKey: "cancel-1",
	})
	if !errors.Is(err, ErrSubscriptionControlFailed) {
		t.Fatalf("expected provider failure, got %v", err)
	}
	if len(cancellations.cancellations) != 0 {
		t.Fatalf("provider failure must not write Walnut cancellation facts: %#v", cancellations.cancellations)
	}
	if metadata := orderMetadataMap(orders.orders["CHK-1"].Metadata); len(metadata) != 0 {
		t.Fatalf("provider failure must not mark order metadata, got %#v", metadata)
	}
}

func TestSubscriptionCancellationService_MapsUnsupportedProviderControl(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	events := newMockPaymentEventRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	gateway := &mockSubscriptionControlGateway{cancelErr: payment.ErrSubscriptionControlUnsupported}
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = &domain.Order{ID: 10, OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, OrderType: domain.OrderTypeCheckout, Provider: "legacy"}
	events.events["evt_1"] = &domain.PaymentEventInbox{ID: "evt_1", OutTradeNo: "CHK-1", RawPayload: `{"object":{"subscription":{"id":"sub_provider_1"}}}`, ReceivedAt: now}
	grants.grants[domain.EntitlementEditorialStudio] = &domain.EntitlementGrant{ID: "grt_editorial", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd}
	svc := NewSubscriptionCancellationService(SubscriptionCancellationDependencies{
		Repositories: SubscriptionCancellationRepositories{
			Orders:            orders,
			Users:             users,
			EntitlementGrants: grants,
			PaymentEvents:     events,
			Cancellations:     cancellations,
		},
		ProviderControl: gateway,
		Now:             func() time.Time { return now },
	})

	_, err := svc.Cancel(context.Background(), SubscriptionCancellationInput{UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, IdempotencyKey: "cancel-1"})
	if !errors.Is(err, ErrSubscriptionControlUnavailable) {
		t.Fatalf("expected unavailable provider control, got %v", err)
	}
}

func TestSubscriptionCancellationService_ResumesProviderSubscription(t *testing.T) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	grants := newMockGrantRepo()
	events := newMockPaymentEventRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	gateway := &mockSubscriptionControlGateway{}
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = &domain.Order{ID: 10, OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, OrderType: domain.OrderTypeCheckout, Provider: "creem"}
	events.events["evt_1"] = &domain.PaymentEventInbox{ID: "evt_1", Provider: "creem", OutTradeNo: "CHK-1", RawPayload: `{"object":{"subscription":{"id":"sub_provider_1"}}}`, ReceivedAt: now}
	grants.grants[domain.EntitlementEditorialStudio] = &domain.EntitlementGrant{ID: "grt_editorial", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd}
	cancellations.cancellations["cancel-1"] = &domain.SubscriptionCancellation{
		ID:                  "sub_cancel_1",
		UserID:              "usr_1",
		SKUCode:             domain.SKUProOwnAIMonthly,
		Status:              SubscriptionCancellationStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:   true,
		CurrentPeriodEndsAt: periodEnd,
		SourceOrderNo:       "CHK-1",
		IdempotencyKey:      "cancel-1",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	svc := NewSubscriptionCancellationService(SubscriptionCancellationDependencies{
		Repositories: SubscriptionCancellationRepositories{
			Orders:            orders,
			Users:             users,
			EntitlementGrants: grants,
			PaymentEvents:     events,
			Cancellations:     cancellations,
		},
		ProviderControl: gateway,
		Now:             func() time.Time { return now },
	})

	if _, err := svc.Resume(context.Background(), SubscriptionResumeInput{
		UserID:         "usr_1",
		SKUCode:        domain.SKUProOwnAIMonthly,
		Source:         "settings_software_access",
		IdempotencyKey: "resume-1",
	}); err != nil {
		t.Fatalf("resume subscription: %v", err)
	}
	if gateway.resumeCalls != 1 || gateway.provider != "creem" || gateway.resumeReq.ProviderSubscriptionID != "sub_provider_1" {
		t.Fatalf("expected one provider resume call, gateway=%#v", gateway)
	}
	metadata := orderMetadataMap(orders.orders["CHK-1"].Metadata)
	if metadata["walnut_provider_subscription_status"] != SubscriptionStatusActive ||
		metadata["walnut_provider_subscription_raw_status"] != "active" {
		t.Fatalf("expected provider resume metadata, got %#v", metadata)
	}
	if cancellations.cancellations["cancel-1"].Status != SubscriptionStatusActive {
		t.Fatalf("expected Walnut cancellation fact neutralized, got %#v", cancellations.cancellations["cancel-1"])
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
