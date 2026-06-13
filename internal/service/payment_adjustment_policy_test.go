package service

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
)

func TestPaymentAdjustmentPolicy_RefundWindowDecisions(t *testing.T) {
	paidAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		Now:                     func() time.Time { return paidAt.AddDate(0, 0, 6) },
	})

	inWindow := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRefunded},
		Order: &domain.Order{PaidAt: &paidAt},
	})
	if !inWindow.ApplyCompensation || inWindow.Action != PaymentAdjustmentActionAutoRefund || inWindow.Reason != PaymentAdjustmentReasonRefundInWindow {
		t.Fatalf("expected auto refund inside window, got %#v", inWindow)
	}

	policy = NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		Now:                     func() time.Time { return paidAt.AddDate(0, 0, 8) },
	})
	outOfWindow := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRefunded},
		Order: &domain.Order{PaidAt: &paidAt},
	})
	if outOfWindow.ApplyCompensation || !outOfWindow.ManualReview || outOfWindow.Reason != PaymentAdjustmentReasonRefundOutOfWindow {
		t.Fatalf("expected manual review outside window, got %#v", outOfWindow)
	}
}

func TestPaymentAdjustmentPolicy_LowUsageGate(t *testing.T) {
	paidAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		LowUsagePolicyEnabled:   true,
		LowUsageMaxCreditsUsed:  100,
		LowUsageAction:          PaymentAdjustmentActionAutoRefund,
		HighUsageAction:         PaymentAdjustmentActionManualReview,
		Now:                     func() time.Time { return paidAt.AddDate(0, 0, 1) },
	})

	lowUsage := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRefunded},
		Order: &domain.Order{PaidAt: &paidAt},
		Usage: PaymentAdjustmentUsageSnapshot{Known: true, CreditsGranted: 600, CreditsAvailableForClawback: 550, CreditsUsed: 50},
	})
	if !lowUsage.ApplyCompensation || lowUsage.Reason != PaymentAdjustmentReasonRefundLowUsage {
		t.Fatalf("expected low usage auto refund, got %#v", lowUsage)
	}

	highUsage := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRefunded},
		Order: &domain.Order{PaidAt: &paidAt},
		Usage: PaymentAdjustmentUsageSnapshot{Known: true, CreditsGranted: 600, CreditsAvailableForClawback: 400, CreditsUsed: 200},
	})
	if highUsage.ApplyCompensation || !highUsage.ManualReview || highUsage.Reason != PaymentAdjustmentReasonRefundHighUsage {
		t.Fatalf("expected high usage manual review, got %#v", highUsage)
	}
}

func TestPaymentAdjustmentPolicy_DisputeAlwaysCreatesRiskAndCompensatesByDefault(t *testing.T) {
	policy := NewConfigurablePaymentAdjustmentPolicy(DefaultPaymentAdjustmentPolicyConfig())
	decision := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeDisputed},
	})
	if !decision.ApplyCompensation || !decision.CreateRiskFlag || decision.Action != PaymentAdjustmentActionAutoRefund || decision.Reason != PaymentAdjustmentReasonDispute {
		t.Fatalf("expected dispute auto compensation and risk flag, got %#v", decision)
	}
}

func TestPaymentAdjustmentPolicy_DisputeManualReviewCreatesRiskWithoutCompensation(t *testing.T) {
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		DisputeAction: PaymentAdjustmentActionManualReview,
	})
	decision := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeDisputed},
	})
	if decision.ApplyCompensation || !decision.CreateRiskFlag || !decision.ManualReview || decision.Reason != PaymentAdjustmentReasonDispute {
		t.Fatalf("expected dispute manual review with risk flag only, got %#v", decision)
	}
}

func TestPaymentAdjustmentPolicy_RefundRejectsUnsupportedKeepCurrentAction(t *testing.T) {
	paidAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:     7,
		RefundInWindowAction: PaymentAdjustmentActionKeepCurrentPeriod,
		Now:                  func() time.Time { return paidAt.AddDate(0, 0, 1) },
	})
	decision := policy.Decide(context.Background(), PaymentAdjustmentPolicyInput{
		Event: &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRefunded},
		Order: &domain.Order{PaidAt: &paidAt},
	})
	if !decision.ApplyCompensation || decision.Action != PaymentAdjustmentActionAutoRefund {
		t.Fatalf("expected unsupported refund action to fall back to auto refund, got %#v", decision)
	}
}
