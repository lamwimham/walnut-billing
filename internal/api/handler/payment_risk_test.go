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
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type mockPaymentRiskService struct {
	query repository.PaymentRiskFlagQuery
	id    string
	input service.ResolvePaymentRiskFlagInput
	flags []domain.PaymentRiskFlag
	flag  *domain.PaymentRiskFlag
	err   error
}

func (m *mockPaymentRiskService) ListFlags(ctx context.Context, query repository.PaymentRiskFlagQuery) ([]domain.PaymentRiskFlag, error) {
	m.query = query
	if m.err != nil {
		return nil, m.err
	}
	return m.flags, nil
}

func (m *mockPaymentRiskService) GetFlag(ctx context.Context, id string) (*domain.PaymentRiskFlag, error) {
	m.id = id
	if m.err != nil {
		return nil, m.err
	}
	if m.flag != nil {
		return m.flag, nil
	}
	return &domain.PaymentRiskFlag{ID: id, Status: domain.PaymentRiskStatusOpen}, nil
}

func (m *mockPaymentRiskService) ResolveFlag(ctx context.Context, input service.ResolvePaymentRiskFlagInput) (*domain.PaymentRiskFlag, error) {
	m.input = input
	if m.err != nil {
		return nil, m.err
	}
	return &domain.PaymentRiskFlag{ID: input.ID, Status: domain.PaymentRiskStatusResolved, ResolvedBy: input.ResolvedBy, Note: input.Note}, nil
}

func setupPaymentRiskRouter(svc service.PaymentRiskService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewPaymentRiskHandler(svc, nil)
	r.GET("/admin/payment-risk-flags", h.ListFlags)
	r.GET("/admin/payment-risk-flags/:id", h.GetFlag)
	r.POST("/admin/payment-risk-flags/:id/resolve", h.ResolveFlag)
	return r
}

func TestPaymentRiskHandler_ListFlagsMapsQuery(t *testing.T) {
	svc := &mockPaymentRiskService{flags: []domain.PaymentRiskFlag{{ID: "prf_1", UserID: "usr_1", Status: domain.PaymentRiskStatusOpen}}}
	router := setupPaymentRiskRouter(svc)
	req, _ := http.NewRequest(http.MethodGet, "/admin/payment-risk-flags?user_id=usr_1&status=open&severity=critical&provider=creem&out_trade_no=CHK-1&limit=25&offset=5", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.query.UserID != "usr_1" || svc.query.Status != "open" || svc.query.Severity != "critical" || svc.query.Provider != "creem" || svc.query.OutTradeNo != "CHK-1" || svc.query.Limit != 25 || svc.query.Offset != 5 {
		t.Fatalf("unexpected query: %#v", svc.query)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if response["total"].(float64) != 1 {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestPaymentRiskHandler_GetAndResolveFlag(t *testing.T) {
	svc := &mockPaymentRiskService{}
	router := setupPaymentRiskRouter(svc)

	getReq, _ := http.NewRequest(http.MethodGet, "/admin/payment-risk-flags/prf_1", nil)
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK || svc.id != "prf_1" {
		t.Fatalf("expected get flag 200/id mapping, code=%d id=%s body=%s", getW.Code, svc.id, getW.Body.String())
	}

	payload := []byte(`{"resolved_by":"ops","note":"verified customer"}`)
	resolveReq, _ := http.NewRequest(http.MethodPost, "/admin/payment-risk-flags/prf_1/resolve", bytes.NewReader(payload))
	resolveReq.Header.Set("Content-Type", "application/json")
	resolveW := httptest.NewRecorder()
	router.ServeHTTP(resolveW, resolveReq)
	if resolveW.Code != http.StatusOK {
		t.Fatalf("expected resolve 200, got %d body=%s", resolveW.Code, resolveW.Body.String())
	}
	if svc.input.ID != "prf_1" || svc.input.ResolvedBy != "ops" || svc.input.Note != "verified customer" {
		t.Fatalf("unexpected resolve input: %#v", svc.input)
	}
}

func TestPaymentRiskHandler_MapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: service.ErrInvalidPaymentRiskFlag, want: http.StatusBadRequest},
		{name: "missing", err: service.ErrPaymentRiskFlagNotFound, want: http.StatusNotFound},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := setupPaymentRiskRouter(&mockPaymentRiskService{err: tc.err})
			req, _ := http.NewRequest(http.MethodGet, "/admin/payment-risk-flags/prf_1", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
