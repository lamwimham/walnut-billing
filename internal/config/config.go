package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"walnut-billing/internal/payment"

	"github.com/spf13/viper"
)

type Config struct {
	Server       ServerConfig
	Database     DatabaseConfig
	Payment      PaymentConfig
	Fulfillment  FulfillmentConfig
	Checkout     CheckoutConfig
	Adjustment   AdjustmentConfig
	Renewal      RenewalConfig
	Access       AccessConfig
	CloudStorage CloudStorageConfig
	Admin        AdminConfig
	RateLimit    RateLimitConfig
}

type ServerConfig struct {
	Port string
	Env  string // dev, prod
}

type DatabaseConfig struct {
	Driver string
	DSN    string
}

type PaymentConfig struct {
	WechatMchID         string
	WechatSerialNo      string
	WechatPrivateKey    string // PEM encoded
	WechatAPIv3Key      string
	WechatAppID         string
	WechatSandbox       bool
	AlipayAppID         string
	AlipayPrivateKey    string // PEM encoded
	AlipayPublicKey     string // Alipay public key (for callback verification)
	AlipaySandbox       bool
	CreemAPIKey         string
	CreemWebhookSecret  string
	CreemAPIBaseURL     string
	CreemSuccessURL     string
	CreemCancelURL      string
	CreemProductMapJSON string
	CreemSandbox        bool
	MockCheckoutBaseURL string
}

// WechatConfig returns a payment.WechatPayV3Config from the current config.
func (p PaymentConfig) WechatConfig() payment.WechatPayV3Config {
	return payment.WechatPayV3Config{
		AppID:       p.WechatAppID,
		MchID:       p.WechatMchID,
		SerialNo:    p.WechatSerialNo,
		PrivateKey:  p.WechatPrivateKey,
		APIv3Key:    p.WechatAPIv3Key,
		NotifyURL:   "", // Set by caller
		SandboxMode: p.WechatSandbox,
	}
}

// AlipayConfig returns a payment.AlipayV2Config from the current config.
func (p PaymentConfig) AlipayConfig() payment.AlipayV2Config {
	return payment.AlipayV2Config{
		AppID:       p.AlipayAppID,
		PrivateKey:  p.AlipayPrivateKey,
		PublicKey:   p.AlipayPublicKey,
		NotifyURL:   "", // Set by caller
		SandboxMode: p.AlipaySandbox,
	}
}

// CreemConfig returns a payment.CreemConfig from the current config.
func (p PaymentConfig) CreemConfig() payment.CreemConfig {
	return payment.CreemConfig{
		APIKey:         p.CreemAPIKey,
		WebhookSecret:  p.CreemWebhookSecret,
		APIBaseURL:     p.CreemAPIBaseURL,
		SuccessURL:     p.CreemSuccessURL,
		CancelURL:      p.CreemCancelURL,
		ProductMapJSON: p.CreemProductMapJSON,
		SandboxMode:    p.CreemSandbox,
	}
}

type FulfillmentConfig struct {
	RulesJSON string
}

type CheckoutConfig struct {
	RiskPolicyEnabled   bool
	RiskBlockSeverities []string
}

type AdjustmentConfig struct {
	RefundWindowDays        int
	RefundInWindowAction    string
	RefundOutOfWindowAction string
	LowUsagePolicyEnabled   bool
	LowUsageMaxCreditsUsed  int64
	LowUsageAction          string
	HighUsageAction         string
	DisputeAction           string
	CancelAction            string
}

type RenewalConfig struct {
	GracePeriodDays int
	ExpiredAction   string
}

type AccessConfig struct {
	SnapshotSignatureAlgorithm           string
	SnapshotSecret                       string
	SnapshotPrivateKey                   string
	SnapshotKeyID                        string
	SnapshotTTLSeconds                   int
	SnapshotOfflineGraceSeconds          int
	MaxDevices                           int
	CloudStorageQuotaMB                  int64
	TrialDurationDays                    int
	LoginChallengeTTLSeconds             int
	LoginChallengeMaxAttempts            int
	LoginChallengeRateLimitWindowSeconds int
	LoginChallengeMaxCreatesPerEmail     int
	LoginChallengeMaxCreatesPerIP        int
	LoginChallengeDelivery               string
	LoginChallengeSecret                 string
}

type CloudStorageConfig struct {
	Provider string
}

type AdminConfig struct {
	APIKeys    []string               // Legacy full-access admin API keys
	Principals []AdminPrincipalConfig // Permission-scoped admin API keys
}

