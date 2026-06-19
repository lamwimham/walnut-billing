package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	gorm_repo "walnut-billing/internal/repository/gorm_repo"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeObjectStorageProvider struct {
	providerID string
}

func (p fakeObjectStorageProvider) ProviderID() string {
	return p.providerID
}

func (p fakeObjectStorageProvider) BuildUploadTarget(ctx context.Context, request CloudObjectUploadRequest) (CloudObjectUploadTarget, error) {
	return CloudObjectUploadTarget{
		ObjectKey: request.ObjectKey,
		UploadURL: "test-provider://" + request.ObjectKey,
		Method:    "PUT",
		Provider:  p.providerID,
		Headers: map[string]string{
			"Content-Type": request.ContentType,
		},
	}, nil
}

func (p fakeObjectStorageProvider) BuildDownloadTarget(ctx context.Context, request CloudObjectDownloadRequest) (CloudObjectDownloadTarget, error) {
	return CloudObjectDownloadTarget{
		ObjectKey:   request.ObjectKey,
		DownloadURL: "test-provider://" + request.ObjectKey,
		Method:      "GET",
		Provider:    p.providerID,
		ExpiresAt:   time.Now().UTC().Add(15 * time.Minute),
	}, nil
}

func (p fakeObjectStorageProvider) DeleteObject(ctx context.Context, request CloudObjectDeleteRequest) error {
	return nil
}

func (p fakeObjectStorageProvider) HeadObject(ctx context.Context, request CloudObjectHeadRequest) (CloudObjectHeadResult, error) {
	return CloudObjectHeadResult{ObjectKey: request.ObjectKey, Exists: true, Provider: p.providerID}, nil
}

type cloudStorageServiceTestDeps struct {
	db        *gorm.DB
	users     *gorm_repo.UserRepo
	grants    *gorm_repo.EntitlementGrantRepo
	projects  *gorm_repo.CloudProjectRepo
	sessions  *gorm_repo.CloudSyncSessionRepo
	manifests *gorm_repo.CloudManifestRepo
	objects   *gorm_repo.CloudObjectRepo
}

func newCloudStorageServiceTestDeps(t *testing.T) cloudStorageServiceTestDeps {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.EntitlementGrant{},
		&domain.CloudProject{},
		&domain.CloudSyncSession{},
		&domain.CloudManifest{},
		&domain.CloudObject{},
	); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return cloudStorageServiceTestDeps{
		db:        db,
		users:     &gorm_repo.UserRepo{DB: db},
		grants:    &gorm_repo.EntitlementGrantRepo{DB: db},
		projects:  &gorm_repo.CloudProjectRepo{DB: db},
		sessions:  &gorm_repo.CloudSyncSessionRepo{DB: db},
		manifests: &gorm_repo.CloudManifestRepo{DB: db},
		objects:   &gorm_repo.CloudObjectRepo{DB: db},
	}
}

func (d cloudStorageServiceTestDeps) serviceWithQuota(quotaBytes int64) CloudStorageService {
	return d.serviceWithPolicy(NewStaticCloudStorageQuotaPolicy(quotaBytes))
}

func (d cloudStorageServiceTestDeps) serviceWithPolicy(policy CloudStorageQuotaPolicy) CloudStorageService {
	return NewCloudStorageService(CloudStorageDependencies{
		Users:             d.users,
		Grants:            d.grants,
		Projects:          d.projects,
		SyncSessions:      d.sessions,
		Manifests:         d.manifests,
		Objects:           d.objects,
		Policy:            policy,
		Provider:          fakeObjectStorageProvider{providerID: "test-provider"},
		UnitOfWorkFactory: func() repository.UnitOfWork { return gorm_repo.NewUnitOfWork(d.db) },
	})
}

