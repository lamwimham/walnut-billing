package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.PaymentEventRepository = (*PaymentEventRepo)(nil)

type PaymentEventRepo struct {
	DB *gorm.DB
}

func (r *PaymentEventRepo) Create(ctx context.Context, event *domain.PaymentEventInbox) error {
	return r.DB.WithContext(ctx).Create(event).Error
}

func (r *PaymentEventRepo) GetByID(ctx context.Context, id string) (*domain.PaymentEventInbox, error) {
	var event domain.PaymentEventInbox
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&event).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &event, nil
}

func (r *PaymentEventRepo) GetByProviderEventID(ctx context.Context, provider string, providerEventID string) (*domain.PaymentEventInbox, error) {
	var event domain.PaymentEventInbox
	if err := r.DB.WithContext(ctx).
		Where("provider = ? AND provider_event_id = ?", provider, providerEventID).
		First(&event).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &event, nil
}

func (r *PaymentEventRepo) List(ctx context.Context, query repository.PaymentEventQuery) ([]domain.PaymentEventInbox, error) {
	var events []domain.PaymentEventInbox
	q := r.DB.WithContext(ctx).Model(&domain.PaymentEventInbox{})
	if query.Provider != "" {
		q = q.Where("provider = ?", query.Provider)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	if query.EventType != "" {
		q = q.Where("event_type = ?", query.EventType)
	}
	if query.OutTradeNo != "" {
		q = q.Where("out_trade_no = ?", query.OutTradeNo)
	}
	q = q.Order("received_at DESC")
	if query.Limit > 0 {
		q = q.Limit(query.Limit)
	}
	if query.Offset > 0 {
		q = q.Offset(query.Offset)
	}
	if err := q.Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (r *PaymentEventRepo) Update(ctx context.Context, event *domain.PaymentEventInbox) error {
	return r.DB.WithContext(ctx).Save(event).Error
}
