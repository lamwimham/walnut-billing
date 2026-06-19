package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminSubscriptionHandler struct {
	AdminSubscriptionSvc service.AdminSubscriptionService
}

func NewAdminSubscriptionHandler(adminSubscriptionSvc service.AdminSubscriptionService) *AdminSubscriptionHandler {
	return &AdminSubscriptionHandler{AdminSubscriptionSvc: adminSubscriptionSvc}
}

// ListSubscriptions handles GET /api/v1/admin/subscriptions.
func (h *AdminSubscriptionHandler) ListSubscriptions(c *gin.Context) {
	if h == nil || h.AdminSubscriptionSvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "admin subscription service is not configured", "code": "admin_subscription_service_unconfigured"})
		return
	}
	result, err := h.AdminSubscriptionSvc.ListSubscriptions(c.Request.Context(), service.AdminSubscriptionQuery{
		UserID:     c.Query("user_id"),
		SKUCode:    c.Query("sku_code"),
		Status:     c.Query("status"),
		Provider:   c.Query("provider"),
		OutTradeNo: c.Query("out_trade_no"),
		Limit:      intQuery(c, "limit", 50),
		Offset:     intQuery(c, "offset", 0),
	})
	if err != nil {
		writeAdminSubscriptionError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeAdminSubscriptionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAdminSubscriptionQuery):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_admin_subscription_query"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "admin_subscription_query_failed"})
	}
}
