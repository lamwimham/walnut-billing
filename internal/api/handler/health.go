package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// HealthHandler provides health check endpoints.
type HealthHandler struct {
	DB *gorm.DB
}

func NewHealthHandler(db *gorm.DB) *HealthHandler {
	return &HealthHandler{DB: db}
}

// Ping handles GET /ping (basic liveness check).
func (h *HealthHandler) Ping(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}

// Health handles GET /health (deep readiness check).
func (h *HealthHandler) Health(c *gin.Context) {
	// Check database connection
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sqlDB, err := h.DB.DB()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"checks": gin.H{
				"database": "failed to get db connection",
			},
		})
		return
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"checks": gin.H{
				"database": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
		"checks": gin.H{
			"database": "ok",
		},
		"version": "0.3.0",
	})
}
