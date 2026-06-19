package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type fakeAdminCloudStorageReadRepo struct {
	usageQuery    repository.AdminCloudStorageUsageQuery
	usageResult   *repository.AdminCloudStorageUsageReadModel
	usageErr      error
	projectQuery  repository.AdminCloudStorageProjectQuery
	projectResult *repository.AdminCloudStorageProjectReadModel
	projectErr    error
}

func (f *fakeAdminCloudStorageReadRepo) ListUsage(ctx context.Context, query repository.AdminCloudStorageUsageQuery) (*repository.AdminCloudStorageUsageReadModel, error) {
	f.usageQuery = query
	if f.usageErr != nil {
		return nil, f.usageErr
	}
	return f.usageResult, nil
}

func (f *fakeAdminCloudStorageReadRepo) ListProjects(ctx context.Context, query repository.AdminCloudStorageProjectQuery) (*repository.AdminCloudStorageProjectReadModel, error) {
	f.projectQuery = query
	if f.projectErr != nil {
		return nil, f.projectErr
	}
	return f.projectResult, nil
}

func TestAdminCloudStorageServiceProjectsPrivacySafeUsage(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-24 * time.Hour)
	committedAt := now.Add(-time.Hour)
	user := domain.User{
		ID:        "usr_1",
		Email:     "writer@example.com",
		Status:    domain.UserStatusActive,
		CreatedAt: startedAt,
		UpdatedAt: now,
	}
	project := domain.CloudProject{
		ID:              "cpr_1",
		UserID:          "usr_1",
		ClientProjectID: "local-project",
		Name:            "Secret Research Project",
		Status:          domain.CloudProjectStatusActive,
		LastManifestID:  "cmf_1",
		CreatedAt:       startedAt,
		UpdatedAt:       now.Add(-time.Minute),
	}
	archivedProject := domain.CloudProject{
		ID:              "cpr_2",
		UserID:          "usr_1",
		ClientProjectID: "archived-project",
		Name:            "Archived Secrets",
		Status:          domain.CloudProjectStatusArchived,
		CreatedAt:       startedAt,
		UpdatedAt:       now.Add(-2 * time.Minute),
	}
	grant := domain.EntitlementGrant{
		ID:            "grt_cloud",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		StartsAt:      startedAt,
		CreatedAt:     startedAt,
		UpdatedAt:     startedAt,
	}
	manifest := domain.CloudManifest{
		ID:              "cmf_1",
		UserID:          "usr_1",
		CloudProjectID:  "cpr_1",
		ClientProjectID: "local-project",
		ManifestHash:    "sha256:super-secret-manifest",
		ManifestVersion: 7,
		ObjectCount:     2,
		TotalBytes:      700,
		Status:          domain.CloudManifestStatusCommitted,
		CreatedAt:       committedAt,
		CommittedAt:     &committedAt,
	}
	repo := &fakeAdminCloudStorageReadRepo{
		usageResult: &repository.AdminCloudStorageUsageReadModel{
			TotalUsers:              1,
			TotalUsedBytes:          700,
			TotalProjectCount:       2,
			TotalActiveProjectCount: 1,
			Users: []repository.AdminCloudStorageUserRecord{{
				User:      user,
				Grants:    []domain.EntitlementGrant{grant},
				Projects:  []domain.CloudProject{project, archivedProject},
				UsedBytes: 700,
			}},
		},
		projectResult: &repository.AdminCloudStorageProjectReadModel{
			User:          user,
			Grants:        []domain.EntitlementGrant{grant},
			UsedBytes:     700,
			TotalProjects: 1,
			Projects: []repository.AdminCloudStorageProjectRecord{{
				Project:           project,
				LastManifest:      &manifest,
				ActiveObjectCount: 2,
				ActiveBytes:       700,
			}},
		},
	}
	svc := NewAdminCloudStorageService(AdminCloudStorageDependencies{
		ReadModel:   repo,
		QuotaPolicy: NewStaticCloudStorageQuotaPolicy(1000),
		Privacy:     NewAdminPrivacyProjector(),
		Now:         func() time.Time { return now },
	})

	usage, err := svc.Usage(context.Background(), AdminCloudStorageUsageQuery{
		UserID: " usr_1 ",
		Status: " active ",
		Limit:  999,
		Offset: -1,
	})
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if repo.usageQuery.UserID != "usr_1" || repo.usageQuery.Status != "active" || repo.usageQuery.Limit != maxAdminCloudStorageLimit || repo.usageQuery.Offset != 0 {
		t.Fatalf("expected normalized usage query, got %#v", repo.usageQuery)
	}
	if usage.TotalUsedBytes != 700 || len(usage.Users) != 1 {
		t.Fatalf("unexpected usage projection: %#v", usage)
	}
	firstUser := usage.Users[0]
	if firstUser.QuotaBytes != 1000 || firstUser.RemainingBytes != 300 || firstUser.ActiveProjectCount != 1 || !firstUser.HasCloudStorageGrant {
		t.Fatalf("unexpected user usage: %#v", firstUser)
	}
	if firstUser.User.EmailMasked == "writer@example.com" || firstUser.User.EmailFingerprint == "" {
		t.Fatalf("expected privacy-projected email, got %#v", firstUser.User)
	}

	projects, err := svc.ListUserProjects(context.Background(), AdminCloudStorageProjectQuery{
		UserID: " usr_1 ",
		Status: " active ",
		Limit:  5,
		Offset: 2,
	})
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	if repo.projectQuery.UserID != "usr_1" || repo.projectQuery.Status != "active" || repo.projectQuery.Limit != 5 || repo.projectQuery.Offset != 2 {
		t.Fatalf("expected normalized project query, got %#v", repo.projectQuery)
	}
	if projects.TotalProjects != 1 || len(projects.Projects) != 1 {
		t.Fatalf("unexpected projects projection: %#v", projects)
	}
	if projects.Projects[0].NameMasked == project.Name || projects.Projects[0].LastManifest.ManifestHashFingerprint == "" {
		t.Fatalf("expected masked project and manifest fingerprint, got %#v", projects.Projects[0])
	}

	raw, _ := json.Marshal(struct {
		Usage    *AdminCloudStorageUsage
		Projects *AdminCloudStorageProjectList
	}{Usage: usage, Projects: projects})
	body := string(raw)
	for _, leaked := range []string{
		"writer@example.com",
		"Secret Research Project",
		"Archived Secrets",
		"sha256:super-secret-manifest",
		"accounts/usr_1/projects/local-project/wiki/hash/page.md",
		"/Users/writer/secret.md",
		"upload_url",
		"object_key",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("admin cloud storage response leaked %q in %s", leaked, body)
		}
	}
}

