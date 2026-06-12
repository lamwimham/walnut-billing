package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.FulfillmentExecutionRepository = (*FulfillmentExecutionRepo)(nil)

type FulfillmentExecutionRepo struct {
	DB *gorm.DB
}

func (r *FulfillmentExecutionRepo) Create(ctx context.Context, execution *domain.FulfillmentExecution) error {
	return r.DB.WithContext(ctx).Create(execution).Error
}

func (r *FulfillmentExecutionRepo) GetByID(ctx context.Context, id string) (*domain.FulfillmentExecution, error) {
	var execution domain.FulfillmentExecution
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&execution).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &execution, nil
}

func (r *FulfillmentExecutionRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.FulfillmentExecution, error) {
	var execution domain.FulfillmentExecution
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&execution).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &execution, nil
}

func (r *FulfillmentExecutionRepo) List(ctx context.Context, query repository.FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error) {
	var executions []domain.FulfillmentExecution
	q := r.DB.WithContext(ctx).Model(&domain.FulfillmentExecution{})
	if query.OrderID > 0 {
		q = q.Where("order_id = ?", query.OrderID)
	}
	if query.OutTradeNo != "" {
		q = q.Where("out_trade_no = ?", query.OutTradeNo)
	}
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.SKUCode != "" {
		q = q.Where("sku_code = ?", query.SKUCode)
	}
	if query.RuleID != "" {
		q = q.Where("rule_id = ?", query.RuleID)
	}
	if query.TargetType != "" {
		q = q.Where("target_type = ?", query.TargetType)
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
	if err := q.Find(&executions).Error; err != nil {
		return nil, err
	}
	return executions, nil
}

func (r *FulfillmentExecutionRepo) Update(ctx context.Context, execution *domain.FulfillmentExecution) error {
	return r.DB.WithContext(ctx).Save(execution).Error
}

func (r *FulfillmentExecutionRepo) WithTx(tx *gorm.DB) *FulfillmentExecutionRepo {
	return &FulfillmentExecutionRepo{DB: tx}
}