func (d cloudStorageServiceTestDeps) seedUser(t *testing.T, withCloudGrant bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	if err := d.users.Create(ctx, &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if !withCloudGrant {
		return
	}
	if err := d.grants.Create(ctx, &domain.EntitlementGrant{
		ID:            "grt_cloud",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceTrial,
		StartsAt:      now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}
}

func TestCloudStorageService_AuthorizeCommitAndReplaceProjectManifest(t *testing.T) {
	deps := newCloudStorageServiceTestDeps(t)
	deps.seedUser(t, true)
	svc := deps.serviceWithQuota(1000)
	ctx := context.Background()

	initialResources := []CloudResourceDescriptor{
		{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:page-v1", SizeBytes: 400, ContentType: "text/markdown", Filename: "page.md"},
		{ResourceID: "raw/report.pdf", ResourceKind: "raw_material", ContentHash: "sha256:report", SizeBytes: 200, ContentType: "application/pdf", Filename: "report.pdf"},
	}
	authorization, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		ProjectName:     "Local Project",
		Resources:       initialResources,
	})
	if err != nil {
		t.Fatalf("authorize initial sync: %v", err)
	}
	if authorization.Provider != "test-provider" || authorization.RequestedBytes != 600 || len(authorization.UploadTargets) != 2 {
		t.Fatalf("unexpected authorization: %#v", authorization)
	}
	if !strings.HasPrefix(authorization.UploadTargets[0].UploadURL, "test-provider://accounts/usr_1/projects/project-local/") {
		t.Fatalf("unexpected upload target: %#v", authorization.UploadTargets[0])
	}

	committed, err := svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		ProjectName:     "Local Project",
		SyncSessionID:   authorization.ID,
		ManifestHash:    "sha256:manifest-v1",
		ManifestVersion: 1,
		Resources:       initialResources,
		IdempotencyKey:  "project-local:v1",
	})
	if err != nil {
		t.Fatalf("commit manifest v1: %v", err)
	}
	if committed.Usage.UsedBytes != 600 || committed.Manifest.ObjectCount != 2 || committed.Project.LastManifestID != committed.Manifest.ID {
		t.Fatalf("unexpected v1 commit: %#v", committed)
	}

	idempotent, err := svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		ProjectName:     "Local Project",
		SyncSessionID:   authorization.ID,
		ManifestHash:    "sha256:manifest-v1",
		ManifestVersion: 1,
		Resources:       initialResources,
		IdempotencyKey:  "project-local:v1",
	})
	if err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}
	if idempotent.Manifest.ID != committed.Manifest.ID {
		t.Fatalf("expected idempotent manifest %s, got %s", committed.Manifest.ID, idempotent.Manifest.ID)
	}

	replacementResources := []CloudResourceDescriptor{
		{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:page-v2", SizeBytes: 900, ContentType: "text/markdown", Filename: "page.md"},
	}
	replacementAuthorization, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		ProjectName:     "Local Project",
		Resources:       replacementResources,
	})
	if err != nil {
		t.Fatalf("replacement authorization should subtract existing project bytes: %v", err)
	}
	if replacementAuthorization.UsedBytes != 600 || replacementAuthorization.RequestedBytes != 900 {
		t.Fatalf("unexpected replacement authorization: %#v", replacementAuthorization)
	}

	replaced, err := svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		ProjectName:     "Renamed Project",
		SyncSessionID:   replacementAuthorization.ID,
		ManifestHash:    "sha256:manifest-v2",
		ManifestVersion: 2,
		Resources:       replacementResources,
		IdempotencyKey:  "project-local:v2",
	})
	if err != nil {
		t.Fatalf("commit replacement manifest: %v", err)
	}
	if replaced.Usage.UsedBytes != 900 || replaced.Project.Name != "Renamed Project" {
		t.Fatalf("unexpected replacement commit: %#v", replaced)
	}
	activeObjects, err := deps.objects.ListByProject(ctx, replaced.Project.ID, domain.CloudObjectStatusActive)
	if err != nil {
		t.Fatalf("list active objects: %v", err)
	}
	if len(activeObjects) != 1 || activeObjects[0].SizeBytes != 900 {
		t.Fatalf("expected one active replacement object, got %#v", activeObjects)
	}
	replacedObjects, err := deps.objects.ListByProject(ctx, replaced.Project.ID, domain.CloudObjectStatusReplaced)
	if err != nil {
		t.Fatalf("list replaced objects: %v", err)
	}
	if len(replacedObjects) != 2 {
		t.Fatalf("expected two replaced v1 objects, got %#v", replacedObjects)
	}

	_, err = svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		Resources: []CloudResourceDescriptor{
			{ResourceID: "wiki/huge.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:huge", SizeBytes: 1200, ContentType: "text/markdown", Filename: "huge.md"},
		},
	})
	if !errors.Is(err, ErrCloudStorageOverQuota) {
		t.Fatalf("expected over quota, got %v", err)
	}
}

