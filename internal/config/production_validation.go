package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
)

const (
	ProductionEnv = "prod"

	creemDefaultProdBaseURL = "https://api.creem.io"
	creemDefaultTestBaseURL = "https://test-api.creem.io"

	defaultDevAccessSnapshotSecret  = "walnut-dev-access-snapshot-secret"
	defaultDevAccessSnapshotKeyID   = "dev"
	defaultDevLoginChallengeSecret  = "walnut-dev-login-challenge-secret"
	defaultDevLoginChallengeAdapter = "dev"
)

var (
	ErrInvalidProductionConfig = errors.New("invalid production config")

	productionCheckoutSKUCodes = []string{
		domain.SKUProOwnAIMonthly,
		domain.SKUProOwnAILifetime,
	}
)

// ValidateProduction keeps launch-safety rules at the configuration boundary.
// Business services receive already-vetted config and stay provider-neutral.
func ValidateProduction(cfg *Config) error {
	if cfg == nil || !isProductionEnv(cfg.Server.Env) {
		return nil
	}

	var violations []string
	violations = append(violations, validateProductionDatabase(cfg.Database)...)
	violations = append(violations, validateProductionHTTP(cfg.HTTP)...)
	violations = append(violations, validateProductionAdmin(cfg.Admin)...)
	violations = append(violations, validateProductionRateLimit(cfg.RateLimit)...)
	violations = append(violations, validateProductionCheckout(cfg.Checkout)...)
	violations = append(violations, validateProductionAccess(cfg.Access)...)
	violations = append(violations, validateProductionCreem(cfg.Payment)...)

	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidProductionConfig, strings.Join(violations, "; "))
}

func validateProductionDatabase(cfg DatabaseConfig) []string {
	var violations []string
	if strings.TrimSpace(cfg.DSN) == "" {
		violations = append(violations, "DATABASE_DSN is required")
	}
	if strings.TrimSpace(cfg.DSN) == ":memory:" {
		violations = append(violations, "DATABASE_DSN must not use in-memory sqlite in prod")
	}
	if strings.TrimSpace(cfg.MigrationMode) != DatabaseMigrationModeVersioned {
		violations = append(violations, "DATABASE_MIGRATION_MODE must be versioned in prod")
	}
	return violations
}

func validateProductionHTTP(cfg HTTPConfig) []string {
	var violations []string
	if len(cfg.CORSAllowedOrigins) == 0 {
		violations = append(violations, "HTTP_CORS_ALLOWED_ORIGINS is required in prod")
	}
	for _, origin := range cfg.CORSAllowedOrigins {
		violations = append(violations, validateProductionCORSOrigin(origin)...)
	}
	if !cfg.SecurityHeaders.Enabled {
		violations = append(violations, "HTTP_SECURITY_HEADERS_ENABLED must be true in prod")
	}
	if cfg.SecurityHeaders.HSTSMaxAgeSeconds < 31536000 {
		violations = append(violations, "HTTP_SECURITY_HEADERS_HSTS_MAX_AGE_SECONDS must be >= 31536000 in prod")
	}
	return violations
}

func validateProductionAdmin(cfg AdminConfig) []string {
	if hasAdminAPIKey(cfg) || hasScopedAdminPrincipal(cfg) {
		return nil
	}
	return []string{"ADMIN_API_KEYS or ADMIN_PRINCIPALS_JSON is required in prod"}
}

func validateProductionRateLimit(cfg RateLimitConfig) []string {
	var violations []string
	if !cfg.Enabled {
		violations = append(violations, "RATELIMIT_ENABLED must be true in prod")
	}
	if cfg.MaxTokens <= 0 {
		violations = append(violations, "RATELIMIT_MAX_TOKENS must be > 0 in prod")
	}
	if cfg.RefillRate <= 0 {
		violations = append(violations, "RATELIMIT_REFILL_RATE must be > 0 in prod")
	}
	return violations
}

