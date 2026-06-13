package handler

import (
	"errors"
	"net/http"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type PaymentRiskHandler struct {
	PaymentRiskSvc service.PaymentRiskService
	AuditSvc       service.AuditService
}

func NewPaymentRiskHandler(paymentRiskSvc service.PaymentRiskService, auditSvc service.AuditService) *PaymentRiskHandler {
	return &PaymentRiskHandler{PaymentRiskSvc: paymentRiskSvc, AuditSvc: auditSvc}
}

type ResolvePaymentRiskFlagRequest struct {
	ResolvedBy string `json:"resolved_by"`
	Note       string `json:"note"`
}

// ListFlags handles GET /api/v1/admin/payment-risk-flags.
func (h *PaymentRiskHandler) ListFlags(c *gin.Context) {
	if h.PaymentRiskSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment risk service unavailable"})
		return
	}
	flags, err := h.PaymentRiskSvc.ListFlags(c.Request.Context(), repository.PaymentRiskFlagQuery{
		UserID:     c.Query("user_id"),
		OutTradeNo: c.Query("out_trade_no"),
		Provider:   c.Query("provider"),
		Reason:     c.Query("reason"),
		Severity:   c.Query("severity"),
		Status:     c.Query("status"),
		Limit:      intQuery(c, "limit", 50),
		Offset:     intQuery(c, "offset", 0),
	})
	if err != nil {
		writePaymentRiskError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"total": len(flags), "risk_flags": flags})
}

// GetFlag handles GET /api/v1/admin/payment-risk-flags/:id.
func (h *PaymentRiskHandler) GetFlag(c *gin.Context) {
	if h.PaymentRiskSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment risk service unavailable"})
		return
	}
	flag, err := h.PaymentRiskSvc.GetFlag(c.Request.Context(), c.Param("id"))
	if err != nil {
		writePaymentRiskError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"risk_flag": flag})
}

// ResolveFlag handles POST /api/v1/admin/payment-risk-flags/:id/resolve.
func (h *PaymentRiskHandler) ResolveFlag(c *gin.Context) {
	if h.PaymentRiskSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment risk service unavailable"})
		return
	}
	var req ResolvePaymentRiskFlagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	flag, err := h.PaymentRiskSvc.ResolveFlag(c.Request.Context(), service.ResolvePaymentRiskFlagInput{
		ID:         c.Param("id"),
		ResolvedBy: req.ResolvedBy,
		Note:       req.Note,
	})
	if err != nil {
		writePaymentRiskError(c, err)
		return
	}
	h.recordAudit(c, defaultStringForHandler(req.ResolvedBy, "admin"), flag.ID, req.Note)
	c.JSON(http.StatusOK, gin.H{"risk_flag": flag})
}

func writePaymentRiskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidPaymentRiskFlag):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrPaymentRiskFlagNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *PaymentRiskHandler) recordAudit(c *gin.Context, actor, target, details string) {
	if h.AuditSvc == nil {
		return
	}
	h.AuditSvc.Record(c.Request.Context(), &domain.AuditEntry{
		Actor:     defaultStringForHandler(strings.TrimSpace(actor), "admin"),
		Action:    domain.AuditActionPaymentRiskResolve,
		Target:    target,
		Success:   true,
		Details:   strings.TrimSpace(details),
		IPAddress: clientIP(c),
	})
}
