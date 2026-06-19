package gorm_repo

import (
	"context"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AdminCloudStorageReadRepository = (*AdminCloudStorageReadRepo)(nil)

// AdminCloudStorageReadRepo builds metadata-only operator views for cloud
// storage. It never reads object keys or object bytes from the provider layer.
type AdminCloudStorageReadRepo struct {
	DB *gorm.DB
}

func (r *AdminCloudStorageReadRepo) ListUsage(ctx context.Context, query repository.AdminCloudStorageUsageQuery) (*repository.AdminCloudStorageUsageReadModel, error) {
	if r == nil || r.DB == nil {
		return nil, repository.ErrNotFound
	}
	limit := normalizeAdminCloudStorageLimit(query.Limit)
	offset := maxAdminCloudStorageOffset(query.Offset)

	userScope := r.DB.WithContext(ctx).Model(&domain.User{})
	userScope = applyAdminCloudStorageUserFilters(userScope, query.UserID, query.Status)

	var totalUsers int64
	if err := userScope.Count(&totalUsers).Error; err != nil {
		return nil, err
	}

	var totalUsedBytes int64
	if err := applyAdminCloudStorageUserJoinFilters(
		r.DB.WithContext(ctx).Model(&domain.CloudObject{}).
			Joins("JOIN users ON users.id = cloud_objects.user_id").
			Where("cloud_objects.status = ?", domain.CloudObjectStatusActive),
		query.UserID,
		query.Status,
	).Select("COALESCE(SUM(cloud_objects.size_bytes), 0)").Scan(&totalUsedBytes).Error; err != nil {
		return nil, err
	}

	var totalProjectCount int64
	if err := applyAdminCloudStorageUserJoinFilters(
		r.DB.WithContext(ctx).Model(&domain.CloudProject{}).
			Joins("JOIN users ON users.id = cloud_projects.user_id"),
		query.UserID,
		query.Status,
	).Count(&totalProjectCount).Error; err != nil {
		return nil, err
	}

	var totalActiveProjectCount int64
	if err := applyAdminCloudStorageUserJoinFilters(
		r.DB.WithContext(ctx).Model(&domain.CloudProject{}).
			Joins("JOIN users ON users.id = cloud_projects.user_id").
			Where("(cloud_projects.status = ? OR cloud_projects.status = '')", domain.CloudProjectStatusActive),
		query.UserID,
		query.Status,
	).Count(&totalActiveProjectCount).Error; err != nil {
		return nil, err
	}

	var users []domain.User
	if err := userScope.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return &repository.AdminCloudStorageUsageReadModel{
			TotalUsers:              totalUsers,
			TotalUsedBytes:          totalUsedBytes,
			TotalProjectCount:       totalProjectCount,
			TotalActiveProjectCount: totalActiveProjectCount,
			Users:                   []repository.AdminCloudStorageUserRecord{},
		}, nil
	}

	userIDs := adminCloudStorageUserIDs(users)
	grantsByUser, err := adminCloudStorageGrantsByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, err
	}
	projectsByUser, err := adminCloudStorageProjectsByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, err
	}
	usedBytesByUser, err := adminCloudStorageUsedBytesByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, err
	}

	records := make([]repository.AdminCloudStorageUserRecord, 0, len(users))
	for _, user := range users {
		records = append(records, repository.AdminCloudStorageUserRecord{
			User:      user,
			Grants:    grantsByUser[user.ID],
			Projects:  projectsByUser[user.ID],
			UsedBytes: usedBytesByUser[user.ID],
		})
	}

	return &repository.AdminCloudStorageUsageReadModel{
		TotalUsers:              totalUsers,
		TotalUsedBytes:          totalUsedBytes,
		TotalProjectCount:       totalProjectCount,
		TotalActiveProjectCount: totalActiveProjectCount,
		Users:                   records,
	}, nil
}

