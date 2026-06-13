package service

import (
	"context"
	"strings"
	"walnut-billing/internal/domain"
)

const (
	SubscriptionRenewalActionFulfillRenewal  = "fulfill_renewal"
	SubscriptionRenewalActionFulfillCheckout = "fulfill_checkout"
	SubscriptionRenewalActionGrantGrace      = "grant_grace"
	SubscriptionRenewalActionExpireGrace     = "expire_grace"
	SubscriptionRenewalActionNaturalExpiry   = "natural_expiry"
	SubscriptionRenewalActionIgnore          = "ignore"
)

const (
	SubscriptionRenewalReasonPaid                    = "renewal_paid"
	SubscriptionRenewalReasonInitialSubscriptionPaid = "initial_subscription_paid"
	SubscriptionRenewalReasonPaymentFailed           = "renewal_payment_failed"
	SubscriptionRenewalReasonExpired                 = "subscription_expired"
	SubscriptionRenewalReasonUnsupported             = "unsupported_subscription_event"
)

type SubscriptionRenewalPolicy interface {
	Decide(ctx context.Context, input SubscriptionRenewalPolicyInput) SubscriptionRenewalPolicyDecision
}

type SubscriptionRenewalPolicyInput struct {
	Event *domain.PaymentEventInbox
	Order *domain.Order
}

type SubscriptionRenewalPolicyDecision struct {
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	GracePeriodDays int    `json:"grace_period_days,omitempty"`
}

type SubscriptionRenewalPolicyConfig struct {
	GracePeriodDays int
	ExpiredAction   string
}

type configurableSubscriptionRenewalPolicy struct {
	config SubscriptionRenewalPolicyConfig
}

func DefaultSubscriptionRenewalPolicyConfig() SubscriptionRenewalPolicyConfig {
	return SubscriptionRenewalPolicyConfig{
		GracePeriodDays: domain.GracePeriodDays,
		ExpiredAction:   SubscriptionRenewalActionExpireGrace,
	}
}

func NewConfigurableSubscriptionRenewalPolicy(config SubscriptionRenewalPolicyConfig) SubscriptionRenewalPolicy {
	return &configurableSubscriptionRenewalPolicy{config: normalizeSubscriptionRenewalPolicyConfig(config)}
}

func (p *configurableSubscriptionRenewalPolicy) Decide(ctx context.Context, input SubscriptionRenewalPolicyInput) SubscriptionRenewalPolicyDecision {
	_ = ctx
	if input.Event == nil {
		return SubscriptionRenewalPolicyDecision{Action: SubscriptionRenewalActionIgnore, Reason: SubscriptionRenewalReasonUnsupported}
	}
	switch strings.TrimSpace(input.Event.EventType) {
	case domain.PaymentEventTypeRenewalPaid:
		return SubscriptionRenewalPolicyDecision{Action: SubscriptionRenewalActionFulfillRenewal, Reason: SubscriptionRenewalReasonPaid}
	case domain.PaymentEventTypeRenewalFailed:
		return SubscriptionRenewalPolicyDecision{
			Action:          SubscriptionRenewalActionGrantGrace,
			Reason:          SubscriptionRenewalReasonPaymentFailed,
			GracePeriodDays: p.config.GracePeriodDays,
		}
	case domain.PaymentEventTypeSubscriptionExpired:
		return SubscriptionRenewalPolicyDecision{
			Action:          p.config.ExpiredAction,
			Reason:          SubscriptionRenewalReasonExpired,
			GracePeriodDays: p.config.GracePeriodDays,
		}
	default:
		return SubscriptionRenewalPolicyDecision{Action: SubscriptionRenewalActionIgnore, Reason: SubscriptionRenewalReasonUnsupported}
	}
}

func normalizeSubscriptionRenewalPolicyConfig(config SubscriptionRenewalPolicyConfig) SubscriptionRenewalPolicyConfig {
	defaults := DefaultSubscriptionRenewalPolicyConfig()
	if config.GracePeriodDays <= 0 {
		config.GracePeriodDays = defaults.GracePeriodDays
	}
	config.ExpiredAction = strings.TrimSpace(config.ExpiredAction)
	switch config.ExpiredAction {
	case SubscriptionRenewalActionExpireGrace, SubscriptionRenewalActionNaturalExpiry:
	default:
		config.ExpiredAction = defaults.ExpiredAction
	}
	return config
}
