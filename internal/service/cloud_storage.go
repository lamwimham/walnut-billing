package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	ErrCloudSyncSessionNotFound          = errors.New("cloud sync session not found")
	ErrCloudSyncSessionExpired           = errors.New("cloud sync session expired")
	ErrCloudSyncSessionAlreadyCommitted  = errors.New("cloud sync session already committed")
)

const (
	CloudStoragePlanNone     = "none"
	CloudStoragePlanTrial    = "trial"
	CloudStoragePlanMonthly  = "monthly"
	CloudStoragePlanLifetime = "lifetime"
	CloudStoragePlanCustom   = "custom"
)

type CloudStorageQuotaPolicy interface {
	Decide(ctx context.Context, input CloudStorageQuotaInput) CloudStorageQuotaDecision
}

type CloudStorageQuotaInput struct {
	User   *domain.User
	Grants []domain.EntitlementGrant
	Now    time.Time
}

type CloudStorageQuotaDecision struct {
	Plan           string `json:"plan"`
	HasEntitlement bool   `json:"has_entitlement"`
	QuotaBytes     int64  `json:"quota_bytes"`
}

type CloudStorageQuotaPolicyConfig struct {
	DefaultQuotaBytes  int64
	TrialQuotaBytes    int64
	MonthlyQuotaBytes  int64
	LifetimeQuotaBytes int64
}

type PlanAwareCloudStorageQuotaPolicy struct {
	config CloudStorageQuotaPolicyConfig
}

func NewStaticCloudStorageQuotaPolicy(quotaBytes int64) CloudStorageQuotaPolicy {
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	return NewPlanAwareCloudStorageQuotaPolicy(CloudStorageQuotaPolicyConfig{
		DefaultQuotaBytes:  quotaBytes,
		TrialQuotaBytes:    quotaBytes,
		MonthlyQuotaBytes:  quotaBytes,
		LifetimeQuotaBytes: quotaBytes,
	})
}

func NewCloudStorageQuotaPolicyFromMB(quotaMB int64) CloudStorageQuotaPolicy {
	return NewStaticCloudStorageQuotaPolicy(quotaMB * 1024 * 1024)
}

func NewPlanAwareCloudStorageQuotaPolicy(config CloudStorageQuotaPolicyConfig) CloudStorageQuotaPolicy {
	config = normalizeCloudStorageQuotaPolicyConfig(config)
	return PlanAwareCloudStorageQuotaPolicy{config: config}
}

func NewPlanAwareCloudStorageQuotaPolicyFromMB(defaultQuotaMB int64, trialQuotaMB int64, monthlyQuotaMB int64, lifetimeQuotaMB int64) CloudStorageQuotaPolicy {
	return NewPlanAwareCloudStorageQuotaPolicy(CloudStorageQuotaPolicyConfig{
		DefaultQuotaBytes:  mbToBytes(defaultQuotaMB),
		TrialQuotaBytes:    mbToBytes(trialQuotaMB),
		MonthlyQuotaBytes:  mbToBytes(monthlyQuotaMB),
		LifetimeQuotaBytes: mbToBytes(lifetimeQuotaMB),
	})
}

func (p PlanAwareCloudStorageQuotaPolicy) Decide(ctx context.Context, input CloudStorageQuotaInput) CloudStorageQuotaDecision {
	_ = ctx
	plan := cloudStoragePlanForGrants(input.Grants, input.Now)
	decision := CloudStorageQuotaDecision{Plan: plan, HasEntitlement: plan != CloudStoragePlanNone}
	switch plan {
	case CloudStoragePlanTrial:
		decision.QuotaBytes = p.config.TrialQuotaBytes
	case CloudStoragePlanMonthly:
		decision.QuotaBytes = p.config.MonthlyQuotaBytes
	case CloudStoragePlanLifetime:
		decision.QuotaBytes = p.config.LifetimeQuotaBytes
	case CloudStoragePlanCustom:
		decision.QuotaBytes = p.config.DefaultQuotaBytes
	default:
		decision.QuotaBytes = 0
	}
	if decision.QuotaBytes < 0 {
		decision.QuotaBytes = 0
	}
	return decision
}

