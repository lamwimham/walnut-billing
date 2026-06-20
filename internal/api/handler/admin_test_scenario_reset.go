package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminTestScenarioResetHandler struct {
	ResetSvc service.AdminTestScenarioResetService
	AuditSvc service.AuditService
}

func NewAdminTestScenarioResetHandler(resetSvc service.AdminTestScenarioResetService, auditSvc service.AuditService) *AdminTestScenarioResetHandler {
	return &AdminTestScenarioResetHandler{ResetSvc: resetSvc, AuditSvc: auditSvc}
}

type AdminTestScenarioResetRequest struct {
	Scenario string `json:"scenario"`
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	DryRun   bool   `json:"dry_run"`
	Reason   string `json:"reason"`
}

func (h *AdminTestScenarioResetHandler) Reset(c *gin.Context) {
	if h == nil || h.ResetSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "admin test scenario reset service is not configured", "code": "admin_test_scenario_reset_unconfigured"})
		return
	}
	principal, ok := middleware.GetAdminPrincipal(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "admin authentication required", "code": "admin_auth_required"})
		return
	}
	if !middleware.PrincipalHasPermission(principal, middleware.PermissionAdminTestWrite) {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin permission denied", "code": "admin_permission_denied", "permission": middleware.PermissionAdminTestWrite})
		return
	}
	var req AdminTestScenarioResetRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_admin_test_scenario_reset"})
		return
	}
	result, err := h.ResetSvc.Reset(c.Request.Context(), service.AdminTestScenarioResetInput{
		Scenario: req.Scenario,
		UserID:   req.UserID,
		Email:    req.Email,
		DryRun:   req.DryRun,
	})
	actor := adminAuditActor(c, "")
	if err != nil {
		h.recordAudit(c, actor, req, nil, false, err)
		writeAdminTestScenarioResetError(c, err)
		return
	}
	h.recordAudit(c, actor, req, result, true, nil)
	c.JSON(http.StatusOK, result)
}

func writeAdminTestScenarioResetError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAdminTestScenarioResetUnavailable):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "admin_test_scenario_reset_unavailable"})
	case errors.Is(err, service.ErrInvalidAdminTestScenarioReset):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_admin_test_scenario_reset"})
	case errors.Is(err, service.ErrAdminTestScenarioNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "admin_test_scenario_not_found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "admin_test_scenario_reset_failed"})
	}
}

func (h *AdminTestScenarioResetHandler) recordAudit(c *gin.Context, actor string, req AdminTestScenarioResetRequest, result *service.AdminTestScenarioResetResult, success bool, err error) {
	if h == nil || h.AuditSvc == nil {
		return
	}
	details := adminTestScenarioResetAuditDetails(req, result, err)
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     defaultStringForHandler(actor, "admin"),
		Action:    domain.AuditActionAdminTestScenarioReset,
		Target:    adminTestScenarioResetAuditTarget(req, result),
		Success:   success,
		Details:   details,
		IPAddress: clientIP(c),
	})
}

func adminTestScenarioResetAuditTarget(req AdminTestScenarioResetRequest, result *service.AdminTestScenarioResetResult) string {
	if result != nil && result.UserID != "" {
		return result.UserID
	}
	if req.UserID != "" {
		return req.UserID
	}
	if req.Email != "" {
		return service.NewAdminPrivacyProjector().RedactIdentifier(req.Email)
	}
	return "admin_test_scenario"
}

func adminTestScenarioResetAuditDetails(req AdminTestScenarioResetRequest, result *service.AdminTestScenarioResetResult, err error) string {
	details := map[string]any{
		"scenario": req.Scenario,
		"dry_run":  req.DryRun,
		"reason":   service.NewAdminPrivacyProjector().RedactFreeText(req.Reason),
	}
	if result != nil {
		details["user_id"] = result.UserID
		details["email_fingerprint"] = result.EmailFingerprint
		details["affected_counts"] = result.AffectedCounts
	}
	if err != nil {
		details["error_code"] = adminTestScenarioResetAuditErrorCode(err)
	}
	payload, marshalErr := json.Marshal(details)
	if marshalErr != nil {
		return `{"error":"audit_details_failed"}`
	}
	return string(payload)
}

func adminTestScenarioResetAuditErrorCode(err error) string {
	switch {
	case errors.Is(err, service.ErrAdminTestScenarioResetUnavailable):
		return "admin_test_scenario_reset_unavailable"
	case errors.Is(err, service.ErrInvalidAdminTestScenarioReset):
		return "invalid_admin_test_scenario_reset"
	case errors.Is(err, service.ErrAdminTestScenarioNotFound):
		return "admin_test_scenario_not_found"
	default:
		return "admin_test_scenario_reset_failed"
	}
}
