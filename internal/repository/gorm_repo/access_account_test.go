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

func TestAccessAccountReadRepoListsUsersWithAccessFacts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.User{}, &domain.UserDevice{}, &domain.TrialGrant{}, &domain.EntitlementGrant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	if err := db.Create(&domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive, LastSeenAt: now}).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := db.Create(&domain.TrialGrant{ID: "trl_1", UserID: "usr_1", Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create trial: %v", err)
	}
	if err := db.Create(&domain.EntitlementGrant{ID: "grt_1", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, StartsAt: now}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}

	records, total, err := (&AccessAccountReadRepo{DB: db}).List(context.Background(), repository.AccessAccountQuery{Email: "writer@example.com", Limit: 10})
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("unexpected records total=%d records=%#v", total, records)
	}
	if records[0].User.ID != "usr_1" || len(records[0].Devices) != 1 || len(records[0].TrialGrants) != 1 || len(records[0].EntitlementGrants) != 1 {
		t.Fatalf("expected grouped account facts, got %#v", records[0])
	}
}

func TestAdminUserAccessSummaryReadRepoGetsCrossModuleFacts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.UserDevice{},
		&domain.TrialGrant{},
		&domain.EntitlementGrant{},
		&domain.Order{},
		&domain.PaymentEventInbox{},
		&domain.PaymentRiskFlag{},
		&domain.CloudProject{},
		&domain.CloudObject{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := db.Create(&domain.UserDevice{ID: "dev_1", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive, LastSeenAt: now}).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := db.Create(&domain.TrialGrant{ID: "trl_1", UserID: "usr_1", Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create trial: %v", err)
	}
	if err := db.Create(&domain.EntitlementGrant{ID: "grt_1", UserID: "usr_1", EntitlementID: domain.EntitlementEditorialStudio, Status: domain.GrantStatusActive, StartsAt: now}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	paidAt := now.Add(-time.Hour)
	if err := db.Create(&domain.Order{OutTradeNo: "CHK-1", UserID: "usr_1", SKUCode: domain.SKUProOwnAIMonthly, Status: domain.OrderStatusFulfilled, Provider: "creem", PaidAt: &paidAt, OrderType: domain.OrderTypeCheckout}).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := db.Create(&domain.PaymentEventInbox{ID: "pev_1", Provider: "creem", ProviderEventID: "evt_1", EventType: domain.PaymentEventTypePaid, OutTradeNo: "CHK-1", Status: domain.PaymentEventStatusProcessed, PayloadHash: "hash", ReceivedAt: now}).Error; err != nil {
		t.Fatalf("create payment event: %v", err)
	}
	if err := db.Create(&domain.PaymentRiskFlag{ID: "risk_1", UserID: "usr_1", OutTradeNo: "CHK-1", Provider: "creem", ProviderEventID: "evt_risk_1", Reason: domain.PaymentRiskReasonDispute, Severity: domain.PaymentRiskSeverityCritical, Status: domain.PaymentRiskStatusOpen, CreatedAt: now}).Error; err != nil {
		t.Fatalf("create risk flag: %v", err)
	}
	if err := db.Create(&domain.CloudProject{ID: "cpj_1", UserID: "usr_1", ClientProjectID: "client-1", Name: "Project", Status: domain.CloudProjectStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := db.Create(&domain.CloudObject{ID: "cob_1", UserID: "usr_1", CloudProjectID: "cpj_1", ClientProjectID: "client-1", ObjectKey: "obj", SizeBytes: 123, Status: domain.CloudObjectStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create object: %v", err)
	}

	record, err := (&AdminUserAccessSummaryReadRepo{DB: db}).Get(context.Background(), repository.AdminUserAccessSummaryQuery{UserID: "usr_1", RecentLimit: 5})
	if err != nil {
		t.Fatalf("get summary record: %v", err)
	}
	if record.User.ID != "usr_1" ||
		len(record.Devices) != 1 ||
		len(record.TrialGrants) != 1 ||
		len(record.EntitlementGrants) != 1 ||
		len(record.Orders) != 1 ||
		len(record.PaymentEvents) != 1 ||
		len(record.RiskFlags) != 1 ||
		len(record.CloudProjects) != 1 ||
		record.CloudUsedBytes != 123 {
		t.Fatalf("expected grouped summary facts, got %#v", record)
	}
}
