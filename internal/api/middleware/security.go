package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type CORSConfig struct {
	AllowedOrigins []string
}

type SecurityHeadersConfig struct {
	Enabled           bool
	HSTSMaxAgeSeconds int
}

func CORS(config CORSConfig) gin.HandlerFunc {
	allowed := normalizeOriginSet(config.AllowedOrigins)
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" {
			c.Header("Vary", appendVary(c.Writer.Header().Get("Vary"), "Origin"))
			if _, ok := allowed[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID")
				c.Header("Access-Control-Max-Age", "600")
			} else {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cors origin not allowed"})
				return
			}
		}
		if c.Request.Method == http.MethodOptions && origin != "" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func SecurityHeaders(config SecurityHeadersConfig) gin.HandlerFunc {
	maxAge := config.HSTSMaxAgeSeconds
	if maxAge <= 0 {
		maxAge = 31536000
	}
	return func(c *gin.Context) {
		if config.Enabled {
			c.Header("X-Content-Type-Options", "nosniff")
			c.Header("X-Frame-Options", "DENY")
			c.Header("Referrer-Policy", "no-referrer")
			c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			c.Header("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			c.Header("Strict-Transport-Security", fmt.Sprintf("max-age=%d; includeSubDomains", maxAge))
		}
		c.Next()
	}
}

func normalizeOriginSet(origins []string) map[string]struct{} {
	allowed := make(map[string]struct{})
	for _, origin := range origins {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin != "" && origin != "*" {
			allowed[origin] = struct{}{}
		}
	}
	return allowed
}

func appendVary(current string, value string) string {
	if strings.TrimSpace(current) == "" {
		return value
	}
	for _, part := range strings.Split(current, ",") {
		if strings.EqualFold(strings.TrimSpace(part), value) {
			return current
		}
	}
	return current + ", " + value
}
