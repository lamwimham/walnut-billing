package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAdminSubscriptionReadRepo struct {
	query  repository.AdminSubscriptionQuery
	result *repository.AdminSubscriptionReadModel
	err    error
}

func (f *fakeAdminSubscriptionReadRepo) List(ctx context.Context, query repository.AdminSubscriptionQuery) (*repository.AdminSubscriptionReadModel, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAdminSubscriptionServiceProjectsPrivacySafeSubscriptions(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	periodStart := now.AddDate(0, -1, 0)
	periodEnd := now.AddDate(0, 1, 0)
	paidAt := now.Add(-2 * time.Hour)
	fulfilledAt := now.Add(-90 * time.Minute)
	processedAt := now.Add(-time.Hour)
	idempotencyKey := "checkout:usr_1:secret"
	repo := &fakeAdminSubscriptionReadRepo{result: &repository.AdminSubscriptionReadModel{
		Records: []repository.AdminSubscriptionRecord{{
			User: domain.User{
				ID:        "usr_1",
				Email:     "Writer@Example.COM",
				Status:    domain.UserStatusActive,
				CreatedAt: now.AddDate(0, -2, 0),
				UpdatedAt: now,
			},
			SKUCode: domain.SKUProOwnAIMonthly,
			Grants: []domain.EntitlementGrant{
				{ID: "grt_studio", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: periodStart, ExpiresAt: &periodEnd, CreatedAt: periodStart},
				{ID: "grt_cloud", UserID: "usr_1", EntitlementID: domain.EntitlementCloudStorage, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: periodStart, ExpiresAt: &periodEnd, CreatedAt: periodStart},
			},
			LatestOrder: &domain.Order{
				OutTradeNo:         "CHK-1",
				UserID:             "usr_1",
				SKUCode:            domain.SKUProOwnAIMonthly,
				Status:             domain.OrderStatusFulfilled,
				Provider:           "creem",
				OrderType:          domain.OrderTypeCheckout,
				ProviderCheckoutID: "ch_secret",
				ProviderCustomerID: "cus_secret",
				CheckoutURL:        "https://checkout.example/secret",
				IdempotencyKey:     &idempotencyKey,
				Metadata:           `{"walnut_provider_subscription_id":"sub_secret","walnut_provider_subscription_status":"active","walnut_provider_subscription_raw_status":"provider_active","walnut_provider_period_start_at":"2026-05-19T10:00:00Z","walnut_provider_period_end_at":"2026-07-19T10:00:00Z"}`,
				PaidAt:             &paidAt,
				FulfilledAt:        &fulfilledAt,
			},
			LatestCancellation: &domain.SubscriptionCancellation{
				ID:                  "sub_cancel_1",
				UserID:              "usr_1",
				SKUCode:             domain.SKUProOwnAIMonthly,
				Status:              SubscriptionCancellationStatusCancelAtPeriodEnd,
				CancelAtPeriodEnd:   true,
				CurrentPeriodEndsAt: periodEnd,
				SourceOrderNo:       "CHK-1",
				Reason:              "contact writer@example.com before renewal",
				Source:              "pc_core",
				IdempotencyKey:      "cancel:secret",
				CreatedAt:           now.Add(-30 * time.Minute),
				UpdatedAt:           now.Add(-20 * time.Minute),
			},
			PaymentEvents: []domain.PaymentEventInbox{
				{ID: "pev_new", Provider: "creem", ProviderEventID: "evt_secret", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-1", PayloadHash: "hash_123", RawPayload: `{"subscription_id":"sub_secret","email":"writer@example.com"}`, Status: domain.PaymentEventStatusProcessed, ReceivedAt: now.Add(-2 * time.Minute), ProcessedAt: &processedAt},
			},
		}},
	}}
	svc := NewAdminSubscriptionService(AdminSubscriptionDependencies{
		ReadModel: repo,
		Privacy:   NewAdminPrivacyProjector(),
		Now:       func() time.Time { return now },
	})

	result, err := svc.ListSubscriptions(context.Background(), AdminSubscriptionQuery{
		UserID:     " usr_1 ",
		SKUCode:    " pro_own_ai_monthly ",
		Status:     " cancel_at_period_end ",
		Provider:   " creem ",
		OutTradeNo: " CHK-1 ",
		Limit:      999,
		Offset:     -10,
	})
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if repo.query.UserID != "usr_1" || repo.query.SKUCode != domain.SKUProOwnAIMonthly || repo.query.Provider != "creem" || repo.query.Limit != maxAdminSubscriptionLimit || repo.query.Offset != 0 {
		t.Fatalf("expected normalized repository query, got %#v", repo.query)
	}
	if result.Total != 1 || len(result.Subscriptions) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	subscription := result.Subscriptions[0]
	if subscription.User.EmailMasked != "wr**er@example.com" || subscription.User.EmailFingerprint == "" || subscription.User.EmailDomain != "example.com" {
		t.Fatalf("expected masked user identity, got %#v", subscription.User)
	}
	if subscription.Status != SoftwareSubscriptionStatusCancelAtPeriodEnd || !subscription.CancelAtPeriodEnd || subscription.CanCancel || !subscription.CanResume {
		t.Fatalf("expected cancel-at-period-end controls, got %#v", subscription)
	}
	if subscription.LatestOrder.OutTradeNo != "CHK-1" || !subscription.LatestOrder.HasProviderSubscription || !subscription.LatestOrder.HasMetadata {
		t.Fatalf("expected safe latest order flags, got %#v", subscription.LatestOrder)
	}
	if subscription.ProviderControl.Status != "active" || subscription.ProviderControl.RawStatus != "provider_active" || subscription.ProviderControl.CurrentPeriodEndsAt == "" {
		t.Fatalf("expected provider-control status projection, got %#v", subscription.ProviderControl)
	}
	if subscription.PaymentEvents.Count != 1 || subscription.PaymentEvents.LatestEvent.PayloadHash != "hash_123" {
		t.Fatalf("expected payment-event hash diagnostics, got %#v", subscription.PaymentEvents)
	}
	if subscription.Cancellation.ReasonRedacted == "contact writer@example.com before renewal" {
		t.Fatalf("expected redacted cancellation reason, got %#v", subscription.Cancellation)
	}

	raw, _ := json.Marshal(result)
	body := string(raw)
	for _, leaked := range []string{
		"Writer@Example.COM",
		"writer@example.com",
		"https://checkout.example/secret",
		"ch_secret",
		"cus_secret",
		"sub_secret",
		"evt_secret",
		"checkout:usr_1:secret",
		"cancel:secret",
		`{"subscription_id":"sub_secret","email":"writer@example.com"}`,
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("admin subscription response leaked %q in %s", leaked, body)
		}
	}
}

func TestAdminSubscriptionServiceFiltersProjectedStatus(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	activeEnd := now.AddDate(0, 1, 0)
	expiredEnd := now.AddDate(0, -1, 0)
	repo := &fakeAdminSubscriptionReadRepo{result: &repository.AdminSubscriptionReadModel{
		Records: []repository.AdminSubscriptionRecord{
			{
				User:    domain.User{ID: "usr_active", Email: "active@example.com", Status: domain.UserStatusActive},
				SKUCode: domain.SKUProOwnAIMonthly,
				Grants:  []domain.EntitlementGrant{{ID: "grt_active", UserID: "usr_active", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &activeEnd}},
			},
			{
				User:    domain.User{ID: "usr_expired", Email: "expired@example.com", Status: domain.UserStatusActive},
				SKUCode: domain.SKUProOwnAIMonthly,
				Grants:  []domain.EntitlementGrant{{ID: "grt_expired", UserID: "usr_expired", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.AddDate(0, -2, 0), ExpiresAt: &expiredEnd}},
			},
		},
	}}
	svc := NewAdminSubscriptionService(AdminSubscriptionDependencies{
		ReadModel: repo,
		Now:       func() time.Time { return now },
	})

	result, err := svc.ListSubscriptions(context.Background(), AdminSubscriptionQuery{Status: SoftwareSubscriptionStatusActive, Limit: 0, Offset: -1})
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if repo.query.Status != SoftwareSubscriptionStatusActive || repo.query.Limit != defaultAdminSubscriptionLimit || repo.query.Offset != 0 {
		t.Fatalf("expected normalized status query, got %#v", repo.query)
	}
	if result.Total != 1 || len(result.Subscriptions) != 1 || result.Subscriptions[0].User.ID != "usr_active" {
		t.Fatalf("expected only active subscription, got %#v", result)
	}
}

func TestAdminSubscriptionServiceProjectsSKUScopedGrants(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	repo := &fakeAdminSubscriptionReadRepo{result: &repository.AdminSubscriptionReadModel{
		Records: []repository.AdminSubscriptionRecord{{
			User:    domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive},
			SKUCode: domain.SKUProOwnAIMonthly,
			Grants: []domain.EntitlementGrant{
				{ID: "grt_monthly", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd},
				{ID: "grt_lifetime", UserID: "usr_1", EntitlementID: domain.EntitlementCloudStorage, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour)},
			},
		}},
	}}
	svc := NewAdminSubscriptionService(AdminSubscriptionDependencies{
		ReadModel: repo,
		Now:       func() time.Time { return now },
	})

	result, err := svc.ListSubscriptions(context.Background(), AdminSubscriptionQuery{SKUCode: domain.SKUProOwnAIMonthly, Limit: 10})
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if result.Total != 1 || len(result.Subscriptions) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	subscription := result.Subscriptions[0]
	if subscription.SKUCode != domain.SKUProOwnAIMonthly || subscription.Status != SoftwareSubscriptionStatusActive || subscription.FulfillmentGrantCount != 1 {
		t.Fatalf("expected monthly-scoped projection, got %#v", subscription)
	}
	if len(subscription.ActiveEntitlements) != 1 || subscription.ActiveEntitlements[0] != domain.EntitlementEditorialStudio {
		t.Fatalf("expected monthly active entitlement only, got %#v", subscription.ActiveEntitlements)
	}
}

func TestAdminSubscriptionServiceRejectsInvalidQuery(t *testing.T) {
	_, err := NewAdminSubscriptionService(AdminSubscriptionDependencies{}).ListSubscriptions(context.Background(), AdminSubscriptionQuery{})
	if !errors.Is(err, ErrInvalidAdminSubscriptionQuery) {
		t.Fatalf("expected invalid query error, got %v", err)
	}
	_, err = NewAdminSubscriptionService(AdminSubscriptionDependencies{
		ReadModel: &fakeAdminSubscriptionReadRepo{},
	}).ListSubscriptions(context.Background(), AdminSubscriptionQuery{})
	if !errors.Is(err, ErrInvalidAdminSubscriptionQuery) {
		t.Fatalf("expected nil read model to be invalid, got %v", err)
	}
}
