package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIKeyAuthPrincipalsAttachesPrincipalAndRequiresPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/access-accounts",
		APIKeyAuthPrincipals([]AdminPrincipal{{Name: "support", APIKey: "support-key", Permissions: []string{PermissionAccessAccountsRead}}}),
		RequirePermission(PermissionAccessAccountsRead),
		func(c *gin.Context) {
			principal, ok := GetAdminPrincipal(c)
			if !ok || principal.Name != "support" || principal.APIKey != "" {
				t.Fatalf("unexpected principal in context: %#v", principal)
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
		},
	)

	req, _ := http.NewRequest(http.MethodGet, "/admin/access-accounts", nil)
	req.Header.Set("Authorization", "Bearer support-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequirePermissionRejectsMissingPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/grants",
		APIKeyAuthPrincipals([]AdminPrincipal{{Name: "support", APIKey: "support-key", Permissions: []string{PermissionAccessAccountsRead}}}),
		RequirePermission(PermissionEntitlementGrantsWrite),
		func(c *gin.Context) { c.Status(http.StatusNoContent) },
	)

	req, _ := http.NewRequest(http.MethodGet, "/admin/grants", nil)
	req.Header.Set("Authorization", "Bearer support-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIKeyAuthPrincipalsSupportsWildcard(t *testing.T) {
	principal := AdminPrincipal{Name: "ops", Permissions: []string{"admin.payment.*"}}
	if !PrincipalHasPermission(principal, PermissionPaymentRead) {
		t.Fatalf("expected wildcard permission to match payment provider read")
	}
	if PrincipalHasPermission(principal, PermissionAccessAccountsRead) {
		t.Fatalf("did not expect payment wildcard to match access accounts")
	}
	legacy := AdminPrincipal{Name: "legacy", Permissions: []string{"admin:*"}}
	if !PrincipalHasPermission(legacy, PermissionDashboardRead) {
		t.Fatalf("expected legacy wildcard to match admin permission")
	}
}

func TestAPIKeyAuthPrincipalsRejectsMissingAndInvalidKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin", APIKeyAuthPrincipals([]AdminPrincipal{{Name: "ops", APIKey: "secret", Permissions: []string{PermissionAdminAll}}}), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	missing, _ := http.NewRequest(http.MethodGet, "/admin", nil)
	missingW := httptest.NewRecorder()
	r.ServeHTTP(missingW, missing)
	if missingW.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing auth to be 401, got %d", missingW.Code)
	}

	invalid, _ := http.NewRequest(http.MethodGet, "/admin", nil)
	invalid.Header.Set("Authorization", "Bearer wrong")
	invalidW := httptest.NewRecorder()
	r.ServeHTTP(invalidW, invalid)
	if invalidW.Code != http.StatusForbidden {
		t.Fatalf("expected invalid key to be 403, got %d", invalidW.Code)
	}
}
