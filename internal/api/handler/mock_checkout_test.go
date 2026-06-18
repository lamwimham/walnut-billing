package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"walnut-billing/internal/service"

	"github.com/gin-gonic/gin"
)

type mockCheckoutPaymentEventService struct {
	input service.PaymentWebhookInput
}

func (m *mockCheckoutPaymentEventService) ReceiveWebhook(ctx context.Context, input service.PaymentWebhookInput) (*service.PaymentEventProcessResult, error) {
	m.input = input
	return &service.PaymentEventProcessResult{
		Event:     &domain.PaymentEventInbox{ID: "pev_1", Provider: input.Provider, Status: domain.PaymentEventStatusProcessed},
		Processed: true,
	}, nil
}

func (m *mockCheckoutPaymentEventService) Process(ctx context.Context, eventID string) (*service.PaymentEventProcessResult, error) {
	return &service.PaymentEventProcessResult{Event: &domain.PaymentEventInbox{ID: eventID}, Processed: true}, nil
}

func (m *mockCheckoutPaymentEventService) ListEvents(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	return nil, nil
}

func (m *mockCheckoutPaymentEventService) GetEvent(ctx context.Context, eventID string) (*domain.PaymentEventInbox, error) {
	return &domain.PaymentEventInbox{ID: eventID}, nil
}

func setupMockCheckoutRouter(svc service.PaymentEventService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewMockCheckoutHandler(svc)
	r.GET("/checkout/:out_trade_no", h.Show)
	r.POST("/checkout/:out_trade_no/complete", h.Complete)
	return r
}

func TestMockCheckoutHandler_ShowRendersCheckoutPage(t *testing.T) {
	router := setupMockCheckoutRouter(&mockCheckoutPaymentEventService{})
	req, _ := http.NewRequest(http.MethodGet, "/checkout/CHK-1?sku_code=pro_own_ai_monthly&user_id=usr_1&success_url=walnut%3A%2F%2Fcheckout%2Fsuccess", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Complete a local test payment") || !strings.Contains(body, "CHK-1") {
		t.Fatalf("expected checkout page content, got %s", body)
	}
	if !strings.Contains(body, `action="/checkout/CHK-1/complete?sku_code=pro_own_ai_monthly&amp;success_url=walnut%3A%2F%2Fcheckout%2Fsuccess&amp;user_id=usr_1"`) {
		t.Fatalf("expected encoded form action, got %s", body)
	}
}

func TestMockCheckoutHandler_CompleteSendsMockPaidWebhook(t *testing.T) {
	svc := &mockCheckoutPaymentEventService{}
	router := setupMockCheckoutRouter(svc)
	req, _ := http.NewRequest(http.MethodPost, "/checkout/CHK-1/complete?success_url=walnut://checkout/success", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected visible success page after success, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Payment completed") || !strings.Contains(body, "Return to Walnut") {
		t.Fatalf("expected success page content, got %s", body)
	}
	if !strings.Contains(body, `href="walnut://checkout/success"`) {
		t.Fatalf("expected safe Walnut callback link, got %s", body)
	}
	if svc.input.Provider != "mock" || svc.input.Params["out_trade_no"] != "CHK-1" || svc.input.Params["event_type"] != "payment.paid" {
		t.Fatalf("expected mock paid webhook input, got %#v", svc.input)
	}
}
