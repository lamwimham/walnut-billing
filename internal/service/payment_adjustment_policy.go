package service

import (
	"context"
	"strings"
	"time"
	"walnut-billing/internal/domain"
)

const (
	PaymentAdjustmentActionAutoRefund        = "auto_refund"
	PaymentAdjustmentActionManualReview      = "manual_review"
	PaymentAdjustmentActionReject            = "reject"
	PaymentAdjustmentActionKeepCurrentPeriod = "keep_current_period"

	PaymentAdjustmentReasonRefundInWindow      = "refund_in_window"
	PaymentAdjustmentReasonRefundOutOfWindow   = "refund_out_of_window"
	PaymentAdjustmentReasonRefundLowUsage      = "refund_low_usage"
	PaymentAdjustmentReasonRefundHighUsage     = "refund_high_usage"
	PaymentAdjustmentReasonRefundWindowUnknown = "refund_window_unknown"
	PaymentAdjustmentReasonDispute             = "payment_disputed"
	PaymentAdjustmentReasonCancelCurrentPeriod = "cancel_keeps_current_paid_period"
)

type PaymentAdjustmentPolicy interface {
	Decide(ctx context.Context, input PaymentAdjustmentPolicyInput) PaymentAdjustmentPolicyDecision
}

type PaymentAdjustmentPolicyInput struct {
	Event *domain.PaymentEventInbox
	Order *domain.Order
	Usage PaymentAdjustmentUsageSnapshot
}

// PaymentAdjustmentUsageSnapshot is intentionally provider-agnostic. Today it is
// derived from Walnut credit ledger state; future usage services can provide a
// more precise feature-usage snapshot without changing webhook adapters.
type PaymentAdjustmentUsageSnapshot struct {
	Known                       bool
	CreditsGranted              int64
	CreditsAvailableForClawback int64
	CreditsUsed                 int64
}

type PaymentAdjustmentPolicyDecision struct {
	Action             string     `json:"action"`
	Reason             string     `json:"reason"`
	ApplyCompensation  bool       `json:"apply_compensation"`
	RevokeEntitlements bool       `json:"revoke_entitlements"`
	ClawbackCredits    bool       `json:"clawback_credits"`
	CreateRiskFlag     bool       `json:"create_risk_flag"`
	RefundWindowEndsAt *time.Time `json:"refund_window_ends_at,omitempty"`
	ManualReview       bool       `json:"manual_review"`
	Rejected           bool       `json:"rejected"`
	Note               string     `json:"note,omitempty"`
}

type PaymentAdjustmentPolicyConfig struct {
	RefundWindowDays        int
	RefundInWindowAction    string
	RefundOutOfWindowAction string
	LowUsagePolicyEnabled   bool
	LowUsageMaxCreditsUsed  int64
	LowUsageAction          string
	HighUsageAction         string
	DisputeAction           string
	CancelAction            string
	Now                     func() time.Time
}

type configurablePaymentAdjustmentPolicy struct {
	config PaymentAdjustmentPolicyConfig
}

func DefaultPaymentAdjustmentPolicyConfig() PaymentAdjustmentPolicyConfig {
	return PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		LowUsagePolicyEnabled:   false,
		LowUsageMaxCreditsUsed:  0,
		LowUsageAction:          PaymentAdjustmentActionAutoRefund,
		HighUsageAction:         PaymentAdjustmentActionManualReview,
		DisputeAction:           PaymentAdjustmentActionAutoRefund,
		CancelAction:            PaymentAdjustmentActionKeepCurrentPeriod,
	}
}

func NewConfigurablePaymentAdjustmentPolicy(config PaymentAdjustmentPolicyConfig) PaymentAdjustmentPolicy {
	return &configurablePaymentAdjustmentPolicy{config: normalizePaymentAdjustmentPolicyConfig(config)}
}

func (p *configurablePaymentAdjustmentPolicy) Decide(ctx context.Context, input PaymentAdjustmentPolicyInput) PaymentAdjustmentPolicyDecision {
	_ = ctx
	if input.Event == nil {
		return decisionFromAction(PaymentAdjustmentActionReject, "invalid_payment_event", false)
	}
	switch input.Event.EventType {
	case domain.PaymentEventTypeCancelled:
		decision := decisionFromAction(p.config.CancelAction, PaymentAdjustmentReasonCancelCurrentPeriod, false)
		decision.Note = PaymentAdjustmentReasonCancelCurrentPeriod
		return decision
	case domain.PaymentEventTypeDisputed:
		decision := decisionFromAction(p.config.DisputeAction, PaymentAdjustmentReasonDispute, true)
		decision.CreateRiskFlag = true
		return decision
	case domain.PaymentEventTypeRefunded:
		return p.refundDecision(input)
	default:
		return decisionFromAction(PaymentAdjustmentActionReject, "unsupported_payment_event", false)
	}
}

