package handler

import (
	"net/http"
	"strconv"
	"strings"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type FulfillmentHandler struct {
	FulfillmentSvc service.FulfillmentService
}

func NewFulfillmentHandler(fulfillmentSvc service.FulfillmentService) *FulfillmentHandler {
	return &FulfillmentHandler{FulfillmentSvc: fulfillmentSvc}
}

// ListExecutions handles GET /api/v1/admin/fulfillments.
func (h *FulfillmentHandler) ListExecutions(c *gin.Context) {
	if h.FulfillmentSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "fulfillment service unavailable"})
		return
	}
	executions, err := h.FulfillmentSvc.ListExecutions(c.Request.Context(), repository.FulfillmentExecutionQuery{
		OrderID:    uintQuery(c, "order_id"),
		OutTradeNo: strings.TrimSpace(c.Query("out_trade_no")),
		UserID:     strings.TrimSpace(c.Query("user_id")),
		SKUCode:    strings.TrimSpace(c.Query("sku_code")),
		RuleID:     strings.TrimSpace(c.Query("rule_id")),
		TargetType: strings.TrimSpace(c.Query("target_type")),
		Status:     strings.TrimSpace(c.Query("status")),
		Limit:      intQuery(c, "limit", 50),
		Offset:     intQuery(c, "offset", 0),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(executions), "fulfillments": executions})
}

func uintQuery(c *gin.Context, key string) uint {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return uint(parsed)
}