type AdminPrincipalConfig struct {
	Name        string   `json:"name" mapstructure:"name"`
	Key         string   `json:"key" mapstructure:"key"`
	Permissions []string `json:"permissions" mapstructure:"permissions"`
}

type RateLimitConfig struct {
	Enabled    bool
	MaxTokens  float64 // Burst size
	RefillRate float64 // Tokens per second
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// 默认配置
	v.SetDefault("server.port", "8082")
	v.SetDefault("server.env", "dev")
	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.dsn", "./walnut_billing.db")
	v.SetDefault("admin.api_keys", []string{})
	v.SetDefault("admin.principals", []AdminPrincipalConfig{})
	v.SetDefault("ratelimit.enabled", false)
	v.SetDefault("ratelimit.max_tokens", 100.0)
	v.SetDefault("ratelimit.refill_rate", 10.0)
	v.SetDefault("payment.wechat_mch_id", "")
	v.SetDefault("payment.wechat_serial_no", "")
	v.SetDefault("payment.wechat_private_key", "")
	v.SetDefault("payment.wechat_api_v3_key", "")
	v.SetDefault("payment.wechat_app_id", "")
	v.SetDefault("payment.wechat_sandbox", false)
	v.SetDefault("payment.alipay_app_id", "")
	v.SetDefault("payment.alipay_private_key", "")
	v.SetDefault("payment.alipay_public_key", "")
	v.SetDefault("payment.alipay_sandbox", false)
	v.SetDefault("payment.creem_api_key", "")
	v.SetDefault("payment.creem_webhook_secret", "")
	v.SetDefault("payment.creem_api_base_url", "")
	v.SetDefault("payment.creem_success_url", "")
	v.SetDefault("payment.creem_cancel_url", "")
	v.SetDefault("payment.creem_product_map_json", "")
	v.SetDefault("payment.creem_sandbox", true)
	v.SetDefault("payment.mock_checkout_base_url", "")
	v.SetDefault("fulfillment.rules_json", "")
	v.SetDefault("checkout.risk_policy_enabled", true)
	v.SetDefault("checkout.risk_block_severities", []string{})
	v.SetDefault("adjustment.refund_window_days", 7)
	v.SetDefault("adjustment.refund_in_window_action", "auto_refund")
	v.SetDefault("adjustment.refund_out_of_window_action", "manual_review")
	v.SetDefault("adjustment.low_usage_policy_enabled", false)
	v.SetDefault("adjustment.low_usage_max_credits_used", int64(0))
	v.SetDefault("adjustment.low_usage_action", "auto_refund")
	v.SetDefault("adjustment.high_usage_action", "manual_review")
	v.SetDefault("adjustment.dispute_action", "auto_refund")
	v.SetDefault("adjustment.cancel_action", "keep_current_period")
	v.SetDefault("renewal.grace_period_days", 3)
	v.SetDefault("renewal.expired_action", "expire_grace")
	v.SetDefault("access.snapshot_signature_algorithm", "HS256")
	v.SetDefault("access.snapshot_secret", "walnut-dev-access-snapshot-secret")
	v.SetDefault("access.snapshot_private_key", "")
	v.SetDefault("access.snapshot_key_id", "dev")
	v.SetDefault("access.snapshot_ttl_seconds", 86400)
	v.SetDefault("access.snapshot_offline_grace_seconds", 604800)
	v.SetDefault("access.max_devices", 2)
	v.SetDefault("access.cloud_storage_quota_mb", int64(1024))
	v.SetDefault("access.trial_duration_days", 14)
	v.SetDefault("access.login_challenge_ttl_seconds", 600)
	v.SetDefault("access.login_challenge_max_attempts", 5)
	v.SetDefault("access.login_challenge_rate_limit_window_seconds", 600)
	v.SetDefault("access.login_challenge_max_creates_per_email", 5)
	v.SetDefault("access.login_challenge_max_creates_per_ip", 20)
	v.SetDefault("access.login_challenge_delivery", "dev")
	v.SetDefault("access.login_challenge_secret", "walnut-dev-login-challenge-secret")
	v.SetDefault("cloudstorage.provider", "")

	// 读取环境变量或配置文件
	v.AutomaticEnv()
	if err := v.ReadInConfig(); err != nil {
		// 如果没有配置文件则忽略，使用默认值和环境变量
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Post-process: parse comma-separated env vars that Viper can't handle
	if val := os.Getenv("ADMIN_API_KEYS"); val != "" {
		cfg.Admin.APIKeys = splitCSV(val)
	}
	if val := os.Getenv("ADMIN_PRINCIPALS_JSON"); val != "" {
		principals, err := parseAdminPrincipalsJSON(val)
		if err != nil {
			return nil, err
		}
		cfg.Admin.Principals = principals
	}
	if val := os.Getenv("CHECKOUT_RISK_BLOCK_SEVERITIES"); val != "" {
		cfg.Checkout.RiskBlockSeverities = splitCSV(val)
	}

	// Boolean env var overrides (Viper can't parse bools from env reliably)
	if val := os.Getenv("RATELIMIT_ENABLED"); val == "true" {
		cfg.RateLimit.Enabled = true
	} else if val == "false" {
		cfg.RateLimit.Enabled = false
	}
	if val := os.Getenv("CHECKOUT_RISK_POLICY_ENABLED"); val == "true" {
		cfg.Checkout.RiskPolicyEnabled = true
	} else if val == "false" {
		cfg.Checkout.RiskPolicyEnabled = false
	}
	if val := os.Getenv("ADJUSTMENT_LOW_USAGE_POLICY_ENABLED"); val == "true" {
		cfg.Adjustment.LowUsagePolicyEnabled = true
	} else if val == "false" {
		cfg.Adjustment.LowUsagePolicyEnabled = false
	}
	if val := os.Getenv("SERVER_ENV"); val != "" {
		cfg.Server.Env = val
	}
	if val := os.Getenv("ADJUSTMENT_REFUND_WINDOW_DAYS"); val != "" {
		cfg.Adjustment.RefundWindowDays = parseIntEnv(val, cfg.Adjustment.RefundWindowDays)
	}
	if val := os.Getenv("ADJUSTMENT_LOW_USAGE_MAX_CREDITS_USED"); val != "" {
		cfg.Adjustment.LowUsageMaxCreditsUsed = int64(parseIntEnv(val, int(cfg.Adjustment.LowUsageMaxCreditsUsed)))
	}
	if val := os.Getenv("ADJUSTMENT_REFUND_IN_WINDOW_ACTION"); val != "" {
		cfg.Adjustment.RefundInWindowAction = val
	}
	if val := os.Getenv("ADJUSTMENT_REFUND_OUT_OF_WINDOW_ACTION"); val != "" {
		cfg.Adjustment.RefundOutOfWindowAction = val
	}
	if val := os.Getenv("ADJUSTMENT_LOW_USAGE_ACTION"); val != "" {
		cfg.Adjustment.LowUsageAction = val
	}
	if val := os.Getenv("ADJUSTMENT_HIGH_USAGE_ACTION"); val != "" {
		cfg.Adjustment.HighUsageAction = val
	}
	if val := os.Getenv("ADJUSTMENT_DISPUTE_ACTION"); val != "" {
		cfg.Adjustment.DisputeAction = val
	}
	if val := os.Getenv("ADJUSTMENT_CANCEL_ACTION"); val != "" {
		cfg.Adjustment.CancelAction = val
	}
	if val := os.Getenv("RENEWAL_GRACE_PERIOD_DAYS"); val != "" {
		cfg.Renewal.GracePeriodDays = parseIntEnv(val, cfg.Renewal.GracePeriodDays)
	}
	if val := os.Getenv("RENEWAL_EXPIRED_ACTION"); val != "" {
		cfg.Renewal.ExpiredAction = val
	}
	if val := os.Getenv("ACCESS_SNAPSHOT_SIGNATURE_ALGORITHM"); val != "" {
		cfg.Access.SnapshotSignatureAlgorithm = val
	}
	if val := os.Getenv("ACCESS_SNAPSHOT_SECRET"); val != "" {
		cfg.Access.SnapshotSecret = val
	}
	if val := os.Getenv("ACCESS_SNAPSHOT_PRIVATE_KEY"); val != "" {
		cfg.Access.SnapshotPrivateKey = val
	}
	if val := os.Getenv("ACCESS_SNAPSHOT_KEY_ID"); val != "" {
		cfg.Access.SnapshotKeyID = val
	}
	if val := os.Getenv("ACCESS_SNAPSHOT_TTL_SECONDS"); val != "" {
		cfg.Access.SnapshotTTLSeconds = parseIntEnv(val, cfg.Access.SnapshotTTLSeconds)
	}
	if val := os.Getenv("ACCESS_SNAPSHOT_OFFLINE_GRACE_SECONDS"); val != "" {
		cfg.Access.SnapshotOfflineGraceSeconds = parseIntEnv(val, cfg.Access.SnapshotOfflineGraceSeconds)
	}
	if val := os.Getenv("ACCESS_MAX_DEVICES"); val != "" {
		cfg.Access.MaxDevices = parseIntEnv(val, cfg.Access.MaxDevices)
	}
	if val := os.Getenv("ACCESS_CLOUD_STORAGE_QUOTA_MB"); val != "" {
		cfg.Access.CloudStorageQuotaMB = int64(parseIntEnv(val, int(cfg.Access.CloudStorageQuotaMB)))
	}
	if val := os.Getenv("ACCESS_TRIAL_DURATION_DAYS"); val != "" {
		cfg.Access.TrialDurationDays = parseIntEnv(val, cfg.Access.TrialDurationDays)
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_TTL_SECONDS"); val != "" {
		cfg.Access.LoginChallengeTTLSeconds = parseIntEnv(val, cfg.Access.LoginChallengeTTLSeconds)
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_MAX_ATTEMPTS"); val != "" {
		cfg.Access.LoginChallengeMaxAttempts = parseIntEnv(val, cfg.Access.LoginChallengeMaxAttempts)
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_RATE_LIMIT_WINDOW_SECONDS"); val != "" {
		cfg.Access.LoginChallengeRateLimitWindowSeconds = parseIntEnv(val, cfg.Access.LoginChallengeRateLimitWindowSeconds)
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_EMAIL"); val != "" {
		cfg.Access.LoginChallengeMaxCreatesPerEmail = parseIntEnv(val, cfg.Access.LoginChallengeMaxCreatesPerEmail)
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_MAX_CREATES_PER_IP"); val != "" {
		cfg.Access.LoginChallengeMaxCreatesPerIP = parseIntEnv(val, cfg.Access.LoginChallengeMaxCreatesPerIP)
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_DELIVERY"); val != "" {
		cfg.Access.LoginChallengeDelivery = val
	}
	if val := os.Getenv("ACCESS_LOGIN_CHALLENGE_SECRET"); val != "" {
		cfg.Access.LoginChallengeSecret = val
	}
	if val := os.Getenv("CLOUD_STORAGE_PROVIDER"); val != "" {
		cfg.CloudStorage.Provider = val
	}
	if val := os.Getenv("FULFILLMENT_RULES_JSON"); val != "" {
		cfg.Fulfillment.RulesJSON = val
	}
	if val := os.Getenv("PAYMENT_CREEM_API_KEY"); val != "" {
		cfg.Payment.CreemAPIKey = val
	}
	if val := os.Getenv("PAYMENT_CREEM_WEBHOOK_SECRET"); val != "" {
		cfg.Payment.CreemWebhookSecret = val
	}
	if val := os.Getenv("PAYMENT_CREEM_API_BASE_URL"); val != "" {
		cfg.Payment.CreemAPIBaseURL = val
	}
	if val := os.Getenv("PAYMENT_CREEM_SUCCESS_URL"); val != "" {
		cfg.Payment.CreemSuccessURL = val
	}
	if val := os.Getenv("PAYMENT_CREEM_CANCEL_URL"); val != "" {
		cfg.Payment.CreemCancelURL = val
	}
	if val := os.Getenv("PAYMENT_CREEM_PRODUCT_MAP_JSON"); val != "" {
		cfg.Payment.CreemProductMapJSON = val
	}
	if val := os.Getenv("PAYMENT_CREEM_SANDBOX"); val == "true" {
		cfg.Payment.CreemSandbox = true
	} else if val == "false" {
		cfg.Payment.CreemSandbox = false
	}
	if val := os.Getenv("PAYMENT_MOCK_CHECKOUT_BASE_URL"); val != "" {
		cfg.Payment.MockCheckoutBaseURL = val
	}

	return cfg, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseIntEnv(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseAdminPrincipalsJSON(value string) ([]AdminPrincipalConfig, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var principals []AdminPrincipalConfig
	if err := json.Unmarshal([]byte(value), &principals); err != nil {
		return nil, fmt.Errorf("parse ADMIN_PRINCIPALS_JSON: %w", err)
	}
	for i := range principals {
		principals[i].Name = strings.TrimSpace(principals[i].Name)
		principals[i].Key = strings.TrimSpace(principals[i].Key)
		principals[i].Permissions = splitAndTrim(principals[i].Permissions)
	}
	return principals, nil
}

func splitAndTrim(values []string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			items = append(items, value)
		}
	}
	return items
}
