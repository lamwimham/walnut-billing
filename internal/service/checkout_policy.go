package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrCheckoutBlockedByRisk     = errors.New("checkout blocked by payment risk")
	ErrCheckoutBlockedByPlan     = errors.New("checkout blocked by subscription state")
	ErrCheckoutPolicyUnavailable = errors.New("checkout policy unavailable")
)

const (
	CheckoutPolicyActionAllow        = "allow"
	CheckoutPolicyActionManualReview = "manual_review"

	CheckoutPolicyReasonOpenPaymentRisk                        = "open_payment_risk"
	CheckoutPolicyReasonDuplicateActiveSubscription            = "duplicate_active_subscription"
	CheckoutPolicyReasonLifetimeAlreadyActive                  = "lifetime_already_active"
	CheckoutPolicyReasonActiveSubscriptionRequiresCancellation = "active_subscription_requires_cancellation"
)

// CheckoutPolicy is the strategy boundary for pre-checkout controls. Policies
// can block purchase creation without leaking provider or risk details to apps.
type CheckoutPolicy interface {
	Evaluate(ctx context.Context, input CheckoutPolicyInput) (CheckoutPolicyDecision, error)
}

type CheckoutPolicyInput struct {
	Checkout      CheckoutInput
	User          *domain.User
	Product       *domain.Product
	ExistingOrder *domain.Order
}

type CheckoutPolicyDecision struct {
	Allowed bool
	Reason  string
	Action  string
	Message string
	Cause   error
}

// CheckoutPolicyRejection carries a stable policy decision while still allowing
// handlers to use errors.Is against a domain-level cause.
type CheckoutPolicyRejection struct {
	Cause    error
	Decision CheckoutPolicyDecision
}

func (e *CheckoutPolicyRejection) Error() string {
	if e == nil || e.Cause == nil {
		return "checkout blocked by policy"
	}
	return e.Cause.Error()
}

func (e *CheckoutPolicyRejection) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func CheckoutPolicyDecisionFromError(err error) (CheckoutPolicyDecision, bool) {
	var rejection *CheckoutPolicyRejection
	if errors.As(err, &rejection) && rejection != nil {
		return rejection.Decision, true
	}
	return CheckoutPolicyDecision{}, false
}

type CheckoutRiskPolicyConfig struct {
	BlockSeverities []string
	OpenStatus      string
	Action          string
	Reason          string
	Message         string
}

func DefaultCheckoutRiskPolicyConfig() CheckoutRiskPolicyConfig {
	return CheckoutRiskPolicyConfig{
		BlockSeverities: []string{
			domain.PaymentRiskSeverityCritical,
			domain.PaymentRiskSeverityHigh,
		},
		OpenStatus: domain.PaymentRiskStatusOpen,
		Action:     CheckoutPolicyActionManualReview,
		Reason:     CheckoutPolicyReasonOpenPaymentRisk,
		Message:    "checkout requires manual review",
	}
}

// PaymentRiskCheckoutPolicy blocks new checkout attempts for users with open
// high/critical payment-risk flags. It affects purchase creation only; app
// access remains driven by Walnut entitlement snapshots.
type PaymentRiskCheckoutPolicy struct {
	flags  repository.PaymentRiskFlagRepository
	config CheckoutRiskPolicyConfig
}

func NewPaymentRiskCheckoutPolicy(flags repository.PaymentRiskFlagRepository, config CheckoutRiskPolicyConfig) *PaymentRiskCheckoutPolicy {
	return &PaymentRiskCheckoutPolicy{
		flags:  flags,
		config: normalizeCheckoutRiskPolicyConfig(config),
	}
}

func (p *PaymentRiskCheckoutPolicy) Evaluate(ctx context.Context, input CheckoutPolicyInput) (CheckoutPolicyDecision, error) {
	if p == nil || p.flags == nil {
		return CheckoutPolicyDecision{}, ErrCheckoutPolicyUnavailable
	}
	userID := ""
	if input.User != nil {
		userID = strings.TrimSpace(input.User.ID)
	}
	if userID == "" {
		return allowCheckoutDecision(), nil
	}

	for _, severity := range p.config.BlockSeverities {
		flags, err := p.flags.List(ctx, repository.PaymentRiskFlagQuery{
			UserID:   userID,
			Severity: severity,
			Status:   p.config.OpenStatus,
			Limit:    1,
		})
		if err != nil {
			return CheckoutPolicyDecision{}, fmt.Errorf("%w: %v", ErrCheckoutPolicyUnavailable, err)
		}
		if len(flags) > 0 {
			return CheckoutPolicyDecision{
				Allowed: false,
				Reason:  p.config.Reason,
				Action:  p.config.Action,
				Message: p.config.Message,
				Cause:   ErrCheckoutBlockedByRisk,
			}, nil
		}
	}

	return allowCheckoutDecision(), nil
}