type ObjectStorageProvider interface {
	ProviderID() string
	BuildUploadTarget(ctx context.Context, request CloudObjectUploadRequest) (CloudObjectUploadTarget, error)
	BuildDownloadTarget(ctx context.Context, request CloudObjectDownloadRequest) (CloudObjectDownloadTarget, error)
	DeleteObject(ctx context.Context, request CloudObjectDeleteRequest) error
	HeadObject(ctx context.Context, request CloudObjectHeadRequest) (CloudObjectHeadResult, error)
}

type CloudObjectLifecycleTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type CloudObjectUploadRequest struct {
	ObjectKey     string
	ContentType   string
	SizeBytes     int64
	ContentHash   string
	LifecycleTags []CloudObjectLifecycleTag
}

type CloudObjectUploadTarget struct {
	ObjectKey string            `json:"object_key"`
	UploadURL string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	Provider  string            `json:"provider"`
}

type CloudObjectDownloadRequest struct {
	ObjectKey string
}

type CloudObjectDownloadTarget struct {
	ObjectKey   string            `json:"object_key"`
	DownloadURL string            `json:"download_url"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
	Provider    string            `json:"provider"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

type CloudObjectDeleteRequest struct {
	ObjectKey string
}

type CloudObjectHeadRequest struct {
	ObjectKey string
}

type CloudObjectHeadResult struct {
	ObjectKey   string `json:"object_key"`
	Exists      bool   `json:"exists"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentHash string `json:"content_hash"`
	ContentType string `json:"content_type"`
	Provider    string `json:"provider"`
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

func (p UnconfiguredObjectStorageProvider) BuildDownloadTarget(ctx context.Context, request CloudObjectDownloadRequest) (CloudObjectDownloadTarget, error) {
	_ = ctx
	_ = request
	return CloudObjectDownloadTarget{}, ErrCloudStorageProviderNotConfigured
}

func (p UnconfiguredObjectStorageProvider) DeleteObject(ctx context.Context, request CloudObjectDeleteRequest) error {
	_ = ctx
	_ = request
	return ErrCloudStorageProviderNotConfigured
}

func (p UnconfiguredObjectStorageProvider) HeadObject(ctx context.Context, request CloudObjectHeadRequest) (CloudObjectHeadResult, error) {
	_ = ctx
	_ = request
	return CloudObjectHeadResult{}, ErrCloudStorageProviderNotConfigured
}

type CloudStorageDependencies struct {
	Users             repository.UserRepository
	Grants            repository.EntitlementGrantRepository
	Projects          repository.CloudProjectRepository
	SyncSessions      repository.CloudSyncSessionRepository
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
	ListProjects(ctx context.Context, query CloudStorageProjectQuery) (*CloudStorageProjectList, error)
	LatestManifest(ctx context.Context, query CloudStorageLatestManifestQuery) (*CloudStorageLatestManifest, error)
	BuildDownloadTarget(ctx context.Context, input CloudDownloadTargetInput) (*CloudDownloadTargetAuthorization, error)
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
	SyncSessionID   string                    `json:"sync_session_id"`
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
	Plan           string `json:"plan"`
	UsedBytes      int64  `json:"used_bytes"`
	QuotaBytes     int64  `json:"quota_bytes"`
	RemainingBytes int64  `json:"remaining_bytes"`
	OverQuota      bool   `json:"over_quota"`
}

type CloudStorageProjectQuery struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type CloudStorageProjectList struct {
	UserID   string                       `json:"user_id"`
	Projects []CloudStorageProjectSummary `json:"projects"`
}

type CloudStorageProjectSummary struct {
	ID              string                `json:"id"`
	ClientProjectID string                `json:"client_project_id"`
	Name            string                `json:"name"`
	Status          string                `json:"status"`
	LastManifestID  string                `json:"last_manifest_id,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at"`
	LatestManifest  *CloudManifestSummary `json:"latest_manifest,omitempty"`
}

type CloudStorageLatestManifestQuery struct {
	UserID          string `json:"user_id"`
	CloudProjectID  string `json:"cloud_project_id"`
	ClientProjectID string `json:"client_project_id"`
}

type CloudStorageLatestManifest struct {
	Project  CloudStorageProjectSummary `json:"project"`
	Manifest *CloudManifestSummary      `json:"manifest,omitempty"`
	Objects  []CloudObjectSummary       `json:"objects"`
}

type CloudDownloadTargetInput struct {
	UserID          string `json:"user_id"`
	CloudProjectID  string `json:"cloud_project_id"`
	ClientProjectID string `json:"client_project_id"`
	ObjectKey       string `json:"object_key"`
}

type CloudDownloadTargetAuthorization struct {
	UserID          string                    `json:"user_id"`
	CloudProjectID  string                    `json:"cloud_project_id"`
	ClientProjectID string                    `json:"client_project_id"`
	Object          CloudObjectSummary        `json:"object"`
	DownloadTarget  CloudObjectDownloadTarget `json:"download_target"`
}

type CloudManifestSummary struct {
	ID              string     `json:"id"`
	ManifestHash    string     `json:"manifest_hash"`
	ManifestVersion int        `json:"manifest_version"`
	ObjectCount     int        `json:"object_count"`
	TotalBytes      int64      `json:"total_bytes"`
	Status          string     `json:"status"`
	CommittedAt     *time.Time `json:"committed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type CloudObjectSummary struct {
	ResourceID   string `json:"resource_id"`
	ResourceKind string `json:"resource_kind"`
	ObjectKey    string `json:"object_key"`
	ContentHash  string `json:"content_hash"`
	SizeBytes    int64  `json:"size_bytes"`
	ContentType  string `json:"content_type"`
	Status       string `json:"status"`
}

type cloudStorageRepositories struct {
	projects     repository.CloudProjectRepository
	syncSessions repository.CloudSyncSessionRepository
	manifests    repository.CloudManifestRepository
	objects      repository.CloudObjectRepository
}

type cloudStorageService struct {
	users             repository.UserRepository
	grants            repository.EntitlementGrantRepository
	projects          repository.CloudProjectRepository
	syncSessions      repository.CloudSyncSessionRepository
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
		syncSessions:      deps.SyncSessions,
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
	repos := cloudStorageRepositories{projects: s.projects, syncSessions: s.syncSessions, manifests: s.manifests, objects: s.objects}
	if err := validateCloudRepos(repos); err != nil {
		return nil, err
	}
	user, quota, usedBytes, err := s.authorizeCloudAccess(ctx, input.UserID)
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
	if projectedUsed > quota.QuotaBytes {
		return nil, ErrCloudStorageOverQuota
	}
	targets := make([]CloudObjectUploadTarget, 0, len(resources))
	for _, resource := range resources {
		objectKey := objectKeyFor(user.ID, input.ClientProjectID, resource)
		target, err := s.provider.BuildUploadTarget(ctx, CloudObjectUploadRequest{
			ObjectKey:     objectKey,
			ContentType:   resource.ContentType,
			SizeBytes:     resource.SizeBytes,
			ContentHash:   resource.ContentHash,
			LifecycleTags: cloudObjectLifecycleTags(user.ID, project.ID, input.ClientProjectID, resource),
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
	now := time.Now().UTC()
	expiresAt := now.Add(15 * time.Minute)
	session := &domain.CloudSyncSession{
		ID:                  sessionID,
		UserID:              user.ID,
		CloudProjectID:      project.ID,
		ClientProjectID:     strings.TrimSpace(input.ClientProjectID),
		Provider:            s.provider.ProviderID(),
		ResourceFingerprint: cloudResourceFingerprint(resources),
		RequestedBytes:      requestedBytes,
		UsedBytes:           usedBytes,
		QuotaBytes:          quota.QuotaBytes,
		Status:              domain.CloudSyncSessionStatusAuthorized,
		ExpiresAt:           expiresAt,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := repos.syncSessions.Create(ctx, session); err != nil {
		return nil, err
	}
	return &CloudSyncAuthorization{
		ID:              sessionID,
		UserID:          user.ID,
		CloudProjectID:  project.ID,
		ClientProjectID: strings.TrimSpace(input.ClientProjectID),
		Provider:        s.provider.ProviderID(),
		QuotaBytes:      quota.QuotaBytes,
		UsedBytes:       usedBytes,
		RequestedBytes:  requestedBytes,
		UploadTargets:   targets,
		ExpiresAt:       expiresAt,
	}, nil
}

func (s *cloudStorageService) CommitManifest(ctx context.Context, input CloudManifestCommitInput) (*CloudManifestCommitResult, error) {
	if err := s.requireConfiguredProvider(); err != nil {
		return nil, err
	}
	repos := cloudStorageRepositories{projects: s.projects, syncSessions: s.syncSessions, manifests: s.manifests, objects: s.objects}
	if err := validateCloudRepos(repos); err != nil {
		return nil, err
	}
	resources, err := normalizeCloudResources(input.Resources)
	if err != nil {
		return nil, err
	}
	idempotencyKey := strings.TrimSpace(input.IdempotencyKey)
	if idempotencyKey == "" || strings.TrimSpace(input.ManifestHash) == "" || strings.TrimSpace(input.SyncSessionID) == "" {
		return nil, ErrInvalidCloudStorage
	}

	user, quota, usedBytes, err := s.authorizeCloudAccess(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if existing, err := repos.manifests.GetByIdempotencyKey(ctx, idempotencyKey); err == nil {
		if existing.UserID != user.ID || (strings.TrimSpace(existing.SyncSessionID) != "" && existing.SyncSessionID != strings.TrimSpace(input.SyncSessionID)) {
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

	return s.commitWithTransaction(ctx, user, quota, usedBytes, input, resources)
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
	quota, err := s.cloudStorageQuotaDecision(ctx, user)
	if err != nil {
		return nil, err
	}
	return usageFor(user.ID, quota.Plan, usedBytes, quota.QuotaBytes), nil
}

func (s *cloudStorageService) ListProjects(ctx context.Context, query CloudStorageProjectQuery) (*CloudStorageProjectList, error) {
	if s.projects == nil || s.manifests == nil {
		return nil, ErrInvalidCloudStorage
	}
	user, err := s.loadActiveUser(ctx, query.UserID)
	if err != nil {
		return nil, err
	}
	if quota, err := s.cloudStorageQuotaDecision(ctx, user); err != nil {
		return nil, err
	} else if !quota.HasEntitlement {
		return nil, ErrCloudStorageAccessDenied
	}
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	projects, err := s.projects.ListByUser(ctx, user.ID, strings.TrimSpace(query.Status), limit, offset)
	if err != nil {
		return nil, err
	}
	result := &CloudStorageProjectList{UserID: user.ID, Projects: make([]CloudStorageProjectSummary, 0, len(projects))}
	for _, project := range projects {
		summary := cloudStorageProjectSummary(project, nil)
		if strings.TrimSpace(project.LastManifestID) != "" {
			if manifest, err := s.manifests.GetByID(ctx, project.LastManifestID); err == nil {
				summary.LatestManifest = cloudManifestSummary(manifest)
			} else if !errors.Is(err, repository.ErrNotFound) {
				return nil, err
			}
		}
		result.Projects = append(result.Projects, summary)
	}
	return result, nil
}

func (s *cloudStorageService) LatestManifest(ctx context.Context, query CloudStorageLatestManifestQuery) (*CloudStorageLatestManifest, error) {
	if s.projects == nil || s.manifests == nil || s.objects == nil {
		return nil, ErrInvalidCloudStorage
	}
	user, err := s.loadActiveUser(ctx, query.UserID)
	if err != nil {
		return nil, err
	}
	if quota, err := s.cloudStorageQuotaDecision(ctx, user); err != nil {
		return nil, err
	} else if !quota.HasEntitlement {
		return nil, ErrCloudStorageAccessDenied
	}
	project, err := s.projectForLatestManifest(ctx, user.ID, query)
	if err != nil {
		return nil, err
	}
	var manifest *domain.CloudManifest
	if strings.TrimSpace(project.LastManifestID) != "" {
		manifest, err = s.manifests.GetByID(ctx, project.LastManifestID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return &CloudStorageLatestManifest{Project: cloudStorageProjectSummary(*project, nil), Objects: []CloudObjectSummary{}}, nil
			}
			return nil, err
		}
	} else {
		manifests, err := s.manifests.ListByProject(ctx, project.ID, 1, 0)
		if err != nil {
			return nil, err
		}
		if len(manifests) > 0 {
			manifest = &manifests[0]
		}
	}
	objects, err := s.objects.ListByProject(ctx, project.ID, domain.CloudObjectStatusActive)
	if err != nil {
		return nil, err
	}
	return &CloudStorageLatestManifest{
		Project:  cloudStorageProjectSummary(*project, manifest),
		Manifest: cloudManifestSummary(manifest),
		Objects:  cloudObjectSummaries(objects),
	}, nil
}

func (s *cloudStorageService) BuildDownloadTarget(ctx context.Context, input CloudDownloadTargetInput) (*CloudDownloadTargetAuthorization, error) {
	if err := s.requireConfiguredProvider(); err != nil {
		return nil, err
	}
	if s.projects == nil || s.objects == nil {
		return nil, ErrInvalidCloudStorage
	}
	user, err := s.loadActiveUser(ctx, input.UserID)
	if err != nil {
		return nil, err
	}
	if quota, err := s.cloudStorageQuotaDecision(ctx, user); err != nil {
		return nil, err
	} else if !quota.HasEntitlement {
		return nil, ErrCloudStorageAccessDenied
	}
	objectKey := strings.TrimSpace(input.ObjectKey)
	if objectKey == "" {
		return nil, ErrInvalidCloudStorage
	}
	object, err := s.objects.GetByObjectKey(ctx, objectKey)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrCloudProjectNotFound
		}
		return nil, err
	}
	if object.UserID != user.ID || object.Status != domain.CloudObjectStatusActive {
		return nil, ErrCloudProjectNotFound
	}
	project, err := s.projectForLatestManifest(ctx, user.ID, CloudStorageLatestManifestQuery{
		CloudProjectID:  firstNonEmpty(input.CloudProjectID, object.CloudProjectID),
		ClientProjectID: firstNonEmpty(input.ClientProjectID, object.ClientProjectID),
	})
	if err != nil {
		return nil, err
	}
	if object.CloudProjectID != project.ID {
		return nil, ErrCloudProjectNotFound
	}
	target, err := s.provider.BuildDownloadTarget(ctx, CloudObjectDownloadRequest{ObjectKey: object.ObjectKey})
	if err != nil {
		return nil, err
	}
	return &CloudDownloadTargetAuthorization{
		UserID:          user.ID,
		CloudProjectID:  project.ID,
		ClientProjectID: project.ClientProjectID,
		Object:          cloudObjectSummary(*object),
		DownloadTarget:  target,
	}, nil
}

func (s *cloudStorageService) commitWithTransaction(ctx context.Context, user *domain.User, quota CloudStorageQuotaDecision, usedBytes int64, input CloudManifestCommitInput, resources []CloudResourceDescriptor) (*CloudManifestCommitResult, error) {
	if s.unitOfWorkFactory == nil {
		return s.commitWithRepos(ctx, user, quota, usedBytes, input, resources, cloudStorageRepositories{projects: s.projects, syncSessions: s.syncSessions, manifests: s.manifests, objects: s.objects})
	}
	uow := s.unitOfWorkFactory()
	if err := uow.Begin(ctx); err != nil {
		return nil, err
	}
	defer uow.Rollback()
	repos := uow.Repos()
	result, err := s.commitWithRepos(ctx, user, quota, usedBytes, input, resources, cloudStorageRepositories{
		projects:     repos.CloudProjectRepo,
		syncSessions: repos.CloudSyncSessionRepo,
		manifests:    repos.CloudManifestRepo,
		objects:      repos.CloudObjectRepo,
	})
	if err != nil {
		return nil, err
	}
	if err := uow.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *cloudStorageService) commitWithRepos(ctx context.Context, user *domain.User, quota CloudStorageQuotaDecision, usedBytes int64, input CloudManifestCommitInput, resources []CloudResourceDescriptor, repos cloudStorageRepositories) (*CloudManifestCommitResult, error) {
	if err := validateCloudRepos(repos); err != nil {
		return nil, err
	}
	session, project, err := s.authorizedCommitSession(ctx, repos, user.ID, input, resources)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.ProjectName) != "" && project.Name != strings.TrimSpace(input.ProjectName) {
		project.Name = strings.TrimSpace(input.ProjectName)
		project.UpdatedAt = time.Now().UTC()
		if err := repos.projects.Update(ctx, project); err != nil {
			return nil, err
		}
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
	if projectedUsed > quota.QuotaBytes {
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
		SyncSessionID:   strings.TrimSpace(input.SyncSessionID),
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
	session.Status = domain.CloudSyncSessionStatusCommitted
	session.ManifestID = manifest.ID
	session.UpdatedAt = now
	session.CommittedAt = &now
	if err := repos.syncSessions.Update(ctx, session); err != nil {
		return nil, err
	}
	return &CloudManifestCommitResult{
		Project:  project,
		Manifest: manifest,
		Usage:    *usageFor(user.ID, quota.Plan, projectedUsed, quota.QuotaBytes),
	}, nil
}

func (s *cloudStorageService) requireConfiguredProvider() error {
	if s.provider == nil || strings.TrimSpace(s.provider.ProviderID()) == "" || s.provider.ProviderID() == "unconfigured" {
		return ErrCloudStorageProviderNotConfigured
	}
	return nil
}

func (s *cloudStorageService) authorizeCloudAccess(ctx context.Context, userID string) (*domain.User, CloudStorageQuotaDecision, int64, error) {
	if s.objects == nil {
		return nil, CloudStorageQuotaDecision{}, 0, ErrInvalidCloudStorage
	}
	user, err := s.loadActiveUser(ctx, userID)
	if err != nil {
		return nil, CloudStorageQuotaDecision{}, 0, err
	}
	quota, err := s.cloudStorageQuotaDecision(ctx, user)
	if err != nil {
		return nil, CloudStorageQuotaDecision{}, 0, err
	}
	if !quota.HasEntitlement {
		return nil, CloudStorageQuotaDecision{}, 0, ErrCloudStorageAccessDenied
	}
	if quota.QuotaBytes <= 0 {
		return nil, CloudStorageQuotaDecision{}, 0, ErrCloudStorageAccessDenied
	}
	usedBytes, err := s.objects.SumActiveBytesByUser(ctx, user.ID)
	if err != nil {
		return nil, CloudStorageQuotaDecision{}, 0, err
	}
	return user, quota, usedBytes, nil
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

func (s *cloudStorageService) cloudStorageQuotaDecision(ctx context.Context, user *domain.User) (CloudStorageQuotaDecision, error) {
	if s.grants == nil {
		return CloudStorageQuotaDecision{}, ErrInvalidCloudStorage
	}
	grants, err := s.grants.List(ctx, repository.EntitlementGrantQuery{
		UserID:        user.ID,
		EntitlementID: domain.EntitlementCloudStorage,
		Status:        domain.GrantStatusActive,
	})
	if err != nil {
		return CloudStorageQuotaDecision{}, err
	}
	return s.policy.Decide(ctx, CloudStorageQuotaInput{User: user, Grants: grants, Now: time.Now().UTC()}), nil
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
	if repos.projects == nil || repos.syncSessions == nil || repos.manifests == nil || repos.objects == nil {
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

func cloudObjectLifecycleTags(userID string, cloudProjectID string, clientProjectID string, resource CloudResourceDescriptor) []CloudObjectLifecycleTag {
	return []CloudObjectLifecycleTag{
		{Key: "walnut.user_id", Value: sanitizePathSegment(userID)},
		{Key: "walnut.cloud_project_id", Value: sanitizePathSegment(cloudProjectID)},
		{Key: "walnut.client_project_id", Value: sanitizePathSegment(clientProjectID)},
		{Key: "walnut.resource_kind", Value: sanitizePathSegment(resource.ResourceKind)},
	}
}

func (s *cloudStorageService) authorizedCommitSession(ctx context.Context, repos cloudStorageRepositories, userID string, input CloudManifestCommitInput, resources []CloudResourceDescriptor) (*domain.CloudSyncSession, *domain.CloudProject, error) {
	session, err := validateCloudSyncSession(ctx, repos.syncSessions, input.SyncSessionID, userID, strings.TrimSpace(input.ClientProjectID), s.provider.ProviderID(), resources)
	if err != nil {
		return nil, nil, err
	}
	project, err := repos.projects.GetByID(ctx, session.CloudProjectID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, ErrCloudProjectNotFound
		}
		return nil, nil, err
	}
	if project.UserID != userID || project.ClientProjectID != strings.TrimSpace(input.ClientProjectID) {
		return nil, nil, ErrInvalidCloudStorage
	}
	return session, project, nil
}

func validateCloudSyncSession(ctx context.Context, repo repository.CloudSyncSessionRepository, sessionID string, userID string, clientProjectID string, provider string, resources []CloudResourceDescriptor) (*domain.CloudSyncSession, error) {
	if repo == nil {
		return nil, ErrInvalidCloudStorage
	}
	session, err := repo.GetByID(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrCloudSyncSessionNotFound
		}
		return nil, err
	}
	if session.UserID != userID || session.ClientProjectID != strings.TrimSpace(clientProjectID) || session.Provider != provider {
		return nil, ErrInvalidCloudStorage
	}
	if session.Status == domain.CloudSyncSessionStatusCommitted || strings.TrimSpace(session.ManifestID) != "" {
		return nil, ErrCloudSyncSessionAlreadyCommitted
	}
	if session.Status != "" && session.Status != domain.CloudSyncSessionStatusAuthorized {
		return nil, ErrInvalidCloudStorage
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		session.Status = domain.CloudSyncSessionStatusExpired
		session.UpdatedAt = time.Now().UTC()
		_ = repo.Update(ctx, session)
		return nil, ErrCloudSyncSessionExpired
	}
	if session.ResourceFingerprint != cloudResourceFingerprint(resources) || session.RequestedBytes != sumResourceBytes(resources) {
		return nil, ErrInvalidCloudStorage
	}
	return session, nil
}

func cloudResourceFingerprint(resources []CloudResourceDescriptor) string {
	normalized := append([]CloudResourceDescriptor(nil), resources...)
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].ResourceKind == normalized[j].ResourceKind {
			return normalized[i].ResourceID < normalized[j].ResourceID
		}
		return normalized[i].ResourceKind < normalized[j].ResourceKind
	})
	h := sha256.New()
	for _, resource := range normalized {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%s\x00%s\n",
			resource.ResourceKind,
			resource.ResourceID,
			resource.ContentHash,
			resource.SizeBytes,
			resource.ContentType,
			resource.Filename,
		)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *cloudStorageService) projectForLatestManifest(ctx context.Context, userID string, query CloudStorageLatestManifestQuery) (*domain.CloudProject, error) {
	cloudProjectID := strings.TrimSpace(query.CloudProjectID)
	if cloudProjectID != "" {
		project, err := s.projects.GetByID(ctx, cloudProjectID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return nil, ErrCloudProjectNotFound
			}
			return nil, err
		}
		if project.UserID != userID {
			return nil, ErrCloudProjectNotFound
		}
		return project, nil
	}
	clientProjectID := strings.TrimSpace(query.ClientProjectID)
	if clientProjectID == "" {
		return nil, ErrInvalidCloudStorage
	}
	project, err := s.projects.GetByUserAndClientProject(ctx, userID, clientProjectID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrCloudProjectNotFound
		}
		return nil, err
	}
	return project, nil
}

func cloudStorageProjectSummary(project domain.CloudProject, manifest *domain.CloudManifest) CloudStorageProjectSummary {
	summary := CloudStorageProjectSummary{
		ID:              project.ID,
		ClientProjectID: project.ClientProjectID,
		Name:            project.Name,
		Status:          project.Status,
		LastManifestID:  project.LastManifestID,
		UpdatedAt:       project.UpdatedAt,
	}
	if manifest != nil {
		summary.LatestManifest = cloudManifestSummary(manifest)
	}
	return summary
}

func cloudManifestSummary(manifest *domain.CloudManifest) *CloudManifestSummary {
	if manifest == nil {
		return nil
	}
	return &CloudManifestSummary{
		ID:              manifest.ID,
		ManifestHash:    manifest.ManifestHash,
		ManifestVersion: manifest.ManifestVersion,
		ObjectCount:     manifest.ObjectCount,
		TotalBytes:      manifest.TotalBytes,
		Status:          manifest.Status,
		CommittedAt:     manifest.CommittedAt,
		CreatedAt:       manifest.CreatedAt,
	}
}

func cloudObjectSummaries(objects []domain.CloudObject) []CloudObjectSummary {
	result := make([]CloudObjectSummary, 0, len(objects))
	for _, object := range objects {
		result = append(result, cloudObjectSummary(object))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ResourceKind == result[j].ResourceKind {
			return result[i].ResourceID < result[j].ResourceID
		}
		return result[i].ResourceKind < result[j].ResourceKind
	})
	return result
}

func cloudObjectSummary(object domain.CloudObject) CloudObjectSummary {
	return CloudObjectSummary{
		ResourceID:   object.ResourceID,
		ResourceKind: object.ResourceKind,
		ObjectKey:    object.ObjectKey,
		ContentHash:  object.ContentHash,
		SizeBytes:    object.SizeBytes,
		ContentType:  object.ContentType,
		Status:       object.Status,
	}
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

func normalizeCloudStorageQuotaPolicyConfig(config CloudStorageQuotaPolicyConfig) CloudStorageQuotaPolicyConfig {
	if config.DefaultQuotaBytes < 0 {
		config.DefaultQuotaBytes = 0
	}
	if config.TrialQuotaBytes <= 0 {
		config.TrialQuotaBytes = config.DefaultQuotaBytes
	}
	if config.MonthlyQuotaBytes <= 0 {
		config.MonthlyQuotaBytes = config.DefaultQuotaBytes
	}
	if config.LifetimeQuotaBytes <= 0 {
		config.LifetimeQuotaBytes = config.DefaultQuotaBytes
	}
	return config
}

func mbToBytes(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return value * 1024 * 1024
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloudStoragePlanForGrants(grants []domain.EntitlementGrant, now time.Time) string {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hasCustom := false
	hasTrial := false
	hasMonthly := false
	hasLifetime := false
	for _, grant := range grants {
		if grant.EntitlementID != domain.EntitlementCloudStorage || !isGrantActive(grant, now) {
			continue
		}
		switch {
		case grant.Source == domain.GrantSourceFulfillment && grant.ExpiresAt == nil:
			hasLifetime = true
		case grant.Source == domain.GrantSourceFulfillment && grant.ExpiresAt != nil:
			hasMonthly = true
		case grant.Source == domain.GrantSourceSubscriptionGrace:
			hasMonthly = true
		case grant.Source == domain.GrantSourceTrial:
			hasTrial = true
		default:
			hasCustom = true
		}
	}
	switch {
	case hasLifetime:
		return CloudStoragePlanLifetime
	case hasMonthly:
		return CloudStoragePlanMonthly
	case hasTrial:
		return CloudStoragePlanTrial
	case hasCustom:
		return CloudStoragePlanCustom
	default:
		return CloudStoragePlanNone
	}
}

func usageFor(userID string, plan string, usedBytes int64, quotaBytes int64) *CloudStorageUsage {
	remaining := quotaBytes - usedBytes
	if remaining < 0 {
		remaining = 0
	}
	if strings.TrimSpace(plan) == "" {
		plan = CloudStoragePlanNone
	}
	return &CloudStorageUsage{
		UserID:         userID,
		Plan:           plan,
		UsedBytes:      usedBytes,
		QuotaBytes:     quotaBytes,
		RemainingBytes: remaining,
		OverQuota:      usedBytes > quotaBytes,
	}
}
