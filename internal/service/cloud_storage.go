package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrCloudStorageAccessDenied          = errors.New("cloud storage access denied")
	ErrCloudStorageOverQuota             = errors.New("cloud storage quota exceeded")
	ErrCloudStorageProviderNotConfigured = errors.New("cloud storage provider not configured")
	ErrInvalidCloudStorage               = errors.New("invalid cloud storage request")
	ErrCloudProjectNotFound              = errors.New("cloud project not found")
)

type CloudStorageQuotaPolicy interface {
	QuotaBytes(ctx context.Context, user *domain.User) int64
}

type StaticCloudStorageQuotaPolicy struct {
	quotaBytes int64
}

func NewStaticCloudStorageQuotaPolicy(quotaBytes int64) CloudStorageQuotaPolicy {
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	return StaticCloudStorageQuotaPolicy{quotaBytes: quotaBytes}
}

func NewCloudStorageQuotaPolicyFromMB(quotaMB int64) CloudStorageQuotaPolicy {
	return NewStaticCloudStorageQuotaPolicy(quotaMB * 1024 * 1024)
}

func (p StaticCloudStorageQuotaPolicy) QuotaBytes(ctx context.Context, user *domain.User) int64 {
	_ = ctx
	_ = user
	return p.quotaBytes
}

type ObjectStorageProvider interface {
	ProviderID() string
	BuildUploadTarget(ctx context.Context, request CloudObjectUploadRequest) (CloudObjectUploadTarget, error)
}

type CloudObjectUploadRequest struct {
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	ContentHash string
}

type CloudObjectUploadTarget struct {
	ObjectKey string            `json:"object_key"`
	UploadURL string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Provider  string            `json:"provider"`
}

type UnconfiguredObjectStorageProvider struct{}

func NewUnconfiguredObjectStorageProvider() UnconfiguredObjectStorageProvider {
	return UnconfiguredObjectStorageProvider{}
}

func (p UnconfiguredObjectStorageProvider) ProviderID() string {
	return "unconfigured"
}

func (p UnconfiguredObjectStorageProvider) BuildUploadTarget(ctx context.Context, request CloudObjectUploadRequest) (CloudObjectUploadTarget, error) {
	_ = ctx
	_ = request
	return CloudObjectUploadTarget{}, ErrCloudStorageProviderNotConfigured
}

type CloudStorageDependencies struct {
	Users             repository.UserRepository
	Grants            repository.EntitlementGrantRepository
	Projects          repository.CloudProjectRepository
	Manifests         repository.CloudManifestRepository
	Objects           repository.CloudObjectRepository
	Policy            CloudStorageQuotaPolicy
	Provider          ObjectStorageProvider
	UnitOfWorkFactory func() repository.UnitOfWork
}

type CloudStorageService interface {
	AuthorizeSync(ctx context.Context, input CloudSyncAuthorizationInput) (*CloudSyncAuthorization, error)
	CommitManifest(ctx context.Context, input CloudManifestCommitInput) (*CloudManifestCommitResult, error)
	Usage(ctx context.Context, userID string) (*CloudStorageUsage, error)
}

type CloudResourceDescriptor struct {
	ResourceID   string `json:"resource_id"`
	ResourceKind string `json:"resource_kind"`
	ContentHash  string `json:"content_hash"`
	SizeBytes    int64  `json:"size_bytes"`
	ContentType  string `json:"content_type"`
	Filename     string `json:"filename"`
}

type CloudSyncAuthorizationInput struct {
	UserID          string                    `json:"user_id"`
	ClientProjectID string                    `json:"client_project_id"`
	ProjectName     string                    `json:"project_name"`
	Resources       []CloudResourceDescriptor `json:"resources"`
}

type CloudSyncAuthorization struct {
	ID              string                    `json:"id"`
	UserID          string                    `json:"user_id"`
	CloudProjectID  string                    `json:"cloud_project_id"`
	ClientProjectID string                    `json:"client_project_id"`
	Provider        string                    `json:"provider"`
	QuotaBytes      int64                     `json:"quota_bytes"`
	UsedBytes       int64                     `json:"used_bytes"`
	RequestedBytes  int64                     `json:"requested_bytes"`
	UploadTargets   []CloudObjectUploadTarget `json:"upload_targets"`
	ExpiresAt       time.Time                 `json:"expires_at"`
}

type CloudManifestCommitInput struct {
	UserID          string                    `json:"user_id"`
	ClientProjectID string                    `json:"client_project_id"`
	ProjectName     string                    `json:"project_name"`
	ManifestHash    string                    `json:"manifest_hash"`
	ManifestVersion int                       `json:"manifest_version"`
	Resources       []CloudResourceDescriptor `json:"resources"`
	IdempotencyKey  string                    `json:"idempotency_key"`
}

