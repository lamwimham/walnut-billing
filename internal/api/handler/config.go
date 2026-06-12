package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
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

	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     "admin",
		Action:    domain.AuditActionConfigUpdate,
		Target:    "payment.wechat",
		Success:   true,
		Details:   "sandbox=" + fmt.Sprint(req.Sandbox),
		IPAddress: clientIP(c),
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

	c.JSON(http.StatusOK, gin.H{
		"message": "Alipay configuration updated",
		"sandbox": req.Sandbox,
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
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown provider"})
		return
	}

	h.PaymentSvc.Registry().Register(providerName, mockAdapter, payment.ProviderStatus{
		IsMock:    true,
		NotifyURL: req.NotifyURL,
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
			} else {
				results["alipay"] = "failed: " + err.Error()
			}
		} else {
			results["alipay"] = "invalid: " + err.Error()
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Provider import completed",
		"results": results,
	})
}

// SafeJSON is a helper that marshals safely (used in tests).
func SafeJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
