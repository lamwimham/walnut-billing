package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// APIKeyAuth validates requests against a list of allowed API keys.
// Expects header: Authorization: Bearer <api-key>
func APIKeyAuth(validKeys []string) gin.HandlerFunc {
	if len(validKeys) == 0 {
		panic("api key auth: no valid keys configured")
	}

	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing authorization header",
			})
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid authorization format, expected: Bearer <key>",
			})
			return
		}

		providedKey := parts[1]

		// Constant-time comparison to prevent timing attacks
		for _, validKey := range validKeys {
			if subtle.ConstantTimeCompare([]byte(providedKey), []byte(validKey)) == 1 {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "invalid API key",
		})
	}
}
