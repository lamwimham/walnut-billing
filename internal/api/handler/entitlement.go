package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type EntitlementHandler struct {
	EntitlementSvc service.EntitlementService
	AuditSvc       service.AuditService
	AdminProjector service.AdminEntitlementProjector
}

func NewEntitlementHandler(entitlementSvc service.EntitlementService, auditSvc service.AuditService) *EntitlementHandler {
	return &EntitlementHandler{
		EntitlementSvc: entitlementSvc,
		AuditSvc:       auditSvc,
		AdminProjector: service.NewAdminEntitlementProjector(service.NewAdminPrivacyProjector()),
	}
}

type SubmitRegistrationRequest struct {
	Email                string `json:"email" binding:"required"`
	DisplayName          string `json:"display_name"`
	RequestedEntitlement string `json:"requested_entitlement"`
	DeviceID             string `json:"device_id"`
	Source               string `json:"source"`
	Note                 string `json:"note"`
}

type ReviewRegistrationRequest struct {
	Status     string `json:"status" binding:"required"`
	ReviewedBy string `json:"reviewed_by"`
	ReviewNote string `json:"review_note"`
}

type CreateGrantRequest struct {
	UserID         string `json:"user_id"`
	RegistrationID string `json:"registration_id"`
	EntitlementID  string `json:"entitlement_id"`
	CreatedBy      string `json:"created_by"`
	Source         string `json:"source"`
	ExpiresAt      string `json:"expires_at"`
}

// SubmitRegistration handles POST /api/v1/registrations.
func (h *EntitlementHandler) SubmitRegistration(c *gin.Context) {
	var req SubmitRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.EntitlementSvc.SubmitRegistration(c.Request.Context(), service.RegistrationInput{
		Email:                req.Email,
		DisplayName:          req.DisplayName,
		RequestedEntitlement: req.RequestedEntitlement,
		DeviceID:             req.DeviceID,
		Source:               req.Source,
		Note:                 req.Note,
	})
	if err != nil {
		writeEntitlementError(c, err)
		return
	}

	h.recordAudit(c, userAuditActor(result.User), domain.AuditActionRegistrationSubmit, result.Registration.ID, true, result.Registration.RequestedEntitlement)
	c.JSON(http.StatusCreated, result)
}

// GetUserEntitlementSnapshot handles GET /api/v1/users/:user_id/entitlements/snapshot.
func (h *EntitlementHandler) GetUserEntitlementSnapshot(c *gin.Context) {
	snapshot, err := h.EntitlementSvc.SnapshotForUser(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		writeEntitlementError(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

// ListRegistrations handles GET /api/v1/admin/registrations.
func (h *EntitlementHandler) ListRegistrations(c *gin.Context) {
	registrations, err := h.EntitlementSvc.ListRegistrations(c.Request.Context(), repository.RegistrationQuery{
		Status: c.Query("status"),
		UserID: c.Query("user_id"),
		Email:  c.Query("email"),
		Limit:  intQuery(c, "limit", 50),
		Offset: intQuery(c, "offset", 0),
	})
	if err != nil {
		writeEntitlementError(c, err)
		return
	}
	result := h.AdminProjector.ProjectRegistrationList(registrations)
	c.JSON(http.StatusOK, result)
}

// ReviewRegistration handles POST /api/v1/admin/registrations/:id/review.
func (h *EntitlementHandler) ReviewRegistration(c *gin.Context) {
	var req ReviewRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	registration, err := h.EntitlementSvc.ReviewRegistration(c.Request.Context(), service.ReviewRegistrationInput{
		RegistrationID: c.Param("id"),
		Status:         req.Status,
		ReviewedBy:     req.ReviewedBy,
		ReviewNote:     req.ReviewNote,
	})
	if err != nil {
		writeEntitlementError(c, err)
		return
	}

	h.recordAudit(c, defaultStringForHandler(req.ReviewedBy, "admin"), domain.AuditActionRegistrationReview, registration.ID, true, registration.Status)
	c.JSON(http.StatusOK, gin.H{"registration": h.AdminProjector.ProjectRegistration(*registration)})
}

// CreateGrant handles POST /api/v1/admin/grants.
func (h *EntitlementHandler) CreateGrant(c *gin.Context) {
	var req CreateGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	expiresAt, ok := parseOptionalTime(c, req.ExpiresAt)
	if !ok {
		return
	}
	grant, err := h.EntitlementSvc.CreateGrant(c.Request.Context(), service.GrantInput{
		UserID:         req.UserID,
		RegistrationID: req.RegistrationID,
		EntitlementID:  req.EntitlementID,
		CreatedBy:      req.CreatedBy,
		Source:         req.Source,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		writeEntitlementError(c, err)
		return
	}

	h.recordAudit(c, defaultStringForHandler(req.CreatedBy, "admin"), domain.AuditActionEntitlementGrant, grant.UserID, true, grant.EntitlementID)
	c.JSON(http.StatusCreated, gin.H{"grant": grant})
}

// ListGrants handles GET /api/v1/admin/grants.
func (h *EntitlementHandler) ListGrants(c *gin.Context) {
	grants, err := h.EntitlementSvc.ListGrants(c.Request.Context(), repository.EntitlementGrantQuery{
		UserID:         c.Query("user_id"),
		EntitlementID:  c.Query("entitlement_id"),
		Status:         c.Query("status"),
		IncludeExpired: c.Query("include_expired") == "true",
		Limit:          intQuery(c, "limit", 50),
		Offset:         intQuery(c, "offset", 0),
	})
	if err != nil {
		writeEntitlementError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(grants), "grants": grants})
}

func writeEntitlementError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidRegistration), errors.Is(err, service.ErrInvalidGrant), errors.Is(err, service.ErrUnknownEntitlement):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrUserNotFound), errors.Is(err, service.ErrRegistrationNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func parseOptionalTime(c *gin.Context, value string) (*time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at must be RFC3339"})
		return nil, false
	}
	return &parsed, true
}

func intQuery(c *gin.Context, key string, fallback int) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func (h *EntitlementHandler) recordAudit(c *gin.Context, actor, action, target string, success bool, details string) {
	if h.AuditSvc == nil {
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

func userAuditActor(user *domain.User) string {
	if user != nil && strings.TrimSpace(user.ID) != "" {
		return user.ID
	}
	return "unknown"
}

func defaultStringForHandler(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
