package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type CheckoutHandler struct {
	CheckoutSvc service.CheckoutService
}

func NewCheckoutHandler(checkoutSvc service.CheckoutService) *CheckoutHandler {
	return &CheckoutHandler{CheckoutSvc: checkoutSvc}
}

type CreateCheckoutSessionRequest struct {
	UserID         string            `json:"user_id" binding:"required"`
	SKUCode        string            `json:"sku_code" binding:"required"`
	Provider       string            `json:"provider" binding:"required"`
	SuccessURL     string            `json:"success_url"`
	CancelURL      string            `json:"cancel_url"`
	IdempotencyKey string            `json:"idempotency_key" binding:"required"`
	Metadata       map[string]string `json:"metadata"`
}

// CreateCheckoutSession handles POST /api/v1/commerce/checkout-sessions.
// The handler stays transport-only; checkout orchestration lives in service.
func (h *CheckoutHandler) CreateCheckoutSession(c *gin.Context) {
	var req CreateCheckoutSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.CheckoutSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "checkout service unavailable"})
		return
	}

	result, err := h.CheckoutSvc.CreateCheckoutSession(c.Request.Context(), service.CheckoutInput{
		UserID:         req.UserID,
		SKUCode:        req.SKUCode,
		Provider:       req.Provider,
		SuccessURL:     req.SuccessURL,
		CancelURL:      req.CancelURL,
		IdempotencyKey: req.IdempotencyKey,
		Metadata:       req.Metadata,
	})
	if err != nil {
		status := checkoutErrorStatus(err)
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"order":        checkoutOrderResponse(result.Order),
		"checkout_url": result.CheckoutURL,
		"provider":     result.Provider,
	})
}

func checkoutErrorStatus(err error) int {
	switch {
	case errors.Is(err, service.ErrInvalidCheckoutRequest):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrUserNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrCheckoutProviderFailed):
		return http.StatusBadGateway
	default:
		return http.StatusBadRequest
	}
}

func checkoutOrderResponse(order *domain.Order) gin.H {
	if order == nil {
		return gin.H{}
	}
	return gin.H{
		"out_trade_no":         order.OutTradeNo,
		"user_id":              order.UserID,
		"sku_code":             order.SKUCode,
		"amount":               order.Amount,
		"currency":             order.Currency,
		"status":               order.Status,
		"provider":             order.Provider,
		"provider_checkout_id": order.ProviderCheckoutID,
		"provider_customer_id": order.ProviderCustomerID,
		"order_type":           order.OrderType,
	}
}