func TestCloudStorageService_CommitRequiresMatchingSyncSession(t *testing.T) {
	deps := newCloudStorageServiceTestDeps(t)
	deps.seedUser(t, true)
	svc := deps.serviceWithQuota(1000)
	ctx := context.Background()

	resources := []CloudResourceDescriptor{
		{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:page", SizeBytes: 100, ContentType: "text/markdown", Filename: "page.md"},
	}
	authorization, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		Resources:       resources,
	})
	if err != nil {
		t.Fatalf("authorize sync: %v", err)
	}

	_, err = svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		SyncSessionID:   "csy_missing",
		ManifestHash:    "sha256:manifest",
		Resources:       resources,
		IdempotencyKey:  "project-local:v1",
	})
	if !errors.Is(err, ErrCloudSyncSessionNotFound) {
		t.Fatalf("expected missing sync session, got %v", err)
	}

	_, err = svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		SyncSessionID:   authorization.ID,
		ManifestHash:    "sha256:manifest",
		Resources: []CloudResourceDescriptor{
			{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:changed", SizeBytes: 100, ContentType: "text/markdown", Filename: "page.md"},
		},
		IdempotencyKey: "project-local:v1",
	})
	if !errors.Is(err, ErrInvalidCloudStorage) {
		t.Fatalf("expected fingerprint mismatch, got %v", err)
	}

	committed, err := svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		SyncSessionID:   authorization.ID,
		ManifestHash:    "sha256:manifest",
		Resources:       resources,
		IdempotencyKey:  "project-local:v1",
	})
	if err != nil {
		t.Fatalf("commit manifest: %v", err)
	}
	if committed.Manifest.SyncSessionID != authorization.ID {
		t.Fatalf("expected manifest to bind sync session, got %#v", committed.Manifest)
	}

	_, err = svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		SyncSessionID:   authorization.ID,
		ManifestHash:    "sha256:manifest-duplicate",
		Resources:       resources,
		IdempotencyKey:  "project-local:v2",
	})
	if !errors.Is(err, ErrCloudSyncSessionAlreadyCommitted) {
		t.Fatalf("expected committed sync session rejection, got %v", err)
	}
}

