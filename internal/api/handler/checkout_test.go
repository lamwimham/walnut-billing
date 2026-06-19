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

type mockCheckoutService struct {
	input  service.CheckoutInput
	result *service.CheckoutResult
	err    error
}

func (m *mockCheckoutService) CreateCheckoutSession(ctx context.Context, input service.CheckoutInput) (*service.CheckoutResult, error) {
	m.input = input
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &service.CheckoutResult{
		Order: &domain.Order{
			OutTradeNo:         "CHK-TEST",
			UserID:             input.UserID,
			SKUCode:            input.SKUCode,
			Amount:             1900,
			Currency:           "CNY",
			Status:             domain.OrderStatusCheckoutCreated,
			Provider:           input.Provider,
			ProviderCheckoutID: "mock_chk_CHK-TEST",
			OrderType:          domain.OrderTypeCheckout,
		},
		CheckoutURL: "https://mock.checkout/CHK-TEST",
		Provider:    input.Provider,
	}, nil
}

func setupCheckoutTestRouter(svc service.CheckoutService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewCheckoutHandler(svc)
	r.POST("/commerce/checkout-sessions", h.CreateCheckoutSession)
	return r
}

func TestCheckoutHandler_CreateCheckoutSession(t *testing.T) {
	svc := &mockCheckoutService{}
	router := setupCheckoutTestRouter(svc)
	body := map[string]any{
		"user_id":         "usr_1",
		"sku_code":        "editorial_studio_monthly",
		"provider":        "mock",
		"success_url":     "walnut://checkout/success",
		"cancel_url":      "walnut://checkout/cancel",
		"idempotency_key": "checkout:usr_1:editorial_studio_monthly:1",
	}
	payload, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", "/commerce/checkout-sessions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.input.UserID != "usr_1" || svc.input.SKUCode != "editorial_studio_monthly" {
		t.Fatalf("expected request mapped to service input, got %#v", svc.input)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if response["checkout_url"] == "" {
		t.Fatalf("expected checkout_url in response: %#v", response)
	}
}

func TestCheckoutHandler_MapsServiceErrors(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		want     int
		wantCode string
	}{
		{name: "invalid", err: service.ErrInvalidCheckoutRequest, want: http.StatusBadRequest},
		{name: "missing user", err: service.ErrUserNotFound, want: http.StatusNotFound},
		{name: "provider", err: service.ErrCheckoutProviderFailed, want: http.StatusBadGateway},
		{
			name: "risk blocked",
			err: &service.CheckoutPolicyRejection{
				Cause: service.ErrCheckoutBlockedByRisk,
				Decision: service.CheckoutPolicyDecision{
					Reason:  service.CheckoutPolicyReasonOpenPaymentRisk,
					Action:  service.CheckoutPolicyActionManualReview,
					Message: "checkout requires manual review",
				},
			},
			want:     http.StatusForbidden,
			wantCode: "checkout_blocked_by_payment_risk",
		},
		{
			name: "subscription state blocked",
			err: &service.CheckoutPolicyRejection{
				Cause: service.ErrCheckoutBlockedByPlan,
				Decision: service.CheckoutPolicyDecision{
					Reason:  service.CheckoutPolicyReasonCancelAtPeriodEnd,
					Action:  service.CheckoutPolicyActionResume,
					Message: "resume instead of checkout",
				},
			},
			want:     http.StatusConflict,
			wantCode: "checkout_blocked_by_subscription_state",
		},
		{
			name: "redirect blocked",
			err: &service.CheckoutPolicyRejection{
				Cause: service.ErrInvalidCheckoutRequest,
				Decision: service.CheckoutPolicyDecision{
					Reason:  service.CheckoutPolicyReasonRedirectNotAllowed,
					Action:  service.CheckoutPolicyActionManualReview,
					Message: "checkout redirect URL is not allowed",
				},
			},
			want:     http.StatusBadRequest,
			wantCode: "checkout_redirect_not_allowed",
		},
		{name: "policy unavailable", err: service.ErrCheckoutPolicyUnavailable, want: http.StatusServiceUnavailable, wantCode: "checkout_policy_unavailable"},
		{name: "unknown", err: errors.New("unknown"), want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := setupCheckoutTestRouter(&mockCheckoutService{err: tc.err})
			body := []byte(`{"user_id":"usr_1","sku_code":"credits_600","provider":"mock","idempotency_key":"checkout:1"}`)
			req, _ := http.NewRequest("POST", "/commerce/checkout-sessions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
			if tc.wantCode != "" {
				var response map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("invalid json response: %v", err)
				}
				if response["code"] != tc.wantCode {
					t.Fatalf("expected code %s, got %#v", tc.wantCode, response)
				}
				if tc.wantCode == "checkout_blocked_by_subscription_state" &&
					(response["reason"] != service.CheckoutPolicyReasonCancelAtPeriodEnd || response["action"] != service.CheckoutPolicyActionResume) {
					t.Fatalf("expected subscription decision payload, got %#v", response)
				}
				if tc.wantCode == "checkout_redirect_not_allowed" &&
					response["reason"] != service.CheckoutPolicyReasonRedirectNotAllowed {
					t.Fatalf("expected redirect decision payload, got %#v", response)
				}
			}
		})
	}
}
