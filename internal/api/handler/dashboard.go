package handler

import (
	"net/http"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

// DashboardHandler aggregates data for the admin dashboard UI.
type DashboardHandler struct {
	LicenseSvc service.LicenseService
	PaymentSvc *payment.PaymentService
}

func NewDashboardHandler(licenseSvc service.LicenseService, paymentSvc *payment.PaymentService) *DashboardHandler {
	return &DashboardHandler{
		LicenseSvc: licenseSvc,
		PaymentSvc: paymentSvc,
	}
}

// GetDashboard GET /api/v1/admin/dashboard
// Aggregates license stats, provider status, and recent licenses for the dashboard UI.
func (h *DashboardHandler) GetDashboard(c *gin.Context) {
	ctx := c.Request.Context()

	// Fetch licenses
	allLicenses, err := h.LicenseSvc.ListLicenses(ctx, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Compute license stats
	licStats := map[string]int{"total": len(allLicenses), "active": 0, "inactive": 0, "expired": 0, "grace": 0}
	for _, l := range allLicenses {
		licStats[l.Status]++
	}

	// Get recent licenses (last 5)
	recent := allLicenses
	if len(allLicenses) > 5 {
		recent = allLicenses[len(allLicenses)-5:]
	}

	// Get provider status
	providers := h.PaymentSvc.GetProviderStatus()

	c.JSON(http.StatusOK, gin.H{
		"license_stats":   licStats,
		"providers":       providers,
		"recent_licenses": recent,
	})
}