func TestAdminCloudStorageServiceMapsMissingUser(t *testing.T) {
	repo := &fakeAdminCloudStorageReadRepo{projectErr: repository.ErrNotFound}
	svc := NewAdminCloudStorageService(AdminCloudStorageDependencies{
		ReadModel:   repo,
		QuotaPolicy: NewStaticCloudStorageQuotaPolicy(1000),
	})
	_, err := svc.ListUserProjects(context.Background(), AdminCloudStorageProjectQuery{UserID: "usr_missing"})
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected user not found, got %v", err)
	}
}

func TestAdminCloudStorageServiceRejectsInvalidQuery(t *testing.T) {
	_, err := NewAdminCloudStorageService(AdminCloudStorageDependencies{}).Usage(context.Background(), AdminCloudStorageUsageQuery{})
	if !errors.Is(err, ErrInvalidAdminCloudStorageQuery) {
		t.Fatalf("expected invalid usage query, got %v", err)
	}
	_, err = NewAdminCloudStorageService(AdminCloudStorageDependencies{ReadModel: &fakeAdminCloudStorageReadRepo{}}).ListUserProjects(context.Background(), AdminCloudStorageProjectQuery{})
	if !errors.Is(err, ErrInvalidAdminCloudStorageQuery) {
		t.Fatalf("expected invalid project query, got %v", err)
	}
}
