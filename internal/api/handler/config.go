package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

// PaymentConfigHandler handles runtime payment configuration management.
// Uses the Command pattern for hot-swapping adapters.
type PaymentConfigHandler struct {
	PaymentSvc *payment.PaymentService
	AuditSvc   service.AuditService
}

func NewPaymentConfigHandler(paymentSvc *payment.PaymentService, auditSvc service.AuditService) *PaymentConfigHandler {
	return &PaymentConfigHandler{PaymentSvc: paymentSvc, AuditSvc: auditSvc}
}

// GetProviderStatus GET /api/v1/admin/payment/providers
// Returns the status of all registered payment providers.
func (h *PaymentConfigHandler) GetProviderStatus(c *gin.Context) {
	status := h.PaymentSvc.Registry().Status()
	c.JSON(http.StatusOK, gin.H{
		"providers": status,
		"count":     len(status),
	})
}

// UpdateWechatConfig PUT /api/v1/admin/payment/wechat
// Hot-reload WeChat Pay configuration without server restart.
func (h *PaymentConfigHandler) UpdateWechatConfig(c *gin.Context) {
	var req struct {
		AppID      string `json:"app_id"`
		MchID      string `json:"mch_id"`
		SerialNo   string `json:"serial_no"`
		PrivateKey string `json:"private_key"`
		APIv3Key   string `json:"api_v3_key"`
		Sandbox    bool   `json:"sandbox"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := payment.WechatPayV3Config{
		AppID:       req.AppID,
		MchID:       req.MchID,
		SerialNo:    req.SerialNo,
		PrivateKey:  req.PrivateKey,
		APIv3Key:    req.APIv3Key,
		NotifyURL:   "", // Keep existing
		SandboxMode: req.Sandbox,
	}

	if err := cfg.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config: " + err.Error()})
		return
	}

	// Preserve existing notify URL
	if h.PaymentSvc.Registry().HasProvider("wechat") {
		// Get current status to preserve notify URL
		status := h.PaymentSvc.Registry().Status()["wechat"]
		cfg.NotifyURL = status.NotifyURL
	}

	adapter, err := payment.NewWechatPayV3Adapter(cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to create adapter: " + err.Error()})
		return
	}

	h.PaymentSvc.Registry().Register("wechat", adapter, payment.ProviderStatus{
		IsMock:      false,
		SandboxMode: req.Sandbox,
		NotifyURL:   cfg.NotifyURL,
	})

	h.recordPaymentConfigAudit(c, "payment.wechat", true, paymentConfigAuditDetails{
		Provider:        "wechat",
		Sandbox:         req.Sandbox,
		FieldsUpdated:   []string{"app_id", "mch_id", "serial_no", "private_key_present", "api_v3_key_present"},
		SecretFieldsSet: []string{"private_key", "api_v3_key"},
	})

	c.JSON(http.StatusOK, gin.H{
		"message": "WeChat Pay configuration updated",
		"sandbox": req.Sandbox,
	})
}

// UpdateAlipayConfig PUT /api/v1/admin/payment/alipay
// Hot-reload Alipay configuration without server restart.
func (h *PaymentConfigHandler) UpdateAlipayConfig(c *gin.Context) {
	var req struct {
		AppID      string `json:"app_id"`
		PrivateKey string `json:"private_key"`
		PublicKey  string `json:"public_key"`
		Sandbox    bool   `json:"sandbox"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := payment.AlipayV2Config{
		AppID:       req.AppID,
		PrivateKey:  req.PrivateKey,
		PublicKey:   req.PublicKey,
		NotifyURL:   "", // Keep existing
		SandboxMode: req.Sandbox,
	}

	if err := cfg.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config: " + err.Error()})
		return
	}

	// Preserve existing notify URL
	if h.PaymentSvc.Registry().HasProvider("alipay") {
		status := h.PaymentSvc.Registry().Status()["alipay"]
		cfg.NotifyURL = status.NotifyURL
	}

	adapter, err := payment.NewAlipayV2Adapter(cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to create adapter: " + err.Error()})
		return
	}

	h.PaymentSvc.Registry().Register("alipay", adapter, payment.ProviderStatus{
		IsMock:      false,
		SandboxMode: req.Sandbox,
		NotifyURL:   cfg.NotifyURL,
	})
	h.recordPaymentConfigAudit(c, "payment.alipay", true, paymentConfigAuditDetails{
		Provider:        "alipay",
		Sandbox:         req.Sandbox,
		FieldsUpdated:   []string{"app_id", "private_key_present", "public_key_present"},
		SecretFieldsSet: []string{"private_key"},
	})

	c.JSON(http.StatusOK, gin.H{
		"message": "Alipay configuration updated",
		"sandbox": req.Sandbox,
	})
}

