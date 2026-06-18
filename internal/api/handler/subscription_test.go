package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type mockSubscriptionCancellationService struct {
	cancelInput service.SubscriptionCancellationInput
	resumeInput service.SubscriptionResumeInput
	result      *service.SubscriptionCancellationResult
	err         error
}

func (m *mockSubscriptionCancellationService) Cancel(ctx context.Context, input service.SubscriptionCancellationInput) (*service.SubscriptionCancellationResult, error) {
	m.cancelInput = input
	if m.err != nil {
		return nil, m.err
	}
	return m.subscriptionResult(input.UserID, input.SKUCode), nil
}

func (m *mockSubscriptionCancellationService) Resume(ctx context.Context, input service.SubscriptionResumeInput) (*service.SubscriptionCancellationResult, error) {
	m.resumeInput = input
	if m.err != nil {
		return nil, m.err
	}
	return m.subscriptionResult(input.UserID, input.SKUCode), nil
}

func (m *mockSubscriptionCancellationService) subscriptionResult(userID string, skuCode string) *service.SubscriptionCancellationResult {
	if m.result != nil {
		return m.result
	}
	return &service.SubscriptionCancellationResult{
		UserID:            userID,
		SKUCode:           skuCode,
		Status:            service.SoftwareSubscriptionStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd: true,
		Projection: service.SoftwareSubscriptionProjection{
			UserID:            userID,
			SKUCode:           skuCode,
			Status:            service.SoftwareSubscriptionStatusCancelAtPeriodEnd,
			CancelAtPeriodEnd: true,
		},
	}
}

func setupSubscriptionTestRouter(svc service.SubscriptionCancellationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewSubscriptionHandler(svc)
	router.POST("/commerce/subscriptions/cancel", handler.Cancel)
	router.POST("/commerce/subscriptions/resume", handler.Resume)
	return router
}

func TestSubscriptionHandler_CancelMapsRequest(t *testing.T) {
	svc := &mockSubscriptionCancellationService{}
	router := setupSubscriptionTestRouter(svc)
	body := []byte(`{"user_id":"usr_1","sku_code":"pro_own_ai_monthly","reason":"user_requested","source":"settings","idempotency_key":"cancel:1"}`)
	req, _ := http.NewRequest(http.MethodPost, "/commerce/subscriptions/cancel", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.cancelInput.UserID != "usr_1" || svc.cancelInput.SKUCode != "pro_own_ai_monthly" || svc.cancelInput.IdempotencyKey != "cancel:1" {
		t.Fatalf("expected request mapped to service input, got %#v", svc.cancelInput)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if response["status"] != service.SoftwareSubscriptionStatusCancelAtPeriodEnd {
		t.Fatalf("expected cancellation status, got %#v", response)
	}
}

func TestSubscriptionHandler_MapsSubscriptionControlErrors(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		want     int
		wantCode string
	}{
		{name: "invalid", err: service.ErrInvalidSubscriptionCancellation, want: http.StatusBadRequest, wantCode: "invalid_subscription_cancellation"},
		{name: "control unavailable", err: service.ErrSubscriptionControlUnavailable, want: http.StatusConflict, wantCode: "subscription_control_unavailable"},
		{name: "control failed", err: errors.Join(service.ErrSubscriptionControlFailed, errors.New("provider down")), want: http.StatusBadGateway, wantCode: "subscription_control_failed"},
		{name: "missing user", err: service.ErrUserNotFound, want: http.StatusNotFound, wantCode: "user_not_found"},
		{name: "missing subscription", err: service.ErrSubscriptionNotFound, want: http.StatusNotFound, wantCode: "subscription_not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := setupSubscriptionTestRouter(&mockSubscriptionCancellationService{err: tc.err})
			body := []byte(`{"user_id":"usr_1","sku_code":"pro_own_ai_monthly","idempotency_key":"cancel:1"}`)
			req, _ := http.NewRequest(http.MethodPost, "/commerce/subscriptions/cancel", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("invalid json response: %v", err)
			}
			if response["code"] != tc.wantCode {
				t.Fatalf("expected code %s, got %#v", tc.wantCode, response)
			}
		})
	}
}
