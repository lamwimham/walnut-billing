package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var ErrInvalidAdminCloudStorageQuery = errors.New("invalid admin cloud storage query")

const (
	defaultAdminCloudStorageLimit = 50
	maxAdminCloudStorageLimit     = 100
)

type AdminCloudStorageService interface {
	Usage(ctx context.Context, query AdminCloudStorageUsageQuery) (*AdminCloudStorageUsage, error)
	ListUserProjects(ctx context.Context, query AdminCloudStorageProjectQuery) (*AdminCloudStorageProjectList, error)
}

type AdminCloudStorageDependencies struct {
	ReadModel   repository.AdminCloudStorageReadRepository
	QuotaPolicy CloudStorageQuotaPolicy
	Privacy     AdminPrivacyProjector
	Now         func() time.Time
}

type AdminCloudStorageUsageQuery struct {
	UserID string
	Status string
	Limit  int
	Offset int
}

type AdminCloudStorageProjectQuery struct {
	UserID string
	Status string
	Limit  int
	Offset int
}

type AdminCloudStorageUsage struct {
	GeneratedAt             string                       `json:"generated_at"`
	TotalUsers              int64                        `json:"total_users"`
	TotalUsedBytes          int64                        `json:"total_used_bytes"`
	TotalProjectCount       int64                        `json:"total_project_count"`
	TotalActiveProjectCount int64                        `json:"total_active_project_count"`
	Users                   []AdminCloudStorageUsageUser `json:"users"`
}

type AdminCloudStorageUsageUser struct {
	User                    AdminCloudStorageUserIdentity `json:"user"`
	UsedBytes               int64                         `json:"used_bytes"`
	QuotaBytes              int64                         `json:"quota_bytes"`
	RemainingBytes          int64                         `json:"remaining_bytes"`
	OverQuota               bool                          `json:"over_quota"`
	ProjectCount            int                           `json:"project_count"`
	ActiveProjectCount      int                           `json:"active_project_count"`
	HasCloudStorageGrant    bool                          `json:"has_cloud_storage_grant"`
	LatestProjectUpdatedAt  string                        `json:"latest_project_updated_at,omitempty"`
	LatestProjectNameMasked string                        `json:"latest_project_name_masked,omitempty"`
}

type AdminCloudStorageProjectList struct {
	GeneratedAt    string                            `json:"generated_at"`
	User           AdminCloudStorageUserIdentity     `json:"user"`
	UsedBytes      int64                             `json:"used_bytes"`
	QuotaBytes     int64                             `json:"quota_bytes"`
	RemainingBytes int64                             `json:"remaining_bytes"`
	OverQuota      bool                              `json:"over_quota"`
	TotalProjects  int64                             `json:"total_projects"`
	Projects       []AdminCloudStorageProjectSummary `json:"projects"`
}