// UpdateCreemConfig PUT /api/v1/admin/payment/creem
// Hot-reload Creem checkout configuration without server restart.
func (h *PaymentConfigHandler) UpdateCreemConfig(c *gin.Context) {
	var req creemConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := req.toPaymentConfig()
	adapter, err := payment.NewCreemAdapter(cfg)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid config: " + err.Error()})
		return
	}

	notifyURL := ""
	if h.PaymentSvc.Registry().HasProvider("creem") {
		status := h.PaymentSvc.Registry().Status()["creem"]
		notifyURL = status.NotifyURL
	}
	h.PaymentSvc.Registry().Register("creem", adapter, payment.ProviderStatus{
		IsMock:      false,
		SandboxMode: cfg.SandboxMode,
		NotifyURL:   notifyURL,
	})
	h.recordPaymentConfigAudit(c, "payment.creem", true, creemConfigAuditDetails(req, cfg.SandboxMode))

	c.JSON(http.StatusOK, gin.H{
		"message": "Creem configuration updated",
		"sandbox": cfg.SandboxMode,
	})
}

// SwitchToMock POST /api/v1/admin/payment/:provider/mock
// Switch a provider to mock mode (for testing).
func (h *PaymentConfigHandler) SwitchToMock(c *gin.Context) {
	providerName := c.Param("provider")
	var req struct {
		NotifyURL string `json:"notify_url"`
	}
	_ = c.ShouldBindJSON(&req)

	var mockAdapter payment.PaymentProvider
	switch providerName {
	case "wechat":
		mockAdapter = payment.NewWechatPayMockAdapter("", req.NotifyURL)
	case "alipay":
		mockAdapter = payment.NewAlipayMockAdapter("", req.NotifyURL)
	case "creem":
		mockAdapter = payment.NewCheckoutMockAdapter(req.NotifyURL)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown provider"})
		return
	}

	h.PaymentSvc.Registry().Register(providerName, mockAdapter, payment.ProviderStatus{
		IsMock:    true,
		NotifyURL: req.NotifyURL,
	})
	h.recordPaymentConfigAudit(c, "payment."+providerName, true, paymentConfigAuditDetails{
		Provider:      providerName,
		Mode:          "mock",
		FieldsUpdated: []string{"notify_url_present"},
	})

	c.JSON(http.StatusOK, gin.H{
		"message": providerName + " switched to mock mode",
	})
}