func (r *AdminCloudStorageReadRepo) ListProjects(ctx context.Context, query repository.AdminCloudStorageProjectQuery) (*repository.AdminCloudStorageProjectReadModel, error) {
	if r == nil || r.DB == nil || strings.TrimSpace(query.UserID) == "" {
		return nil, repository.ErrNotFound
	}
	limit := normalizeAdminCloudStorageLimit(query.Limit)
	offset := maxAdminCloudStorageOffset(query.Offset)

	var user domain.User
	if err := r.DB.WithContext(ctx).Where("id = ?", strings.TrimSpace(query.UserID)).First(&user).Error; err != nil {
		return nil, mapGormNotFound(err)
	}

	var grants []domain.EntitlementGrant
	if err := r.DB.WithContext(ctx).
		Where("user_id = ? AND entitlement_id = ?", user.ID, domain.EntitlementCloudStorage).
		Order("created_at DESC").
		Find(&grants).Error; err != nil {
		return nil, err
	}

	var usedBytes int64
	if err := r.DB.WithContext(ctx).Model(&domain.CloudObject{}).
		Where("user_id = ? AND status = ?", user.ID, domain.CloudObjectStatusActive).
		Select("COALESCE(SUM(size_bytes), 0)").Scan(&usedBytes).Error; err != nil {
		return nil, err
	}

	projectQuery := r.DB.WithContext(ctx).Model(&domain.CloudProject{}).Where("user_id = ?", user.ID)
	if strings.TrimSpace(query.Status) != "" {
		projectQuery = projectQuery.Where("status = ?", strings.TrimSpace(query.Status))
	}

	var totalProjects int64
	if err := projectQuery.Count(&totalProjects).Error; err != nil {
		return nil, err
	}

	var projects []domain.CloudProject
	if err := projectQuery.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&projects).Error; err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return &repository.AdminCloudStorageProjectReadModel{
			User:          user,
			Grants:        grants,
			UsedBytes:     usedBytes,
			TotalProjects: totalProjects,
			Projects:      []repository.AdminCloudStorageProjectRecord{},
		}, nil
	}

	projectIDs := adminCloudStorageProjectIDs(projects)
	manifestByProject, err := adminCloudStorageLastManifestByProject(ctx, r.DB, projects)
	if err != nil {
		return nil, err
	}
	objectStatsByProject, err := adminCloudStorageObjectStatsByProject(ctx, r.DB, projectIDs)
	if err != nil {
		return nil, err
	}

	records := make([]repository.AdminCloudStorageProjectRecord, 0, len(projects))
	for _, project := range projects {
		stats := objectStatsByProject[project.ID]
		records = append(records, repository.AdminCloudStorageProjectRecord{
			Project:           project,
			LastManifest:      manifestByProject[project.ID],
			ActiveObjectCount: stats.count,
			ActiveBytes:       stats.bytes,
		})
	}

	return &repository.AdminCloudStorageProjectReadModel{
		User:          user,
		Grants:        grants,
		UsedBytes:     usedBytes,
		TotalProjects: totalProjects,
		Projects:      records,
	}, nil
}

func applyAdminCloudStorageUserFilters(q *gorm.DB, userID string, status string) *gorm.DB {
	if strings.TrimSpace(userID) != "" {
		q = q.Where("id = ?", strings.TrimSpace(userID))
	}
	if strings.TrimSpace(status) != "" {
		q = q.Where("status = ?", strings.TrimSpace(status))
	}
	return q
}

func applyAdminCloudStorageUserJoinFilters(q *gorm.DB, userID string, status string) *gorm.DB {
	if strings.TrimSpace(userID) != "" {
		q = q.Where("users.id = ?", strings.TrimSpace(userID))
	}
	if strings.TrimSpace(status) != "" {
		q = q.Where("users.status = ?", strings.TrimSpace(status))
	}
	return q
}

func adminCloudStorageUserIDs(users []domain.User) []string {
	ids := make([]string, 0, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.ID) != "" {
			ids = append(ids, user.ID)
		}
	}
	return ids
}

