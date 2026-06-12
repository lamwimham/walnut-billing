package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger is a Gin middleware that logs request method, path, status, and duration.
func Logger(l *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()
		reqID := GetRequestID(c.Request.Context())

		l.Info("request",
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"ip", c.ClientIP(),
			"req_id", reqID,
		)
	}
}

// Recovery is a custom recovery middleware that logs the panic.
func Recovery(l ...*slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				if len(l) > 0 {
					l[0].Error("panic recovered", "error", err, "path", c.Request.URL.Path)
				}
				c.AbortWithStatusJSON(500, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	}
}
