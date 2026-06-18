package handler

import (
	"errors"
	"net/http"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type AccessSnapshotHandler struct {
	Issuer service.AccessSnapshotIssuer
}

func NewAccessSnapshotHandler(issuer service.AccessSnapshotIssuer) *AccessSnapshotHandler {
	return &AccessSnapshotHandler{Issuer: issuer}
}

// GetSnapshot handles GET /api/v1/users/:user_id/access/snapshot.
func (h *AccessSnapshotHandler) GetSnapshot(c *gin.Context) {
	if h == nil || h.Issuer == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": service.ErrInvalidAccessSnapshot.Error()})
		return
	}
	snapshot, err := h.Issuer.Issue(c.Request.Context(), service.AccessSnapshotIssueInput{
		UserID:   c.Param("user_id"),
		DeviceID: c.Query("device_id"),
	})
	if err != nil {
		writeAccessSnapshotError(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func writeAccessSnapshotError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidAccessSnapshot):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
