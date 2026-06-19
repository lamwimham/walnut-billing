package handler

import (
	"errors"
	"io"
	"net/http"
	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AccessAdminHandler struct {
	AccessAdminSvc       service.AccessAdminService
	DeviceAdminSvc       service.AccessDeviceAdminService
	UserAccessSummarySvc service.AdminUserAccessSummaryService
	AuditSvc             service.AuditService
}

func NewAccessAdminHandler(accessAdminSvc service.AccessAdminService, deviceAdminSvc service.AccessDeviceAdminService, auditSvc service.AuditService) *AccessAdminHandler {
	return NewAccessAdminHandlerWithSummary(accessAdminSvc, deviceAdminSvc, nil, auditSvc)
}

func NewAccessAdminHandlerWithSummary(
	accessAdminSvc service.AccessAdminService,
	deviceAdminSvc service.AccessDeviceAdminService,
	userAccessSummarySvc service.AdminUserAccessSummaryService,
	auditSvc service.AuditService,
) *AccessAdminHandler {
	return &AccessAdminHandler{
		AccessAdminSvc:       accessAdminSvc,
		DeviceAdminSvc:       deviceAdminSvc,
		UserAccessSummarySvc: userAccessSummarySvc,
		AuditSvc:             auditSvc,
	}
}

// ListAccounts handles GET /api/v1/admin/access-accounts.
func (h *AccessAdminHandler) ListAccounts(c *gin.Context) {
	if h == nil || h.AccessAdminSvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "access admin service is not configured"})
		return
	}
	result, err := h.AccessAdminSvc.ListAccounts(c.Request.Context(), service.AccessAdminQuery{
		UserID: c.Query("user_id"),
		Email:  c.Query("email"),
		Status: c.Query("status"),
		Limit:  intQuery(c, "limit", 50),
		Offset: intQuery(c, "offset", 0),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// GetUserAccessSummary handles GET /api/v1/admin/users/:user_id/access.
func (h *AccessAdminHandler) GetUserAccessSummary(c *gin.Context) {
	if h == nil || h.UserAccessSummarySvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "admin user access summary service is not configured", "code": "admin_user_access_summary_unconfigured"})
		return
	}
	result, err := h.UserAccessSummarySvc.Get(c.Request.Context(), service.AdminUserAccessSummaryInput{
		UserID:      c.Param("user_id"),
		RecentLimit: intQuery(c, "recent_limit", 10),
	})
	if err != nil {
		writeAdminUserAccessSummaryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

type RevokeAccessDeviceRequest struct {
	RevokedBy string `json:"revoked_by"`
	Reason    string `json:"reason"`
}

// RevokeDevice handles POST /api/v1/admin/devices/:id/revoke.
func (h *AccessAdminHandler) RevokeDevice(c *gin.Context) {
	if h == nil || h.DeviceAdminSvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "access device admin service is not configured"})
		return
	}
	var req RevokeAccessDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_access_device_revoke"})
			return
		}
	}
	deviceID := c.Param("id")
	actor := adminAuditActor(c, req.RevokedBy)
	device, err := h.DeviceAdminSvc.RevokeDevice(c.Request.Context(), service.AccessDeviceRevokeInput{
		DeviceID:  deviceID,
		RevokedBy: actor,
		Reason:    req.Reason,
	})
	if err != nil {
		h.recordAudit(c, actor, domain.AuditActionAccessDeviceRevoke, defaultStringForHandler(deviceID, "access_device"), false, req.Reason)
		writeAccessDeviceAdminError(c, err)
		return
	}
	h.recordAudit(c, actor, domain.AuditActionAccessDeviceRevoke, device.ID, true, req.Reason)
	c.JSON(http.StatusOK, gin.H{"device": accessDeviceAdminResponse(device)})
}

func writeAccessDeviceAdminError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAccessDevice):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_access_device"})
	case errors.Is(err, service.ErrAccessDeviceNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "access_device_not_found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func writeAdminUserAccessSummaryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAdminUserAccessSummary):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_admin_user_access_summary"})
	case errors.Is(err, service.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "user_not_found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "admin_user_access_summary_failed"})
	}
}

func accessDeviceAdminResponse(device *domain.UserDevice) gin.H {
	if device == nil {
		return gin.H{}
	}
	return gin.H{
		"id":            device.ID,
		"user_id":       device.UserID,
		"status":        device.Status,
		"revoked_at":    device.RevokedAt,
		"revoked_by":    device.RevokedBy,
		"revoke_reason": device.RevokeReason,
	}
}

func (h *AccessAdminHandler) recordAudit(c *gin.Context, actor, action, target string, success bool, details string) {
	if h == nil || h.AuditSvc == nil {
		return
	}
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     defaultStringForHandler(actor, "admin"),
		Action:    action,
		Target:    target,
		Success:   success,
		Details:   details,
		IPAddress: clientIP(c),
	})
}

func adminAuditActor(c *gin.Context, fallback string) string {
	if principal, ok := middleware.GetAdminPrincipal(c); ok {
		return defaultStringForHandler(principal.Name, "admin")
	}
	return defaultStringForHandler(fallback, "admin")
}