// ImportProviders bulk-imports provider configs from JSON (for migration).
func (h *PaymentConfigHandler) ImportProviders(c *gin.Context) {
	var req struct {
		Wechat payment.WechatPayV3Config `json:"wechat"`
		Alipay payment.AlipayV2Config    `json:"alipay"`
		Creem  creemConfigRequest        `json:"creem"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	results := map[string]string{}

	if req.Wechat.MchID != "" {
		if err := req.Wechat.Validate(); err == nil {
			adapter, err := payment.NewWechatPayV3Adapter(req.Wechat)
			if err == nil {
				h.PaymentSvc.Registry().Register("wechat", adapter, payment.ProviderStatus{
					IsMock:      false,
					SandboxMode: req.Wechat.SandboxMode,
					NotifyURL:   req.Wechat.NotifyURL,
				})
				results["wechat"] = "ok"
				h.recordPaymentConfigAudit(c, "payment.wechat", true, paymentConfigAuditDetails{
					Provider:        "wechat",
					Mode:            "import",
					Sandbox:         req.Wechat.SandboxMode,
					FieldsUpdated:   []string{"mch_id", "app_id", "serial_no", "private_key_present", "api_v3_key_present", "notify_url_present"},
					SecretFieldsSet: []string{"private_key", "api_v3_key"},
				})
			} else {
				results["wechat"] = "failed: " + err.Error()
			}
		} else {
			results["wechat"] = "invalid: " + err.Error()
		}
	}

	if req.Alipay.AppID != "" {
		if err := req.Alipay.Validate(); err == nil {
			adapter, err := payment.NewAlipayV2Adapter(req.Alipay)
			if err == nil {
				h.PaymentSvc.Registry().Register("alipay", adapter, payment.ProviderStatus{
					IsMock:      false,
					SandboxMode: req.Alipay.SandboxMode,
					NotifyURL:   req.Alipay.NotifyURL,
				})
				results["alipay"] = "ok"
				h.recordPaymentConfigAudit(c, "payment.alipay", true, paymentConfigAuditDetails{
					Provider:        "alipay",
					Mode:            "import",
					Sandbox:         req.Alipay.SandboxMode,
					FieldsUpdated:   []string{"app_id", "private_key_present", "public_key_present", "notify_url_present"},
					SecretFieldsSet: []string{"private_key"},
				})
			} else {
				results["alipay"] = "failed: " + err.Error()
			}
		} else {
			results["alipay"] = "invalid: " + err.Error()
		}
	}

	if req.Creem.hasConfig() {
		cfg := req.Creem.toPaymentConfig()
		adapter, err := payment.NewCreemAdapter(cfg)
		if err == nil {
			h.PaymentSvc.Registry().Register("creem", adapter, payment.ProviderStatus{
				IsMock:      false,
				SandboxMode: cfg.SandboxMode,
			})
			results["creem"] = "ok"
			h.recordPaymentConfigAudit(c, "payment.creem", true, creemConfigAuditDetails(req.Creem, cfg.SandboxMode))
		} else {
			results["creem"] = "invalid: " + err.Error()
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Provider import completed",
		"results": results,
	})
}

type creemConfigRequest struct {
	APIKey         string            `json:"api_key"`
	WebhookSecret  string            `json:"webhook_secret"`
	APIBaseURL     string            `json:"api_base_url"`
	SuccessURL     string            `json:"success_url"`
	CancelURL      string            `json:"cancel_url"`
	Sandbox        *bool             `json:"sandbox"`
	ProductIDs     map[string]string `json:"product_ids"`
	ProductMapJSON string            `json:"product_map_json"`
}

func (r creemConfigRequest) toPaymentConfig() payment.CreemConfig {
	return payment.CreemConfig{
		APIKey:         r.APIKey,
		WebhookSecret:  r.WebhookSecret,
		APIBaseURL:     r.APIBaseURL,
		SuccessURL:     r.SuccessURL,
		CancelURL:      r.CancelURL,
		SandboxMode:    r.sandboxMode(),
		ProductIDs:     r.ProductIDs,
		ProductMapJSON: r.ProductMapJSON,
	}
}

func (r creemConfigRequest) sandboxMode() bool {
	if r.Sandbox == nil {
		return true
	}
	return *r.Sandbox
}

func (r creemConfigRequest) hasConfig() bool {
	return r.APIKey != "" || r.WebhookSecret != "" || r.ProductMapJSON != "" || len(r.ProductIDs) > 0
}

type paymentConfigAuditDetails struct {
	Provider        string   `json:"provider"`
	Mode            string   `json:"mode,omitempty"`
	Sandbox         bool     `json:"sandbox"`
	FieldsUpdated   []string `json:"fields_updated,omitempty"`
	SecretFieldsSet []string `json:"secret_fields_set,omitempty"`
	ProductMapCount int      `json:"product_map_count,omitempty"`
}

func (h *PaymentConfigHandler) recordPaymentConfigAudit(c *gin.Context, target string, success bool, details paymentConfigAuditDetails) {
	if h == nil || h.AuditSvc == nil {
		return
	}
	details.FieldsUpdated = compactStrings(details.FieldsUpdated)
	details.SecretFieldsSet = compactStrings(details.SecretFieldsSet)
	payload, _ := json.Marshal(details)
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     adminActorFromContext(c),
		Action:    domain.AuditActionConfigUpdate,
		Target:    target,
		Success:   success,
		Details:   string(payload),
		IPAddress: clientIP(c),
	})
}

func creemConfigAuditDetails(req creemConfigRequest, sandbox bool) paymentConfigAuditDetails {
	fields := []string{}
	if strings.TrimSpace(req.APIBaseURL) != "" {
		fields = append(fields, "api_base_url_present")
	}
	if strings.TrimSpace(req.SuccessURL) != "" {
		fields = append(fields, "success_url_present")
	}
	if strings.TrimSpace(req.CancelURL) != "" {
		fields = append(fields, "cancel_url_present")
	}
	secrets := []string{}
	if strings.TrimSpace(req.APIKey) != "" {
		secrets = append(secrets, "api_key")
	}
	if strings.TrimSpace(req.WebhookSecret) != "" {
		secrets = append(secrets, "webhook_secret")
	}
	if strings.TrimSpace(req.ProductMapJSON) != "" || len(req.ProductIDs) > 0 {
		fields = append(fields, "product_map_present")
	}
	return paymentConfigAuditDetails{
		Provider:        "creem",
		Sandbox:         sandbox,
		FieldsUpdated:   fields,
		SecretFieldsSet: secrets,
		ProductMapCount: len(req.ProductIDs),
	}
}

func compactStrings(values []string) []string {
	items := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		items = append(items, value)
	}
	return items
}

func adminActorFromContext(c *gin.Context) string {
	if principal, ok := middleware.GetAdminPrincipal(c); ok {
		return defaultStringForHandler(principal.Name, "admin")
	}
	return "admin"
}

// SafeJSON is a helper that marshals safely (used in tests).
func SafeJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
