package handler

import (
	"fmt"
	"net/http"
	"time"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	LicenseSvc service.LicenseService
	AuditSvc   service.AuditService
}

func NewAdminHandler(licenseSvc service.LicenseService, auditSvc service.AuditService) *AdminHandler {
	return &AdminHandler{LicenseSvc: licenseSvc, AuditSvc: auditSvc}
}

// ListLicenses handles GET /api/v1/admin/licenses
func (h *AdminHandler) ListLicenses(c *gin.Context) {
	status := c.Query("status") // optional filter

	licenses, err := h.LicenseSvc.ListLicenses(c.Request.Context(), status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total": len(licenses),
		"licenses": licenses,
	})
}

// GetLicense handles GET /api/v1/admin/licenses/:key
func (h *AdminHandler) GetLicense(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	lic, err := h.LicenseSvc.GetLicenseByKey(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "license not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"license": lic})
}

// Stats handles GET /api/v1/admin/stats
func (h *AdminHandler) Stats(c *gin.Context) {
	allLicenses, err := h.LicenseSvc.ListLicenses(c.Request.Context(), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	stats := map[string]int{
		"total":    len(allLicenses),
		"active":   0,
		"inactive": 0,
		"expired":  0,
	}

	for _, lic := range allLicenses {
		stats[lic.Status]++
	}

	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

// CheckExpiry handles POST /api/v1/admin/licenses/check-expiry
// Manually trigger expiry check for all active licenses.
func (h *AdminHandler) CheckExpiry(c *gin.Context) {
	count, err := h.LicenseSvc.CheckExpiry(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"expired_count": count,
		"message":       fmt.Sprintf("Checked and expired %d license(s)", count),
	})
}

// LicenseStatus handles GET /api/v1/licenses/:key/status
// Returns detailed status including grace period info.
func (h *AdminHandler) LicenseStatus(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}

	info, err := h.LicenseSvc.GetLicenseStatus(c.Request.Context(), key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "license not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": info})
}

// ListExpiring handles GET /api/v1/admin/licenses/expiring
// Returns licenses expiring within N days (default: 30).
func (h *AdminHandler) ListExpiring(c *gin.Context) {
	days := 30 // default
	if d := c.Query("days"); d != "" {
		if _, err := fmt.Sscanf(d, "%d", &days); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid days parameter"})
			return
		}
	}

	expiring, err := h.LicenseSvc.ListExpiringSoon(c.Request.Context(), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total":    len(expiring),
		"days":     days,
		"licenses": expiring,
	})
}

// GetAuditLogs handles GET /api/v1/admin/audit
// Returns audit logs with filtering and pagination.
func (h *AdminHandler) GetAuditLogs(c *gin.Context) {
	var q repository.AuditQuery
	q.Action = c.Query("action")
	q.Actor = c.Query("actor")
	q.Target = c.Query("target")

	if s := c.Query("success"); s != "" {
		val := s == "true"
		q.Success = &val
	}

	if start := c.Query("start"); start != "" {
		if t, err := parseTime(start); err == nil {
			q.StartTime = t
		}
	}
	if end := c.Query("end"); end != "" {
		if t, err := parseTime(end); err == nil {
			q.EndTime = t
		}
	}

	q.Limit = 50
	if l := c.Query("limit"); l != "" {
		if _, err := fmt.Sscanf(l, "%d", &q.Limit); err != nil || q.Limit > 200 {
			q.Limit = 50
		}
	}
	if o := c.Query("offset"); o != "" {
		if _, err := fmt.Sscanf(o, "%d", &q.Offset); err != nil {
			q.Offset = 0
		}
	}

	entries, total, err := h.AuditSvc.Query(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total": total,
		"logs":  entries,
	})
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
