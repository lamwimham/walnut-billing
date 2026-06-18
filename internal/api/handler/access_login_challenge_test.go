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

type mockAccessLoginChallengeService struct {
	createInput service.AccessLoginChallengeCreateInput
	verifyInput service.AccessLoginChallengeVerifyInput
	createErr   error
	verifyErr   error
}

func (m *mockAccessLoginChallengeService) Create(ctx context.Context, input service.AccessLoginChallengeCreateInput) (*service.AccessLoginChallengeCreateResult, error) {
	m.createInput = input
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &service.AccessLoginChallengeCreateResult{ChallengeID: "alc_1", Email: input.Email, DeviceID: input.DeviceID, ExpiresAt: time.Date(2026, 6, 18, 8, 10, 0, 0, time.UTC), Delivery: "dev", DevToken: "123456"}, nil
}

func (m *mockAccessLoginChallengeService) Verify(ctx context.Context, input service.AccessLoginChallengeVerifyInput) (*service.AccessSessionResult, error) {
	m.verifyInput = input
	if m.verifyErr != nil {
		return nil, m.verifyErr
	}
	return &service.AccessSessionResult{User: &domain.User{ID: "usr_1", Email: "writer@example.com"}, Device: &domain.UserDevice{DeviceID: input.DeviceID}}, nil
}

func setupAccessLoginChallengeRouter(svc service.AccessLoginChallengeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAccessLoginChallengeHandler(svc, &mockAuditService{})
	r.POST("/access/login-challenges", h.Create)
	r.POST("/access/login-challenges/verify", h.Verify)
	return r
}

func setupAccessLoginChallengeRouterWithAudit(svc service.AccessLoginChallengeService, audit service.AuditService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewAccessLoginChallengeHandler(svc, audit)
	r.POST("/access/login-challenges", h.Create)
	r.POST("/access/login-challenges/verify", h.Verify)
	return r
}

func TestAccessLoginChallengeHandler_Create(t *testing.T) {
	svc := &mockAccessLoginChallengeService{}
	router := setupAccessLoginChallengeRouter(svc)
	payload := []byte(`{"email":"writer@example.com","device_id":"device-1","source":"desktop","idempotency_key":"login:1"}`)
	req, _ := http.NewRequest("POST", "/access/login-challenges", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.createInput.Email != "writer@example.com" || svc.createInput.DeviceID != "device-1" || svc.createInput.IdempotencyKey != "login:1" {
		t.Fatalf("unexpected service input: %#v", svc.createInput)
	}
	if svc.createInput.ClientIP == "" {
		t.Fatalf("expected request metadata to be passed to service, got %#v", svc.createInput)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["challenge_id"] != "alc_1" || response["dev_token"] != "123456" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestAccessLoginChallengeHandler_Verify(t *testing.T) {
	svc := &mockAccessLoginChallengeService{}
	router := setupAccessLoginChallengeRouter(svc)
	payload := []byte(`{"challenge_id":"alc_1","token":"123456","device_id":"device-1","display_name":"Writer"}`)
	req, _ := http.NewRequest("POST", "/access/login-challenges/verify", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.verifyInput.ChallengeID != "alc_1" || svc.verifyInput.Token != "123456" || svc.verifyInput.DeviceID != "device-1" || svc.verifyInput.DisplayName != "Writer" {
		t.Fatalf("unexpected service input: %#v", svc.verifyInput)
	}
	if svc.verifyInput.ClientIP == "" {
		t.Fatalf("expected request metadata to be passed to service, got %#v", svc.verifyInput)
	}
}

func TestAccessLoginChallengeHandler_AuditsLoginChallengeActions(t *testing.T) {
	svc := &mockAccessLoginChallengeService{}
	audit := &mockAuditWithData{}
	router := setupAccessLoginChallengeRouterWithAudit(svc, audit)

	createReq, _ := http.NewRequest("POST", "/access/login-challenges", bytes.NewReader([]byte(`{"email":"writer@example.com","device_id":"device-1"}`)))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	router.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusAccepted {
		t.Fatalf("expected create 202, got %d body=%s", createW.Code, createW.Body.String())
	}

	verifyReq, _ := http.NewRequest("POST", "/access/login-challenges/verify", bytes.NewReader([]byte(`{"challenge_id":"alc_1","token":"123456","device_id":"device-1"}`)))
	verifyReq.Header.Set("Content-Type", "application/json")
	verifyW := httptest.NewRecorder()
	router.ServeHTTP(verifyW, verifyReq)
	if verifyW.Code != http.StatusOK {
		t.Fatalf("expected verify 200, got %d body=%s", verifyW.Code, verifyW.Body.String())
	}

	if len(audit.entries) != 2 {
		t.Fatalf("expected two audit entries, got %#v", audit.entries)
	}
	if audit.entries[0].Action != domain.AuditActionAccessLoginChallengeCreate || audit.entries[1].Action != domain.AuditActionAccessLoginChallengeVerify {
		t.Fatalf("unexpected audit actions: %#v", audit.entries)
	}
	if audit.entries[1].Actor != "usr_1" || audit.entries[1].Actor == "writer@example.com" {
		t.Fatalf("expected verify audit actor to use user id, got %#v", audit.entries[1])
	}
}

func TestAccessLoginChallengeHandler_MapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		code string
	}{
		{name: "invalid", err: service.ErrInvalidAccessLoginChallenge, want: http.StatusBadRequest, code: "invalid_login_challenge"},
		{name: "expired", err: service.ErrAccessLoginChallengeExpired, want: http.StatusGone, code: "login_challenge_expired"},
		{name: "failed", err: service.ErrAccessLoginChallengeFailed, want: http.StatusUnauthorized, code: "login_challenge_failed"},
		{name: "delivery", err: service.ErrAccessLoginChallengeDeliveryUnavailable, want: http.StatusServiceUnavailable, code: "login_challenge_delivery_unavailable"},
		{name: "rate_limited", err: service.ErrAccessLoginChallengeRateLimited, want: http.StatusTooManyRequests, code: "login_challenge_rate_limited"},
		{name: "revoked_device", err: service.ErrAccessDeviceRevoked, want: http.StatusForbidden, code: "access_device_revoked"},
		{name: "device", err: service.ErrDeviceLimitExceeded, want: http.StatusConflict, code: "device_limit_exceeded"},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockAccessLoginChallengeService{verifyErr: tc.err}
			router := setupAccessLoginChallengeRouter(svc)
			req, _ := http.NewRequest("POST", "/access/login-challenges/verify", bytes.NewReader([]byte(`{"challenge_id":"alc_1","token":"123456","device_id":"device-1"}`)))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
			if tc.code != "" {
				var response map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if response["code"] != tc.code {
					t.Fatalf("expected code %s, got %#v", tc.code, response)
				}
			}
		})
	}
}
