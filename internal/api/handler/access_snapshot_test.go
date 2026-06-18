package handler

import (
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

type fakeAccessSnapshotIssuer struct {
	input  service.AccessSnapshotIssueInput
	result *domain.AccessSnapshotV2
	err    error
}

func (f *fakeAccessSnapshotIssuer) Issue(ctx context.Context, input service.AccessSnapshotIssueInput) (*domain.AccessSnapshotV2, error) {
	f.input = input
	return f.result, f.err
}

func TestAccessSnapshotHandler_GetSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &fakeAccessSnapshotIssuer{result: &domain.AccessSnapshotV2{
		Version:      2,
		User:         domain.AccessSnapshotUserV2{ID: "usr_1", Email: "writer@example.com"},
		License:      domain.AccessSnapshotLicenseV2{State: service.AccessLicenseStateBasic, Plan: domain.PlanBasicOwnAI, AIMode: service.AccessAIModeBYOK},
		Entitlements: map[string]bool{},
		Features:     map[string]any{},
		Credits:      map[string]int64{},
		Source:       "billing_provider",
		Signature:    "sig",
	}}
	h := NewAccessSnapshotHandler(fake)
	r := gin.New()
	r.GET("/users/:user_id/access/snapshot", h.GetSnapshot)

	req, _ := http.NewRequest(http.MethodGet, "/users/usr_1/access/snapshot?device_id=device-1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if fake.input.UserID != "usr_1" || fake.input.DeviceID != "device-1" {
		t.Fatalf("unexpected issuer input: %#v", fake.input)
	}
	var resp domain.AccessSnapshotV2
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Version != 2 || resp.Signature != "sig" {
		t.Fatalf("unexpected snapshot response: %#v", resp)
	}
}

func TestAccessSnapshotHandler_MapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid", service.ErrInvalidAccessSnapshot, http.StatusBadRequest, "invalid_access_snapshot"},
		{"revoked_device", service.ErrAccessDeviceRevoked, http.StatusForbidden, "access_device_revoked"},
		{"not_found", service.ErrUserNotFound, http.StatusNotFound, "access_user_not_found"},
		{"other", errors.New("boom"), http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			h := NewAccessSnapshotHandler(&fakeAccessSnapshotIssuer{err: tt.err})
			r := gin.New()
			r.GET("/users/:user_id/access/snapshot", h.GetSnapshot)

			req, _ := http.NewRequest(http.MethodGet, "/users/usr_1/access/snapshot", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, w.Code, w.Body.String())
			}
			if tt.code != "" {
				var response map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if response["code"] != tt.code {
					t.Fatalf("expected code %s, got %#v", tt.code, response)
				}
			}
		})
	}
}
