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

type fakeAdminSubscriptionService struct {
	query  service.AdminSubscriptionQuery
	result *service.AdminSubscriptionList
	err    error
}

func (f *fakeAdminSubscriptionService) ListSubscriptions(ctx context.Context, query service.AdminSubscriptionQuery) (*service.AdminSubscriptionList, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAdminSubscriptionHandler_ListSubscriptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAdminSubscriptionService{result: &service.AdminSubscriptionList{
		Total: 1,
		Subscriptions: []service.AdminSubscriptionRecord{{
			User:    service.AdminSubscriptionUser{ID: "usr_1", EmailMasked: "wr**er@example.com"},
			SKUCode: "pro_own_ai_monthly",
			Status:  service.SoftwareSubscriptionStatusActive,
		}},
	}}
	handler := NewAdminSubscriptionHandler(svc)
	r := gin.New()
	r.GET("/admin/subscriptions", handler.ListSubscriptions)

	req, _ := http.NewRequest(http.MethodGet, "/admin/subscriptions?user_id=usr_1&sku_code=pro_own_ai_monthly&status=active&provider=creem&out_trade_no=CHK-1&limit=5&offset=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.query.UserID != "usr_1" || svc.query.SKUCode != "pro_own_ai_monthly" || svc.query.Status != "active" || svc.query.Provider != "creem" || svc.query.OutTradeNo != "CHK-1" || svc.query.Limit != 5 || svc.query.Offset != 2 {
		t.Fatalf("unexpected query mapping: %#v", svc.query)
	}
	var response service.AdminSubscriptionList
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || response.Subscriptions[0].User.ID != "usr_1" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestAdminSubscriptionHandler_MapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid", service.ErrInvalidAdminSubscriptionQuery, http.StatusBadRequest, "invalid_admin_subscription_query"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "admin_subscription_query_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			handler := NewAdminSubscriptionHandler(&fakeAdminSubscriptionService{err: tt.err})
			r := gin.New()
			r.GET("/admin/subscriptions", handler.ListSubscriptions)

			req, _ := http.NewRequest(http.MethodGet, "/admin/subscriptions", nil)
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
