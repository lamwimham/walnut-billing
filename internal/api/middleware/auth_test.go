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

func TestUsersReadPermissionIsScopedSeparatelyFromAccessAccounts(t *testing.T) {
	support := AdminPrincipal{Name: "support", Permissions: []string{PermissionAccessAccountsRead}}
	if PrincipalHasPermission(support, PermissionUsersRead) {
		t.Fatalf("access account list permission must not imply user access summary permission")
	}
	users := AdminPrincipal{Name: "support-users", Permissions: []string{PermissionUsersRead}}
	if !PrincipalHasPermission(users, PermissionUsersRead) {
		t.Fatalf("expected users read permission to match itself")
	}
}

func TestOrdersReadPermissionIsScopedSeparatelyFromPaymentEvents(t *testing.T) {
	finance := AdminPrincipal{Name: "finance", Permissions: []string{PermissionPaymentEventsRead}}
	if PrincipalHasPermission(finance, PermissionOrdersRead) {
		t.Fatalf("payment event read permission must not imply admin orders read permission")
	}
	orders := AdminPrincipal{Name: "orders", Permissions: []string{PermissionOrdersRead}}
	if !PrincipalHasPermission(orders, PermissionOrdersRead) {
		t.Fatalf("expected orders read permission to match itself")
	}
}

func TestCloudStorageReadPermissionIsScopedSeparatelyFromUsersAndOrders(t *testing.T) {
	support := AdminPrincipal{Name: "support", Permissions: []string{PermissionUsersRead, PermissionOrdersRead}}
	if PrincipalHasPermission(support, PermissionCloudStorageRead) {
		t.Fatalf("user/order read permissions must not imply cloud storage read permission")
	}
	cloudOps := AdminPrincipal{Name: "cloud-ops", Permissions: []string{PermissionCloudStorageRead}}
	if !PrincipalHasPermission(cloudOps, PermissionCloudStorageRead) {
		t.Fatalf("expected cloud storage read permission to match itself")
	}
}

func TestSubscriptionsReadPermissionIsScopedSeparatelyFromOrdersAndPaymentEvents(t *testing.T) {
	finance := AdminPrincipal{Name: "finance", Permissions: []string{PermissionOrdersRead, PermissionPaymentEventsRead}}
	if PrincipalHasPermission(finance, PermissionSubscriptionsRead) {
		t.Fatalf("order/payment-event read permissions must not imply admin subscriptions read permission")
	}
	subscriptions := AdminPrincipal{Name: "subscription-ops", Permissions: []string{PermissionSubscriptionsRead}}
	if !PrincipalHasPermission(subscriptions, PermissionSubscriptionsRead) {
		t.Fatalf("expected subscriptions read permission to match itself")
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
