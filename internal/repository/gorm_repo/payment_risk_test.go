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

func TestPaymentRiskFlagRepo_CRUDAndFilters(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:payment_risk_repo?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.PaymentRiskFlag{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := &PaymentRiskFlagRepo{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	flag := &domain.PaymentRiskFlag{
		ID:              "prf_1",
		UserID:          "usr_1",
		OutTradeNo:      "CHK-1",
		Provider:        "creem",
		ProviderEventID: "evt_dispute_1",
		Reason:          domain.PaymentRiskReasonDispute,
		Severity:        domain.PaymentRiskSeverityCritical,
		Status:          domain.PaymentRiskStatusOpen,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repo.Create(ctx, flag); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByProviderEventID(ctx, "creem", "evt_dispute_1")
	if err != nil {
		t.Fatalf("get by provider event: %v", err)
	}
	if got.ID != "prf_1" || got.UserID != "usr_1" {
		t.Fatalf("unexpected flag: %#v", got)
	}
	if _, err := repo.GetByProviderEventID(ctx, "other", "evt_dispute_1"); err != repository.ErrNotFound {
		t.Fatalf("expected provider-scoped idempotency lookup, got %v", err)
	}
	list, err := repo.List(ctx, repository.PaymentRiskFlagQuery{
		UserID:   "usr_1",
		Provider: "creem",
		Status:   domain.PaymentRiskStatusOpen,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != "prf_1" {
		t.Fatalf("unexpected list result: %#v", list)
	}
	resolvedAt := now.Add(time.Hour)
	flag.Status = domain.PaymentRiskStatusResolved
	flag.ResolvedAt = &resolvedAt
	flag.ResolvedBy = "ops"
	if err := repo.Update(ctx, flag); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := repo.GetByID(ctx, "prf_1")
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if updated.Status != domain.PaymentRiskStatusResolved || updated.ResolvedAt == nil || updated.ResolvedBy != "ops" {
		t.Fatalf("expected resolved flag, got %#v", updated)
	}
}
