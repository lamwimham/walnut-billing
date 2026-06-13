package config

import "testing"

func TestLoadReadsPaymentAdjustmentPolicyEnvConfig(t *testing.T) {
	t.Setenv("ADJUSTMENT_REFUND_WINDOW_DAYS", "14")
	t.Setenv("ADJUSTMENT_REFUND_IN_WINDOW_ACTION", "auto_refund")
	t.Setenv("ADJUSTMENT_REFUND_OUT_OF_WINDOW_ACTION", "reject")
	t.Setenv("ADJUSTMENT_LOW_USAGE_POLICY_ENABLED", "true")
	t.Setenv("ADJUSTMENT_LOW_USAGE_MAX_CREDITS_USED", "120")
	t.Setenv("ADJUSTMENT_LOW_USAGE_ACTION", "auto_refund")
	t.Setenv("ADJUSTMENT_HIGH_USAGE_ACTION", "manual_review")
	t.Setenv("ADJUSTMENT_DISPUTE_ACTION", "auto_refund")
	t.Setenv("ADJUSTMENT_CANCEL_ACTION", "keep_current_period")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Adjustment.RefundWindowDays != 14 {
		t.Fatalf("unexpected refund window: %#v", cfg.Adjustment)
	}
	if !cfg.Adjustment.LowUsagePolicyEnabled || cfg.Adjustment.LowUsageMaxCreditsUsed != 120 {
		t.Fatalf("unexpected low usage config: %#v", cfg.Adjustment)
	}
	if cfg.Adjustment.RefundOutOfWindowAction != "reject" || cfg.Adjustment.HighUsageAction != "manual_review" || cfg.Adjustment.CancelAction != "keep_current_period" {
		t.Fatalf("unexpected adjustment actions: %#v", cfg.Adjustment)
	}
}
