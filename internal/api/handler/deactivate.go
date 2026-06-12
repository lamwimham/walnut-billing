package handler

import (
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type DeactivateHandler struct {
	LicenseSvc service.LicenseService
}

func NewDeactivateHandler(licSvc service.LicenseService) *DeactivateHandler {
	return &DeactivateHandler{LicenseSvc: licSvc}
}

type DeactivateRequest struct {
	Key      string `json:"key" binding:"required"`
	DeviceID string `json:"device_id" binding:"required"`
}

func (h *DeactivateHandler) Deactivate(c *gin.Context) {
	var req DeactivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.LicenseSvc.Deactivate(c.Request.Context(), req.Key, req.DeviceID)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "deactivated",
		"message": "License deactivated successfully",
	})
}
