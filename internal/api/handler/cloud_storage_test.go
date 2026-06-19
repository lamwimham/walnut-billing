package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeCloudStorageService struct {
	authorizeInput  service.CloudSyncAuthorizationInput
	authorizeResult *service.CloudSyncAuthorization
	commitInput     service.CloudManifestCommitInput
	commitResult    *service.CloudManifestCommitResult
	usageUserID     string
	usageResult     *service.CloudStorageUsage
	projectQuery    service.CloudStorageProjectQuery
	projectResult   *service.CloudStorageProjectList
	manifestQuery   service.CloudStorageLatestManifestQuery
	manifestResult  *service.CloudStorageLatestManifest
	downloadInput   service.CloudDownloadTargetInput
	downloadResult  *service.CloudDownloadTargetAuthorization
	err             error
}

func (f *fakeCloudStorageService) AuthorizeSync(ctx context.Context, input service.CloudSyncAuthorizationInput) (*service.CloudSyncAuthorization, error) {
	f.authorizeInput = input
	return f.authorizeResult, f.err
}

func (f *fakeCloudStorageService) CommitManifest(ctx context.Context, input service.CloudManifestCommitInput) (*service.CloudManifestCommitResult, error) {
	f.commitInput = input
	return f.commitResult, f.err
}

func (f *fakeCloudStorageService) Usage(ctx context.Context, userID string) (*service.CloudStorageUsage, error) {
	f.usageUserID = userID
	return f.usageResult, f.err
}

func (f *fakeCloudStorageService) ListProjects(ctx context.Context, query service.CloudStorageProjectQuery) (*service.CloudStorageProjectList, error) {
	f.projectQuery = query
	return f.projectResult, f.err
}

func (f *fakeCloudStorageService) LatestManifest(ctx context.Context, query service.CloudStorageLatestManifestQuery) (*service.CloudStorageLatestManifest, error) {
	f.manifestQuery = query
	return f.manifestResult, f.err
}

func (f *fakeCloudStorageService) BuildDownloadTarget(ctx context.Context, input service.CloudDownloadTargetInput) (*service.CloudDownloadTargetAuthorization, error) {
	f.downloadInput = input
	return f.downloadResult, f.err
}

