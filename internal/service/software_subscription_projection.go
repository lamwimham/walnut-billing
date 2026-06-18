package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

const (
	SoftwareSubscriptionStatusNone              = "none"
	SoftwareSubscriptionStatusActive            = "active"
	SoftwareSubscriptionStatusCancelAtPeriodEnd = "cancel_at_period_end"
	SoftwareSubscriptionStatusPastDue           = "past_due"
	SoftwareSubscriptionStatusExpired           = "expired"
	SoftwareSubscriptionStatusCancelled         = "cancelled"
)

// SoftwareSubscriptionProjection is the read model used by checkout policies,
// subscription APIs, and signed snapshots to keep client button state coherent.
type SoftwareSubscriptionProjection struct {
	UserID               string `json:"user_id"`
	SKUCode              string `json:"sku_code"`
	Status               string `json:"status"`
	CancelAtPeriodEnd    bool   `json:"cancel_at_period_end"`
	CurrentPeriodStartAt string `json:"current_period_start_at,omitempty"`
	CurrentPeriodEndsAt  string `json:"current_period_ends_at,omitempty"`
}

type SoftwareSubscriptionProjector interface {
	Project(ctx context.Context, userID string) (SoftwareSubscriptionProjection, error)
}

type SoftwareSubscriptionProjectionRepositories struct {
	EntitlementGrants repository.EntitlementGrantRepository
	Cancellations     repository.SubscriptionCancellationRepository
}

type softwareSubscriptionProjector struct {
	repos SoftwareSubscriptionProjectionRepositories
	now   func() time.Time
}

func NewSoftwareSubscriptionProjector(repos SoftwareSubscriptionProjectionRepositories, now func() time.Time) SoftwareSubscriptionProjector {
	return &softwareSubscriptionProjector{repos: repos, now: now}
}

func (p *softwareSubscriptionProjector) Project(ctx context.Context, userID string) (SoftwareSubscriptionProjection, error) {
	userID = strings.TrimSpace(userID)
	if p == nil || p.repos.EntitlementGrants == nil || userID == "" {
		return SoftwareSubscriptionProjection{}, ErrCheckoutPolicyUnavailable
	}
	grants, err := p.repos.EntitlementGrants.List(ctx, repository.EntitlementGrantQuery{
		UserID:         userID,
		Status:         domain.GrantStatusActive,
		IncludeExpired: false,
	})
	if err != nil {
		return SoftwareSubscriptionProjection{}, err
	}
	return projectSoftwareSubscriptionFromGrants(ctx, p.repos.Cancellations, userID, grants, p.currentTime())
}

func (p *softwareSubscriptionProjector) currentTime() time.Time {
	if p != nil && p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}

func projectSoftwareSubscriptionFromGrants(
	ctx context.Context,
	cancellations repository.SubscriptionCancellationRepository,
	userID string,
	grants []domain.EntitlementGrant,
	now time.Time,
) (SoftwareSubscriptionProjection, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return SoftwareSubscriptionProjection{}, ErrUserNotFound
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	lifetime := latestActiveLifetimeGrant(grants, now)
	if lifetime != nil {
		return SoftwareSubscriptionProjection{
			UserID:               userID,
			SKUCode:              domain.SKUProOwnAILifetime,
			Status:               SoftwareSubscriptionStatusActive,
			CurrentPeriodStartAt: formatSoftwareSubscriptionTime(lifetime.StartsAt),
		}, nil
	}

	monthly := latestActiveMonthlyGrant(grants, now)
	if monthly == nil || monthly.ExpiresAt == nil {
		return SoftwareSubscriptionProjection{
			UserID: userID,
			Status: SoftwareSubscriptionStatusNone,
		}, nil
	}

	projection := SoftwareSubscriptionProjection{
		UserID:               userID,
		SKUCode:              domain.SKUProOwnAIMonthly,
		Status:               SoftwareSubscriptionStatusActive,
		CurrentPeriodStartAt: formatSoftwareSubscriptionTime(monthly.StartsAt),
		CurrentPeriodEndsAt:  monthly.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if cancellations == nil {
		return projection, nil
	}
	cancellation, err := cancellations.FindActive(ctx, repository.SubscriptionCancellationQuery{
		UserID:  userID,
		SKUCode: domain.SKUProOwnAIMonthly,
		Status:  SubscriptionCancellationStatusCancelAtPeriodEnd,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return projection, nil
		}
		return SoftwareSubscriptionProjection{}, err
	}
	if cancellation == nil {
		return projection, nil
	}
	projection.Status = SoftwareSubscriptionStatusCancelAtPeriodEnd
	projection.CancelAtPeriodEnd = true
	if !cancellation.CurrentPeriodEndsAt.IsZero() {
		projection.CurrentPeriodEndsAt = cancellation.CurrentPeriodEndsAt.UTC().Format(time.RFC3339)
	}
	return projection, nil
}

func latestActiveLifetimeGrant(grants []domain.EntitlementGrant, now time.Time) *domain.EntitlementGrant {
	var selected *domain.EntitlementGrant
	for idx := range grants {
		grant := grants[idx]
		if !isSoftwareSubscriptionGrant(grant, now) || grant.ExpiresAt != nil {
			continue
		}
		if selected == nil || grant.StartsAt.After(selected.StartsAt) {
			copy := grant
			selected = &copy
		}
	}
	return selected
}

func latestActiveMonthlyGrant(grants []domain.EntitlementGrant, now time.Time) *domain.EntitlementGrant {
	var selected *domain.EntitlementGrant
	for idx := range grants {
		grant := grants[idx]
		if !isSoftwareSubscriptionGrant(grant, now) || grant.ExpiresAt == nil {
			continue
		}
		if selected == nil || grant.ExpiresAt.After(*selected.ExpiresAt) {
			copy := grant
			selected = &copy
		}
	}
	return selected
}

func isSoftwareSubscriptionGrant(grant domain.EntitlementGrant, now time.Time) bool {
	return grant.Source == domain.GrantSourceFulfillment &&
		IsCurrentAdvancedEntitlementID(grant.EntitlementID) &&
		isGrantActive(grant, now)
}

func softwareSubscriptionIsLifetimeActive(projection SoftwareSubscriptionProjection) bool {
	return projection.SKUCode == domain.SKUProOwnAILifetime && projection.Status == SoftwareSubscriptionStatusActive
}

func softwareSubscriptionIsMonthlyActive(projection SoftwareSubscriptionProjection) bool {
	if projection.SKUCode != domain.SKUProOwnAIMonthly {
		return false
	}
	return projection.Status == SoftwareSubscriptionStatusActive ||
		projection.Status == SoftwareSubscriptionStatusCancelAtPeriodEnd
}

func softwareSubscriptionIsCancelAtPeriodEnd(projection SoftwareSubscriptionProjection) bool {
	return projection.SKUCode == domain.SKUProOwnAIMonthly &&
		projection.Status == SoftwareSubscriptionStatusCancelAtPeriodEnd &&
		projection.CancelAtPeriodEnd
}

func formatSoftwareSubscriptionTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
