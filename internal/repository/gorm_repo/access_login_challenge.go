package gorm_repo

import (
	"context"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AccessLoginChallengeRepository = (*AccessLoginChallengeRepo)(nil)

type AccessLoginChallengeRepo struct {
	DB *gorm.DB
}

func (r *AccessLoginChallengeRepo) Create(ctx context.Context, challenge *domain.AccessLoginChallenge) error {
	return r.DB.WithContext(ctx).Create(challenge).Error
}

func (r *AccessLoginChallengeRepo) GetByID(ctx context.Context, id string) (*domain.AccessLoginChallenge, error) {
	var challenge domain.AccessLoginChallenge
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&challenge).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &challenge, nil
}

func (r *AccessLoginChallengeRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.AccessLoginChallenge, error) {
	var challenge domain.AccessLoginChallenge
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&challenge).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &challenge, nil
}

func (r *AccessLoginChallengeRepo) Update(ctx context.Context, challenge *domain.AccessLoginChallenge) error {
	return r.DB.WithContext(ctx).Save(challenge).Error
}

func (r *AccessLoginChallengeRepo) ConsumePending(ctx context.Context, id string, consumedAt time.Time) (bool, error) {
	result := r.DB.WithContext(ctx).Model(&domain.AccessLoginChallenge{}).
		Where("id = ? AND status = ? AND consumed_at IS NULL", id, domain.AccessLoginChallengeStatusPending).
		Updates(map[string]any{
			"status":      domain.AccessLoginChallengeStatusConsumed,
			"consumed_at": consumedAt,
			"updated_at":  consumedAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *AccessLoginChallengeRepo) WithTx(tx *gorm.DB) *AccessLoginChallengeRepo {
	return &AccessLoginChallengeRepo{DB: tx}
}
