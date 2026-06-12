package handler

import (
	"net/http"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	LicenseSvc service.LicenseService
	AuditSvc   service.AuditService
}

func NewAuthHandler(lSvc service.LicenseService, aSvc service.AuditService) *AuthHandler {
	return &AuthHandler{LicenseSvc: lSvc, AuditSvc: aSvc}
}

type VerifyRequest struct {
	Key      string `json:"key" binding:"required"`
	DeviceID string `json:"device_id" binding:"required"`
}

type ActivateRequest struct {
	Key      string `json:"key" binding:"required"`
	DeviceID string `json:"device_id" binding:"required"`
}

func (h *AuthHandler) Verify(c *gin.Context) {
	var req VerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lic, err := h.LicenseSvc.Verify(c.Request.Context(), req.Key, req.DeviceID)
	success := err == nil

	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     req.Key,
		Action:    domain.AuditActionLicenseVerify,
		Target:    req.Key,
		Success:   success,
		Details:   clientIP(c) + " verify " + req.DeviceID,
		IPAddress: clientIP(c),
	})

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"license":    lic,
		"expires_at": lic.ExpiresAt,
	})
}

func (h *AuthHandler) Activate(c *gin.Context) {
	var req ActivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := h.LicenseSvc.Activate(c.Request.Context(), req.Key, req.DeviceID)
	success := err == nil

	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     req.Key,
		Action:    domain.AuditActionLicenseActivate,
		Target:    req.Key,
		Success:   success,
		Details:   clientIP(c) + " activate " + req.DeviceID,
		IPAddress: clientIP(c),
	})

	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"status": "error", "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "activated",
		"message": "License activated successfully",
	})
}

func clientIP(c *gin.Context) string {
	ip := c.ClientIP()
	if ip == "" {
		return "unknown"
	}
	return ip
}
