package gorm_repo

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminOrderReadRepoListsOrdersWithDiagnostics(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.Order{},
		&domain.PaymentEventInbox{},
		&domain.FulfillmentExecution{},
		&domain.PaymentRiskFlag{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&domain.Order{
		OutTradeNo: "CHK-1",
		UserID:     "usr_1",
		SKUCode:    domain.SKUProOwnAIMonthly,
		Status:     domain.OrderStatusFulfilled,
		Provider:   "creem",
		OrderType:  domain.OrderTypeCheckout,
		PaidAt:     &now,
	}).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := db.Create(&domain.PaymentEventInbox{ID: "pev_old", Provider: "creem", ProviderEventID: "evt_old", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-1", PayloadHash: "old", Status: domain.PaymentEventStatusProcessed, ReceivedAt: now.Add(-time.Hour)}).Error; err != nil {
		t.Fatalf("create old event: %v", err)
	}
	if err := db.Create(&domain.PaymentEventInbox{ID: "pev_new", Provider: "creem", ProviderEventID: "evt_new", EventType: domain.PaymentEventTypeRenewalPaid, OutTradeNo: "CHK-1", PayloadHash: "new", Status: domain.PaymentEventStatusProcessed, ReceivedAt: now}).Error; err != nil {
		t.Fatalf("create new event: %v", err)
	}
	if err := db.Create(&domain.FulfillmentExecution{ID: "ful_ok", OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, IdempotencyKey: "ful:ok", Status: domain.FulfillmentExecutionStatusApplied}).Error; err != nil {
		t.Fatalf("create fulfillment ok: %v", err)
	}
	if err := db.Create(&domain.FulfillmentExecution{ID: "ful_failed", OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, IdempotencyKey: "ful:failed", Status: domain.FulfillmentExecutionStatusFailed}).Error; err != nil {
		t.Fatalf("create fulfillment failed: %v", err)
	}
	if err := db.Create(&domain.PaymentRiskFlag{ID: "risk_open", UserID: "usr_1", OutTradeNo: "CHK-1", Provider: "creem", ProviderEventID: "evt_risk_1", Status: domain.PaymentRiskStatusOpen, Severity: domain.PaymentRiskSeverityCritical, Reason: domain.PaymentRiskReasonDispute, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create risk flag: %v", err)
	}

	records, total, err := (&AdminOrderReadRepo{DB: db}).List(context.Background(), repository.AdminOrderQuery{
		UserID:    "usr_1",
		SKUCode:   domain.SKUProOwnAIMonthly,
		Status:    domain.OrderStatusFulfilled,
		Provider:  "creem",
		OrderType: domain.OrderTypeCheckout,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("unexpected records total=%d records=%#v", total, records)
	}
	record := records[0]
	if record.PaymentEventCount != 2 || record.LatestPaymentEvent == nil || record.LatestPaymentEvent.ID != "pev_new" {
		t.Fatalf("expected latest payment event stats, got %#v", record)
	}
	if record.FulfillmentCount != 2 || record.FailedFulfillmentCount != 1 || record.OpenRiskFlagCount != 1 {
		t.Fatalf("expected fulfillment/risk stats, got %#v", record)
	}
}
