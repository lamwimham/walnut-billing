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

func TestAccessSessionRepositoriesPersistDevicesAndTrialGrants(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access_session_repos?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.UserDevice{}, &domain.TrialGrant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	devices := &UserDeviceRepo{DB: db}
	trials := &TrialGrantRepo{DB: db}
	now := time.Now().UTC()

	device := &domain.UserDevice{
		ID:          "dev_1",
		UserID:      "usr_1",
		DeviceID:    "machine-1",
		Status:      domain.DeviceStatusActive,
		FirstSeenAt: now,
		LastSeenAt:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := devices.Create(ctx, device); err != nil {
		t.Fatalf("create device: %v", err)
	}
	loadedDevice, err := devices.GetByUserAndDevice(ctx, "usr_1", "machine-1")
	if err != nil || loadedDevice.ID != "dev_1" {
		t.Fatalf("load device: %#v err=%v", loadedDevice, err)
	}
	loadedDevice.LastSeenAt = now.Add(time.Hour)
	if err := devices.Update(ctx, loadedDevice); err != nil {
		t.Fatalf("update device: %v", err)
	}
	activeDevices, err := devices.ListByUser(ctx, "usr_1", domain.DeviceStatusActive)
	if err != nil || len(activeDevices) != 1 || !activeDevices[0].LastSeenAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("list active devices: %#v err=%v", activeDevices, err)
	}

	expires := now.AddDate(0, 0, 14)
	trial := &domain.TrialGrant{
		ID:             "trl_1",
		UserID:         "usr_1",
		Email:          "writer@example.com",
		GrantType:      domain.TrialGrantTypeProOwnAI,
		Status:         domain.TrialGrantStatusIssued,
		StartsAt:       now,
		ExpiresAt:      &expires,
		IdempotencyKey: "trial:writer@example.com",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := trials.Create(ctx, trial); err != nil {
		t.Fatalf("create trial: %v", err)
	}
	loadedTrial, err := trials.GetByIdempotencyKey(ctx, "trial:writer@example.com")
	if err != nil || loadedTrial.ID != "trl_1" {
		t.Fatalf("load trial: %#v err=%v", loadedTrial, err)
	}
	matches, err := trials.List(ctx, repository.TrialGrantQuery{Email: "writer@example.com", GrantType: domain.TrialGrantTypeProOwnAI, Status: domain.TrialGrantStatusIssued})
	if err != nil || len(matches) != 1 {
		t.Fatalf("list trial grants: %#v err=%v", matches, err)
	}
	loadedTrial.Status = domain.TrialGrantStatusRevoked
	if err := trials.Update(ctx, loadedTrial); err != nil {
		t.Fatalf("update trial: %v", err)
	}
	revoked, err := trials.List(ctx, repository.TrialGrantQuery{UserID: "usr_1", Status: domain.TrialGrantStatusRevoked})
	if err != nil || len(revoked) != 1 {
		t.Fatalf("list revoked trial: %#v err=%v", revoked, err)
	}
}

func TestAccessSessionRepositoriesReturnNotFound(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:access_session_repos_not_found?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.UserDevice{}, &domain.TrialGrant{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	if _, err := (&UserDeviceRepo{DB: db}).GetByUserAndDevice(ctx, "usr_1", "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected device not found, got %v", err)
	}
	if _, err := (&TrialGrantRepo{DB: db}).GetByIdempotencyKey(ctx, "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected trial not found, got %v", err)
	}
}

func TestUserDeviceRepo_GetByIDAndPersistRevocation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:user_device_revoke?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.UserDevice{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := &UserDeviceRepo{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	device := &domain.UserDevice{ID: "dev_revoke", UserID: "usr_1", DeviceID: "device-1", Status: domain.DeviceStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, device); err != nil {
		t.Fatalf("create: %v", err)
	}
	loaded, err := repo.GetByID(ctx, "dev_revoke")
	if err != nil || loaded.DeviceID != "device-1" {
		t.Fatalf("get by id=%#v err=%v", loaded, err)
	}
	revokedAt := now.Add(time.Minute)
	loaded.Status = domain.DeviceStatusDisabled
	loaded.RevokedAt = &revokedAt
	loaded.RevokedBy = "ops"
	loaded.RevokeReason = "lost laptop"
	if err := repo.Update(ctx, loaded); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := repo.GetByID(ctx, "dev_revoke")
	if err != nil || updated.Status != domain.DeviceStatusDisabled || updated.RevokedAt == nil || updated.RevokedBy != "ops" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := repo.GetByID(ctx, "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}
