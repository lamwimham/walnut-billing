package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminCloudStorageHandler struct {
	AdminCloudStorageSvc service.AdminCloudStorageService
}

func NewAdminCloudStorageHandler(adminCloudStorageSvc service.AdminCloudStorageService) *AdminCloudStorageHandler {
	return &AdminCloudStorageHandler{AdminCloudStorageSvc: adminCloudStorageSvc}
}

// Usage handles GET /api/v1/admin/cloud-storage/usage.
func (h *AdminCloudStorageHandler) Usage(c *gin.Context) {
	if h == nil || h.AdminCloudStorageSvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "admin cloud storage service is not configured", "code": "admin_cloud_storage_service_unconfigured"})
		return
	}
	result, err := h.AdminCloudStorageSvc.Usage(c.Request.Context(), service.AdminCloudStorageUsageQuery{
		UserID: c.Query("user_id"),
		Status: c.Query("status"),
		Limit:  intQuery(c, "limit", 50),
		Offset: intQuery(c, "offset", 0),
	})
	if err != nil {
		writeAdminCloudStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ListUserProjects handles GET /api/v1/admin/users/:user_id/cloud-storage/projects.
func (h *AdminCloudStorageHandler) ListUserProjects(c *gin.Context) {
	if h == nil || h.AdminCloudStorageSvc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "admin cloud storage service is not configured", "code": "admin_cloud_storage_service_unconfigured"})
		return
	}
	result, err := h.AdminCloudStorageSvc.ListUserProjects(c.Request.Context(), service.AdminCloudStorageProjectQuery{
		UserID: c.Param("user_id"),
		Status: c.Query("status"),
		Limit:  intQuery(c, "limit", 50),
		Offset: intQuery(c, "offset", 0),
	})
	if err != nil {
		writeAdminCloudStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeAdminCloudStorageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAdminCloudStorageQuery):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_admin_cloud_storage_query"})
	case errors.Is(err, service.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "user_not_found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "admin_cloud_storage_query_failed"})
	}
}
