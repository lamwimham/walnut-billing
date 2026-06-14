package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type CreditHandler struct {
	CreditSvc service.CreditService
	AuditSvc  service.AuditService
}

func NewCreditHandler(creditSvc service.CreditService, auditSvc service.AuditService) *CreditHandler {
	return &CreditHandler{CreditSvc: creditSvc, AuditSvc: auditSvc}
}

type CreditGrantRequest struct {
	UserID         string `json:"user_id" binding:"required"`
	Amount         int64  `json:"amount" binding:"required"`
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	Source         string `json:"source"`
	Description    string `json:"description"`
}

type CreditReserveRequest struct {
	UserID          string `json:"user_id" binding:"required"`
	Operation       string `json:"operation" binding:"required"`
	Amount          int64  `json:"amount" binding:"required"`
	IdempotencyKey  string `json:"idempotency_key" binding:"required"`
	FeatureID       string `json:"feature_id"`
	ExecutionID     string `json:"execution_id"`
	ProjectID       string `json:"project_id"`
	DocumentID      string `json:"document_id"`
	ConversationID  string `json:"conversation_id"`
	ClientMessageID string `json:"client_message_id"`
	ExpiresAt       string `json:"expires_at"`
}

type CreditFinalizeRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
}

type CreditBucketExpireRequest struct {
	Now   string `json:"now"`
	Limit int    `json:"limit"`
}

// GetAccount handles GET /api/v1/users/:user_id/credits/account.
func (h *CreditHandler) GetAccount(c *gin.Context) {
	account, err := h.CreditSvc.AccountForUser(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		writeCreditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"account": account})
}

// Grant handles POST /api/v1/admin/credits/grants.
func (h *CreditHandler) Grant(c *gin.Context) {
	var req CreditGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.CreditSvc.Grant(c.Request.Context(), service.CreditGrantInput{
		UserID:         req.UserID,
		Amount:         req.Amount,
		IdempotencyKey: req.IdempotencyKey,
		Source:         req.Source,
		Description:    req.Description,
	})
	if err != nil {
		writeCreditError(c, err)
		return
	}
	h.recordAudit(c, defaultStringForHandler(req.Source, "admin"), domain.AuditActionCreditGrant, req.UserID, true, req.Description)
	c.JSON(http.StatusCreated, result)
}

// Reserve handles POST /api/v1/credits/reservations.
func (h *CreditHandler) Reserve(c *gin.Context) {
	var req CreditReserveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	expiresAt, ok := parseOptionalTime(c, req.ExpiresAt)
	if !ok {
		return
	}
	result, err := h.CreditSvc.Reserve(c.Request.Context(), service.CreditReservationInput{
		UserID:         req.UserID,
		Operation:      req.Operation,
		Amount:         req.Amount,
		IdempotencyKey: req.IdempotencyKey,
		FeatureID:      req.FeatureID,
		ExecutionID:    req.ExecutionID,
		Metadata:       usageMetadataFromReserveRequest(req),
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		writeCreditError(c, err)
		return
	}
	h.recordAudit(c, req.UserID, domain.AuditActionCreditReserve, result.Reservation.ID, true, req.Operation)
	c.JSON(http.StatusCreated, result)
}

// Commit handles POST /api/v1/credits/reservations/:id/commit.
func (h *CreditHandler) Commit(c *gin.Context) {
	h.finalize(c, domain.AuditActionCreditCommit, h.CreditSvc.Commit)
}

// Release handles POST /api/v1/credits/reservations/:id/release.
func (h *CreditHandler) Release(c *gin.Context) {
	h.finalize(c, domain.AuditActionCreditRelease, h.CreditSvc.Release)
}

// ExpireBuckets handles POST /api/v1/admin/credits/buckets/expire.
func (h *CreditHandler) ExpireBuckets(c *gin.Context) {
	var req CreditBucketExpireRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	now, ok := parseOptionalRFC3339(c, "now", req.Now)
	if !ok {
		return
	}
	input := service.CreditBucketExpiryInput{Limit: req.Limit}
	if now != nil {
		input.Now = *now
	}
	result, err := h.CreditSvc.ExpireBuckets(c.Request.Context(), input)
	if err != nil {
		writeCreditError(c, err)
		return
	}
	h.recordAudit(c, "admin", domain.AuditActionCreditExpire, "credit_buckets", true, "expire credit buckets")
	c.JSON(http.StatusOK, result)
}

// ListTransactions handles GET /api/v1/admin/users/:user_id/credits/transactions.
func (h *CreditHandler) ListTransactions(c *gin.Context) {
	transactions, err := h.CreditSvc.ListTransactions(
		c.Request.Context(),
		c.Param("user_id"),
		intQuery(c, "limit", 50),
		intQuery(c, "offset", 0),
	)
	if err != nil {
		writeCreditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(transactions), "transactions": transactions})
}

// ListUsageRecords handles GET /api/v1/admin/users/:user_id/credits/usage-records.
func (h *CreditHandler) ListUsageRecords(c *gin.Context) {
	records, err := h.CreditSvc.ListUsageRecords(
		c.Request.Context(),
		service.UsageRecordQuery{
			UserID:      c.Param("user_id"),
			FeatureID:   strings.TrimSpace(c.Query("feature_id")),
			Operation:   strings.TrimSpace(c.Query("operation")),
			ExecutionID: strings.TrimSpace(c.Query("execution_id")),
			Status:      strings.TrimSpace(c.Query("status")),
			Limit:       intQuery(c, "limit", 50),
			Offset:      intQuery(c, "offset", 0),
		},
	)
	if err != nil {
		writeCreditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(records), "usage_records": records})
}

func (h *CreditHandler) finalize(
	c *gin.Context,
	auditAction string,
	fn func(context.Context, service.CreditFinalizationInput) (*service.CreditMutationResult, error),
) {
	var req CreditFinalizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := fn(c.Request.Context(), service.CreditFinalizationInput{
		ReservationID:  c.Param("id"),
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		writeCreditError(c, err)
		return
	}
	h.recordAudit(c, result.Account.UserID, auditAction, result.Reservation.ID, true, result.Reservation.Operation)
	c.JSON(http.StatusOK, result)
}

func writeCreditError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidCreditAmount), errors.Is(err, service.ErrIdempotencyRequired):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrInsufficientCredits):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrUserNotFound), errors.Is(err, service.ErrCreditAccountNotFound), errors.Is(err, service.ErrReservationNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrReservationNotPending), errors.Is(err, service.ErrReservationExpired):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *CreditHandler) recordAudit(c *gin.Context, actor, action, target string, success bool, details string) {
	if h.AuditSvc == nil {
		return
	}
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     defaultStringForHandler(strings.TrimSpace(actor), "unknown"),
		Action:    action,
		Target:    target,
		Success:   success,
		Details:   details,
		IPAddress: clientIP(c),
	})
}

func parseOptionalRFC3339(c *gin.Context, field string, value string) (*time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": field + " must be RFC3339"})
		return nil, false
	}
	parsed = parsed.UTC()
	return &parsed, true
}

func usageMetadataFromReserveRequest(req CreditReserveRequest) map[string]any {
	metadata := make(map[string]any)
	putUsageMetadata(metadata, "project_id", req.ProjectID)
	putUsageMetadata(metadata, "document_id", req.DocumentID)
	putUsageMetadata(metadata, "conversation_id", req.ConversationID)
	putUsageMetadata(metadata, "client_message_id", req.ClientMessageID)
	return metadata
}

func putUsageMetadata(metadata map[string]any, key string, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		metadata[key] = value
	}
}
