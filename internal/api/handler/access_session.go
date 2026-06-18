package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AccessSessionHandler struct {
	AccessSessionSvc service.AccessSessionService
	AuditSvc         service.AuditService
}

func NewAccessSessionHandler(accessSessionSvc service.AccessSessionService, auditSvc service.AuditService) *AccessSessionHandler {
	return &AccessSessionHandler{AccessSessionSvc: accessSessionSvc, AuditSvc: auditSvc}
}

type RegisterOrRestoreAccessRequest struct {
	Email       string `json:"email" binding:"required"`
	DisplayName string `json:"display_name"`
	DeviceID    string `json:"device_id" binding:"required"`
	Source      string `json:"source"`
	Note        string `json:"note"`
}

// RegisterOrRestore handles POST /api/v1/access/registrations.
func (h *AccessSessionHandler) RegisterOrRestore(c *gin.Context) {
	var req RegisterOrRestoreAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.AccessSessionSvc.RegisterOrRestore(c.Request.Context(), service.AccessSessionInput{
		Email:       req.Email,
		DisplayName: req.DisplayName,
		DeviceID:    req.DeviceID,
		Source:      req.Source,
		Note:        req.Note,
	})
	if err != nil {
		writeAccessSessionError(c, err)
		return
	}
	h.recordAudit(c, userAuditActor(result.User), domain.AuditActionRegistrationSubmit, result.User.ID, true, "access_register_or_restore")
	c.JSON(http.StatusOK, result)
}

func writeAccessSessionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAccessSession):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_access_session"})
	case errors.Is(err, service.ErrDeviceLimitExceeded):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "device_limit_exceeded"})
	case errors.Is(err, service.ErrAccessDeviceRevoked):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "access_device_revoked"})
	case errors.Is(err, service.ErrAccessUserDisabled):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "access_user_disabled"})
	case errors.Is(err, service.ErrUserNotFound), errors.Is(err, service.ErrUnknownEntitlement):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "access_resource_not_found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *AccessSessionHandler) recordAudit(c *gin.Context, actor, action, target string, success bool, details string) {
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
