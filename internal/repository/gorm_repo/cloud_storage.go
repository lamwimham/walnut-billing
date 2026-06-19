package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var _ repository.CloudProjectRepository = (*CloudProjectRepo)(nil)
var _ repository.CloudSyncSessionRepository = (*CloudSyncSessionRepo)(nil)
var _ repository.CloudManifestRepository = (*CloudManifestRepo)(nil)
var _ repository.CloudObjectRepository = (*CloudObjectRepo)(nil)

type CloudProjectRepo struct {
	DB *gorm.DB
}

func (r *CloudProjectRepo) Create(ctx context.Context, project *domain.CloudProject) error {
	return r.DB.WithContext(ctx).Create(project).Error
}

func (r *CloudProjectRepo) GetByID(ctx context.Context, id string) (*domain.CloudProject, error) {
	var project domain.CloudProject
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&project).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &project, nil
}

func (r *CloudProjectRepo) GetByUserAndClientProject(ctx context.Context, userID string, clientProjectID string) (*domain.CloudProject, error) {
	var project domain.CloudProject
	if err := r.DB.WithContext(ctx).Where("user_id = ? AND client_project_id = ?", userID, clientProjectID).First(&project).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &project, nil
}

func (r *CloudProjectRepo) ListByUser(ctx context.Context, userID string, status string, limit int, offset int) ([]domain.CloudProject, error) {
	var projects []domain.CloudProject
	q := r.DB.WithContext(ctx).Model(&domain.CloudProject{}).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Order("updated_at DESC").Find(&projects).Error; err != nil {
		return nil, err
	}
	return projects, nil
}

func (r *CloudProjectRepo) Update(ctx context.Context, project *domain.CloudProject) error {
	return r.DB.WithContext(ctx).Save(project).Error
}

func (r *CloudProjectRepo) WithTx(tx *gorm.DB) *CloudProjectRepo {
	return &CloudProjectRepo{DB: tx}
}

type CloudSyncSessionRepo struct {
	DB *gorm.DB
}

func (r *CloudSyncSessionRepo) Create(ctx context.Context, session *domain.CloudSyncSession) error {
	return r.DB.WithContext(ctx).Create(session).Error
}

func (r *CloudSyncSessionRepo) GetByID(ctx context.Context, id string) (*domain.CloudSyncSession, error) {
	var session domain.CloudSyncSession
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&session).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &session, nil
}

func (r *CloudSyncSessionRepo) Update(ctx context.Context, session *domain.CloudSyncSession) error {
	return r.DB.WithContext(ctx).Save(session).Error
}

func (r *CloudSyncSessionRepo) WithTx(tx *gorm.DB) *CloudSyncSessionRepo {
	return &CloudSyncSessionRepo{DB: tx}
}

type CloudManifestRepo struct {
	DB *gorm.DB
}

func (r *CloudManifestRepo) Create(ctx context.Context, manifest *domain.CloudManifest) error {
	return r.DB.WithContext(ctx).Create(manifest).Error
}

func (r *CloudManifestRepo) GetByID(ctx context.Context, id string) (*domain.CloudManifest, error) {
	var manifest domain.CloudManifest
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&manifest).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &manifest, nil
}

func (r *CloudManifestRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CloudManifest, error) {
	var manifest domain.CloudManifest
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&manifest).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &manifest, nil
}

func (r *CloudManifestRepo) ListByProject(ctx context.Context, cloudProjectID string, limit int, offset int) ([]domain.CloudManifest, error) {
	var manifests []domain.CloudManifest
	q := r.DB.WithContext(ctx).Model(&domain.CloudManifest{}).Where("cloud_project_id = ?", cloudProjectID)
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Order("created_at DESC").Find(&manifests).Error; err != nil {
		return nil, err
	}
	return manifests, nil
}

func (r *CloudManifestRepo) WithTx(tx *gorm.DB) *CloudManifestRepo {
	return &CloudManifestRepo{DB: tx}
}

type CloudObjectRepo struct {
	DB *gorm.DB
}

func (r *CloudObjectRepo) Upsert(ctx context.Context, object *domain.CloudObject) error {
	return r.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "object_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"manifest_id", "resource_id", "resource_kind", "content_hash", "size_bytes", "content_type", "status", "updated_at",
		}),
	}).Create(object).Error
}

func (r *CloudObjectRepo) Update(ctx context.Context, object *domain.CloudObject) error {
	return r.DB.WithContext(ctx).Save(object).Error
}

func (r *CloudObjectRepo) GetByObjectKey(ctx context.Context, objectKey string) (*domain.CloudObject, error) {
	var object domain.CloudObject
	if err := r.DB.WithContext(ctx).Where("object_key = ?", objectKey).First(&object).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &object, nil
}

func (r *CloudObjectRepo) ListByProject(ctx context.Context, cloudProjectID string, status string) ([]domain.CloudObject, error) {
	var objects []domain.CloudObject
	q := r.DB.WithContext(ctx).Model(&domain.CloudObject{}).Where("cloud_project_id = ?", cloudProjectID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Order("updated_at DESC").Find(&objects).Error; err != nil {
		return nil, err
	}
	return objects, nil
}

func (r *CloudObjectRepo) SumActiveBytesByUser(ctx context.Context, userID string) (int64, error) {
	var total int64
	if err := r.DB.WithContext(ctx).Model(&domain.CloudObject{}).
		Where("user_id = ? AND status = ?", userID, domain.CloudObjectStatusActive).
		Select("COALESCE(SUM(size_bytes), 0)").Scan(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

func (r *CloudObjectRepo) WithTx(tx *gorm.DB) *CloudObjectRepo {
	return &CloudObjectRepo{DB: tx}
}
