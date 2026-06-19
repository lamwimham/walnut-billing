package config

import "testing"

func TestLoadReadsCheckoutRiskPolicyEnvConfig(t *testing.T) {
	t.Setenv("CHECKOUT_RISK_POLICY_ENABLED", "false")
	t.Setenv("CHECKOUT_RISK_BLOCK_SEVERITIES", "critical, high, medium")
	t.Setenv("CHECKOUT_REDIRECT_ALLOWLIST", "https://app.walnut.example, https://billing.walnut.example")
	t.Setenv("HTTP_CORS_ALLOWED_ORIGINS", "https://app.walnut.example, https://ops.walnut.example")
	t.Setenv("HTTP_SECURITY_HEADERS_ENABLED", "false")
	t.Setenv("HTTP_SECURITY_HEADERS_HSTS_MAX_AGE_SECONDS", "63072000")

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
	wantRedirects := []string{"https://app.walnut.example", "https://billing.walnut.example"}
	if len(cfg.Checkout.RedirectAllowlist) != len(wantRedirects) {
		t.Fatalf("unexpected redirect allowlist: %#v", cfg.Checkout.RedirectAllowlist)
	}
	for i := range wantRedirects {
		if cfg.Checkout.RedirectAllowlist[i] != wantRedirects[i] {
			t.Fatalf("unexpected redirect allowlist: %#v", cfg.Checkout.RedirectAllowlist)
		}
	}
	wantOrigins := []string{"https://app.walnut.example", "https://ops.walnut.example"}
	if len(cfg.HTTP.CORSAllowedOrigins) != len(wantOrigins) {
		t.Fatalf("unexpected cors origins: %#v", cfg.HTTP.CORSAllowedOrigins)
	}
	for i := range wantOrigins {
		if cfg.HTTP.CORSAllowedOrigins[i] != wantOrigins[i] {
			t.Fatalf("unexpected cors origins: %#v", cfg.HTTP.CORSAllowedOrigins)
		}
	}
	if cfg.HTTP.SecurityHeaders.Enabled || cfg.HTTP.SecurityHeaders.HSTSMaxAgeSeconds != 63072000 {
		t.Fatalf("unexpected security header config: %#v", cfg.HTTP.SecurityHeaders)
	}
}
