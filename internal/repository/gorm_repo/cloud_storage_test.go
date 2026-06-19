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

func openCloudStorageRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&domain.CloudProject{}, &domain.CloudSyncSession{}, &domain.CloudManifest{}, &domain.CloudObject{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestCloudStorageRepositoriesPersistManifestAndObjectUsage(t *testing.T) {
	db := openCloudStorageRepoTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	projects := &CloudProjectRepo{DB: db}
	sessions := &CloudSyncSessionRepo{DB: db}
	manifests := &CloudManifestRepo{DB: db}
	objects := &CloudObjectRepo{DB: db}

	project := &domain.CloudProject{
		ID:              "cpr_1",
		UserID:          "usr_1",
		ClientProjectID: "local-project",
		Name:            "Local Project",
		Status:          domain.CloudProjectStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := projects.Create(ctx, project); err != nil {
		t.Fatalf("create project: %v", err)
	}
	loadedProject, err := projects.GetByUserAndClientProject(ctx, "usr_1", "local-project")
	if err != nil || loadedProject.ID != "cpr_1" {
		t.Fatalf("load project: %#v err=%v", loadedProject, err)
	}
	listedProjects, err := projects.ListByUser(ctx, "usr_1", domain.CloudProjectStatusActive, 10, 0)
	if err != nil || len(listedProjects) != 1 {
		t.Fatalf("list projects: %#v err=%v", listedProjects, err)
	}

	committedAt := now.Add(time.Minute)
	session := &domain.CloudSyncSession{
		ID:                  "csy_1",
		UserID:              "usr_1",
		CloudProjectID:      "cpr_1",
		ClientProjectID:     "local-project",
		Provider:            "test-provider",
		ResourceFingerprint: "fingerprint",
		RequestedBytes:      300,
		UsedBytes:           0,
		QuotaBytes:          1000,
		Status:              domain.CloudSyncSessionStatusAuthorized,
		ExpiresAt:           now.Add(15 * time.Minute),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := sessions.Create(ctx, session); err != nil {
		t.Fatalf("create sync session: %v", err)
	}
	loadedSession, err := sessions.GetByID(ctx, "csy_1")
	if err != nil || loadedSession.Status != domain.CloudSyncSessionStatusAuthorized {
		t.Fatalf("load sync session: %#v err=%v", loadedSession, err)
	}
	loadedSession.Status = domain.CloudSyncSessionStatusCommitted
	loadedSession.ManifestID = "cmf_1"
	if err := sessions.Update(ctx, loadedSession); err != nil {
		t.Fatalf("update sync session: %v", err)
	}
	manifest := &domain.CloudManifest{
		ID:              "cmf_1",
		UserID:          "usr_1",
		CloudProjectID:  "cpr_1",
		ClientProjectID: "local-project",
		ManifestHash:    "sha256:manifest",
		ManifestVersion: 1,
		ObjectCount:     2,
		TotalBytes:      300,
		Status:          domain.CloudManifestStatusCommitted,
		SyncSessionID:   "csy_1",
		IdempotencyKey:  "idem-1",
		CreatedAt:       now,
		CommittedAt:     &committedAt,
	}
	if err := manifests.Create(ctx, manifest); err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	loadedManifest, err := manifests.GetByIdempotencyKey(ctx, "idem-1")
	if err != nil || loadedManifest.ID != "cmf_1" {
		t.Fatalf("load manifest: %#v err=%v", loadedManifest, err)
	}

	first := &domain.CloudObject{
		ID:              "cob_1",
		UserID:          "usr_1",
		CloudProjectID:  "cpr_1",
		ClientProjectID: "local-project",
		ManifestID:      "cmf_1",
		ResourceID:      "wiki/a.md",
		ResourceKind:    "wiki",
		ObjectKey:       "accounts/usr_1/projects/local-project/wiki/hash-a/a.md",
		ContentHash:     "hash-a",
		SizeBytes:       100,
		ContentType:     "text/markdown",
		Status:          domain.CloudObjectStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	second := &domain.CloudObject{
		ID:              "cob_2",
		UserID:          "usr_1",
		CloudProjectID:  "cpr_1",
		ClientProjectID: "local-project",
		ManifestID:      "cmf_1",
		ResourceID:      "wiki/b.md",
		ResourceKind:    "wiki",
		ObjectKey:       "accounts/usr_1/projects/local-project/wiki/hash-b/b.md",
		ContentHash:     "hash-b",
		SizeBytes:       200,
		ContentType:     "text/markdown",
		Status:          domain.CloudObjectStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := objects.Upsert(ctx, first); err != nil {
		t.Fatalf("upsert first object: %v", err)
	}
	if err := objects.Upsert(ctx, second); err != nil {
		t.Fatalf("upsert second object: %v", err)
	}
	loadedObject, err := objects.GetByObjectKey(ctx, first.ObjectKey)
	if err != nil || loadedObject.ID != first.ID {
		t.Fatalf("load object by key: %#v err=%v", loadedObject, err)
	}
	used, err := objects.SumActiveBytesByUser(ctx, "usr_1")
	if err != nil || used != 300 {
		t.Fatalf("sum active bytes: used=%d err=%v", used, err)
	}

	first.SizeBytes = 150
	first.UpdatedAt = now.Add(2 * time.Minute)
	if err := objects.Upsert(ctx, first); err != nil {
		t.Fatalf("upsert updated first object: %v", err)
	}
	used, err = objects.SumActiveBytesByUser(ctx, "usr_1")
	if err != nil || used != 350 {
		t.Fatalf("sum active bytes after upsert: used=%d err=%v", used, err)
	}

	first.Status = domain.CloudObjectStatusReplaced
	if err := objects.Update(ctx, first); err != nil {
		t.Fatalf("replace first object: %v", err)
	}
	used, err = objects.SumActiveBytesByUser(ctx, "usr_1")
	if err != nil || used != 200 {
		t.Fatalf("sum active bytes after replacement: used=%d err=%v", used, err)
	}
}

func TestCloudStorageRepositoriesReturnNotFound(t *testing.T) {
	db := openCloudStorageRepoTestDB(t)
	ctx := context.Background()
	if _, err := (&CloudProjectRepo{DB: db}).GetByUserAndClientProject(ctx, "usr_1", "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected project not found, got %v", err)
	}
	if _, err := (&CloudManifestRepo{DB: db}).GetByIdempotencyKey(ctx, "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected manifest not found, got %v", err)
	}
	if _, err := (&CloudSyncSessionRepo{DB: db}).GetByID(ctx, "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected sync session not found, got %v", err)
	}
	if _, err := (&CloudObjectRepo{DB: db}).GetByObjectKey(ctx, "missing"); err != repository.ErrNotFound {
		t.Fatalf("expected object not found, got %v", err)
	}
}
