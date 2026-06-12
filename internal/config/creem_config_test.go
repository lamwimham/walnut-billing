package config

import "testing"

func TestLoadReadsCreemEnvConfig(t *testing.T) {
	t.Setenv("PAYMENT_CREEM_API_KEY", "creem_test_key")
	t.Setenv("PAYMENT_CREEM_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("PAYMENT_CREEM_API_BASE_URL", "https://test-api.creem.io")
	t.Setenv("PAYMENT_CREEM_SUCCESS_URL", "https://walnut.local/success")
	t.Setenv("PAYMENT_CREEM_CANCEL_URL", "https://walnut.local/cancel")
	t.Setenv("PAYMENT_CREEM_PRODUCT_MAP_JSON", `{"editorial_studio_monthly":"prod_studio"}`)
	t.Setenv("PAYMENT_CREEM_SANDBOX", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Payment.CreemAPIKey != "creem_test_key" || cfg.Payment.CreemWebhookSecret != "whsec_test" {
		t.Fatalf("unexpected creem credentials: %#v", cfg.Payment)
	}
	if cfg.Payment.CreemAPIBaseURL != "https://test-api.creem.io" || cfg.Payment.CreemSuccessURL == "" || cfg.Payment.CreemCancelURL == "" {
		t.Fatalf("unexpected creem urls: %#v", cfg.Payment)
	}
	if cfg.Payment.CreemProductMapJSON == "" {
		t.Fatalf("expected creem product map json")
	}
	if cfg.Payment.CreemSandbox {
		t.Fatalf("expected PAYMENT_CREEM_SANDBOX=false to override default")
	}
}
