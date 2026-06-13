package config

import (
	"os"
	"strings"

	"walnut-billing/internal/payment"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig
	Database    DatabaseConfig
	Payment     PaymentConfig
	Fulfillment FulfillmentConfig
	Checkout    CheckoutConfig
	Admin       AdminConfig
	RateLimit   RateLimitConfig
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

type AdminConfig struct {
	APIKeys []string // List of allowed admin API keys
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
	v.SetDefault("fulfillment.rules_json", "")
	v.SetDefault("checkout.risk_policy_enabled", true)
	v.SetDefault("checkout.risk_block_severities", []string{})

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
	if val := os.Getenv("SERVER_ENV"); val != "" {
		cfg.Server.Env = val
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
