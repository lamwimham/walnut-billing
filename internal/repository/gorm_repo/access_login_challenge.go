package gorm_repo

import (
	"context"
	"strings"
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

func (r *AccessLoginChallengeRepo) Count(ctx context.Context, query repository.AccessLoginChallengeQuery) (int64, error) {
	db := r.DB.WithContext(ctx).Model(&domain.AccessLoginChallenge{})
	if email := strings.TrimSpace(query.Email); email != "" {
		db = db.Where("email = ?", email)
	}
	if clientIPHash := strings.TrimSpace(query.ClientIPHash); clientIPHash != "" {
		db = db.Where("client_ip_hash = ?", clientIPHash)
	}
	if !query.CreatedAfter.IsZero() {
		db = db.Where("created_at >= ?", query.CreatedAfter.UTC())
	}
	statuses := normalizeAccessLoginChallengeStatuses(query.Statuses)
	if len(statuses) > 0 {
		db = db.Where("status IN ?", statuses)
	}
	var count int64
	if err := db.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
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

func normalizeAccessLoginChallengeStatuses(statuses []string) []string {
	result := make([]string, 0, len(statuses))
	seen := map[string]bool{}
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status == "" || seen[status] {
			continue
		}
		seen[status] = true
		result = append(result, status)
	}
	return result
}
