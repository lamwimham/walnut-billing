package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.UserDeviceRepository = (*UserDeviceRepo)(nil)
var _ repository.TrialGrantRepository = (*TrialGrantRepo)(nil)

type UserDeviceRepo struct {
	DB *gorm.DB
}

func (r *UserDeviceRepo) Create(ctx context.Context, device *domain.UserDevice) error {
	return r.DB.WithContext(ctx).Create(device).Error
}

func (r *UserDeviceRepo) GetByUserAndDevice(ctx context.Context, userID string, deviceID string) (*domain.UserDevice, error) {
	var device domain.UserDevice
	if err := r.DB.WithContext(ctx).Where("user_id = ? AND device_id = ?", userID, deviceID).First(&device).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &device, nil
}

func (r *UserDeviceRepo) ListByUser(ctx context.Context, userID string, status string) ([]domain.UserDevice, error) {
	var devices []domain.UserDevice
	q := r.DB.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Order("last_seen_at DESC").Find(&devices).Error; err != nil {
		return nil, err
	}
	return devices, nil
}

func (r *UserDeviceRepo) Update(ctx context.Context, device *domain.UserDevice) error {
	return r.DB.WithContext(ctx).Save(device).Error
}

func (r *UserDeviceRepo) WithTx(tx *gorm.DB) *UserDeviceRepo {
	return &UserDeviceRepo{DB: tx}
}

type TrialGrantRepo struct {
	DB *gorm.DB
}

func (r *TrialGrantRepo) Create(ctx context.Context, grant *domain.TrialGrant) error {
	return r.DB.WithContext(ctx).Create(grant).Error
}

func (r *TrialGrantRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.TrialGrant, error) {
	var grant domain.TrialGrant
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&grant).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &grant, nil
}

func (r *TrialGrantRepo) List(ctx context.Context, query repository.TrialGrantQuery) ([]domain.TrialGrant, error) {
	var grants []domain.TrialGrant
	q := r.DB.WithContext(ctx).Model(&domain.TrialGrant{})
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.Email != "" {
		q = q.Where("email = ?", query.Email)
	}
	if query.GrantType != "" {
		q = q.Where("grant_type = ?", query.GrantType)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	if query.Limit > 0 {
		q = q.Limit(query.Limit)
	}
	if query.Offset > 0 {
		q = q.Offset(query.Offset)
	}
	if err := q.Order("created_at DESC").Find(&grants).Error; err != nil {
		return nil, err
	}
	return grants, nil
}

func (r *TrialGrantRepo) Update(ctx context.Context, grant *domain.TrialGrant) error {
	return r.DB.WithContext(ctx).Save(grant).Error
}

func (r *TrialGrantRepo) WithTx(tx *gorm.DB) *TrialGrantRepo {
	return &TrialGrantRepo{DB: tx}
}
