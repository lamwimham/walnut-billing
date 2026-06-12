package gorm_repo

import (
	"context"
	"errors"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.UserRepository = (*UserRepo)(nil)
var _ repository.RegistrationRepository = (*RegistrationRepo)(nil)
var _ repository.EntitlementGrantRepository = (*EntitlementGrantRepo)(nil)

type UserRepo struct {
	DB *gorm.DB
}

func (r *UserRepo) Create(ctx context.Context, user *domain.User) error {
	return r.DB.WithContext(ctx).Create(user).Error
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	var user domain.User
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&user).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &user, nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	if err := r.DB.WithContext(ctx).Where("email = ?", email).First(&user).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &user, nil
}

func (r *UserRepo) Update(ctx context.Context, user *domain.User) error {
	return r.DB.WithContext(ctx).Save(user).Error
}

func (r *UserRepo) WithTx(tx *gorm.DB) *UserRepo {
	return &UserRepo{DB: tx}
}

type RegistrationRepo struct {
	DB *gorm.DB
}

func (r *RegistrationRepo) Create(ctx context.Context, registration *domain.RegistrationRequest) error {
	return r.DB.WithContext(ctx).Create(registration).Error
}

func (r *RegistrationRepo) GetByID(ctx context.Context, id string) (*domain.RegistrationRequest, error) {
	var registration domain.RegistrationRequest
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&registration).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &registration, nil
}

func (r *RegistrationRepo) List(ctx context.Context, query repository.RegistrationQuery) ([]domain.RegistrationRequest, error) {
	var registrations []domain.RegistrationRequest
	q := r.DB.WithContext(ctx).Model(&domain.RegistrationRequest{})
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.Email != "" {
		q = q.Where("email = ?", query.Email)
	}
	if query.Limit > 0 {
		q = q.Limit(query.Limit)
	}
	if query.Offset > 0 {
		q = q.Offset(query.Offset)
	}
	if err := q.Order("created_at DESC").Find(&registrations).Error; err != nil {
		return nil, err
	}
	return registrations, nil
}

func (r *RegistrationRepo) Update(ctx context.Context, registration *domain.RegistrationRequest) error {
	return r.DB.WithContext(ctx).Save(registration).Error
}

type EntitlementGrantRepo struct {
	DB *gorm.DB
}

func (r *EntitlementGrantRepo) Create(ctx context.Context, grant *domain.EntitlementGrant) error {
	return r.DB.WithContext(ctx).Create(grant).Error
}

func (r *EntitlementGrantRepo) GetByID(ctx context.Context, id string) (*domain.EntitlementGrant, error) {
	var grant domain.EntitlementGrant
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&grant).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &grant, nil
}

func (r *EntitlementGrantRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.EntitlementGrant, error) {
	var grant domain.EntitlementGrant
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&grant).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &grant, nil
}

func (r *EntitlementGrantRepo) List(ctx context.Context, query repository.EntitlementGrantQuery) ([]domain.EntitlementGrant, error) {
	var grants []domain.EntitlementGrant
	q := r.DB.WithContext(ctx).Model(&domain.EntitlementGrant{})
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.EntitlementID != "" {
		q = q.Where("entitlement_id = ?", query.EntitlementID)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	if !query.IncludeExpired {
		q = q.Where("expires_at IS NULL OR expires_at > ?", time.Now().UTC())
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

func (r *EntitlementGrantRepo) ListByUser(ctx context.Context, userID string) ([]domain.EntitlementGrant, error) {
	var grants []domain.EntitlementGrant
	if err := r.DB.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC").Find(&grants).Error; err != nil {
		return nil, err
	}
	return grants, nil
}

func (r *EntitlementGrantRepo) Update(ctx context.Context, grant *domain.EntitlementGrant) error {
	return r.DB.WithContext(ctx).Save(grant).Error
}

func (r *EntitlementGrantRepo) WithTx(tx *gorm.DB) *EntitlementGrantRepo {
	return &EntitlementGrantRepo{DB: tx}
}

func mapGormNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return repository.ErrNotFound
	}
	return err
}
