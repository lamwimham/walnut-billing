package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

type ctxKey string

const (
	RequestIDKey ctxKey = "request_id"
)

var requestCounter atomic.Uint64

// RequestID generates a unique request ID and injects it into:
// 1. Gin context (ctx.Value)
// 2. slog logger context (with "req_id" attribute)
// 3. Response header (X-Request-ID)
// 4. Client request header (forwarded if present)
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Use client-provided ID if present (for tracing across services)
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			// Generate: short counter-based ID for readability
			// Format: "req-{counter}-{timestamp}"
			counter := requestCounter.Add(1)
			reqID = c.Request.Header.Get("X-Forwarded-For")
			if reqID == "" {
				reqID = c.ClientIP()
			}
			// Use a simple monotonic counter for uniqueness
			reqID = formatRequestID(counter)
		}

		// Inject into context
		ctx := context.WithValue(c.Request.Context(), RequestIDKey, reqID)
		c.Request = c.Request.WithContext(ctx)

		// Create request-scoped logger
		reqLogger := slog.Default().With("req_id", reqID)
		ctx = context.WithValue(ctx, loggerKey{}, reqLogger)
		c.Request = c.Request.WithContext(ctx)

		// Set response header
		c.Header("X-Request-ID", reqID)

		c.Next()
	}
}

// GetRequestID returns the request ID from context.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}

// GetLogger returns the request-scoped logger from context.
func GetLogger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

type loggerKey struct{}

func formatRequestID(counter uint64) string {
	// Simple format: 8-char hex counter
	return fmt.Sprintf("%08x", counter)
}
