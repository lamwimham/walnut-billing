package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

// Ensure implementation conforms to interface
var _ repository.LicenseRepository = (*LicenseRepo)(nil)
var _ repository.OrderRepository = (*OrderRepo)(nil)

type LicenseRepo struct {
	DB *gorm.DB
}

func (r *LicenseRepo) Create(ctx context.Context, license *domain.License) error {
	return r.DB.WithContext(ctx).Create(license).Error
}

func (r *LicenseRepo) GetByKey(ctx context.Context, key string) (*domain.License, error) {
	var license domain.License
	err := r.DB.WithContext(ctx).Where("key = ?", key).First(&license).Error
	if err != nil {
		return nil, err
	}
	return &license, nil
}

func (r *LicenseRepo) Update(ctx context.Context, license *domain.License) error {
	return r.DB.WithContext(ctx).Save(license).Error
}

func (r *LicenseRepo) List(ctx context.Context, status string) ([]domain.License, error) {
	var licenses []domain.License
	query := r.DB.WithContext(ctx)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Find(&licenses).Error
	return licenses, err
}

type OrderRepo struct {
	DB *gorm.DB
}

func (r *OrderRepo) Create(ctx context.Context, order *domain.Order) error {
	return r.DB.WithContext(ctx).Create(order).Error
}

func (r *OrderRepo) GetByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.Order, error) {
	var order domain.Order
	err := r.DB.WithContext(ctx).Where("out_trade_no = ?", outTradeNo).First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *OrderRepo) Update(ctx context.Context, order *domain.Order) error {
	return r.DB.WithContext(ctx).Save(order).Error
}

// WithTx returns new repository instances bound to the given transaction.
func (r *LicenseRepo) WithTx(tx *gorm.DB) *LicenseRepo {
	return &LicenseRepo{DB: tx}
}

func (r *OrderRepo) WithTx(tx *gorm.DB) *OrderRepo {
	return &OrderRepo{DB: tx}
}
