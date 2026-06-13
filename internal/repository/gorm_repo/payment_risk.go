package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.PaymentRiskFlagRepository = (*PaymentRiskFlagRepo)(nil)

type PaymentRiskFlagRepo struct {
	DB *gorm.DB
}

func (r *PaymentRiskFlagRepo) Create(ctx context.Context, flag *domain.PaymentRiskFlag) error {
	return r.DB.WithContext(ctx).Create(flag).Error
}

func (r *PaymentRiskFlagRepo) GetByID(ctx context.Context, id string) (*domain.PaymentRiskFlag, error) {
	var flag domain.PaymentRiskFlag
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&flag).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &flag, nil
}

func (r *PaymentRiskFlagRepo) GetByProviderEventID(ctx context.Context, provider string, providerEventID string) (*domain.PaymentRiskFlag, error) {
	var flag domain.PaymentRiskFlag
	if err := r.DB.WithContext(ctx).Where("provider = ? AND provider_event_id = ?", provider, providerEventID).First(&flag).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &flag, nil
}

func (r *PaymentRiskFlagRepo) List(ctx context.Context, query repository.PaymentRiskFlagQuery) ([]domain.PaymentRiskFlag, error) {
	var flags []domain.PaymentRiskFlag
	q := r.DB.WithContext(ctx).Model(&domain.PaymentRiskFlag{})
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.OutTradeNo != "" {
		q = q.Where("out_trade_no = ?", query.OutTradeNo)
	}
	if query.Provider != "" {
		q = q.Where("provider = ?", query.Provider)
	}
	if query.Reason != "" {
		q = q.Where("reason = ?", query.Reason)
	}
	if query.Severity != "" {
		q = q.Where("severity = ?", query.Severity)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	q = q.Order("created_at DESC")
	if query.Limit > 0 {
		q = q.Limit(query.Limit)
	}
	if query.Offset > 0 {
		q = q.Offset(query.Offset)
	}
	if err := q.Find(&flags).Error; err != nil {
		return nil, err
	}
	return flags, nil
}

func (r *PaymentRiskFlagRepo) Update(ctx context.Context, flag *domain.PaymentRiskFlag) error {
	return r.DB.WithContext(ctx).Save(flag).Error
}

func (r *PaymentRiskFlagRepo) WithTx(tx *gorm.DB) *PaymentRiskFlagRepo {
	return &PaymentRiskFlagRepo{DB: tx}
}
