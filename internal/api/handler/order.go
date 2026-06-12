package handler

import (
	"net/http"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type OrderHandler struct {
	OrderSvc   service.OrderService
	PaymentSvc *payment.PaymentService
	LicenseSvc service.LicenseService
	AuditSvc   service.AuditService
}

func NewOrderHandler(
	orderSvc service.OrderService,
	paymentSvc *payment.PaymentService,
	licenseSvc service.LicenseService,
	auditSvc service.AuditService,
) *OrderHandler {
	return &OrderHandler{
		OrderSvc:   orderSvc,
		PaymentSvc: paymentSvc,
		LicenseSvc: licenseSvc,
		AuditSvc:   auditSvc,
	}
}

// CreateOrderRequest is the request body for creating a new order.
type CreateOrderRequest struct {
	ProductCode string `json:"product_code" binding:"required"`
}

// CreateOrder handles POST /api/v1/orders
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	order, err := h.OrderSvc.Create(c.Request.Context(), req.ProductCode)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"order": gin.H{
			"out_trade_no": order.OutTradeNo,
			"license_key":  order.LicenseKey,
			"amount":       order.Amount,
			"currency":     order.Currency,
			"status":       order.Status,
		},
		"message": "Order created. Complete payment to activate.",
	})
}

// GetPaymentURLRequest is the request body for getting a payment URL.
type GetPaymentURLRequest struct {
	OutTradeNo string `json:"out_trade_no" binding:"required"`
	Provider   string `json:"provider" binding:"required,oneof=wechat alipay"`
}

// GetPaymentURL handles POST /api/v1/orders/pay
func (h *OrderHandler) GetPaymentURL(c *gin.Context) {
	var req GetPaymentURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	payURL, err := h.PaymentSvc.CreatePayment(c.Request.Context(), req.OutTradeNo, req.Provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"payment_url": payURL,
		"provider":    req.Provider,
	})
}

// PaymentCallback handles POST /api/v1/callbacks/:provider
// This is the webhook endpoint called by WeChat/Alipay after payment.
func (h *OrderHandler) PaymentCallback(c *gin.Context) {
	provider := c.Param("provider")
	if provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider is required"})
		return
	}

	// Collect callback parameters from both query string and form data
	params := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	// Also check form data (WeChat sends XML, we'll parse it in a real impl)
	for key, values := range c.Request.PostForm {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}

	contentType, body, statusCode := h.PaymentSvc.HandleCallback(c.Request.Context(), provider, params)

	// Audit log for payment callback
	success := statusCode == 200
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     provider,
		Action:    domain.AuditActionPaymentCallback,
		Target:    params["out_trade_no"],
		Success:   success,
		Details:   body,
		IPAddress: clientIP(c),
	})

	c.Data(statusCode, contentType, []byte(body))
}