func validateProductionCheckout(cfg CheckoutConfig) []string {
	var violations []string
	if !cfg.RiskPolicyEnabled {
		violations = append(violations, "CHECKOUT_RISK_POLICY_ENABLED must be true in prod")
	}
	if len(cfg.RedirectAllowlist) == 0 {
		violations = append(violations, "CHECKOUT_REDIRECT_ALLOWLIST is required in prod")
	}
	for _, rawURL := range cfg.RedirectAllowlist {
		violations = append(violations, validateProductionRedirectAllowlistURL(rawURL)...)
	}
	return violations
}

func validateProductionAccess(cfg AccessConfig) []string {
	var violations []string
	algorithm := strings.TrimSpace(cfg.SnapshotSignatureAlgorithm)
	if algorithm != "Ed25519" && algorithm != "EdDSA" {
		violations = append(violations, "ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM must be Ed25519 or EdDSA in prod")
	}
	if strings.TrimSpace(cfg.SnapshotPrivateKey) == "" {
		violations = append(violations, "ACCESS_SNAPSHOT_PRIVATE_KEY is required in prod")
	} else if !validProductionEd25519PrivateKey(cfg.SnapshotPrivateKey) {
		violations = append(violations, "ACCESS_SNAPSHOT_PRIVATE_KEY must be a base64 Ed25519 private key or seed in prod")
	}
	if keyID := strings.TrimSpace(cfg.SnapshotKeyID); keyID == "" || keyID == defaultDevAccessSnapshotKeyID {
		violations = append(violations, "ACCESS_SNAPSHOT_KEY_ID must be non-dev in prod")
	}
	if strings.TrimSpace(cfg.SnapshotSecret) == defaultDevAccessSnapshotSecret {
		violations = append(violations, "ACCESS_SNAPSHOT_SECRET must not use the dev default in prod")
	}
	if strings.TrimSpace(cfg.LoginChallengeDelivery) == "" || strings.TrimSpace(cfg.LoginChallengeDelivery) == defaultDevLoginChallengeAdapter {
		violations = append(violations, "ACCESS_LOGIN_CHALLENGE_DELIVERY must not be dev in prod")
	}
	if strings.TrimSpace(cfg.LoginChallengeSecret) == "" || strings.TrimSpace(cfg.LoginChallengeSecret) == defaultDevLoginChallengeSecret {
		violations = append(violations, "ACCESS_LOGIN_CHALLENGE_SECRET must be non-dev in prod")
	}
	return violations
}

func validateProductionCreem(cfg PaymentConfig) []string {
	var violations []string
	if strings.TrimSpace(cfg.CreemAPIKey) == "" {
		violations = append(violations, "PAYMENT_CREEM_API_KEY is required in prod")
	}
	if strings.TrimSpace(cfg.CreemWebhookSecret) == "" {
		violations = append(violations, "PAYMENT_CREEM_WEBHOOK_SECRET is required in prod")
	}
	if cfg.CreemSandbox {
		violations = append(violations, "PAYMENT_CREEM_SANDBOX must be false in prod")
	}
	violations = append(violations, validateProductionCreemEnvironment(cfg)...)
	productMap, err := parseProductionCreemProductMap(cfg.CreemProductMapJSON)
	if err != nil {
		violations = append(violations, "PAYMENT_CREEM_PRODUCT_MAP_JSON must be valid SKU to product JSON")
	} else {
		violations = append(violations, missingProductionSKUMappings(productMap)...)
	}
	violations = append(violations, validateProductionCheckoutURL("PAYMENT_CREEM_SUCCESS_URL", cfg.CreemSuccessURL)...)
	violations = append(violations, validateProductionCheckoutURL("PAYMENT_CREEM_CANCEL_URL", cfg.CreemCancelURL)...)
	return violations
}