func normalizeCheckoutRiskPolicyConfig(config CheckoutRiskPolicyConfig) CheckoutRiskPolicyConfig {
	defaults := DefaultCheckoutRiskPolicyConfig()
	if len(config.BlockSeverities) == 0 {
		config.BlockSeverities = defaults.BlockSeverities
	}
	var severities []string
	seen := map[string]struct{}{}
	for _, severity := range config.BlockSeverities {
		severity = strings.TrimSpace(severity)
		if severity == "" {
			continue
		}
		if _, ok := seen[severity]; ok {
			continue
		}
		seen[severity] = struct{}{}
		severities = append(severities, severity)
	}
	if len(severities) == 0 {
		severities = defaults.BlockSeverities
	}
	config.BlockSeverities = severities
	config.OpenStatus = defaultString(strings.TrimSpace(config.OpenStatus), defaults.OpenStatus)
	config.Action = defaultString(strings.TrimSpace(config.Action), defaults.Action)
	config.Reason = defaultString(strings.TrimSpace(config.Reason), defaults.Reason)
	config.Message = defaultString(strings.TrimSpace(config.Message), defaults.Message)
	return config
}

func allowCheckoutDecision() CheckoutPolicyDecision {
	return CheckoutPolicyDecision{Allowed: true, Action: CheckoutPolicyActionAllow}
}

// SoftwareAccessPlanCheckoutPolicy keeps Walnut's mutually exclusive software
// tiers server-authoritative. Clients may hide buttons, but billing still
// rejects duplicate monthly checkout and repeat lifetime purchases.
type SoftwareAccessPlanCheckoutPolicy struct {
	grants        repository.EntitlementGrantRepository
	cancellations repository.SubscriptionCancellationRepository
	now           func() time.Time
}

func NewSoftwareAccessPlanCheckoutPolicy(
	grants repository.EntitlementGrantRepository,
	cancellations repository.SubscriptionCancellationRepository,
	now func() time.Time,
) *SoftwareAccessPlanCheckoutPolicy {
	return &SoftwareAccessPlanCheckoutPolicy{grants: grants, cancellations: cancellations, now: now}
}

func (p *SoftwareAccessPlanCheckoutPolicy) Evaluate(ctx context.Context, input CheckoutPolicyInput) (CheckoutPolicyDecision, error) {
	if p == nil || p.grants == nil {
		return CheckoutPolicyDecision{}, ErrCheckoutPolicyUnavailable
	}
	userID := ""
	if input.User != nil {
		userID = strings.TrimSpace(input.User.ID)
	}
	if userID == "" {
		return allowCheckoutDecision(), nil
	}
	skuCode := strings.TrimSpace(input.Checkout.SKUCode)
	if skuCode != domain.SKUProOwnAIMonthly && skuCode != domain.SKUProOwnAILifetime {
		return allowCheckoutDecision(), nil
	}
	summary, err := p.currentPlan(ctx, userID)
	if err != nil {
		return CheckoutPolicyDecision{}, err
	}
	if summary.HasLifetime {
		return CheckoutPolicyDecision{
			Allowed: false,
			Reason:  CheckoutPolicyReasonLifetimeAlreadyActive,
			Action:  CheckoutPolicyActionManualReview,
			Message: "lifetime access is already active",
			Cause:   ErrCheckoutBlockedByPlan,
		}, nil
	}
	if skuCode == domain.SKUProOwnAIMonthly && summary.HasActiveSubscription {
		return CheckoutPolicyDecision{
			Allowed: false,
			Reason:  CheckoutPolicyReasonDuplicateActiveSubscription,
			Action:  CheckoutPolicyActionManualReview,
			Message: "monthly subscription is already active",
			Cause:   ErrCheckoutBlockedByPlan,
		}, nil
	}
	if skuCode == domain.SKUProOwnAILifetime && summary.HasActiveSubscription && !summary.HasCancelAtPeriodEnd {
		return CheckoutPolicyDecision{
			Allowed: false,
			Reason:  CheckoutPolicyReasonActiveSubscriptionRequiresCancellation,
			Action:  CheckoutPolicyActionManualReview,
			Message: "cancel monthly renewal before buying lifetime access",
			Cause:   ErrCheckoutBlockedByPlan,
		}, nil
	}
	return allowCheckoutDecision(), nil
}

type softwareAccessPlanSummary struct {
	HasLifetime           bool
	HasActiveSubscription bool
	HasCancelAtPeriodEnd  bool
}

func (p *SoftwareAccessPlanCheckoutPolicy) currentPlan(ctx context.Context, userID string) (softwareAccessPlanSummary, error) {
	grants, err := p.grants.List(ctx, repository.EntitlementGrantQuery{
		UserID:         userID,
		Status:         domain.GrantStatusActive,
		IncludeExpired: false,
	})
	if err != nil {
		return softwareAccessPlanSummary{}, err
	}
	now := p.currentTime()
	summary := softwareAccessPlanSummary{}
	for _, grant := range grants {
		if grant.Source != domain.GrantSourceFulfillment || !IsCurrentAdvancedEntitlementID(grant.EntitlementID) {
			continue
		}
		if grant.ExpiresAt == nil {
			summary.HasLifetime = true
			continue
		}
		if grant.ExpiresAt.UTC().After(now) {
			summary.HasActiveSubscription = true
		}
	}
	if summary.HasActiveSubscription && p.cancellations != nil {
		cancellation, err := p.cancellations.FindActive(ctx, repository.SubscriptionCancellationQuery{
			UserID:  userID,
			SKUCode: domain.SKUProOwnAIMonthly,
			Status:  SubscriptionCancellationStatusCancelAtPeriodEnd,
		})
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return softwareAccessPlanSummary{}, err
		}
		summary.HasCancelAtPeriodEnd = cancellation != nil
	}
	return summary, nil
}

func (p *SoftwareAccessPlanCheckoutPolicy) currentTime() time.Time {
	if p != nil && p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}
