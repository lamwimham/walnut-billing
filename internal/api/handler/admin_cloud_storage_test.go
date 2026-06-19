package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeAdminCloudStorageService struct {
	usageQuery    service.AdminCloudStorageUsageQuery
	usageResult   *service.AdminCloudStorageUsage
	projectQuery  service.AdminCloudStorageProjectQuery
	projectResult *service.AdminCloudStorageProjectList
	err           error
}

func (f *fakeAdminCloudStorageService) Usage(ctx context.Context, query service.AdminCloudStorageUsageQuery) (*service.AdminCloudStorageUsage, error) {
	f.usageQuery = query
	if f.err != nil {
		return nil, f.err
	}
	return f.usageResult, nil
}

func (f *fakeAdminCloudStorageService) ListUserProjects(ctx context.Context, query service.AdminCloudStorageProjectQuery) (*service.AdminCloudStorageProjectList, error) {
	f.projectQuery = query
	if f.err != nil {
		return nil, f.err
	}
	return f.projectResult, nil
}

func TestAdminCloudStorageHandler_Usage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAdminCloudStorageService{usageResult: &service.AdminCloudStorageUsage{
		TotalUsers:     1,
		TotalUsedBytes: 700,
		Users: []service.AdminCloudStorageUsageUser{{
			User:      service.AdminCloudStorageUserIdentity{ID: "usr_1", EmailMasked: "wr****er@example.com"},
			UsedBytes: 700,
		}},
	}}
	handler := NewAdminCloudStorageHandler(svc)
	r := gin.New()
	r.GET("/admin/cloud-storage/usage", handler.Usage)

	req, _ := http.NewRequest(http.MethodGet, "/admin/cloud-storage/usage?user_id=usr_1&status=active&limit=5&offset=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.usageQuery.UserID != "usr_1" || svc.usageQuery.Status != "active" || svc.usageQuery.Limit != 5 || svc.usageQuery.Offset != 2 {
		t.Fatalf("unexpected usage query mapping: %#v", svc.usageQuery)
	}
	var response service.AdminCloudStorageUsage
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TotalUsers != 1 || response.Users[0].UsedBytes != 700 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestAdminCloudStorageHandler_ListUserProjects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAdminCloudStorageService{projectResult: &service.AdminCloudStorageProjectList{
		User:          service.AdminCloudStorageUserIdentity{ID: "usr_1"},
		TotalProjects: 1,
		Projects: []service.AdminCloudStorageProjectSummary{{
			ID:                "cpr_1",
			ClientProjectID:   "local-project",
			NameMasked:        "Se****ct",
			ActiveObjectCount: 2,
			ActiveBytes:       700,
		}},
	}}
	handler := NewAdminCloudStorageHandler(svc)
	r := gin.New()
	r.GET("/admin/users/:user_id/cloud-storage/projects", handler.ListUserProjects)

	req, _ := http.NewRequest(http.MethodGet, "/admin/users/usr_1/cloud-storage/projects?status=active&limit=5&offset=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.projectQuery.UserID != "usr_1" || svc.projectQuery.Status != "active" || svc.projectQuery.Limit != 5 || svc.projectQuery.Offset != 2 {
		t.Fatalf("unexpected project query mapping: %#v", svc.projectQuery)
	}
	var response service.AdminCloudStorageProjectList
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.TotalProjects != 1 || response.Projects[0].ID != "cpr_1" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestAdminCloudStorageHandler_MapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid", service.ErrInvalidAdminCloudStorageQuery, http.StatusBadRequest, "invalid_admin_cloud_storage_query"},
		{"not_found", service.ErrUserNotFound, http.StatusNotFound, "user_not_found"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "admin_cloud_storage_query_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			handler := NewAdminCloudStorageHandler(&fakeAdminCloudStorageService{err: tt.err})
			r := gin.New()
			r.GET("/admin/cloud-storage/usage", handler.Usage)

			req, _ := http.NewRequest(http.MethodGet, "/admin/cloud-storage/usage", nil)
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
