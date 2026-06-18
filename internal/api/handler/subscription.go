package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type SubscriptionHandler struct {
	CancellationSvc service.SubscriptionCancellationService
}

func NewSubscriptionHandler(cancellationSvc service.SubscriptionCancellationService) *SubscriptionHandler {
	return &SubscriptionHandler{CancellationSvc: cancellationSvc}
}

type CancelSubscriptionRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	SKUCode        string `json:"sku_code" binding:"required"`
	Reason         string `json:"reason"`
	Source         string `json:"source"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ResumeSubscriptionRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	SKUCode        string `json:"sku_code" binding:"required"`
	Source         string `json:"source"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Cancel handles POST /api/v1/commerce/subscriptions/cancel.
func (h *SubscriptionHandler) Cancel(c *gin.Context) {
	var req CancelSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.CancellationSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "subscription cancellation service unavailable"})
		return
	}
	result, err := h.CancellationSvc.Cancel(c.Request.Context(), service.SubscriptionCancellationInput{
		UserID:         req.UserID,
		SKUCode:        req.SKUCode,
		Reason:         req.Reason,
		Source:         req.Source,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		c.JSON(subscriptionCancellationErrorStatus(err), subscriptionCancellationErrorResponse(err))
		return
	}
	c.JSON(http.StatusOK, result)
}

// Resume handles POST /api/v1/commerce/subscriptions/resume.
func (h *SubscriptionHandler) Resume(c *gin.Context) {
	var req ResumeSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.CancellationSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "subscription resume service unavailable"})
		return
	}
	result, err := h.CancellationSvc.Resume(c.Request.Context(), service.SubscriptionResumeInput{
		UserID:         req.UserID,
		SKUCode:        req.SKUCode,
		Source:         req.Source,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		c.JSON(subscriptionCancellationErrorStatus(err), subscriptionCancellationErrorResponse(err))
		return
	}
	c.JSON(http.StatusOK, result)
}

func subscriptionCancellationErrorStatus(err error) int {
	switch {
	case errors.Is(err, service.ErrInvalidSubscriptionCancellation):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrSubscriptionControlUnavailable):
		return http.StatusConflict
	case errors.Is(err, service.ErrSubscriptionControlFailed):
		return http.StatusBadGateway
	case errors.Is(err, service.ErrUserNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrSubscriptionNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func subscriptionCancellationErrorResponse(err error) gin.H {
	response := gin.H{"error": err.Error(), "code": "subscription_control_failed"}
	switch {
	case errors.Is(err, service.ErrInvalidSubscriptionCancellation):
		response["code"] = "invalid_subscription_cancellation"
	case errors.Is(err, service.ErrSubscriptionControlUnavailable):
		response["code"] = "subscription_control_unavailable"
	case errors.Is(err, service.ErrSubscriptionControlFailed):
		response["code"] = "subscription_control_failed"
	case errors.Is(err, service.ErrUserNotFound):
		response["code"] = "user_not_found"
	case errors.Is(err, service.ErrSubscriptionNotFound):
		response["code"] = "subscription_not_found"
	}
	return response
}
