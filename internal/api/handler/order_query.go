package handler

import (
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type OrderQueryHandler struct {
	OrderSvc service.OrderService
}

func NewOrderQueryHandler(orderSvc service.OrderService) *OrderQueryHandler {
	return &OrderQueryHandler{OrderSvc: orderSvc}
}

// GetOrder handles GET /api/v1/orders/:out_trade_no
func (h *OrderQueryHandler) GetOrder(c *gin.Context) {
	outTradeNo := c.Param("out_trade_no")
	if outTradeNo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "out_trade_no is required"})
		return
	}

	order, err := h.OrderSvc.GetByOutTradeNo(c.Request.Context(), outTradeNo)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"order": gin.H{
			"out_trade_no": order.OutTradeNo,
			"license_key":  order.LicenseKey,
			"amount":       order.Amount,
			"currency":     order.Currency,
			"status":       order.Status,
			"provider":     order.Provider,
			"trade_no":     order.TradeNo,
			"paid_at":      order.PaidAt,
		},
	})
}
