package middleware

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	AdminPrincipalContextKey = "admin_principal"

	PermissionAdminAll               = "admin.*"
	PermissionDashboardRead          = "admin.dashboard.read"
	PermissionLicensesRead           = "admin.licenses.read"
	PermissionLicensesWrite          = "admin.licenses.write"
	PermissionOrdersRead             = "admin.orders.read"
	PermissionSubscriptionsRead      = "admin.subscriptions.read"
	PermissionPaymentRead            = "admin.payment.read"
	PermissionPaymentWrite           = "admin.payment.write"
	PermissionAuditRead              = "admin.audit.read"
	PermissionPaymentEventsRead      = "admin.payment_events.read"
	PermissionPaymentEventsWrite     = "admin.payment_events.write"
	PermissionPaymentRiskRead        = "admin.payment_risk.read"
	PermissionPaymentRiskWrite       = "admin.payment_risk.write"
	PermissionFulfillmentsRead       = "admin.fulfillments.read"
	PermissionAccessAccountsRead     = "admin.access_accounts.read"
	PermissionAccessAccountsWrite    = "admin.access_accounts.write"
	PermissionUsersRead              = "admin.users.read"
	PermissionCloudStorageRead       = "admin.cloud_storage.read"
	PermissionAdminTestWrite         = "admin.test.write"
	PermissionRegistrationsRead      = "admin.registrations.read"
	PermissionRegistrationsWrite     = "admin.registrations.write"
	PermissionEntitlementGrantsRead  = "admin.entitlement_grants.read"
	PermissionEntitlementGrantsWrite = "admin.entitlement_grants.write"
	PermissionCreditsRead            = "admin.credits.read"
	PermissionCreditsWrite           = "admin.credits.write"
)

// AdminPrincipal is the authenticated operator identity attached to admin API
// requests. APIKey is used only for verification and is never returned by APIs.
type AdminPrincipal struct {
	Name        string   `json:"name"`
	APIKey      string   `json:"-"`
	Permissions []string `json:"permissions"`
}

// PrincipalsFromAPIKeys adapts legacy ADMIN_API_KEYS into full-access principals.
func PrincipalsFromAPIKeys(keys []string) []AdminPrincipal {
	principals := make([]AdminPrincipal, 0, len(keys))
	for i, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		principals = append(principals, AdminPrincipal{
			Name:        legacyAdminName(i),
			APIKey:      key,
			Permissions: []string{PermissionAdminAll},
		})
	}
	return principals
}

// APIKeyAuth validates requests against a list of allowed API keys.
// Expects header: Authorization: Bearer <api-key>.
func APIKeyAuth(validKeys []string) gin.HandlerFunc {
	return APIKeyAuthPrincipals(PrincipalsFromAPIKeys(validKeys))
}

// APIKeyAuthPrincipals authenticates an admin request and attaches the matching
// principal to Gin context so route-level permission middleware can authorize it.
func APIKeyAuthPrincipals(validPrincipals []AdminPrincipal) gin.HandlerFunc {
	principals := normalizeAdminPrincipals(validPrincipals)
	if len(principals) == 0 {
		panic("api key auth: no valid principals configured")
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
		for _, principal := range principals {
			if subtle.ConstantTimeCompare([]byte(providedKey), []byte(principal.APIKey)) == 1 {
				c.Set(AdminPrincipalContextKey, principal.withoutSecret())
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "invalid API key",
		})
	}
}

// RequirePermission authorizes an authenticated admin principal for one route.
func RequirePermission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := GetAdminPrincipal(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "admin authentication required", "code": "admin_auth_required"})
			return
		}
		if !PrincipalHasPermission(principal, permission) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin permission denied", "code": "admin_permission_denied", "permission": permission})
			return
		}
		c.Next()
	}
}

// GetAdminPrincipal returns the authenticated admin principal from Gin context.
func GetAdminPrincipal(c *gin.Context) (AdminPrincipal, bool) {
	value, ok := c.Get(AdminPrincipalContextKey)
	if !ok {
		return AdminPrincipal{}, false
	}
	principal, ok := value.(AdminPrincipal)
	return principal, ok
}

// PrincipalHasPermission matches exact permissions plus admin.* / admin:* style wildcards.
func PrincipalHasPermission(principal AdminPrincipal, permission string) bool {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return false
	}
	for _, candidate := range principal.Permissions {
		candidate = strings.TrimSpace(candidate)
		switch {
		case candidate == "*", candidate == PermissionAdminAll:
			return true
		case candidate == "admin:*" && strings.HasPrefix(permission, "admin."):
			return true
		case candidate == permission:
			return true
		case strings.HasSuffix(candidate, ".*") && strings.HasPrefix(permission, strings.TrimSuffix(candidate, "*")):
			return true
		case strings.HasSuffix(candidate, ":*") && strings.HasPrefix(permission, strings.TrimSuffix(candidate, "*")):
			return true
		}
	}
	return false
}

func normalizeAdminPrincipals(principals []AdminPrincipal) []AdminPrincipal {
	result := make([]AdminPrincipal, 0, len(principals))
	for i, principal := range principals {
		principal.APIKey = strings.TrimSpace(principal.APIKey)
		if principal.APIKey == "" {
			continue
		}
		principal.Name = strings.TrimSpace(principal.Name)
		if principal.Name == "" {
			principal.Name = legacyAdminName(i)
		}
		principal.Permissions = normalizePermissions(principal.Permissions)
		result = append(result, principal)
	}
	return result
}

func normalizePermissions(permissions []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		permission = strings.TrimSpace(permission)
		if permission == "" || seen[permission] {
			continue
		}
		seen[permission] = true
		result = append(result, permission)
	}
	return result
}

func legacyAdminName(index int) string {
	return fmt.Sprintf("legacy-admin-%d", index+1)
}

func (p AdminPrincipal) withoutSecret() AdminPrincipal {
	p.APIKey = ""
	return p
}