func TestCloudStorageService_RestoreMetadataListsProjectsAndLatestManifest(t *testing.T) {
	deps := newCloudStorageServiceTestDeps(t)
	deps.seedUser(t, true)
	svc := deps.serviceWithQuota(1000)
	ctx := context.Background()
	resources := []CloudResourceDescriptor{
		{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:page", SizeBytes: 100, ContentType: "text/markdown", Filename: "page.md"},
	}
	authorization, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		ProjectName:     "Local Project",
		Resources:       resources,
	})
	if err != nil {
		t.Fatalf("authorize sync: %v", err)
	}
	committed, err := svc.CommitManifest(ctx, CloudManifestCommitInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		SyncSessionID:   authorization.ID,
		ManifestHash:    "sha256:manifest",
		ManifestVersion: 3,
		Resources:       resources,
		IdempotencyKey:  "project-local:v3",
	})
	if err != nil {
		t.Fatalf("commit manifest: %v", err)
	}

	projects, err := svc.ListProjects(ctx, CloudStorageProjectQuery{UserID: "usr_1"})
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects.Projects) != 1 || projects.Projects[0].LatestManifest == nil || projects.Projects[0].LatestManifest.ID != committed.Manifest.ID {
		t.Fatalf("unexpected project list: %#v", projects)
	}

	latest, err := svc.LatestManifest(ctx, CloudStorageLatestManifestQuery{UserID: "usr_1", CloudProjectID: committed.Project.ID})
	if err != nil {
		t.Fatalf("latest manifest: %v", err)
	}
	if latest.Manifest == nil || latest.Manifest.ManifestVersion != 3 || len(latest.Objects) != 1 || latest.Objects[0].ObjectKey == "" {
		t.Fatalf("unexpected latest manifest: %#v", latest)
	}

	download, err := svc.BuildDownloadTarget(ctx, CloudDownloadTargetInput{
		UserID:         "usr_1",
		CloudProjectID: committed.Project.ID,
		ObjectKey:      latest.Objects[0].ObjectKey,
	})
	if err != nil {
		t.Fatalf("build download target: %v", err)
	}
	if download.DownloadTarget.Provider != "test-provider" || download.Object.ObjectKey != latest.Objects[0].ObjectKey {
		t.Fatalf("unexpected download target authorization: %#v", download)
	}
}

func TestPlanAwareCloudStorageQuotaPolicy_SelectsPlanSpecificQuota(t *testing.T) {
	now := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	monthlyEnd := now.AddDate(0, 1, 0)
	policy := NewPlanAwareCloudStorageQuotaPolicy(CloudStorageQuotaPolicyConfig{
		DefaultQuotaBytes:  100,
		TrialQuotaBytes:    200,
		MonthlyQuotaBytes:  300,
		LifetimeQuotaBytes: 400,
	})

	trial := policy.Decide(context.Background(), CloudStorageQuotaInput{Now: now, Grants: []domain.EntitlementGrant{{
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceTrial,
		StartsAt:      now.Add(-time.Hour),
		ExpiresAt:     &monthlyEnd,
	}}})
	if trial.Plan != CloudStoragePlanTrial || trial.QuotaBytes != 200 || !trial.HasEntitlement {
		t.Fatalf("expected trial quota decision, got %#v", trial)
	}

	monthly := policy.Decide(context.Background(), CloudStorageQuotaInput{Now: now, Grants: []domain.EntitlementGrant{{
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now.Add(-time.Hour),
		ExpiresAt:     &monthlyEnd,
	}}})
	if monthly.Plan != CloudStoragePlanMonthly || monthly.QuotaBytes != 300 {
		t.Fatalf("expected monthly quota decision, got %#v", monthly)
	}

	grace := policy.Decide(context.Background(), CloudStorageQuotaInput{Now: now, Grants: []domain.EntitlementGrant{{
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceSubscriptionGrace,
		StartsAt:      now.Add(-time.Hour),
		ExpiresAt:     &monthlyEnd,
	}}})
	if grace.Plan != CloudStoragePlanMonthly || grace.QuotaBytes != 300 {
		t.Fatalf("expected grace to keep monthly quota decision, got %#v", grace)
	}

	lifetime := policy.Decide(context.Background(), CloudStorageQuotaInput{Now: now, Grants: []domain.EntitlementGrant{{
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      now.Add(-time.Hour),
	}}})
	if lifetime.Plan != CloudStoragePlanLifetime || lifetime.QuotaBytes != 400 {
		t.Fatalf("expected lifetime quota decision, got %#v", lifetime)
	}
}

