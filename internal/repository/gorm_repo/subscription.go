package gorm_repo

import (
	"context"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.SubscriptionCancellationRepository = (*SubscriptionCancellationRepo)(nil)

type SubscriptionCancellationRepo struct {
	DB *gorm.DB
}

func (r *SubscriptionCancellationRepo) Create(ctx context.Context, cancellation *domain.SubscriptionCancellation) error {
	return r.DB.WithContext(ctx).Create(cancellation).Error
}

func (r *SubscriptionCancellationRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error) {
	var cancellation domain.SubscriptionCancellation
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&cancellation).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &cancellation, nil
}

func (r *SubscriptionCancellationRepo) GetByResumeIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error) {
	var cancellation domain.SubscriptionCancellation
	if key == "" {
		return nil, repository.ErrNotFound
	}
	if err := r.DB.WithContext(ctx).Where("resume_idempotency_key = ?", key).First(&cancellation).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &cancellation, nil
}

func (r *SubscriptionCancellationRepo) FindActive(ctx context.Context, query repository.SubscriptionCancellationQuery) (*domain.SubscriptionCancellation, error) {
	q := r.DB.WithContext(ctx).Model(&domain.SubscriptionCancellation{})
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.SKUCode != "" {
		q = q.Where("sku_code = ?", query.SKUCode)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	q = q.Where("cancel_at_period_end = ? AND current_period_ends_at > ?", true, time.Now().UTC())
	var cancellation domain.SubscriptionCancellation
	if err := q.Order("current_period_ends_at DESC, created_at DESC").First(&cancellation).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &cancellation, nil
}

func (r *SubscriptionCancellationRepo) Update(ctx context.Context, cancellation *domain.SubscriptionCancellation) error {
	return r.DB.WithContext(ctx).Save(cancellation).Error
}

func (r *SubscriptionCancellationRepo) WithTx(tx *gorm.DB) *SubscriptionCancellationRepo {
	return &SubscriptionCancellationRepo{DB: tx}
}