type AdminCloudStorageUserIdentity struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	EmailMasked      string `json:"email_masked"`
	EmailFingerprint string `json:"email_fingerprint"`
	EmailDomain      string `json:"email_domain"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type AdminCloudStorageProjectSummary struct {
	ID                string                    `json:"id"`
	ClientProjectID   string                    `json:"client_project_id"`
	NameMasked        string                    `json:"name_masked"`
	Status            string                    `json:"status"`
	LastManifestID    string                    `json:"last_manifest_id,omitempty"`
	LastManifest      AdminCloudStorageManifest `json:"last_manifest,omitempty"`
	ActiveObjectCount int                       `json:"active_object_count"`
	ActiveBytes       int64                     `json:"active_bytes"`
	CreatedAt         string                    `json:"created_at"`
	UpdatedAt         string                    `json:"updated_at"`
}

type AdminCloudStorageManifest struct {
	ID                      string `json:"id,omitempty"`
	ManifestHashFingerprint string `json:"manifest_hash_fingerprint,omitempty"`
	ManifestVersion         int    `json:"manifest_version,omitempty"`
	ObjectCount             int    `json:"object_count,omitempty"`
	TotalBytes              int64  `json:"total_bytes,omitempty"`
	Status                  string `json:"status,omitempty"`
	CommittedAt             string `json:"committed_at,omitempty"`
	CreatedAt               string `json:"created_at,omitempty"`
}

type adminCloudStorageService struct {
	readModel   repository.AdminCloudStorageReadRepository
	quotaPolicy CloudStorageQuotaPolicy
	privacy     AdminPrivacyProjector
	now         func() time.Time
}

func NewAdminCloudStorageService(deps AdminCloudStorageDependencies) AdminCloudStorageService {
	privacy := deps.Privacy
	if privacy == (AdminPrivacyProjector{}) {
		privacy = NewAdminPrivacyProjector()
	}
	return &adminCloudStorageService{
		readModel:   deps.ReadModel,
		quotaPolicy: deps.QuotaPolicy,
		privacy:     privacy,
		now:         deps.Now,
	}
}

func (s *adminCloudStorageService) Usage(ctx context.Context, query AdminCloudStorageUsageQuery) (*AdminCloudStorageUsage, error) {
	if s == nil || s.readModel == nil {
		return nil, ErrInvalidAdminCloudStorageQuery
	}
	repoQuery := repository.AdminCloudStorageUsageQuery{
		UserID: strings.TrimSpace(query.UserID),
		Status: strings.TrimSpace(query.Status),
		Limit:  normalizeAdminCloudStorageQueryLimit(query.Limit),
		Offset: maxInt(query.Offset, 0),
	}
	record, err := s.readModel.ListUsage(ctx, repoQuery)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrInvalidAdminCloudStorageQuery
	}
	now := s.currentTime()
	users := make([]AdminCloudStorageUsageUser, 0, len(record.Users))
	for _, userRecord := range record.Users {
		users = append(users, s.projectUsageUser(ctx, userRecord, now))
	}
	return &AdminCloudStorageUsage{
		GeneratedAt:             formatTime(now),
		TotalUsers:              record.TotalUsers,
		TotalUsedBytes:          record.TotalUsedBytes,
		TotalProjectCount:       record.TotalProjectCount,
		TotalActiveProjectCount: record.TotalActiveProjectCount,
		Users:                   users,
	}, nil
}

func (s *adminCloudStorageService) ListUserProjects(ctx context.Context, query AdminCloudStorageProjectQuery) (*AdminCloudStorageProjectList, error) {
	userID := strings.TrimSpace(query.UserID)
	if s == nil || s.readModel == nil || userID == "" {
		return nil, ErrInvalidAdminCloudStorageQuery
	}
	repoQuery := repository.AdminCloudStorageProjectQuery{
		UserID: userID,
		Status: strings.TrimSpace(query.Status),
		Limit:  normalizeAdminCloudStorageQueryLimit(query.Limit),
		Offset: maxInt(query.Offset, 0),
	}
	record, err := s.readModel.ListProjects(ctx, repoQuery)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if record == nil || strings.TrimSpace(record.User.ID) == "" {
		return nil, ErrUserNotFound
	}
	now := s.currentTime()
	quotaBytes, hasGrant := s.quotaFor(ctx, record.User, record.Grants, now)
	usage := usageFor(record.User.ID, record.UsedBytes, quotaBytes)
	projects := make([]AdminCloudStorageProjectSummary, 0, len(record.Projects))
	for _, project := range record.Projects {
		projects = append(projects, projectAdminCloudStorageProject(project))
	}
	return &AdminCloudStorageProjectList{
		GeneratedAt:    formatTime(now),
		User:           s.projectUserIdentity(record.User),
		UsedBytes:      usage.UsedBytes,
		QuotaBytes:     usage.QuotaBytes,
		RemainingBytes: usage.RemainingBytes,
		OverQuota:      usage.OverQuota || !hasGrant && usage.UsedBytes > 0,
		TotalProjects:  record.TotalProjects,
		Projects:       projects,
	}, nil
}

func (s *adminCloudStorageService) projectUsageUser(ctx context.Context, record repository.AdminCloudStorageUserRecord, now time.Time) AdminCloudStorageUsageUser {
	quotaBytes, hasGrant := s.quotaFor(ctx, record.User, record.Grants, now)
	usage := usageFor(record.User.ID, record.UsedBytes, quotaBytes)
	activeProjects := 0
	for _, project := range record.Projects {
		if project.Status == "" || project.Status == domain.CloudProjectStatusActive {
			activeProjects++
		}
	}
	latestProject := latestAdminCloudStorageProject(record.Projects)
	return AdminCloudStorageUsageUser{
		User:                    s.projectUserIdentity(record.User),
		UsedBytes:               usage.UsedBytes,
		QuotaBytes:              usage.QuotaBytes,
		RemainingBytes:          usage.RemainingBytes,
		OverQuota:               usage.OverQuota || !hasGrant && usage.UsedBytes > 0,
		ProjectCount:            len(record.Projects),
		ActiveProjectCount:      activeProjects,
		HasCloudStorageGrant:    hasGrant,
		LatestProjectUpdatedAt:  formatTime(latestProject.UpdatedAt),
		LatestProjectNameMasked: maskToken(latestProject.Name),
	}
}

func (s *adminCloudStorageService) projectUserIdentity(user domain.User) AdminCloudStorageUserIdentity {
	email := s.privacy.ProjectEmail(user.Email)
	return AdminCloudStorageUserIdentity{
		ID:               user.ID,
		Status:           defaultString(strings.TrimSpace(user.Status), domain.UserStatusActive),
		EmailMasked:      email.Masked,
		EmailFingerprint: email.Fingerprint,
		EmailDomain:      email.Domain,
		CreatedAt:        formatTime(user.CreatedAt),
		UpdatedAt:        formatTime(user.UpdatedAt),
	}
}

func (s *adminCloudStorageService) quotaFor(ctx context.Context, user domain.User, grants []domain.EntitlementGrant, now time.Time) (int64, bool) {
	hasGrant := hasActiveEntitlement(grants, domain.EntitlementCloudStorage, now)
	if !hasGrant || s == nil || s.quotaPolicy == nil {
		return 0, hasGrant
	}
	quotaBytes := s.quotaPolicy.QuotaBytes(ctx, &user)
	if quotaBytes < 0 {
		quotaBytes = 0
	}
	return quotaBytes, true
}

func projectAdminCloudStorageProject(record repository.AdminCloudStorageProjectRecord) AdminCloudStorageProjectSummary {
	project := record.Project
	return AdminCloudStorageProjectSummary{
		ID:                project.ID,
		ClientProjectID:   project.ClientProjectID,
		NameMasked:        maskToken(project.Name),
		Status:            defaultString(project.Status, domain.CloudProjectStatusActive),
		LastManifestID:    project.LastManifestID,
		LastManifest:      projectAdminCloudStorageManifest(record.LastManifest),
		ActiveObjectCount: record.ActiveObjectCount,
		ActiveBytes:       record.ActiveBytes,
		CreatedAt:         formatTime(project.CreatedAt),
		UpdatedAt:         formatTime(project.UpdatedAt),
	}
}

func projectAdminCloudStorageManifest(manifest *domain.CloudManifest) AdminCloudStorageManifest {
	if manifest == nil {
		return AdminCloudStorageManifest{}
	}
	return AdminCloudStorageManifest{
		ID:                      manifest.ID,
		ManifestHashFingerprint: tokenFingerprint(manifest.ManifestHash),
		ManifestVersion:         manifest.ManifestVersion,
		ObjectCount:             manifest.ObjectCount,
		TotalBytes:              manifest.TotalBytes,
		Status:                  defaultString(manifest.Status, domain.CloudManifestStatusCommitted),
		CommittedAt:             formatOptionalTime(manifest.CommittedAt),
		CreatedAt:               formatTime(manifest.CreatedAt),
	}
}

func latestAdminCloudStorageProject(projects []domain.CloudProject) domain.CloudProject {
	var latest domain.CloudProject
	for _, project := range projects {
		if latest.ID == "" || project.UpdatedAt.After(latest.UpdatedAt) {
			latest = project
		}
	}
	return latest
}

func (s *adminCloudStorageService) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func normalizeAdminCloudStorageQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminCloudStorageLimit
	}
	if limit > maxAdminCloudStorageLimit {
		return maxAdminCloudStorageLimit
	}
	return limit
}