func TestCloudStorageHandler_AuthorizeSync(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	fake := &fakeCloudStorageService{authorizeResult: &service.CloudSyncAuthorization{
		ID:              "csy_1",
		UserID:          "usr_1",
		CloudProjectID:  "cpr_1",
		ClientProjectID: "project-local",
		Provider:        "test-provider",
		QuotaBytes:      1000,
		RequestedBytes:  200,
		UploadTargets: []service.CloudObjectUploadTarget{{
			ObjectKey: "accounts/usr_1/projects/project-local/wiki/hash/page.md",
			UploadURL: "test-provider://accounts/usr_1/projects/project-local/wiki/hash/page.md",
			Method:    "PUT",
			Provider:  "test-provider",
		}},
		ExpiresAt: expiresAt,
	}}
	h := NewCloudStorageHandler(fake)
	r := gin.New()
	r.POST("/cloud-storage/sync-sessions", h.AuthorizeSync)

	body := bytes.NewBufferString(`{"user_id":"usr_1","client_project_id":"project-local","project_name":"Local Project","resources":[{"resource_id":"wiki/page.md","resource_kind":"wiki_markdown","content_hash":"hash","size_bytes":200,"content_type":"text/markdown","filename":"page.md"}]}`)
	req, _ := http.NewRequest(http.MethodPost, "/cloud-storage/sync-sessions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.authorizeInput.UserID != "usr_1" || fake.authorizeInput.ClientProjectID != "project-local" || len(fake.authorizeInput.Resources) != 1 {
		t.Fatalf("unexpected authorize input: %#v", fake.authorizeInput)
	}
	var resp struct {
		SyncSession service.CloudSyncAuthorization `json:"sync_session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SyncSession.ID != "csy_1" || len(resp.SyncSession.UploadTargets) != 1 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestCloudStorageHandler_CommitManifestAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeCloudStorageService{
		commitResult: &service.CloudManifestCommitResult{
			Project:  &domain.CloudProject{ID: "cpr_1", UserID: "usr_1", ClientProjectID: "project-local"},
			Manifest: &domain.CloudManifest{ID: "cmf_1", UserID: "usr_1", ManifestHash: "hash", ObjectCount: 1},
			Usage:    service.CloudStorageUsage{UserID: "usr_1", UsedBytes: 200, QuotaBytes: 1000, RemainingBytes: 800},
		},
	}
	h := NewCloudStorageHandler(fake)
	r := gin.New()
	r.POST("/cloud-storage/manifests", h.CommitManifest)

	body := bytes.NewBufferString(`{"user_id":"usr_1","client_project_id":"project-local","sync_session_id":"csy_1","manifest_hash":"hash","manifest_version":1,"idempotency_key":"idem-1","resources":[{"resource_id":"wiki/page.md","content_hash":"hash","size_bytes":200}]}`)
	req, _ := http.NewRequest(http.MethodPost, "/cloud-storage/manifests", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.commitInput.IdempotencyKey != "idem-1" || fake.commitInput.SyncSessionID != "csy_1" || fake.commitInput.ManifestHash != "hash" || len(fake.commitInput.Resources) != 1 {
		t.Fatalf("unexpected commit input: %#v", fake.commitInput)
	}

	fake.usageResult = &service.CloudStorageUsage{UserID: "usr_1", UsedBytes: 200, QuotaBytes: 1000, RemainingBytes: 800}
	r.GET("/users/:user_id/cloud-storage/usage", h.Usage)
	req, _ = http.NewRequest(http.MethodGet, "/users/usr_1/cloud-storage/usage", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.usageUserID != "usr_1" {
		t.Fatalf("unexpected usage user: %s", fake.usageUserID)
	}
}

func TestCloudStorageHandler_ListProjectsAndLatestManifest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeCloudStorageService{
		projectResult: &service.CloudStorageProjectList{
			UserID: "usr_1",
			Projects: []service.CloudStorageProjectSummary{{
				ID:              "cpr_1",
				ClientProjectID: "project-local",
				Status:          domain.CloudProjectStatusActive,
			}},
		},
		manifestResult: &service.CloudStorageLatestManifest{
			Project: service.CloudStorageProjectSummary{ID: "cpr_1", ClientProjectID: "project-local"},
			Manifest: &service.CloudManifestSummary{
				ID:           "cmf_1",
				ManifestHash: "sha256:manifest",
			},
		},
	}
	h := NewCloudStorageHandler(fake)
	r := gin.New()
	r.GET("/users/:user_id/cloud-storage/projects", h.ListProjects)
	r.GET("/cloud-storage/projects/:project_id/manifests/latest", h.LatestManifest)

	req, _ := http.NewRequest(http.MethodGet, "/users/usr_1/cloud-storage/projects?status=active&limit=5&offset=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.projectQuery.UserID != "usr_1" || fake.projectQuery.Status != "active" || fake.projectQuery.Limit != 5 || fake.projectQuery.Offset != 1 {
		t.Fatalf("unexpected project query: %#v", fake.projectQuery)
	}

	req, _ = http.NewRequest(http.MethodGet, "/cloud-storage/projects/cpr_1/manifests/latest?user_id=usr_1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.manifestQuery.UserID != "usr_1" || fake.manifestQuery.CloudProjectID != "cpr_1" {
		t.Fatalf("unexpected manifest query: %#v", fake.manifestQuery)
	}
}

func TestCloudStorageHandler_BuildDownloadTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeCloudStorageService{
		downloadResult: &service.CloudDownloadTargetAuthorization{
			UserID:          "usr_1",
			CloudProjectID:  "cpr_1",
			ClientProjectID: "project-local",
			Object:          service.CloudObjectSummary{ObjectKey: "accounts/usr_1/projects/project-local/wiki/hash/page.md"},
			DownloadTarget: service.CloudObjectDownloadTarget{
				ObjectKey:   "accounts/usr_1/projects/project-local/wiki/hash/page.md",
				DownloadURL: "test-provider://accounts/usr_1/projects/project-local/wiki/hash/page.md",
				Method:      "GET",
				Provider:    "test-provider",
			},
		},
	}
	h := NewCloudStorageHandler(fake)
	r := gin.New()
	r.POST("/cloud-storage/download-targets", h.BuildDownloadTarget)

	body := bytes.NewBufferString(`{"user_id":"usr_1","cloud_project_id":"cpr_1","object_key":"accounts/usr_1/projects/project-local/wiki/hash/page.md"}`)
	req, _ := http.NewRequest(http.MethodPost, "/cloud-storage/download-targets", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.downloadInput.UserID != "usr_1" || fake.downloadInput.CloudProjectID != "cpr_1" || fake.downloadInput.ObjectKey == "" {
		t.Fatalf("unexpected download input: %#v", fake.downloadInput)
	}
}

func TestCloudStorageHandler_MapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"invalid", service.ErrInvalidCloudStorage, http.StatusBadRequest},
		{"not_found", service.ErrUserNotFound, http.StatusNotFound},
		{"denied", service.ErrCloudStorageAccessDenied, http.StatusForbidden},
		{"provider_not_configured", service.ErrCloudStorageProviderNotConfigured, http.StatusConflict},
		{"over_quota", service.ErrCloudStorageOverQuota, http.StatusPaymentRequired},
		{"sync_session_missing", service.ErrCloudSyncSessionNotFound, http.StatusNotFound},
		{"sync_session_expired", service.ErrCloudSyncSessionExpired, http.StatusConflict},
		{"sync_session_committed", service.ErrCloudSyncSessionAlreadyCommitted, http.StatusConflict},
		{"other", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h := NewCloudStorageHandler(&fakeCloudStorageService{err: tt.err})
			r := gin.New()
			r.GET("/users/:user_id/cloud-storage/usage", h.Usage)
			req, _ := http.NewRequest(http.MethodGet, "/users/usr_1/cloud-storage/usage", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
		})
	}
}
