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

func TestAdminSubscriptionReadRepoListsSubscriptionFacts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.Order{},
		&domain.EntitlementGrant{},
		&domain.SubscriptionCancellation{},
		&domain.PaymentEventInbox{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	olderPaidAt := now.Add(-4 * time.Hour)
	newerPaidAt := now.Add(-2 * time.Hour)
	receivedOld := now.Add(-90 * time.Minute)
	receivedNew := now.Add(-30 * time.Minute)
	if err := db.Create(&domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive, CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	orders := []domain.Order{
		{OutTradeNo: "CHK-OLD", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, Provider: "creem", OrderType: domain.OrderTypeCheckout, PaidAt: &olderPaidAt},
		{OutTradeNo: "CHK-NEW", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, Provider: "creem", OrderType: domain.OrderTypeRenewal, PaidAt: &newerPaidAt, Metadata: `{"walnut_provider_subscription_id":"sub_secret","walnut_provider_subscription_status":"active"}`},
		{OutTradeNo: "CHK-MOCK", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, Provider: "mock", OrderType: domain.OrderTypeCheckout, PaidAt: &now},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatalf("create orders: %v", err)
	}
	grants := []domain.EntitlementGrant{
		{ID: "grt_studio", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd, CreatedAt: now},
		{ID: "grt_cloud", UserID: "usr_1", EntitlementID: domain.EntitlementCloudStorage, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), ExpiresAt: &periodEnd, CreatedAt: now},
	}
	if err := db.Create(&grants).Error; err != nil {
		t.Fatalf("create grants: %v", err)
	}
	cancellations := []domain.SubscriptionCancellation{
		{ID: "sub_cancel_old", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: "resumed", CancelAtPeriodEnd: false, CurrentPeriodEndsAt: periodEnd, SourceOrderNo: "CHK-OLD", IdempotencyKey: "cancel-old", CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "sub_cancel_new", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.SubscriptionStatusCancelAtPeriodEnd, CancelAtPeriodEnd: true, CurrentPeriodEndsAt: periodEnd, SourceOrderNo: "CHK-NEW", IdempotencyKey: "cancel-new", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
	}
	if err := db.Create(&cancellations).Error; err != nil {
		t.Fatalf("create cancellations: %v", err)
	}
	events := []domain.PaymentEventInbox{
		{ID: "pev_old", Provider: "creem", ProviderEventID: "evt_old", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-NEW", PayloadHash: "old", RawPayload: `{"subscription_id":"sub_secret"}`, Status: domain.PaymentEventStatusProcessed, ReceivedAt: receivedOld},
		{ID: "pev_new", Provider: "creem", ProviderEventID: "evt_new", EventType: domain.PaymentEventTypeRenewalPaid, OutTradeNo: "CHK-NEW", PayloadHash: "new", RawPayload: `{"subscription_id":"sub_secret"}`, Status: domain.PaymentEventStatusProcessed, ReceivedAt: receivedNew},
		{ID: "pev_mock", Provider: "mock", ProviderEventID: "evt_mock", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-MOCK", PayloadHash: "mock", Status: domain.PaymentEventStatusProcessed, ReceivedAt: now},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatalf("create events: %v", err)
	}

	readModel, err := (&AdminSubscriptionReadRepo{DB: db}).List(context.Background(), repository.AdminSubscriptionQuery{
		UserID:   "usr_1",
		SKUCode:  domain.SKUProOwnAIMonthly,
		Provider: "creem",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if len(readModel.Records) != 1 {
		t.Fatalf("expected one record, got %#v", readModel)
	}
	record := readModel.Records[0]
	if record.User.ID != "usr_1" || record.SKUCode != domain.SKUProOwnAIMonthly {
		t.Fatalf("unexpected record identity: %#v", record)
	}
	if len(record.Grants) != 2 {
		t.Fatalf("expected grants loaded for projection, got %#v", record.Grants)
	}
	if record.LatestOrder == nil || record.LatestOrder.OutTradeNo != "CHK-NEW" {
		t.Fatalf("expected latest creem order, got %#v", record.LatestOrder)
	}
	if record.LatestCancellation == nil || record.LatestCancellation.ID != "sub_cancel_new" {
		t.Fatalf("expected latest cancellation, got %#v", record.LatestCancellation)
	}
	if len(record.PaymentEvents) != 2 || record.PaymentEvents[0].ID != "pev_new" {
		t.Fatalf("expected latest payment event first, got %#v", record.PaymentEvents)
	}
}

func TestAdminSubscriptionReadRepoIncludesGrantOnlyCandidates(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.Order{},
		&domain.EntitlementGrant{},
		&domain.SubscriptionCancellation{},
		&domain.PaymentEventInbox{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&domain.User{ID: "usr_life", Email: "life@example.com", Status: domain.UserStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&domain.EntitlementGrant{ID: "grt_life", UserID: "usr_life", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, Source: domain.GrantSourceFulfillment, StartsAt: now.Add(-time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}

	readModel, err := (&AdminSubscriptionReadRepo{DB: db}).List(context.Background(), repository.AdminSubscriptionQuery{UserID: "usr_life", Limit: 10})
	if err != nil {
		t.Fatalf("list subscriptions: %v", err)
	}
	if len(readModel.Records) != 1 || readModel.Records[0].SKUCode != domain.SKUProOwnAILifetime {
		t.Fatalf("expected grant-only lifetime candidate, got %#v", readModel)
	}
}
