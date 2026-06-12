package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.ProductRepository = (*ProductRepo)(nil)

type ProductRepo struct {
	DB *gorm.DB
}

func (r *ProductRepo) Create(ctx context.Context, product *domain.Product) error {
	return r.DB.WithContext(ctx).Create(product).Error
}

func (r *ProductRepo) GetByCode(ctx context.Context, code string) (*domain.Product, error) {
	var product domain.Product
	err := r.DB.WithContext(ctx).Where("code = ?", code).First(&product).Error
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *ProductRepo) List(ctx context.Context, visibleOnly bool) ([]domain.Product, error) {
	var products []domain.Product
	query := r.DB.WithContext(ctx)
	if visibleOnly {
		query = query.Where("is_visible = ?", true)
	}
	err := query.Order("code").Find(&products).Error
	return products, err
}
