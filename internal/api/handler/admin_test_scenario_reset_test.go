package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"walnut-billing/internal/api/middleware"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeAdminTestScenarioResetService struct {
	input  service.AdminTestScenarioResetInput
	result *service.AdminTestScenarioResetResult
	err    error
}

func (f *fakeAdminTestScenarioResetService) Reset(ctx context.Context, input service.AdminTestScenarioResetInput) (*service.AdminTestScenarioResetResult, error) {
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAdminTestScenarioResetHandlerReset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetSvc := &fakeAdminTestScenarioResetService{result: &service.AdminTestScenarioResetResult{
		Scenario:         service.AdminTestScenarioUserControlPlane,
		DryRun:           true,
		UserID:           "usr_1",
		EmailMasked:      "wr**er@example.com",
		EmailFingerprint: "fp123",
		AffectedCounts:   map[string]int64{"orders": 1},
	}}
	audit := &mockAuditWithData{}
	h := NewAdminTestScenarioResetHandler(resetSvc, audit)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AdminPrincipalContextKey, middleware.AdminPrincipal{Name: "ops", Permissions: []string{middleware.PermissionAdminTestWrite}})
		c.Next()
	})
	r.POST("/admin/test/scenarios/reset", h.Reset)

	req, _ := http.NewRequest(http.MethodPost, "/admin/test/scenarios/reset", strings.NewReader(`{"scenario":"user_control_plane","email":"writer@example.com","dry_run":true,"reason":"reset writer@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if resetSvc.input.Email != "writer@example.com" || !resetSvc.input.DryRun {
		t.Fatalf("unexpected reset input: %#v", resetSvc.input)
	}
	if strings.Contains(w.Body.String(), `"email":"`) || strings.Contains(w.Body.String(), "writer@example.com") {
		t.Fatalf("response leaked raw email: %s", w.Body.String())
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != domain.AuditActionAdminTestScenarioReset || audit.entries[0].Actor != "ops" || !audit.entries[0].Success {
		t.Fatalf("expected successful audit from principal, got %#v", audit.entries)
	}
	if strings.Contains(audit.entries[0].Details, "writer@example.com") {
		t.Fatalf("audit details leaked raw email: %s", audit.entries[0].Details)
	}
}

func TestAdminTestScenarioResetHandlerMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"unavailable", service.ErrAdminTestScenarioResetUnavailable, http.StatusForbidden, "admin_test_scenario_reset_unavailable"},
		{"invalid", service.ErrInvalidAdminTestScenarioReset, http.StatusBadRequest, "invalid_admin_test_scenario_reset"},
		{"not_found", service.ErrAdminTestScenarioNotFound, http.StatusNotFound, "admin_test_scenario_not_found"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "admin_test_scenario_reset_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h := NewAdminTestScenarioResetHandler(&fakeAdminTestScenarioResetService{err: tt.err}, &mockAuditWithData{})
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(middleware.AdminPrincipalContextKey, middleware.AdminPrincipal{Name: "ops", Permissions: []string{middleware.PermissionAdminTestWrite}})
				c.Next()
			})
			r.POST("/admin/test/scenarios/reset", h.Reset)

			req, _ := http.NewRequest(http.MethodPost, "/admin/test/scenarios/reset", strings.NewReader(`{"email":"writer@example.com"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("expected status %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tt.code) {
				t.Fatalf("expected code %s, got %s", tt.code, w.Body.String())
			}
		})
	}
}

func TestAdminTestScenarioResetHandlerRequiresScopedPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAdminTestScenarioResetHandler(&fakeAdminTestScenarioResetService{}, nil)

	missingAuth := gin.New()
	missingAuth.POST("/admin/test/scenarios/reset", h.Reset)
	req, _ := http.NewRequest(http.MethodPost, "/admin/test/scenarios/reset", strings.NewReader(`{"email":"writer@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	missingAuth.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing principal to be 401, got %d: %s", w.Code, w.Body.String())
	}

	support := gin.New()
	support.Use(func(c *gin.Context) {
		c.Set(middleware.AdminPrincipalContextKey, middleware.AdminPrincipal{Name: "support", Permissions: []string{middleware.PermissionUsersRead}})
		c.Next()
	})
	support.POST("/admin/test/scenarios/reset", h.Reset)
	req, _ = http.NewRequest(http.MethodPost, "/admin/test/scenarios/reset", strings.NewReader(`{"email":"writer@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	support.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected missing admin.test.write to be 403, got %d: %s", w.Code, w.Body.String())
	}
}
