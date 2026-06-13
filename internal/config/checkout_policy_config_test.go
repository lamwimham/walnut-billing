package config

import "testing"

func TestLoadReadsCheckoutRiskPolicyEnvConfig(t *testing.T) {
	t.Setenv("CHECKOUT_RISK_POLICY_ENABLED", "false")
	t.Setenv("CHECKOUT_RISK_BLOCK_SEVERITIES", "critical, high, medium")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Checkout.RiskPolicyEnabled {
		t.Fatalf("expected checkout risk policy to be disabled")
	}
	want := []string{"critical", "high", "medium"}
	if len(cfg.Checkout.RiskBlockSeverities) != len(want) {
		t.Fatalf("unexpected severity count: %#v", cfg.Checkout.RiskBlockSeverities)
	}
	for i := range want {
		if cfg.Checkout.RiskBlockSeverities[i] != want[i] {
			t.Fatalf("unexpected severities: %#v", cfg.Checkout.RiskBlockSeverities)
		}
	}
}