type CloudManifestCommitResult struct {
	Project  *domain.CloudProject  `json:"project"`
	Manifest *domain.CloudManifest `json:"manifest"`
	Usage    CloudStorageUsage     `json:"usage"`
}

type CloudStorageUsage struct {
	UserID         string `json:"user_id"`
	UsedBytes      int64  `json:"used_bytes"`
	QuotaBytes     int64  `json:"quota_bytes"`
	RemainingBytes int64  `json:"remaining_bytes"`
	OverQuota      bool   `json:"over_quota"`
}

type cloudStorageRepositories struct {
	projects  repository.CloudProjectRepository
	manifests repository.CloudManifestRepository
	objects   repository.CloudObjectRepository
}

type cloudStorageService struct {
	users             repository.UserRepository
	grants            repository.EntitlementGrantRepository
	projects          repository.CloudProjectRepository
	manifests         repository.CloudManifestRepository
	objects           repository.CloudObjectRepository
	policy            CloudStorageQuotaPolicy
	provider          ObjectStorageProvider
	unitOfWorkFactory func() repository.UnitOfWork
}

func NewCloudStorageService(deps CloudStorageDependencies) CloudStorageService {
	policy := deps.Policy
	if policy == nil {
		policy = NewStaticCloudStorageQuotaPolicy(0)
	}
	provider := deps.Provider
	if provider == nil {
		provider = NewUnconfiguredObjectStorageProvider()
	}
	return &cloudStorageService{
		users:             deps.Users,
		grants:            deps.Grants,
		projects:          deps.Projects,
		manifests:         deps.Manifests,
		objects:           deps.Objects,
		policy:            policy,
		provider:          provider,
		unitOfWorkFactory: deps.UnitOfWorkFactory,
	}
}

func (s *cloudStorageService) AuthorizeSync(ctx context.Context, input CloudSyncAuthorizationInput) (*CloudSyncAuthorization, error) {
	if err := s.requireConfiguredProvider(); err != nil {
		return nil, err
	}
	resources, err := normalizeCloudResources(input.Resources)
	if err != nil {
		return nil, err
	}
	repos := cloudStorageRepositories{projects: s.projects, manifests: s.manifests, objects: s.objects}
	if err := validateCloudRepos(repos); err != nil {
		return nil, err
	}
	user, quotaBytes, usedBytes, err := s.authorizeCloudAccess(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	project, err := s.ensureProject(ctx, repos, user.ID, input.ClientProjectID, input.ProjectName)
	if err != nil {
		return nil, err
	}
	projectedUsed, err := projectedUsageForResources(ctx, repos.objects, project.ID, usedBytes, resources)
	if err != nil {
		return nil, err
	}
	requestedBytes := sumResourceBytes(resources)
	if projectedUsed > quotaBytes {
		return nil, ErrCloudStorageOverQuota
	}
	targets := make([]CloudObjectUploadTarget, 0, len(resources))
	for _, resource := range resources {
		objectKey := objectKeyFor(user.ID, input.ClientProjectID, resource)
		target, err := s.provider.BuildUploadTarget(ctx, CloudObjectUploadRequest{
			ObjectKey:   objectKey,
			ContentType: resource.ContentType,
			SizeBytes:   resource.SizeBytes,
			ContentHash: resource.ContentHash,
		})
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ObjectKey < targets[j].ObjectKey })
	sessionID, err := generateEntityID("csy_")
	if err != nil {
		return nil, err
	}
	return &CloudSyncAuthorization{
		ID:              sessionID,
		UserID:          user.ID,
		CloudProjectID:  project.ID,
		ClientProjectID: strings.TrimSpace(input.ClientProjectID),
		Provider:        s.provider.ProviderID(),
		QuotaBytes:      quotaBytes,
		UsedBytes:       usedBytes,
		RequestedBytes:  requestedBytes,
		UploadTargets:   targets,
		ExpiresAt:       time.Now().UTC().Add(15 * time.Minute),
	}, nil
}

