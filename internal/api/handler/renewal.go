package handler

import (
	"net/http"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

// RenewalHandler handles license renewal requests.
type RenewalHandler struct {
	OrderSvc   service.OrderService
	PaymentSvc *payment.PaymentService
}

func NewRenewalHandler(orderSvc service.OrderService, paymentSvc *payment.PaymentService) *RenewalHandler {
	return &RenewalHandler{
		OrderSvc:   orderSvc,
		PaymentSvc: paymentSvc,
	}
}

// CreateRenewalOrder handles POST /api/v1/orders/renew
func (h *RenewalHandler) CreateRenewalOrder(c *gin.Context) {
	var req struct {
		LicenseKey string `json:"license_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	order, err := h.OrderSvc.CreateRenewal(c.Request.Context(), req.LicenseKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"out_trade_no": order.OutTradeNo,
		"license_key":  order.LicenseKey,
		"amount":       order.Amount,
		"currency":     order.Currency,
		"status":       order.Status,
		"order_type":   order.OrderType,
		"message":      "Renewal order created. Complete payment to extend license.",
	})
}

// RenewAndPay handles POST /api/v1/orders/renew/pay
func (h *RenewalHandler) RenewAndPay(c *gin.Context) {
	var req struct {
		LicenseKey string `json:"license_key" binding:"required"`
		Provider   string `json:"provider" binding:"required,oneof=wechat alipay"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create renewal order
	order, err := h.OrderSvc.CreateRenewal(c.Request.Context(), req.LicenseKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get payment URL
	payURL, err := h.PaymentSvc.CreatePayment(c.Request.Context(), order.OutTradeNo, req.Provider)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"out_trade_no": order.OutTradeNo,
		"payment_url":  payURL,
		"provider":     req.Provider,
		"amount":       order.Amount,
	})
}
