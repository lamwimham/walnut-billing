package gorm_repo

import (
	"context"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openAccessLoginChallengeRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.AccessLoginChallenge{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestAccessLoginChallengeRepo_CRUD(t *testing.T) {
	db := openAccessLoginChallengeRepoTestDB(t)
	repo := &AccessLoginChallengeRepo{DB: db}
	ctx := context.Background()
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	challenge := &domain.AccessLoginChallenge{
		ID:             "alc_1",
		Email:          "writer@example.com",
		DeviceID:       "device-1",
		TokenHash:      "hash",
		Status:         domain.AccessLoginChallengeStatusPending,
		MaxAttempts:    5,
		IdempotencyKey: "login:1",
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := repo.Create(ctx, challenge); err != nil {
		t.Fatalf("create: %v", err)
	}
	byID, err := repo.GetByID(ctx, "alc_1")
	if err != nil || byID.Email != "writer@example.com" {
		t.Fatalf("get by id=%#v err=%v", byID, err)
	}
	byKey, err := repo.GetByIdempotencyKey(ctx, "login:1")
	if err != nil || byKey.ID != "alc_1" {
		t.Fatalf("get by key=%#v err=%v", byKey, err)
	}
	byKey.Status = domain.AccessLoginChallengeStatusConsumed
	consumedAt := time.Now().UTC()
	byKey.ConsumedAt = &consumedAt
	if err := repo.Update(ctx, byKey); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := repo.GetByID(ctx, "alc_1")
	if err != nil || updated.Status != domain.AccessLoginChallengeStatusConsumed || updated.ConsumedAt == nil {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := repo.GetByID(ctx, "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestAccessLoginChallengeRepo_ConsumePendingIsAtomic(t *testing.T) {
	db := openAccessLoginChallengeRepoTestDB(t)
	repo := &AccessLoginChallengeRepo{DB: db}
	ctx := context.Background()
	now := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	challenge := &domain.AccessLoginChallenge{
		ID:             "alc_atomic",
		Email:          "writer@example.com",
		DeviceID:       "device-1",
		TokenHash:      "hash",
		Status:         domain.AccessLoginChallengeStatusPending,
		MaxAttempts:    5,
		IdempotencyKey: "login:atomic",
		ExpiresAt:      now.Add(10 * time.Minute),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := repo.Create(ctx, challenge); err != nil {
		t.Fatalf("create: %v", err)
	}
	consumedAt := now.Add(time.Minute)
	consumed, err := repo.ConsumePending(ctx, "alc_atomic", consumedAt)
	if err != nil || !consumed {
		t.Fatalf("expected first consume to win, consumed=%v err=%v", consumed, err)
	}
	consumed, err = repo.ConsumePending(ctx, "alc_atomic", consumedAt.Add(time.Second))
	if err != nil || consumed {
		t.Fatalf("expected second consume to lose, consumed=%v err=%v", consumed, err)
	}
	updated, err := repo.GetByID(ctx, "alc_atomic")
	if err != nil {
		t.Fatalf("load consumed challenge: %v", err)
	}
	if updated.Status != domain.AccessLoginChallengeStatusConsumed || updated.ConsumedAt == nil || !updated.ConsumedAt.Equal(consumedAt) {
		t.Fatalf("unexpected consumed challenge: %#v", updated)
	}
}
