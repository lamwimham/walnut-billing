package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAdminOrderReadRepo struct {
	query   repository.AdminOrderQuery
	records []repository.AdminOrderRecord
	total   int64
	err     error
}

func (f *fakeAdminOrderReadRepo) List(ctx context.Context, query repository.AdminOrderQuery) ([]repository.AdminOrderRecord, int64, error) {
	f.query = query
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.records, f.total, nil
}

func TestAdminOrderServiceProjectsSafeOrderDiagnostics(t *testing.T) {
	paidAt := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	processedAt := paidAt.Add(time.Minute)
	idempotencyKey := "checkout:usr_1:secret"
	repo := &fakeAdminOrderReadRepo{
		total: 1,
		records: []repository.AdminOrderRecord{{
			Order: domain.Order{
				OutTradeNo:         "CHK-1",
				UserID:             "usr_1",
				SKUCode:            domain.SKUProOwnAIMonthly,
				Status:             domain.OrderStatusFulfilled,
				Provider:           "creem",
				OrderType:          domain.OrderTypeCheckout,
				Amount:             1200,
				Currency:           "USD",
				ProviderCheckoutID: "ch_secret",
				ProviderCustomerID: "cus_secret",
				CheckoutURL:        "https://checkout.example/secret",
				Metadata:           `{"walnut_provider_subscription_id":"sub_secret"}`,
				IdempotencyKey:     &idempotencyKey,
				PaidAt:             &paidAt,
				FulfilledAt:        &processedAt,
			},
			PaymentEventCount: 2,
			LatestPaymentEvent: &domain.PaymentEventInbox{
				ID:              "pev_1",
				Provider:        "creem",
				ProviderEventID: "evt_secret",
				EventType:       domain.PaymentEventTypePaid,
				OutTradeNo:      "CHK-1",
				PayloadHash:     "hash_123",
				RawPayload:      `{"email":"writer@example.com"}`,
				Status:          domain.PaymentEventStatusProcessed,
				ReceivedAt:      paidAt,
				ProcessedAt:     &processedAt,
			},
			FulfillmentCount:       2,
			FailedFulfillmentCount: 1,
			OpenRiskFlagCount:      1,
		}},
	}
	svc := NewAdminOrderService(repo)

	result, err := svc.ListOrders(context.Background(), AdminOrderQuery{
		UserID:     " usr_1 ",
		SKUCode:    " pro_own_ai_monthly ",
		Status:     " fulfilled ",
		Provider:   " creem ",
		OrderType:  " checkout ",
		OutTradeNo: " CHK-1 ",
		Limit:      999,
		Offset:     -10,
	})
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	if repo.query.UserID != "usr_1" || repo.query.Limit != maxAdminOrderLimit || repo.query.Offset != 0 {
		t.Fatalf("expected normalized query, got %#v", repo.query)
	}
	if result.Total != 1 || len(result.Orders) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	order := result.Orders[0]
	if !order.HasCheckoutSession || !order.HasProviderCustomer || !order.HasProviderSubscription || !order.HasMetadata {
		t.Fatalf("expected provider presence flags, got %#v", order)
	}
	if order.LatestPaymentEvent.PayloadHash != "hash_123" || order.FailedFulfillmentCount != 1 || order.OpenRiskFlagCount != 1 {
		t.Fatalf("expected diagnostics, got %#v", order)
	}
	raw, _ := json.Marshal(result)
	body := string(raw)
	for _, leaked := range []string{
		"https://checkout.example/secret",
		"ch_secret",
		"cus_secret",
		"sub_secret",
		"checkout:usr_1:secret",
		"evt_secret",
		`{"email":"writer@example.com"}`,
		"writer@example.com",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("admin order response leaked %q in %s", leaked, body)
		}
	}
}

func TestAdminOrderServiceRejectsUnconfiguredRepository(t *testing.T) {
	_, err := NewAdminOrderService(nil).ListOrders(context.Background(), AdminOrderQuery{})
	if !errors.Is(err, ErrInvalidAdminOrderQuery) {
		t.Fatalf("expected invalid query error, got %v", err)
	}
}
