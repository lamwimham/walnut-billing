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

type fakeAdminOrderService struct {
	query  service.AdminOrderQuery
	result *service.AdminOrderList
	err    error
}

func (f *fakeAdminOrderService) ListOrders(ctx context.Context, query service.AdminOrderQuery) (*service.AdminOrderList, error) {
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestAdminOrderHandler_ListOrders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &fakeAdminOrderService{result: &service.AdminOrderList{Total: 1, Orders: []service.AdminOrderRecord{{OutTradeNo: "CHK-1", UserID: "usr_1", HasCheckoutSession: true}}}}
	handler := NewAdminOrderHandler(svc)
	r := gin.New()
	r.GET("/admin/orders", handler.ListOrders)

	req, _ := http.NewRequest(http.MethodGet, "/admin/orders?user_id=usr_1&sku_code=pro_own_ai_monthly&status=fulfilled&provider=creem&order_type=checkout&out_trade_no=CHK-1&limit=5&offset=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if svc.query.UserID != "usr_1" || svc.query.SKUCode != "pro_own_ai_monthly" || svc.query.Limit != 5 || svc.query.Offset != 2 {
		t.Fatalf("unexpected query mapping: %#v", svc.query)
	}
	var response service.AdminOrderList
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || response.Orders[0].OutTradeNo != "CHK-1" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestAdminOrderHandler_MapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid", service.ErrInvalidAdminOrderQuery, http.StatusBadRequest, "invalid_admin_order_query"},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, "admin_order_query_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			handler := NewAdminOrderHandler(&fakeAdminOrderService{err: tt.err})
			r := gin.New()
			r.GET("/admin/orders", handler.ListOrders)

			req, _ := http.NewRequest(http.MethodGet, "/admin/orders", nil)
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
