package config

import "testing"

func TestLoadReadsAdminPrincipalsJSON(t *testing.T) {
	t.Setenv("ADMIN_API_KEYS", "legacy-key")
	t.Setenv("ADMIN_PRINCIPALS_JSON", `[{"name":"support","key":"support-key","permissions":["admin.access_accounts.read"," admin.audit.read "]}]`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Admin.APIKeys) != 1 || cfg.Admin.APIKeys[0] != "legacy-key" {
		t.Fatalf("unexpected legacy keys: %#v", cfg.Admin.APIKeys)
	}
	if len(cfg.Admin.Principals) != 1 {
		t.Fatalf("expected one scoped principal, got %#v", cfg.Admin.Principals)
	}
	principal := cfg.Admin.Principals[0]
	if principal.Name != "support" || principal.Key != "support-key" {
		t.Fatalf("unexpected principal identity: %#v", principal)
	}
	if len(principal.Permissions) != 2 || principal.Permissions[1] != "admin.audit.read" {
		t.Fatalf("expected trimmed permissions, got %#v", principal.Permissions)
	}
}

func TestLoadRejectsInvalidAdminPrincipalsJSON(t *testing.T) {
	t.Setenv("ADMIN_PRINCIPALS_JSON", `{not-json}`)
	if _, err := Load(); err == nil {
		t.Fatalf("expected invalid admin principals json to fail")
	}
}
