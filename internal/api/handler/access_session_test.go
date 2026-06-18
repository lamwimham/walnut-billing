package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type fakeAccessSessionService struct {
	result *service.AccessSessionResult
	err    error
	input  service.AccessSessionInput
}

func (f *fakeAccessSessionService) RegisterOrRestore(ctx context.Context, input service.AccessSessionInput) (*service.AccessSessionResult, error) {
	f.input = input
	return f.result, f.err
}

func TestAccessSessionHandler_RegisterOrRestore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccessSessionService{result: &service.AccessSessionResult{
		User:   &domain.User{ID: "usr_1", Email: "writer@example.com"},
		Device: &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive},
		DeviceCapacity: service.AccessDeviceCapacity{
			ActiveDeviceCount:    1,
			MaxDevices:           2,
			RemainingDeviceSlots: 1,
		},
		Trial:        &domain.TrialGrant{ID: "trl_1", UserID: "usr_1", GrantType: domain.TrialGrantTypeProOwnAI},
		TrialCreated: true,
		Snapshot: &domain.EntitlementSnapshot{
			User:         domain.EntitlementSnapshotUser{ID: "usr_1", Email: "writer@example.com"},
			Entitlements: map[string]bool{domain.EntitlementEditorialStudio: true},
			Features:     map[string]any{},
			Credits:      map[string]int64{},
			Source:       "billing_provider",
		},
		AccessSnapshot: &domain.AccessSnapshotV2{Version: 2, Signature: "sig"},
		Source:         "billing_provider",
	}}
	h := NewAccessSessionHandler(fake, nil)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com","display_name":"Writer","device_id":"device-1","source":"desktop"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.input.Email != "writer@example.com" || fake.input.DeviceID != "device-1" || fake.input.DisplayName != "Writer" {
		t.Fatalf("unexpected handler input: %#v", fake.input)
	}
	var resp service.AccessSessionResult
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.User.ID != "usr_1" || !resp.Snapshot.Entitlements[domain.EntitlementEditorialStudio] || !resp.TrialCreated || resp.AccessSnapshot == nil || resp.AccessSnapshot.Signature == "" {
		t.Fatalf("unexpected access session response: %#v", resp)
	}
	if resp.DeviceCapacity.ActiveDeviceCount != 1 || resp.DeviceCapacity.MaxDevices != 2 || resp.DeviceCapacity.RemainingDeviceSlots != 1 {
		t.Fatalf("expected service-projected device capacity, got %#v", resp.DeviceCapacity)
	}
}

func TestAccessSessionHandler_DeviceLimitExceeded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccessSessionHandler(&fakeAccessSessionService{err: service.ErrDeviceLimitExceeded}, nil)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com","device_id":"device-2"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["code"] != "device_limit_exceeded" {
		t.Fatalf("expected stable error code, got %#v", response)
	}
}

func TestAccessSessionHandler_InvalidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccessSessionHandler(&fakeAccessSessionService{}, nil)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAccessSessionHandler_UserDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccessSessionHandler(&fakeAccessSessionService{err: service.ErrAccessUserDisabled}, nil)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com","device_id":"device-1"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAccessSessionHandler_DeviceRevoked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccessSessionHandler(&fakeAccessSessionService{err: service.ErrAccessDeviceRevoked}, nil)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com","device_id":"device-1"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d: %s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["code"] != "access_device_revoked" {
		t.Fatalf("expected stable error code, got %#v", response)
	}
}

func TestAccessSessionHandler_UnexpectedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccessSessionHandler(&fakeAccessSessionService{err: errors.New("boom")}, nil)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com","device_id":"device-1"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAccessSessionHandler_AuditUsesUserIDNotEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccessSessionService{result: &service.AccessSessionResult{
		User:   &domain.User{ID: "usr_1", Email: "writer@example.com"},
		Device: &domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive},
		DeviceCapacity: service.AccessDeviceCapacity{
			ActiveDeviceCount:    1,
			MaxDevices:           2,
			RemainingDeviceSlots: 1,
		},
		Trial:        &domain.TrialGrant{ID: "trl_1", UserID: "usr_1", GrantType: domain.TrialGrantTypeProOwnAI},
		TrialCreated: true,
		Snapshot: &domain.EntitlementSnapshot{
			User:         domain.EntitlementSnapshotUser{ID: "usr_1", Email: "writer@example.com"},
			Entitlements: map[string]bool{domain.EntitlementEditorialStudio: true},
			Features:     map[string]any{},
			Credits:      map[string]int64{},
			Source:       "billing_provider",
		},
		AccessSnapshot: &domain.AccessSnapshotV2{Version: 2, Signature: "sig"},
		Source:         "billing_provider",
	}}
	audit := &mockAuditWithData{}
	h := NewAccessSessionHandler(fake, audit)
	r := gin.New()
	r.POST("/access/registrations", h.RegisterOrRestore)

	body := bytes.NewBufferString(`{"email":"writer@example.com","device_id":"device-1"}`)
	req, _ := http.NewRequest(http.MethodPost, "/access/registrations", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(audit.entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(audit.entries))
	}
	if audit.entries[0].Actor != "usr_1" || audit.entries[0].Actor == "writer@example.com" {
		t.Fatalf("expected user id audit actor, got %#v", audit.entries[0])
	}
}
