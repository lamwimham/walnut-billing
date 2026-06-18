package handler

import (
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AccessAdminHandler struct {
	AccessAdminSvc service.AccessAdminService
}

func NewAccessAdminHandler(accessAdminSvc service.AccessAdminService) *AccessAdminHandler {
	return &AccessAdminHandler{AccessAdminSvc: accessAdminSvc}
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
