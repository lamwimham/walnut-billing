package config

import (
	"errors"
	"strings"
	"testing"
)

func TestProductionConfigValidationSkipsNonProduction(t *testing.T) {
	cfg := &Config{Server: ServerConfig{Env: "dev"}}

	if err := ValidateProduction(cfg); err != nil {
		t.Fatalf("expected dev config to skip production validation, got %v", err)
	}
}

func TestProductionConfigValidationRejectsMissingCriticalSettings(t *testing.T) {
	cfg := minimalValidProductionConfig()
	cfg.Database.DSN = ":memory:"
	cfg.Database.MigrationMode = DatabaseMigrationModeAuto
	cfg.HTTP.CORSAllowedOrigins = []string{"*", "http://walnut.example"}
	cfg.HTTP.SecurityHeaders.Enabled = false
	cfg.HTTP.SecurityHeaders.HSTSMaxAgeSeconds = 300
	cfg.Admin = AdminConfig{}
	cfg.RateLimit.Enabled = false
	cfg.Checkout.RiskPolicyEnabled = false
	cfg.Checkout.RedirectAllowlist = []string{"http://walnut.example"}
	cfg.Access.SnapshotSignatureAlgorithm = "HS256"
	cfg.Access.SnapshotPrivateKey = ""
	cfg.Access.SnapshotKeyID = "dev"
	cfg.Access.SnapshotSecret = defaultDevAccessSnapshotSecret
	cfg.Access.LoginChallengeDelivery = "dev"
	cfg.Access.LoginChallengeSecret = defaultDevLoginChallengeSecret
	cfg.Access.CloudStorageQuotaMB = 0
	cfg.Access.CloudStorageTrialQuotaMB = -1
	cfg.Payment.CreemAPIKey = "creem_test_key"
	cfg.Payment.CreemWebhookSecret = ""
	cfg.Payment.CreemSandbox = true
	cfg.Payment.CreemAPIBaseURL = creemDefaultTestBaseURL
	cfg.Payment.CreemSuccessURL = "walnut://checkout/success"
	cfg.Payment.CreemCancelURL = ""
	cfg.Payment.CreemProductMapJSON = `{"pro_own_ai_monthly":"prod_monthly"}`

	err := ValidateProduction(cfg)
	if !errors.Is(err, ErrInvalidProductionConfig) {
		t.Fatalf("expected ErrInvalidProductionConfig, got %v", err)
	}
	for _, want := range []string{
		"DATABASE_DSN must not use in-memory sqlite in prod",
		"DATABASE_MIGRATION_MODE must be versioned in prod",
		"HTTP_CORS_ALLOWED_ORIGINS must not contain wildcard origins in prod",
		"HTTP_CORS_ALLOWED_ORIGINS must contain https origins only in prod",
		"HTTP_SECURITY_HEADERS_ENABLED must be true in prod",
		"HTTP_SECURITY_HEADERS_HSTS_MAX_AGE_SECONDS must be >= 31536000 in prod",
		"ADMIN_API_KEYS or ADMIN_PRINCIPALS_JSON is required in prod",
		"RATELIMIT_ENABLED must be true in prod",
		"CHECKOUT_RISK_POLICY_ENABLED must be true in prod",
		"CHECKOUT_REDIRECT_ALLOWLIST must contain https or app-scheme URLs in prod",
		"ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM must be Ed25519 or EdDSA in prod",
		"ACCESS_SNAPSHOT_PRIVATE_KEY is required in prod",
		"ACCESS_LOGIN_CHALLENGE_DELIVERY must not be dev in prod",
		"ACCESS_LOGIN_CHALLENGE_SECRET must be non-dev in prod",
		"ACCESS_CLOUD_STORAGE_QUOTA_MB must be > 0 in prod",
		"ACCESS_CLOUD_STORAGE_*_QUOTA_MB must be >= 0 in prod",
		"PAYMENT_CREEM_WEBHOOK_SECRET is required in prod",
		"PAYMENT_CREEM_SANDBOX must be false in prod",
		"PAYMENT_CREEM_API_BASE_URL must target https://api.creem.io in prod",
		"PAYMENT_CREEM_API_KEY must not be a test key in prod",
		"PAYMENT_CREEM_PRODUCT_MAP_JSON must map pro_own_ai_lifetime in prod",
		"PAYMENT_CREEM_SUCCESS_URL must be an https URL in prod",
		"PAYMENT_CREEM_CANCEL_URL is required in prod",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected violation %q in %v", want, err)
		}
	}
}

