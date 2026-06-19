package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminOrderHandler struct {
	AdminOrderSvc service.AdminOrderService
}

func NewAdminOrderHandler(adminOrderSvc service.AdminOrderService) *AdminOrderHandler {
	return &AdminOrderHandler{AdminOrderSvc: adminOrderSvc}
}

// ListOrders handles GET /api/v1/admin/orders.
func (h *AdminOrderHandler) ListOrders(c *gin.Context) {
	if h == nil || h.AdminOrderSvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "admin order service is not configured", "code": "admin_order_service_unconfigured"})
		return
	}
	result, err := h.AdminOrderSvc.ListOrders(c.Request.Context(), service.AdminOrderQuery{
		UserID:     c.Query("user_id"),
		SKUCode:    c.Query("sku_code"),
		Status:     c.Query("status"),
		Provider:   c.Query("provider"),
		OrderType:  c.Query("order_type"),
		OutTradeNo: c.Query("out_trade_no"),
		Limit:      intQuery(c, "limit", 50),
		Offset:     intQuery(c, "offset", 0),
	})
	if err != nil {
		writeAdminOrderError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeAdminOrderError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAdminOrderQuery):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_admin_order_query"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "admin_order_query_failed"})
	}
}