func (s *cloudStorageService) CommitManifest(ctx context.Context, input CloudManifestCommitInput) (*CloudManifestCommitResult, error) {
	if err := s.requireConfiguredProvider(); err != nil {
		return nil, err
	}
	repos := cloudStorageRepositories{projects: s.projects, manifests: s.manifests, objects: s.objects}
	if err := validateCloudRepos(repos); err != nil {
		return nil, err
	}
	resources, err := normalizeCloudResources(input.Resources)
	if err != nil {
		return nil, err
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" || strings.TrimSpace(input.ManifestHash) == "" {
		return nil, ErrInvalidCloudStorage
	}

	user, quotaBytes, usedBytes, err := s.authorizeCloudAccess(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if existing, err := repos.manifests.GetByIdempotencyKey(ctx, idempotencyKey); err == nil {
		if existing.UserID != user.ID {
			return nil, ErrInvalidCloudStorage
		}
		project, projectErr := repos.projects.GetByID(ctx, existing.CloudProjectID)
		if projectErr != nil {
			return nil, projectErr
		}
		usage, usageErr := s.Usage(ctx, user.ID)
		if usageErr != nil {
			return nil, usageErr
		}
		return &CloudManifestCommitResult{Project: project, Manifest: existing, Usage: *usage}, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	return s.commitWithTransaction(ctx, user, quotaBytes, usedBytes, input, resources)
}

func (s *cloudStorageService) Usage(ctx context.Context, userID string) (*CloudStorageUsage, error) {
	if s.objects == nil || s.grants == nil {
		return nil, ErrInvalidCloudStorage
	}
	user, err := s.loadActiveUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	usedBytes, err := s.objects.SumActiveBytesByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	hasGrant, err := s.hasCloudStorageGrant(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	quotaBytes := int64(0)
	if hasGrant {
		quotaBytes = s.policy.QuotaBytes(ctx, user)
		if quotaBytes < 0 {
			quotaBytes = 0
		}
	}
	return usageFor(user.ID, usedBytes, quotaBytes), nil
}

func (s *cloudStorageService) commitWithTransaction(ctx context.Context, user *domain.User, quotaBytes int64, usedBytes int64, input CloudManifestCommitInput, resources []CloudResourceDescriptor) (*CloudManifestCommitResult, error) {
	if s.unitOfWorkFactory == nil {
		return s.commitWithRepos(ctx, user, quotaBytes, usedBytes, input, resources, cloudStorageRepositories{projects: s.projects, manifests: s.manifests, objects: s.objects})
	}
	uow := s.unitOfWorkFactory()
	if err := uow.Begin(ctx); err != nil {
		return nil, err
	}
	defer uow.Rollback()
	repos := uow.Repos()
	result, err := s.commitWithRepos(ctx, user, quotaBytes, usedBytes, input, resources, cloudStorageRepositories{
		projects:  repos.CloudProjectRepo,
		manifests: repos.CloudManifestRepo,
		objects:   repos.CloudObjectRepo,
	})
	if err != nil {
		return nil, err
	}
	if err := uow.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *cloudStorageService) commitWithRepos(ctx context.Context, user *domain.User, quotaBytes int64, usedBytes int64, input CloudManifestCommitInput, resources []CloudResourceDescriptor, repos cloudStorageRepositories) (*CloudManifestCommitResult, error) {
	if err := validateCloudRepos(repos); err != nil {
		return nil, err
	}
	project, err := s.ensureProject(ctx, repos, user.ID, input.ClientProjectID, input.ProjectName)
	if err != nil {
		return nil, err
	}
	existingObjects, err := repos.objects.ListByProject(ctx, project.ID, domain.CloudObjectStatusActive)
	if err != nil {
		return nil, err
	}
	existingProjectBytes := sumObjectBytes(existingObjects)
	newProjectBytes := sumResourceBytes(resources)
	projectedUsed := usedBytes - existingProjectBytes + newProjectBytes
	if projectedUsed < newProjectBytes {
		projectedUsed = newProjectBytes
	}
	if projectedUsed > quotaBytes {
		return nil, ErrCloudStorageOverQuota
	}

	now := time.Now().UTC()
	manifestID, err := generateEntityID("cmf_")
	if err != nil {
		return nil, err
	}
	manifestVersion := input.ManifestVersion
	if manifestVersion <= 0 {
		manifestVersion = 1
	}
	manifest := &domain.CloudManifest{
		ID:              manifestID,
		UserID:          user.ID,
		CloudProjectID:  project.ID,
		ClientProjectID: strings.TrimSpace(input.ClientProjectID),
		ManifestHash:    strings.TrimSpace(input.ManifestHash),
		ManifestVersion: manifestVersion,
		ObjectCount:     len(resources),
		TotalBytes:      newProjectBytes,
		Status:          domain.CloudManifestStatusCommitted,
		IdempotencyKey:  strings.TrimSpace(input.IdempotencyKey),
		CreatedAt:       now,
		CommittedAt:     &now,
	}
	if err := repos.manifests.Create(ctx, manifest); err != nil {
		return nil, err
	}

	keep := map[string]struct{}{}
	for _, resource := range resources {
		objectKey := objectKeyFor(user.ID, input.ClientProjectID, resource)
		keep[objectKey] = struct{}{}
		objectID, err := generateEntityID("cob_")
		if err != nil {
			return nil, err
		}
		object := &domain.CloudObject{
			ID:              objectID,
			UserID:          user.ID,
			CloudProjectID:  project.ID,
			ClientProjectID: strings.TrimSpace(input.ClientProjectID),
			ManifestID:      manifest.ID,
			ResourceID:      resource.ResourceID,
			ResourceKind:    resource.ResourceKind,
			ObjectKey:       objectKey,
			ContentHash:     resource.ContentHash,
			SizeBytes:       resource.SizeBytes,
			ContentType:     resource.ContentType,
			Status:          domain.CloudObjectStatusActive,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := repos.objects.Upsert(ctx, object); err != nil {
			return nil, err
		}
	}
	for i := range existingObjects {
		if _, ok := keep[existingObjects[i].ObjectKey]; ok {
			continue
		}
		existingObjects[i].Status = domain.CloudObjectStatusReplaced
		existingObjects[i].UpdatedAt = now
		if err := repos.objects.Update(ctx, &existingObjects[i]); err != nil {
			return nil, err
		}
	}

	project.LastManifestID = manifest.ID
	project.UpdatedAt = now
	if err := repos.projects.Update(ctx, project); err != nil {
		return nil, err
	}
	return &CloudManifestCommitResult{
		Project:  project,
		Manifest: manifest,
		Usage:    *usageFor(user.ID, projectedUsed, quotaBytes),
	}, nil
}

func (s *cloudStorageService) requireConfiguredProvider() error {
	if s.provider == nil || strings.TrimSpace(s.provider.ProviderID()) == "" || s.provider.ProviderID() == "unconfigured" {
		return ErrCloudStorageProviderNotConfigured
	}
	return nil
}

func (s *cloudStorageService) authorizeCloudAccess(ctx context.Context, userID string) (*domain.User, int64, int64, error) {
	if s.objects == nil {
		return nil, 0, 0, ErrInvalidCloudStorage
	}
	user, err := s.loadActiveUser(ctx, userID)
	if err != nil {
		return nil, 0, 0, err
	}
	hasGrant, err := s.hasCloudStorageGrant(ctx, user.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	if !hasGrant {
		return nil, 0, 0, ErrCloudStorageAccessDenied
	}
	quotaBytes := s.policy.QuotaBytes(ctx, user)
	if quotaBytes <= 0 {
		return nil, 0, 0, ErrCloudStorageAccessDenied
	}
	usedBytes, err := s.objects.SumActiveBytesByUser(ctx, user.ID)
	if err != nil {
		return nil, 0, 0, err
	}
	return user, quotaBytes, usedBytes, nil
}

func (s *cloudStorageService) loadActiveUser(ctx context.Context, userID string) (*domain.User, error) {
	if s.users == nil {
		return nil, ErrInvalidCloudStorage
	}
	cleanUserID := strings.TrimSpace(userID)
	if cleanUserID == "" {
		return nil, ErrInvalidCloudStorage
	}
	user, err := s.users.GetByID(ctx, cleanUserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if user.Status != "" && user.Status != domain.UserStatusActive {
		return nil, ErrCloudStorageAccessDenied
	}
	return user, nil
}

func (s *cloudStorageService) hasCloudStorageGrant(ctx context.Context, userID string) (bool, error) {
	if s.grants == nil {
		return false, ErrInvalidCloudStorage
	}
	grants, err := s.grants.List(ctx, repository.EntitlementGrantQuery{
		UserID:        userID,
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
		Limit:         1,
	})
	if err != nil {
		return false, err
	}
	return len(grants) > 0, nil
}

func projectedUsageForResources(ctx context.Context, objects repository.CloudObjectRepository, cloudProjectID string, currentUsedBytes int64, resources []CloudResourceDescriptor) (int64, error) {
	if objects == nil {
		return 0, ErrInvalidCloudStorage
	}
	existingObjects, err := objects.ListByProject(ctx, cloudProjectID, domain.CloudObjectStatusActive)
	if err != nil {
		return 0, err
	}
	newProjectBytes := sumResourceBytes(resources)
	projectedUsed := currentUsedBytes - sumObjectBytes(existingObjects) + newProjectBytes
	if projectedUsed < newProjectBytes {
		projectedUsed = newProjectBytes
	}
	return projectedUsed, nil
}

func validateCloudRepos(repos cloudStorageRepositories) error {
	if repos.projects == nil || repos.manifests == nil || repos.objects == nil {
		return ErrInvalidCloudStorage
	}
	return nil
}

func (s *cloudStorageService) ensureProject(ctx context.Context, repos cloudStorageRepositories, userID string, clientProjectID string, projectName string) (*domain.CloudProject, error) {
	cleanClientProjectID := strings.TrimSpace(clientProjectID)
	if cleanClientProjectID == "" {
		return nil, ErrInvalidCloudStorage
	}
	project, err := repos.projects.GetByUserAndClientProject(ctx, userID, cleanClientProjectID)
	if err == nil {
		if strings.TrimSpace(projectName) != "" && project.Name != strings.TrimSpace(projectName) {
			project.Name = strings.TrimSpace(projectName)
			project.UpdatedAt = time.Now().UTC()
			if err := repos.projects.Update(ctx, project); err != nil {
				return nil, err
			}
		}
		return project, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	projectID, err := generateEntityID("cpr_")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	project = &domain.CloudProject{
		ID:              projectID,
		UserID:          userID,
		ClientProjectID: cleanClientProjectID,
		Name:            strings.TrimSpace(projectName),
		Status:          domain.CloudProjectStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := repos.projects.Create(ctx, project); err != nil {
		return nil, err
	}
	return project, nil
}

func normalizeCloudResources(resources []CloudResourceDescriptor) ([]CloudResourceDescriptor, error) {
	if len(resources) == 0 {
		return nil, ErrInvalidCloudStorage
	}
	seen := map[string]CloudResourceDescriptor{}
	for _, resource := range resources {
		resource.ResourceID = strings.TrimSpace(resource.ResourceID)
		resource.ResourceKind = sanitizePathSegment(defaultString(resource.ResourceKind, "resource"))
		resource.ContentHash = strings.TrimSpace(resource.ContentHash)
		resource.ContentType = defaultString(strings.TrimSpace(resource.ContentType), "application/octet-stream")
		resource.Filename = sanitizeFilename(defaultString(resource.Filename, path.Base(resource.ResourceID)))
		if resource.ResourceID == "" || resource.ContentHash == "" || resource.SizeBytes < 0 {
			return nil, ErrInvalidCloudStorage
		}
		seen[resource.ResourceKind+"\x00"+resource.ResourceID] = resource
	}
	result := make([]CloudResourceDescriptor, 0, len(seen))
	for _, resource := range seen {
		result = append(result, resource)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ResourceKind == result[j].ResourceKind {
			return result[i].ResourceID < result[j].ResourceID
		}
		return result[i].ResourceKind < result[j].ResourceKind
	})
	return result, nil
}

func objectKeyFor(userID string, clientProjectID string, resource CloudResourceDescriptor) string {
	return fmt.Sprintf(
		"accounts/%s/projects/%s/%s/%s/%s",
		sanitizePathSegment(userID),
		sanitizePathSegment(clientProjectID),
		sanitizePathSegment(resource.ResourceKind),
		sanitizePathSegment(resource.ContentHash),
		sanitizeFilename(resource.Filename),
	)
}

var unsafePathSegmentRE = regexp.MustCompile(`[^a-zA-Z0-9_.:-]+`)

func sanitizePathSegment(value string) string {
	cleaned := unsafePathSegmentRE.ReplaceAllString(strings.TrimSpace(value), "-")
	cleaned = strings.Trim(cleaned, "-._")
	if cleaned == "" {
		return "resource"
	}
	if len(cleaned) > 128 {
		return cleaned[:128]
	}
	return cleaned
}

func sanitizeFilename(value string) string {
	cleaned := sanitizePathSegment(path.Base(strings.TrimSpace(value)))
	if cleaned == "resource" {
		return "object.bin"
	}
	return cleaned
}

func sumResourceBytes(resources []CloudResourceDescriptor) int64 {
	var total int64
	for _, resource := range resources {
		total += resource.SizeBytes
	}
	return total
}

func sumObjectBytes(objects []domain.CloudObject) int64 {
	var total int64
	for _, object := range objects {
		total += object.SizeBytes
	}
	return total
}

func usageFor(userID string, usedBytes int64, quotaBytes int64) *CloudStorageUsage {
	remaining := quotaBytes - usedBytes
	if remaining < 0 {
		remaining = 0
	}
	return &CloudStorageUsage{
		UserID:         userID,
		UsedBytes:      usedBytes,
		QuotaBytes:     quotaBytes,
		RemainingBytes: remaining,
		OverQuota:      usedBytes > quotaBytes,
	}
}