func TestProductionConfigValidationRejectsUnsafeRedirectSchemes(t *testing.T) {
	cfg := minimalValidProductionConfig()
	cfg.Checkout.RedirectAllowlist = []string{"javascript://checkout/success"}

	err := ValidateProduction(cfg)
	if !errors.Is(err, ErrInvalidProductionConfig) || !strings.Contains(err.Error(), "CHECKOUT_REDIRECT_ALLOWLIST must contain https or app-scheme URLs in prod") {
		t.Fatalf("expected unsafe redirect scheme violation, got %v", err)
	}
}

func TestProductionConfigValidationAcceptsCompleteProductionConfig(t *testing.T) {
	cfg := minimalValidProductionConfig()

	if err := ValidateProduction(cfg); err != nil {
		t.Fatalf("expected valid production config, got %v", err)
	}
}

func TestProductionConfigValidationAcceptsScopedAdminPrincipalAndWrappedProductMap(t *testing.T) {
	cfg := minimalValidProductionConfig()
	cfg.Admin.APIKeys = nil
	cfg.Admin.Principals = []AdminPrincipalConfig{{
		Name:        "support",
		Key:         "support-key",
		Permissions: []string{"admin.dashboard.read"},
	}}
	cfg.Payment.CreemProductMapJSON = `{"products":{"pro_own_ai_monthly":"prod_monthly","pro_own_ai_lifetime":"prod_lifetime"}}`

	if err := ValidateProduction(cfg); err != nil {
		t.Fatalf("expected scoped production config, got %v", err)
	}
}

func TestLoadFailsFastForInvalidProductionEnvironment(t *testing.T) {
	t.Setenv("SERVER_ENV", "prod")
	t.Setenv("ADMIN_API_KEYS", "")
	t.Setenv("RATELIMIT_ENABLED", "false")

	_, err := Load()
	if !errors.Is(err, ErrInvalidProductionConfig) {
		t.Fatalf("expected load to fail with production config error, got %v", err)
	}
}

func minimalValidProductionConfig() *Config {
	return &Config{
		Server:   ServerConfig{Env: "prod"},
		Database: DatabaseConfig{Driver: "sqlite", DSN: "./data/walnut_billing.db", MigrationMode: DatabaseMigrationModeVersioned},
		HTTP: HTTPConfig{
			CORSAllowedOrigins: []string{"https://app.walnut.example", "https://ops.walnut.example"},
			SecurityHeaders: HTTPSecurityHeadersConfig{
				Enabled:           true,
				HSTSMaxAgeSeconds: 31536000,
			},
		},
		Admin: AdminConfig{APIKeys: []string{"ops-key"}},
		RateLimit: RateLimitConfig{
			Enabled:    true,
			MaxTokens:  20,
			RefillRate: 2,
		},
		Checkout: CheckoutConfig{
			RiskPolicyEnabled: true,
			RedirectAllowlist: []string{"https://walnut.example"},
		},
		Access: AccessConfig{
			SnapshotSignatureAlgorithm:  "Ed25519",
			SnapshotPrivateKey:          "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
			SnapshotKeyID:               "kid-2026-06",
			SnapshotSecret:              "non-default-prod-secret",
			LoginChallengeDelivery:      "email",
			LoginChallengeSecret:        "non-default-login-secret",
			CloudStorageQuotaMB:         1024,
			CloudStorageTrialQuotaMB:    256,
			CloudStorageMonthlyQuotaMB:  1024,
			CloudStorageLifetimeQuotaMB: 2048,
		},
		Payment: PaymentConfig{
			CreemAPIKey:         "creem_live_key",
			CreemWebhookSecret:  "whsec_live",
			CreemSandbox:        false,
			CreemSuccessURL:     "https://walnut.example/checkout/success",
			CreemCancelURL:      "https://walnut.example/checkout/cancel",
			CreemProductMapJSON: `{"pro_own_ai_monthly":"prod_monthly","pro_own_ai_lifetime":"prod_lifetime"}`,
		},
	}
}
