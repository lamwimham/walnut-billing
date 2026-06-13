package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrCheckoutBlockedByRisk     = errors.New("checkout blocked by payment risk")
	ErrCheckoutPolicyUnavailable = errors.New("checkout policy unavailable")
)

const (
	CheckoutPolicyActionAllow        = "allow"
	CheckoutPolicyActionManualReview = "manual_review"

	CheckoutPolicyReasonOpenPaymentRisk = "open_payment_risk"
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
