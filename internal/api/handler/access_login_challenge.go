package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AccessLoginChallengeHandler struct {
	ChallengeSvc service.AccessLoginChallengeService
	AuditSvc     service.AuditService
}

func NewAccessLoginChallengeHandler(challengeSvc service.AccessLoginChallengeService, auditSvc service.AuditService) *AccessLoginChallengeHandler {
	return &AccessLoginChallengeHandler{ChallengeSvc: challengeSvc, AuditSvc: auditSvc}
}

type CreateAccessLoginChallengeRequest struct {
	Email          string `json:"email" binding:"required"`
	DeviceID       string `json:"device_id" binding:"required"`
	Source         string `json:"source"`
	IdempotencyKey string `json:"idempotency_key"`
}

type VerifyAccessLoginChallengeRequest struct {
	ChallengeID string `json:"challenge_id" binding:"required"`
	Token       string `json:"token" binding:"required"`
	DeviceID    string `json:"device_id" binding:"required"`
	DisplayName string `json:"display_name"`
	Source      string `json:"source"`
}

func (h *AccessLoginChallengeHandler) Create(c *gin.Context) {
	if h == nil || h.ChallengeSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "access login challenge service unavailable"})
		return
	}
	var req CreateAccessLoginChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.ChallengeSvc.Create(c.Request.Context(), service.AccessLoginChallengeCreateInput{
		Email:          req.Email,
		DeviceID:       req.DeviceID,
		Source:         req.Source,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		h.recordAudit(c, "email", domain.AuditActionAccessLoginChallengeCreate, "access_login_challenge", false, "access_login_challenge_create_failed")
		writeAccessLoginChallengeError(c, err)
		return
	}
	h.recordAudit(c, "email", domain.AuditActionAccessLoginChallengeCreate, result.ChallengeID, true, "access_login_challenge_create")
	c.JSON(http.StatusAccepted, result)
}

func (h *AccessLoginChallengeHandler) Verify(c *gin.Context) {
	if h == nil || h.ChallengeSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "access login challenge service unavailable"})
		return
	}
	var req VerifyAccessLoginChallengeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.ChallengeSvc.Verify(c.Request.Context(), service.AccessLoginChallengeVerifyInput{
		ChallengeID: req.ChallengeID,
		Token:       req.Token,
		DeviceID:    req.DeviceID,
		DisplayName: req.DisplayName,
		Source:      req.Source,
	})
	if err != nil {
		h.recordAudit(c, "unknown", domain.AuditActionAccessLoginChallengeVerify, defaultStringForHandler(req.ChallengeID, "access_login_challenge"), false, "access_login_challenge_verify_failed")
		writeAccessLoginChallengeError(c, err)
		return
	}
	h.recordAudit(c, userAuditActor(result.User), domain.AuditActionAccessLoginChallengeVerify, userAuditActor(result.User), true, "access_login_challenge_verify")
	c.JSON(http.StatusOK, result)
}

func writeAccessLoginChallengeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAccessLoginChallenge):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_login_challenge"})
	case errors.Is(err, service.ErrAccessLoginChallengeExpired):
		c.JSON(http.StatusGone, gin.H{"error": err.Error(), "code": "login_challenge_expired"})
	case errors.Is(err, service.ErrAccessLoginChallengeFailed):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error(), "code": "login_challenge_failed"})
	case errors.Is(err, service.ErrAccessLoginChallengeDeliveryUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": "login_challenge_delivery_unavailable"})
	case errors.Is(err, service.ErrAccessDeviceRevoked):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "access_device_revoked"})
	case errors.Is(err, service.ErrDeviceLimitExceeded):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "device_limit_exceeded"})
	case errors.Is(err, service.ErrAccessUserDisabled):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "access_user_disabled"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *AccessLoginChallengeHandler) recordAudit(c *gin.Context, actor, action, target string, success bool, details string) {
	if h == nil || h.AuditSvc == nil {
		return
	}
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     defaultStringForHandler(actor, "unknown"),
		Action:    action,
		Target:    target,
		Success:   success,
		Details:   details,
		IPAddress: clientIP(c),
	})
}