func (p *configurablePaymentAdjustmentPolicy) refundDecision(input PaymentAdjustmentPolicyInput) PaymentAdjustmentPolicyDecision {
	withinWindow, windowEndsAt, known := p.refundWindow(input.Order)
	if !known {
		decision := decisionFromAction(p.config.RefundOutOfWindowAction, PaymentAdjustmentReasonRefundWindowUnknown, false)
		decision.RefundWindowEndsAt = windowEndsAt
		return decision
	}
	if !withinWindow {
		decision := decisionFromAction(p.config.RefundOutOfWindowAction, PaymentAdjustmentReasonRefundOutOfWindow, false)
		decision.RefundWindowEndsAt = windowEndsAt
		return decision
	}
	if p.config.LowUsagePolicyEnabled && input.Usage.Known {
		if input.Usage.CreditsUsed <= p.config.LowUsageMaxCreditsUsed {
			decision := decisionFromAction(p.config.LowUsageAction, PaymentAdjustmentReasonRefundLowUsage, false)
			decision.RefundWindowEndsAt = windowEndsAt
			return decision
		}
		decision := decisionFromAction(p.config.HighUsageAction, PaymentAdjustmentReasonRefundHighUsage, false)
		decision.RefundWindowEndsAt = windowEndsAt
		return decision
	}
	decision := decisionFromAction(p.config.RefundInWindowAction, PaymentAdjustmentReasonRefundInWindow, false)
	decision.RefundWindowEndsAt = windowEndsAt
	return decision
}

func (p *configurablePaymentAdjustmentPolicy) refundWindow(order *domain.Order) (bool, *time.Time, bool) {
	if p.config.RefundWindowDays <= 0 {
		return true, nil, true
	}
	if order == nil || order.PaidAt == nil {
		return false, nil, false
	}
	windowEndsAt := order.PaidAt.UTC().AddDate(0, 0, p.config.RefundWindowDays)
	return !p.now().After(windowEndsAt), &windowEndsAt, true
}

func (p *configurablePaymentAdjustmentPolicy) now() time.Time {
	if p.config.Now != nil {
		return p.config.Now().UTC()
	}
	return time.Now().UTC()
}

func decisionFromAction(action string, reason string, createRiskFlag bool) PaymentAdjustmentPolicyDecision {
	action = normalizePaymentAdjustmentAction(action, PaymentAdjustmentActionManualReview)
	decision := PaymentAdjustmentPolicyDecision{
		Action:             action,
		Reason:             reason,
		CreateRiskFlag:     createRiskFlag,
		ApplyCompensation:  action == PaymentAdjustmentActionAutoRefund,
		RevokeEntitlements: action == PaymentAdjustmentActionAutoRefund,
		ClawbackCredits:    action == PaymentAdjustmentActionAutoRefund,
		ManualReview:       action == PaymentAdjustmentActionManualReview,
		Rejected:           action == PaymentAdjustmentActionReject,
	}
	if decision.ManualReview {
		decision.Note = "refund_policy_manual_review"
	}
	if decision.Rejected {
		decision.Note = "refund_policy_rejected"
	}
	if action == PaymentAdjustmentActionKeepCurrentPeriod {
		decision.ApplyCompensation = false
		decision.RevokeEntitlements = false
		decision.ClawbackCredits = false
	}
	return decision
}

func normalizePaymentAdjustmentPolicyConfig(config PaymentAdjustmentPolicyConfig) PaymentAdjustmentPolicyConfig {
	defaults := DefaultPaymentAdjustmentPolicyConfig()
	if config.RefundWindowDays == 0 {
		config.RefundWindowDays = defaults.RefundWindowDays
	}
	config.RefundInWindowAction = normalizePaymentAdjustmentCompensationAction(config.RefundInWindowAction, defaults.RefundInWindowAction)
	config.RefundOutOfWindowAction = normalizePaymentAdjustmentCompensationAction(config.RefundOutOfWindowAction, defaults.RefundOutOfWindowAction)
	config.LowUsageAction = normalizePaymentAdjustmentCompensationAction(config.LowUsageAction, defaults.LowUsageAction)
	config.HighUsageAction = normalizePaymentAdjustmentCompensationAction(config.HighUsageAction, defaults.HighUsageAction)
	config.DisputeAction = normalizePaymentAdjustmentCompensationAction(config.DisputeAction, defaults.DisputeAction)
	config.CancelAction = normalizePaymentAdjustmentAction(config.CancelAction, defaults.CancelAction)
	return config
}

func normalizePaymentAdjustmentCompensationAction(action string, fallback string) string {
	switch strings.TrimSpace(action) {
	case PaymentAdjustmentActionAutoRefund:
		return PaymentAdjustmentActionAutoRefund
	case PaymentAdjustmentActionManualReview:
		return PaymentAdjustmentActionManualReview
	case PaymentAdjustmentActionReject:
		return PaymentAdjustmentActionReject
	default:
		return fallback
	}
}

func normalizePaymentAdjustmentAction(action string, fallback string) string {
	switch strings.TrimSpace(action) {
	case PaymentAdjustmentActionAutoRefund:
		return PaymentAdjustmentActionAutoRefund
	case PaymentAdjustmentActionManualReview:
		return PaymentAdjustmentActionManualReview
	case PaymentAdjustmentActionReject:
		return PaymentAdjustmentActionReject
	case PaymentAdjustmentActionKeepCurrentPeriod:
		return PaymentAdjustmentActionKeepCurrentPeriod
	default:
		return fallback
	}
}