func validateProductionCreemEnvironment(cfg PaymentConfig) []string {
	var violations []string
	baseURL := normalizeProductionCreemAPIBaseURL(cfg.CreemAPIBaseURL, cfg.CreemSandbox)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		violations = append(violations, "PAYMENT_CREEM_API_BASE_URL must be an https URL in prod")
	}
	base := strings.ToLower(baseURL)
	key := strings.ToLower(strings.TrimSpace(cfg.CreemAPIKey))
	if strings.Contains(base, "test-api.creem.io") {
		violations = append(violations, fmt.Sprintf("PAYMENT_CREEM_API_BASE_URL must target %s in prod", creemDefaultProdBaseURL))
	}
	if strings.HasPrefix(key, "creem_test") {
		violations = append(violations, "PAYMENT_CREEM_API_KEY must not be a test key in prod")
	}
	return violations
}

func validateProductionCORSOrigin(origin string) []string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return []string{"HTTP_CORS_ALLOWED_ORIGINS must not contain empty origins in prod"}
	}
	if origin == "*" {
		return []string{"HTTP_CORS_ALLOWED_ORIGINS must not contain wildcard origins in prod"}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return []string{"HTTP_CORS_ALLOWED_ORIGINS must contain https origins only in prod"}
	}
	return nil
}

func validateProductionCheckoutURL(fieldName string, rawURL string) []string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return []string{fieldName + " is required in prod"}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return []string{fieldName + " must be an https URL in prod"}
	}
	return nil
}

func validateProductionRedirectAllowlistURL(rawURL string) []string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return []string{"CHECKOUT_REDIRECT_ALLOWLIST must not contain empty URLs in prod"}
	}
	parsed, err := url.Parse(rawURL)
	scheme := ""
	if parsed != nil {
		scheme = strings.ToLower(strings.TrimSpace(parsed.Scheme))
	}
	if err != nil || scheme == "" || unsafeRedirectScheme(scheme) {
		return []string{"CHECKOUT_REDIRECT_ALLOWLIST must contain https or app-scheme URLs in prod"}
	}
	if scheme == "https" && parsed.Host == "" {
		return []string{"CHECKOUT_REDIRECT_ALLOWLIST must contain https or app-scheme URLs in prod"}
	}
	return nil
}

func unsafeRedirectScheme(scheme string) bool {
	switch scheme {
	case "http", "javascript", "data", "file", "about", "blob":
		return true
	default:
		return false
	}
}

func hasAdminAPIKey(cfg AdminConfig) bool {
	for _, key := range cfg.APIKeys {
		if strings.TrimSpace(key) != "" {
			return true
		}
	}
	return false
}

func hasScopedAdminPrincipal(cfg AdminConfig) bool {
	for _, principal := range cfg.Principals {
		if strings.TrimSpace(principal.Key) == "" {
			continue
		}
		for _, permission := range principal.Permissions {
			if strings.TrimSpace(permission) != "" {
				return true
			}
		}
	}
	return false
}

func isProductionEnv(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), ProductionEnv)
}

func normalizeProductionCreemAPIBaseURL(baseURL string, sandbox bool) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		if sandbox {
			return creemDefaultTestBaseURL
		}
		return creemDefaultProdBaseURL
	}
	return strings.TrimRight(strings.TrimSuffix(baseURL, "/v1"), "/")
}

func parseProductionCreemProductMap(rawJSON string) (map[string]string, error) {
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON == "" {
		return nil, errors.New("empty product map")
	}
	return payment.ParseCreemProductMap(nil, rawJSON)
}

func validProductionEd25519PrivateKey(value string) bool {
	raw, err := decodeProductionBase64(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return len(raw) == ed25519.PrivateKeySize || len(raw) == ed25519.SeedSize
}

func decodeProductionBase64(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func missingProductionSKUMappings(productMap map[string]string) []string {
	var violations []string
	for _, sku := range productionCheckoutSKUCodes {
		if strings.TrimSpace(productMap[sku]) == "" {
			violations = append(violations, fmt.Sprintf("PAYMENT_CREEM_PRODUCT_MAP_JSON must map %s in prod", sku))
		}
	}
	return violations
}
