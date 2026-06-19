package handler

import (
	"context"
	"encoding/json"
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

type fakeAccessAdminService struct {
	query  service.AccessAdminQuery
	result *service.AccessAccountList
}

type fakeAccessDeviceAdminService struct {
	input  service.AccessDeviceRevokeInput
	device *domain.UserDevice
	err    error
}

type fakeAdminUserAccessSummaryService struct {
	input  service.AdminUserAccessSummaryInput
	result *service.AdminUserAccessSummary
	err    error
}

func (f *fakeAccessAdminService) ListAccounts(ctx context.Context, query service.AccessAdminQuery) (*service.AccessAccountList, error) {
	f.query = query
	return f.result, nil
}

func (f *fakeAccessDeviceAdminService) RevokeDevice(ctx context.Context, input service.AccessDeviceRevokeInput) (*domain.UserDevice, error) {
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	return f.device, nil
}

func (f *fakeAdminUserAccessSummaryService) Get(ctx context.Context, input service.AdminUserAccessSummaryInput) (*service.AdminUserAccessSummary, error) {
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAccessAdminHandler_ListAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccessAdminService{result: &service.AccessAccountList{Total: 1, Accounts: []service.AccessAccountRecord{{UserID: "usr_1", EmailMasked: "wr**er@example.com"}}}}
	handler := NewAccessAdminHandler(fake, nil, nil)
	r := gin.New()
	r.GET("/admin/access-accounts", handler.ListAccounts)

	req, _ := http.NewRequest(http.MethodGet, "/admin/access-accounts?email=writer@example.com&limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.query.Email != "writer@example.com" || fake.query.Limit != 5 {
		t.Fatalf("expected query passthrough, got %#v", fake.query)
	}
	if strings.Contains(w.Body.String(), `"email":"`) || strings.Contains(w.Body.String(), "writer@example.com") {
		t.Fatalf("response leaked raw email: %s", w.Body.String())
	}
	var resp service.AccessAccountList
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || resp.Accounts[0].EmailMasked != "wr**er@example.com" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestAccessAdminHandler_GetUserAccessSummary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	summarySvc := &fakeAdminUserAccessSummaryService{result: &service.AdminUserAccessSummary{User: service.AdminUserAccessIdentity{ID: "usr_1", EmailMasked: "wr**er@example.com"}}}
	handler := NewAccessAdminHandlerWithSummary(nil, nil, summarySvc, nil)
	r := gin.New()
	r.GET("/admin/users/:user_id/access", handler.GetUserAccessSummary)

	req, _ := http.NewRequest(http.MethodGet, "/admin/users/usr_1/access?recent_limit=3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if summarySvc.input.UserID != "usr_1" || summarySvc.input.RecentLimit != 3 {
		t.Fatalf("unexpected summary input: %#v", summarySvc.input)
	}
	if strings.Contains(w.Body.String(), `"email":"`) || strings.Contains(w.Body.String(), "writer@example.com") {
		t.Fatalf("response leaked raw email: %s", w.Body.String())
	}
}

func TestAccessAdminHandler_GetUserAccessSummaryMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid", service.ErrInvalidAdminUserAccessSummary, http.StatusBadRequest, "invalid_admin_user_access_summary"},
		{"not_found", service.ErrUserNotFound, http.StatusNotFound, "user_not_found"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "admin_user_access_summary_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			handler := NewAccessAdminHandlerWithSummary(nil, nil, &fakeAdminUserAccessSummaryService{err: tt.err}, nil)
			r := gin.New()
			r.GET("/admin/users/:user_id/access", handler.GetUserAccessSummary)

			req, _ := http.NewRequest(http.MethodGet, "/admin/users/usr_missing/access", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("expected status %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tt.code) {
				t.Fatalf("expected error code %s, got %s", tt.code, w.Body.String())
			}
		})
	}
}

func TestAccessAdminHandler_RevokeDevice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deviceSvc := &fakeAccessDeviceAdminService{device: &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusDisabled}}
	audit := &mockAuditWithData{}
	handler := NewAccessAdminHandler(nil, deviceSvc, audit)
	r := gin.New()
	r.POST("/admin/devices/:id/revoke", handler.RevokeDevice)

	req, _ := http.NewRequest(http.MethodPost, "/admin/devices/dev_1/revoke", strings.NewReader(`{"revoked_by":"ops","reason":"lost laptop"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if deviceSvc.input.DeviceID != "dev_1" || deviceSvc.input.RevokedBy != "ops" || deviceSvc.input.Reason != "lost laptop" {
		t.Fatalf("unexpected revoke input: %#v", deviceSvc.input)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != domain.AuditActionAccessDeviceRevoke || !audit.entries[0].Success {
		t.Fatalf("expected successful revoke audit, got %#v", audit.entries)
	}
	if strings.Contains(w.Body.String(), "device-1") {
		t.Fatalf("revoke response leaked raw device id: %s", w.Body.String())
	}
}

func TestAccessAdminHandler_RevokeDeviceUsesAuthenticatedPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deviceSvc := &fakeAccessDeviceAdminService{device: &domain.UserDevice{ID: "dev_1", UserID: "usr_1", Status: domain.DeviceStatusDisabled}}
	audit := &mockAuditWithData{}
	handler := NewAccessAdminHandler(nil, deviceSvc, audit)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AdminPrincipalContextKey, middleware.AdminPrincipal{Name: "ops-principal"})
		c.Next()
	})
	r.POST("/admin/devices/:id/revoke", handler.RevokeDevice)

	req, _ := http.NewRequest(http.MethodPost, "/admin/devices/dev_1/revoke", strings.NewReader(`{"revoked_by":"body-actor","reason":"lost laptop"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if deviceSvc.input.RevokedBy != "ops-principal" {
		t.Fatalf("expected principal actor to override body actor, got %#v", deviceSvc.input)
	}
	if len(audit.entries) != 1 || audit.entries[0].Actor != "ops-principal" {
		t.Fatalf("expected principal audit actor, got %#v", audit.entries)
	}
}

func TestAccessAdminHandler_RevokeDeviceMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid", service.ErrInvalidAccessDevice, http.StatusBadRequest, "invalid_access_device"},
		{"not_found", service.ErrAccessDeviceNotFound, http.StatusNotFound, "access_device_not_found"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			handler := NewAccessAdminHandler(nil, &fakeAccessDeviceAdminService{err: tt.err}, nil)
			r := gin.New()
			r.POST("/admin/devices/:id/revoke", handler.RevokeDevice)

			req, _ := http.NewRequest(http.MethodPost, "/admin/devices/dev_missing/revoke", strings.NewReader(`{"revoked_by":"ops"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("expected status %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
			if tt.code != "" && !strings.Contains(w.Body.String(), tt.code) {
				t.Fatalf("expected error code %s, got %s", tt.code, w.Body.String())
			}
		})
	}
}