func TestCloudStorageService_UsesPlanAwareQuota(t *testing.T) {
	deps := newCloudStorageServiceTestDeps(t)
	deps.seedUser(t, true)
	svc := deps.serviceWithPolicy(NewPlanAwareCloudStorageQuotaPolicy(CloudStorageQuotaPolicyConfig{
		DefaultQuotaBytes:  1000,
		TrialQuotaBytes:    100,
		MonthlyQuotaBytes:  1000,
		LifetimeQuotaBytes: 2000,
	}))
	ctx := context.Background()

	_, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		Resources: []CloudResourceDescriptor{{
			ResourceID: "wiki/page.md", ContentHash: "sha256:page", SizeBytes: 200,
		}},
	})
	if !errors.Is(err, ErrCloudStorageOverQuota) {
		t.Fatalf("expected trial quota overage, got %v", err)
	}

	periodEnd := time.Now().UTC().AddDate(0, 1, 0)
	if err := deps.grants.Create(ctx, &domain.EntitlementGrant{
		ID:            "monthly_cloud",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      time.Now().UTC().Add(-time.Hour),
		ExpiresAt:     &periodEnd,
	}); err != nil {
		t.Fatalf("create monthly grant: %v", err)
	}
	authorization, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		Resources: []CloudResourceDescriptor{{
			ResourceID: "wiki/page.md", ContentHash: "sha256:page", SizeBytes: 200,
		}},
	})
	if err != nil {
		t.Fatalf("expected monthly quota to authorize sync, got %v", err)
	}
	if authorization.QuotaBytes != 1000 {
		t.Fatalf("expected monthly quota bytes, got %#v", authorization)
	}
	usage, err := svc.Usage(ctx, "usr_1")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage.Plan != CloudStoragePlanMonthly || usage.QuotaBytes != 1000 {
		t.Fatalf("expected monthly usage projection, got %#v", usage)
	}
}

func TestCloudStorageService_DeniesSyncWithoutGrantButReportsUsage(t *testing.T) {
	deps := newCloudStorageServiceTestDeps(t)
	deps.seedUser(t, false)
	svc := deps.serviceWithQuota(1000)
	ctx := context.Background()

	_, err := svc.AuthorizeSync(ctx, CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		Resources: []CloudResourceDescriptor{
			{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:page", SizeBytes: 100, ContentType: "text/markdown", Filename: "page.md"},
		},
	})
	if !errors.Is(err, ErrCloudStorageAccessDenied) {
		t.Fatalf("expected access denied, got %v", err)
	}
	usage, err := svc.Usage(ctx, "usr_1")
	if err != nil {
		t.Fatalf("usage without grant: %v", err)
	}
	if usage.QuotaBytes != 0 || usage.UsedBytes != 0 || usage.OverQuota {
		t.Fatalf("expected basic zero-quota usage projection, got %#v", usage)
	}
}

func TestCloudStorageService_DefaultProviderIsUnconfigured(t *testing.T) {
	deps := newCloudStorageServiceTestDeps(t)
	deps.seedUser(t, true)
	svc := NewCloudStorageService(CloudStorageDependencies{
		Users:        deps.users,
		Grants:       deps.grants,
		Projects:     deps.projects,
		SyncSessions: deps.sessions,
		Manifests:    deps.manifests,
		Objects:      deps.objects,
		Policy:       NewStaticCloudStorageQuotaPolicy(1000),
	})

	_, err := svc.AuthorizeSync(context.Background(), CloudSyncAuthorizationInput{
		UserID:          "usr_1",
		ClientProjectID: "project-local",
		Resources: []CloudResourceDescriptor{
			{ResourceID: "wiki/page.md", ResourceKind: "wiki_markdown", ContentHash: "sha256:page", SizeBytes: 100, ContentType: "text/markdown", Filename: "page.md"},
		},
	})
	if !errors.Is(err, ErrCloudStorageProviderNotConfigured) {
		t.Fatalf("expected provider not configured, got %v", err)
	}
	projects, err := deps.projects.ListByUser(context.Background(), "usr_1", "", 10, 0)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("unconfigured provider must not create project anchors, got %#v", projects)
	}
}
