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

type mockPaymentEventService struct {
	input  service.PaymentWebhookInput
	result *service.PaymentEventProcessResult
	events []domain.PaymentEventInbox
	err    error
}

func (m *mockPaymentEventService) ReceiveWebhook(ctx context.Context, input service.PaymentWebhookInput) (*service.PaymentEventProcessResult, error) {
	m.input = input
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &service.PaymentEventProcessResult{Event: &domain.PaymentEventInbox{ID: "pev_1", Provider: input.Provider, Status: domain.PaymentEventStatusProcessed}, Processed: true}, nil
}

func (m *mockPaymentEventService) Process(ctx context.Context, eventID string) (*service.PaymentEventProcessResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &service.PaymentEventProcessResult{Event: &domain.PaymentEventInbox{ID: eventID, Status: domain.PaymentEventStatusProcessed}, Processed: true}, nil
}

func (m *mockPaymentEventService) ListEvents(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.events, nil
}

func (m *mockPaymentEventService) GetEvent(ctx context.Context, eventID string) (*domain.PaymentEventInbox, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &domain.PaymentEventInbox{ID: eventID, Status: domain.PaymentEventStatusProcessed}, nil
}

func setupPaymentEventRouter(svc service.PaymentEventService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewPaymentEventHandler(svc)
	r.POST("/webhooks/:provider", h.ReceiveWebhook)
	r.GET("/admin/payment-events", h.ListEvents)
	r.GET("/admin/payment-events/:id", h.GetEvent)
	r.POST("/admin/payment-events/:id/reprocess", h.ReprocessEvent)
	return r
}

func TestPaymentEventHandler_ReceiveWebhookMapsTransport(t *testing.T) {
	svc := &mockPaymentEventService{}
	router := setupPaymentEventRouter(svc)
	payload := []byte(`{"provider_event_id":"evt_1","event_type":"payment.paid","out_trade_no":"CHK-1"}`)
	req, _ := http.NewRequest("POST", "/webhooks/mock?currency=CNY", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Signature", "ok")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.input.Provider != "mock" || string(svc.input.RawPayload) != string(payload) {
		t.Fatalf("expected provider/raw payload mapped, got %#v", svc.input)
	}
	if svc.input.Headers["X-Test-Signature"] != "ok" || svc.input.Params["currency"] != "CNY" {
		t.Fatalf("expected headers/query params mapped, got %#v %#v", svc.input.Headers, svc.input.Params)
	}
}

func TestPaymentEventHandler_ReceiveWebhookParsesFormPayload(t *testing.T) {
	svc := &mockPaymentEventService{}
	router := setupPaymentEventRouter(svc)
	req, _ := http.NewRequest("POST", "/webhooks/mock", bytes.NewReader([]byte("out_trade_no=CHK-FORM&provider_event_id=evt_form")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.input.Params["out_trade_no"] != "CHK-FORM" || svc.input.Params["provider_event_id"] != "evt_form" {
		t.Fatalf("expected form params parsed from raw payload, got %#v", svc.input.Params)
	}
}

func TestPaymentEventHandler_DuplicateAccepted(t *testing.T) {
	svc := &mockPaymentEventService{result: &service.PaymentEventProcessResult{Event: &domain.PaymentEventInbox{ID: "pev_1", Status: domain.PaymentEventStatusProcessed}, Duplicate: true, Processed: true}}
	router := setupPaymentEventRouter(svc)
	req, _ := http.NewRequest("POST", "/webhooks/mock", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("expected json response, got %v", err)
	}
	if response["duplicate"] != true || response["processed"] != true {
		t.Fatalf("expected duplicate processed response, got %#v", response)
	}
}

func TestPaymentEventHandler_MapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: service.ErrInvalidPaymentEvent, want: http.StatusBadRequest},
		{name: "missing", err: service.ErrPaymentEventNotFound, want: http.StatusNotFound},
		{name: "unprocessable", err: service.ErrPaymentEventNotProcessable, want: http.StatusUnprocessableEntity},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := setupPaymentEventRouter(&mockPaymentEventService{err: tc.err})
			req, _ := http.NewRequest("POST", "/webhooks/mock", bytes.NewReader([]byte(`{}`)))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d body=%s", tc.want, w.Code, w.Body.String())
			}
		})
	}
}
