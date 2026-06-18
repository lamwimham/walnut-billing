package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeEntitlementService struct {
	registrationResult *service.RegistrationResult
	snapshot           *domain.EntitlementSnapshot
	registrations      []domain.RegistrationRequest
	reviewed           *domain.RegistrationRequest
}

func (f *fakeEntitlementService) SubmitRegistration(ctx context.Context, input service.RegistrationInput) (*service.RegistrationResult, error) {
	return f.registrationResult, nil
}

func (f *fakeEntitlementService) ListRegistrations(ctx context.Context, query repository.RegistrationQuery) ([]domain.RegistrationRequest, error) {
	return f.registrations, nil
}

func (f *fakeEntitlementService) ReviewRegistration(ctx context.Context, input service.ReviewRegistrationInput) (*domain.RegistrationRequest, error) {
	if f.reviewed != nil {
		return f.reviewed, nil
	}
	return &domain.RegistrationRequest{ID: input.RegistrationID, Status: input.Status}, nil
}

func (f *fakeEntitlementService) CreateGrant(ctx context.Context, input service.GrantInput) (*domain.EntitlementGrant, error) {
	return &domain.EntitlementGrant{ID: "grt_1", UserID: input.UserID, EntitlementID: input.EntitlementID}, nil
}

func (f *fakeEntitlementService) ListGrants(ctx context.Context, query repository.EntitlementGrantQuery) ([]domain.EntitlementGrant, error) {
	return nil, nil
}

func (f *fakeEntitlementService) SnapshotForUser(ctx context.Context, userID string) (*domain.EntitlementSnapshot, error) {
	return f.snapshot, nil
}

func TestEntitlementHandler_SubmitRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewEntitlementHandler(&fakeEntitlementService{registrationResult: &service.RegistrationResult{
		User: &domain.User{ID: "usr_1", Email: "writer@example.com"},
		Registration: &domain.RegistrationRequest{
			ID:                   "reg_1",
			UserID:               "usr_1",
			RequestedEntitlement: domain.EntitlementEditorialStudio,
			Status:               domain.RegistrationStatusPending,
		},
	}}, nil)
	r := gin.New()
	r.POST("/registrations", handler.SubmitRegistration)

	body := bytes.NewBufferString(`{"email":"writer@example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, "/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp service.RegistrationResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Registration.ID != "reg_1" {
		t.Fatalf("expected registration response")
	}
}

func TestEntitlementHandler_GetUserEntitlementSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewEntitlementHandler(&fakeEntitlementService{snapshot: &domain.EntitlementSnapshot{
		User:         domain.EntitlementSnapshotUser{ID: "usr_1", Email: "writer@example.com"},
		Entitlements: map[string]bool{domain.EntitlementEditorialStudio: true},
		Features:     map[string]any{},
		Credits:      map[string]int64{},
		Source:       "billing_provider",
	}}, nil)
	r := gin.New()
	r.GET("/users/:user_id/entitlements/snapshot", handler.GetUserEntitlementSnapshot)

	req, _ := http.NewRequest(http.MethodGet, "/users/usr_1/entitlements/snapshot", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	var resp domain.EntitlementSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Entitlements[domain.EntitlementEditorialStudio] {
		t.Fatalf("expected editorial studio entitlement")
	}
}

func TestEntitlementHandler_SubmitRegistrationAuditUsesUserIDNotEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit := &mockAuditWithData{}
	handler := NewEntitlementHandler(&fakeEntitlementService{registrationResult: &service.RegistrationResult{
		User: &domain.User{ID: "usr_1", Email: "writer@example.com"},
		Registration: &domain.RegistrationRequest{
			ID:                   "reg_1",
			UserID:               "usr_1",
			RequestedEntitlement: domain.EntitlementEditorialStudio,
			Status:               domain.RegistrationStatusPending,
		},
	}}, audit)
	r := gin.New()
	r.POST("/registrations", handler.SubmitRegistration)

	body := bytes.NewBufferString(`{"email":"writer@example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, "/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}
	if len(audit.entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(audit.entries))
	}
	if audit.entries[0].Actor != "usr_1" || audit.entries[0].Actor == "writer@example.com" {
		t.Fatalf("expected user id audit actor, got %#v", audit.entries[0])
	}
}

func TestEntitlementHandler_ListRegistrationsMasksEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewEntitlementHandler(&fakeEntitlementService{registrations: []domain.RegistrationRequest{{
		ID:                   "reg_1",
		UserID:               "usr_1",
		Email:                "Writer@Example.COM",
		DisplayName:          "Writer",
		RequestedEntitlement: domain.EntitlementEditorialStudio,
		Status:               domain.RegistrationStatusPending,
		Note:                 "please review Writer@Example.COM",
	}}}, nil)
	r := gin.New()
	r.GET("/admin/registrations", handler.ListRegistrations)

	req, _ := http.NewRequest(http.MethodGet, "/admin/registrations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "writer@example.com") || strings.Contains(w.Body.String(), "Writer@Example.COM") || strings.Contains(w.Body.String(), `"email"`) {
		t.Fatalf("registration list leaked raw email: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"email_masked"`) || !strings.Contains(w.Body.String(), `"email_fingerprint"`) {
		t.Fatalf("expected masked email projection, got %s", w.Body.String())
	}
}

func TestEntitlementHandler_ReviewRegistrationMasksEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewEntitlementHandler(&fakeEntitlementService{reviewed: &domain.RegistrationRequest{
		ID:                   "reg_1",
		UserID:               "usr_1",
		Email:                "writer@example.com",
		DisplayName:          "Writer",
		RequestedEntitlement: domain.EntitlementEditorialStudio,
		Status:               domain.RegistrationStatusApproved,
		ReviewNote:           "approved writer@example.com",
	}}, nil)
	r := gin.New()
	r.POST("/admin/registrations/:id/review", handler.ReviewRegistration)

	body := bytes.NewBufferString(`{"status":"approved","reviewed_by":"admin"}`)
	req, _ := http.NewRequest(http.MethodPost, "/admin/registrations/reg_1/review", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "writer@example.com") || strings.Contains(w.Body.String(), `"email"`) {
		t.Fatalf("review response leaked raw email: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"email_masked"`) {
		t.Fatalf("expected projected registration, got %s", w.Body.String())
	}
}
