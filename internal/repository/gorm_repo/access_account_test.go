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