func adminCloudStorageProjectIDs(projects []domain.CloudProject) []string {
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		if strings.TrimSpace(project.ID) != "" {
			ids = append(ids, project.ID)
		}
	}
	return ids
}

func adminCloudStorageGrantsByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string][]domain.EntitlementGrant, error) {
	var grants []domain.EntitlementGrant
	if err := db.WithContext(ctx).
		Where("user_id IN ? AND entitlement_id = ?", userIDs, domain.EntitlementCloudStorage).
		Order("created_at DESC").
		Find(&grants).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.EntitlementGrant, len(userIDs))
	for _, grant := range grants {
		result[grant.UserID] = append(result[grant.UserID], grant)
	}
	return result, nil
}

func adminCloudStorageProjectsByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string][]domain.CloudProject, error) {
	var projects []domain.CloudProject
	if err := db.WithContext(ctx).
		Where("user_id IN ?", userIDs).
		Order("updated_at DESC, id DESC").
		Find(&projects).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.CloudProject, len(userIDs))
	for _, project := range projects {
		result[project.UserID] = append(result[project.UserID], project)
	}
	return result, nil
}

func adminCloudStorageUsedBytesByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string]int64, error) {
	type row struct {
		UserID string
		Bytes  int64
	}
	var rows []row
	if err := db.WithContext(ctx).Model(&domain.CloudObject{}).
		Where("user_id IN ? AND status = ?", userIDs, domain.CloudObjectStatusActive).
		Select("user_id, COALESCE(SUM(size_bytes), 0) AS bytes").
		Group("user_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(userIDs))
	for _, row := range rows {
		result[row.UserID] = row.Bytes
	}
	return result, nil
}

type adminCloudStorageObjectStat struct {
	count int
	bytes int64
}

func adminCloudStorageObjectStatsByProject(ctx context.Context, db *gorm.DB, projectIDs []string) (map[string]adminCloudStorageObjectStat, error) {
	type row struct {
		CloudProjectID string
		Count          int
		Bytes          int64
	}
	var rows []row
	if err := db.WithContext(ctx).Model(&domain.CloudObject{}).
		Where("cloud_project_id IN ? AND status = ?", projectIDs, domain.CloudObjectStatusActive).
		Select("cloud_project_id, COUNT(*) AS count, COALESCE(SUM(size_bytes), 0) AS bytes").
		Group("cloud_project_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]adminCloudStorageObjectStat, len(projectIDs))
	for _, row := range rows {
		result[row.CloudProjectID] = adminCloudStorageObjectStat{count: row.Count, bytes: row.Bytes}
	}
	return result, nil
}

func adminCloudStorageLastManifestByProject(ctx context.Context, db *gorm.DB, projects []domain.CloudProject) (map[string]*domain.CloudManifest, error) {
	projectIDs := adminCloudStorageProjectIDs(projects)
	var manifests []domain.CloudManifest
	if err := db.WithContext(ctx).
		Where("cloud_project_id IN ?", projectIDs).
		Order("created_at DESC, id DESC").
		Find(&manifests).Error; err != nil {
		return nil, err
	}
	result := make(map[string]*domain.CloudManifest, len(projects))
	for _, manifest := range manifests {
		if _, ok := result[manifest.CloudProjectID]; ok {
			continue
		}
		copy := manifest
		result[manifest.CloudProjectID] = &copy
	}

	// Prefer the project pointer when LastManifestID is set but timestamps are equal.
	manifestByID := make(map[string]domain.CloudManifest, len(manifests))
	for _, manifest := range manifests {
		manifestByID[manifest.ID] = manifest
	}
	for _, project := range projects {
		if strings.TrimSpace(project.LastManifestID) == "" {
			continue
		}
		if manifest, ok := manifestByID[project.LastManifestID]; ok {
			copy := manifest
			result[project.ID] = &copy
		}
	}
	return result, nil
}

func normalizeAdminCloudStorageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func maxAdminCloudStorageOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
