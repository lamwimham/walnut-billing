package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type CloudStorageHandler struct {
	CloudStorage service.CloudStorageService
}

func NewCloudStorageHandler(cloudStorage service.CloudStorageService) *CloudStorageHandler {
	return &CloudStorageHandler{CloudStorage: cloudStorage}
}

type CloudStorageResourceRequest struct {
	ResourceID   string `json:"resource_id" binding:"required"`
	ResourceKind string `json:"resource_kind"`
	ContentHash  string `json:"content_hash" binding:"required"`
	SizeBytes    int64  `json:"size_bytes"`
	ContentType  string `json:"content_type"`
	Filename     string `json:"filename"`
}

type CloudSyncAuthorizationRequest struct {
	UserID          string                        `json:"user_id" binding:"required"`
	ClientProjectID string                        `json:"client_project_id" binding:"required"`
	ProjectName     string                        `json:"project_name"`
	Resources       []CloudStorageResourceRequest `json:"resources" binding:"required"`
}

type CloudManifestCommitRequest struct {
	UserID          string                        `json:"user_id" binding:"required"`
	ClientProjectID string                        `json:"client_project_id" binding:"required"`
	ProjectName     string                        `json:"project_name"`
	ManifestHash    string                        `json:"manifest_hash" binding:"required"`
	ManifestVersion int                           `json:"manifest_version"`
	Resources       []CloudStorageResourceRequest `json:"resources" binding:"required"`
	IdempotencyKey  string                        `json:"idempotency_key" binding:"required"`
}

func (h *CloudStorageHandler) AuthorizeSync(c *gin.Context) {
	var req CloudSyncAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.CloudStorage.AuthorizeSync(c.Request.Context(), service.CloudSyncAuthorizationInput{
		UserID:          req.UserID,
		ClientProjectID: req.ClientProjectID,
		ProjectName:     req.ProjectName,
		Resources:       cloudResourceDescriptors(req.Resources),
	})
	if err != nil {
		writeCloudStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sync_session": result})
}

func (h *CloudStorageHandler) CommitManifest(c *gin.Context) {
	var req CloudManifestCommitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.CloudStorage.CommitManifest(c.Request.Context(), service.CloudManifestCommitInput{
		UserID:          req.UserID,
		ClientProjectID: req.ClientProjectID,
		ProjectName:     req.ProjectName,
		ManifestHash:    req.ManifestHash,
		ManifestVersion: req.ManifestVersion,
		Resources:       cloudResourceDescriptors(req.Resources),
		IdempotencyKey:  req.IdempotencyKey,
	})
	if err != nil {
		writeCloudStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *CloudStorageHandler) Usage(c *gin.Context) {
	usage, err := h.CloudStorage.Usage(c.Request.Context(), c.Param("user_id"))
	if err != nil {
		writeCloudStorageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": usage})
}

func cloudResourceDescriptors(resources []CloudStorageResourceRequest) []service.CloudResourceDescriptor {
	result := make([]service.CloudResourceDescriptor, 0, len(resources))
	for _, resource := range resources {
		result = append(result, service.CloudResourceDescriptor{
			ResourceID:   resource.ResourceID,
			ResourceKind: resource.ResourceKind,
			ContentHash:  resource.ContentHash,
			SizeBytes:    resource.SizeBytes,
			ContentType:  resource.ContentType,
			Filename:     resource.Filename,
		})
	}
	return result
}

func writeCloudStorageError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidCloudStorage):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrUserNotFound), errors.Is(err, service.ErrCloudProjectNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrCloudStorageAccessDenied):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrCloudStorageProviderNotConfigured):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrCloudStorageOverQuota):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
