package gorm_repo

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAdminCloudStorageReadRepoAggregatesUsageAndProjectStats(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.EntitlementGrant{},
		&domain.CloudProject{},
		&domain.CloudManifest{},
		&domain.CloudObject{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	if err := db.Create(&domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create user 1: %v", err)
	}
	if err := db.Create(&domain.User{ID: "usr_2", Email: "disabled@example.com", Status: domain.UserStatusDisabled, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create user 2: %v", err)
	}
	if err := db.Create(&domain.EntitlementGrant{ID: "grt_1", UserID: "usr_1", EntitlementID: domain.EntitlementCloudStorage, Status: domain.GrantStatusActive, StartsAt: now.Add(-24 * time.Hour), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	activeProject := domain.CloudProject{ID: "cpr_1", UserID: "usr_1", ClientProjectID: "local-project", Name: "Secret Research", Status: domain.CloudProjectStatusActive, LastManifestID: "cmf_2", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now}
	archivedProject := domain.CloudProject{ID: "cpr_2", UserID: "usr_1", ClientProjectID: "archived-project", Name: "Archive", Status: domain.CloudProjectStatusArchived, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour)}
	disabledProject := domain.CloudProject{ID: "cpr_3", UserID: "usr_2", ClientProjectID: "disabled-project", Name: "Disabled", Status: domain.CloudProjectStatusActive, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now}
	if err := db.Create(&activeProject).Error; err != nil {
		t.Fatalf("create active project: %v", err)
	}
	if err := db.Create(&archivedProject).Error; err != nil {
		t.Fatalf("create archived project: %v", err)
	}
	if err := db.Create(&disabledProject).Error; err != nil {
		t.Fatalf("create disabled project: %v", err)
	}
	committedAt := now.Add(-30 * time.Minute)
	if err := db.Create(&domain.CloudManifest{ID: "cmf_1", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestHash: "hash-old", ManifestVersion: 1, ObjectCount: 1, TotalBytes: 100, Status: domain.CloudManifestStatusCommitted, IdempotencyKey: "idem-old", CreatedAt: now.Add(-time.Hour), CommittedAt: &committedAt}).Error; err != nil {
		t.Fatalf("create old manifest: %v", err)
	}
	if err := db.Create(&domain.CloudManifest{ID: "cmf_2", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestHash: "hash-new", ManifestVersion: 2, ObjectCount: 2, TotalBytes: 700, Status: domain.CloudManifestStatusCommitted, IdempotencyKey: "idem-new", CreatedAt: now, CommittedAt: &committedAt}).Error; err != nil {
		t.Fatalf("create new manifest: %v", err)
	}
	objects := []domain.CloudObject{
		{ID: "cob_1", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestID: "cmf_2", ResourceID: "wiki/a.md", ResourceKind: "wiki", ObjectKey: "accounts/usr_1/projects/local-project/wiki/hash-a/a.md", ContentHash: "hash-a", SizeBytes: 400, ContentType: "text/markdown", Status: domain.CloudObjectStatusActive, CreatedAt: now, UpdatedAt: now},
		{ID: "cob_2", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestID: "cmf_2", ResourceID: "wiki/b.md", ResourceKind: "wiki", ObjectKey: "accounts/usr_1/projects/local-project/wiki/hash-b/b.md", ContentHash: "hash-b", SizeBytes: 300, ContentType: "text/markdown", Status: domain.CloudObjectStatusActive, CreatedAt: now, UpdatedAt: now},
		{ID: "cob_old", UserID: "usr_1", CloudProjectID: "cpr_1", ClientProjectID: "local-project", ManifestID: "cmf_1", ResourceID: "wiki/old.md", ResourceKind: "wiki", ObjectKey: "accounts/usr_1/projects/local-project/wiki/hash-old/old.md", ContentHash: "hash-old", SizeBytes: 100, ContentType: "text/markdown", Status: domain.CloudObjectStatusReplaced, CreatedAt: now, UpdatedAt: now},
		{ID: "cob_3", UserID: "usr_2", CloudProjectID: "cpr_3", ClientProjectID: "disabled-project", ManifestID: "cmf_3", ResourceID: "wiki/x.md", ResourceKind: "wiki", ObjectKey: "accounts/usr_2/projects/disabled-project/wiki/hash-x/x.md", ContentHash: "hash-x", SizeBytes: 900, ContentType: "text/markdown", Status: domain.CloudObjectStatusActive, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&objects).Error; err != nil {
		t.Fatalf("create objects: %v", err)
	}

	repo := &AdminCloudStorageReadRepo{DB: db}
	usage, err := repo.ListUsage(context.Background(), repository.AdminCloudStorageUsageQuery{Status: domain.UserStatusActive, Limit: 10})
	if err != nil {
		t.Fatalf("list usage: %v", err)
	}
	if usage.TotalUsers != 1 || usage.TotalUsedBytes != 700 || usage.TotalProjectCount != 2 || usage.TotalActiveProjectCount != 1 {
		t.Fatalf("unexpected usage totals: %#v", usage)
	}
	if len(usage.Users) != 1 || usage.Users[0].User.ID != "usr_1" || usage.Users[0].UsedBytes != 700 || len(usage.Users[0].Projects) != 2 || len(usage.Users[0].Grants) != 1 {
		t.Fatalf("unexpected usage records: %#v", usage.Users)
	}

	projects, err := repo.ListProjects(context.Background(), repository.AdminCloudStorageProjectQuery{UserID: "usr_1", Status: domain.CloudProjectStatusActive, Limit: 10})
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if projects.User.ID != "usr_1" || projects.UsedBytes != 700 || projects.TotalProjects != 1 || len(projects.Projects) != 1 {
		t.Fatalf("unexpected project list: %#v", projects)
	}
	projectRecord := projects.Projects[0]
	if projectRecord.Project.ID != "cpr_1" || projectRecord.LastManifest == nil || projectRecord.LastManifest.ID != "cmf_2" {
		t.Fatalf("expected latest manifest, got %#v", projectRecord)
	}
	if projectRecord.ActiveObjectCount != 2 || projectRecord.ActiveBytes != 700 {
		t.Fatalf("expected active object stats, got %#v", projectRecord)
	}
}

func TestAdminCloudStorageReadRepoReturnsNotFoundForMissingUser(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.User{}, &domain.EntitlementGrant{}, &domain.CloudProject{}, &domain.CloudManifest{}, &domain.CloudObject{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, err = (&AdminCloudStorageReadRepo{DB: db}).ListProjects(context.Background(), repository.AdminCloudStorageProjectQuery{UserID: "usr_missing"})
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}
