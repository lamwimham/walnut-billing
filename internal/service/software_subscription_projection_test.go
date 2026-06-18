package service

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
)

func TestSoftwareSubscriptionProjector_ProjectsMonthlyActive(t *testing.T) {
	grants := newMockGrantRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	now := time.Date(2026, 6, 19, 2, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	grants.grants["monthly"] = &domain.EntitlementGrant{
		ID:            "monthly",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now.Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}
	projector := NewSoftwareSubscriptionProjector(SoftwareSubscriptionProjectionRepositories{
		EntitlementGrants: grants,
		Cancellations:     cancellations,
	}, func() time.Time { return now })

	projection, err := projector.Project(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("project subscription: %v", err)
	}
	if projection.SKUCode != domain.SKUProOwnAIMonthly || projection.Status != SoftwareSubscriptionStatusActive || projection.CancelAtPeriodEnd {
		t.Fatalf("expected monthly active projection, got %#v", projection)
	}
	if projection.CurrentPeriodEndsAt != periodEnd.Format(time.RFC3339) {
		t.Fatalf("expected period end %s, got %#v", periodEnd.Format(time.RFC3339), projection)
	}
}

func TestSoftwareSubscriptionProjector_ProjectsCancelAtPeriodEnd(t *testing.T) {
	grants := newMockGrantRepo()
	cancellations := newMockSubscriptionCancellationRepo()
	now := time.Date(2026, 6, 19, 2, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	grants.grants["monthly"] = &domain.EntitlementGrant{
		ID:            "monthly",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now.Add(-time.Hour),
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
	projector := NewSoftwareSubscriptionProjector(SoftwareSubscriptionProjectionRepositories{
		EntitlementGrants: grants,
		Cancellations:     cancellations,
	}, func() time.Time { return now })

	projection, err := projector.Project(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("project subscription: %v", err)
	}
	if projection.SKUCode != domain.SKUProOwnAIMonthly || projection.Status != SoftwareSubscriptionStatusCancelAtPeriodEnd || !projection.CancelAtPeriodEnd {
		t.Fatalf("expected cancel-at-period-end projection, got %#v", projection)
	}
}

func TestSoftwareSubscriptionProjector_LifetimeOverridesMonthly(t *testing.T) {
	grants := newMockGrantRepo()
	now := time.Date(2026, 6, 19, 2, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	grants.grants["monthly"] = &domain.EntitlementGrant{
		ID:            "monthly",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now.Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}
	grants.grants["lifetime"] = &domain.EntitlementGrant{
		ID:            "lifetime",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now,
	}
	projector := NewSoftwareSubscriptionProjector(SoftwareSubscriptionProjectionRepositories{
		EntitlementGrants: grants,
	}, func() time.Time { return now })

	projection, err := projector.Project(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("project subscription: %v", err)
	}
	if projection.SKUCode != domain.SKUProOwnAILifetime || projection.Status != SoftwareSubscriptionStatusActive || projection.CurrentPeriodEndsAt != "" {
		t.Fatalf("expected lifetime projection, got %#v", projection)
	}
}
