package service

import (
	"context"
	"fmt"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/generator"
	"walnut-billing/internal/metrics"
	"walnut-billing/internal/repository"
	"time"
)

// OrderService handles order creation and retrieval.
type OrderService interface {
	Create(ctx context.Context, productCode string) (*domain.Order, error)
	CreateRenewal(ctx context.Context, licenseKey string) (*domain.Order, error)
	GetByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.Order, error)
}

type orderServiceImpl struct {
	productRepo repository.ProductRepository
	licenseRepo repository.LicenseRepository
	orderRepo   repository.OrderRepository
	keyFactory  *generator.KeyGeneratorFactory
	uowFactory  func() repository.UnitOfWork
}

func NewOrderService(
	orderRepo repository.OrderRepository,
	productRepo repository.ProductRepository,
	licenseRepo repository.LicenseRepository,
	keyFactory *generator.KeyGeneratorFactory,
	uowFactory func() repository.UnitOfWork,
) OrderService {
	return &orderServiceImpl{
		orderRepo:   orderRepo,
		productRepo: productRepo,
		licenseRepo: licenseRepo,
		keyFactory:  keyFactory,
		uowFactory:  uowFactory,
	}
}

func (s *orderServiceImpl) Create(ctx context.Context, productCode string) (*domain.Order, error) {
	// 1. Validate product exists and get price (read-only, no transaction needed)
	product, err := s.productRepo.GetByCode(ctx, productCode)
	if err != nil {
		return nil, fmt.Errorf("product %q not found: %w", productCode, err)
	}
	if !product.IsVisible {
		return nil, fmt.Errorf("product %q is not available for purchase", productCode)
	}

	// 2. Generate license key
	gen, err := s.keyFactory.Get(productCode)
	if err != nil {
		return nil, fmt.Errorf("key generator for %q: %w", productCode, err)
	}
	key, err := gen.Generate(productCode)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	// 3. Build License record
	now := time.Now()
	lic := &domain.License{
		Key:      key,
		PlanCode: productCode,
		Status:   "inactive",
		Validity: product.Validity,
		MaxSeats: 1,
	}

	switch product.Validity {
	case "monthly":
		exp := now.AddDate(0, 1, 0)
		lic.ExpiresAt = &exp
	case "yearly":
		exp := now.AddDate(1, 0, 0)
		lic.ExpiresAt = &exp
	}

	// 4. Build Order
	order := &domain.Order{
		OutTradeNo: fmt.Sprintf("ORD-%d-%s", time.Now().UnixNano(), productCode),
		LicenseKey: key,
		Amount:     product.Price,
		Currency:   "CNY",
		Status:     "pending",
	}

	// 5. Execute in transaction (Order + License are atomic)
	uow := s.uowFactory()
	if err := uow.Begin(ctx); err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			uow.Rollback()
		}
	}()

	repos := uow.Repos()

	if err = repos.OrderRepo.Create(ctx, order); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	if err = repos.LicenseRepo.Create(ctx, lic); err != nil {
		return nil, fmt.Errorf("pre-create license: %w", err)
	}

	if err = uow.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// Record metrics
	metrics.OrdersCreatedTotal.Inc()

	return order, nil
}

func (s *orderServiceImpl) GetByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.Order, error) {
	return s.orderRepo.GetByOutTradeNo(ctx, outTradeNo)
}

func (s *orderServiceImpl) CreateRenewal(ctx context.Context, licenseKey string) (*domain.Order, error) {
	// 1. Look up existing license
	lic, err := s.licenseRepo.GetByKey(ctx, licenseKey)
	if err != nil {
		return nil, fmt.Errorf("license %q not found: %w", licenseKey, err)
	}

	// 2. Validate it's a subscription product
	if lic.Validity == "lifetime" {
		return nil, fmt.Errorf("lifetime license cannot be renewed")
	}

	// 3. Look up product for price
	product, err := s.productRepo.GetByCode(ctx, lic.PlanCode)
	if err != nil {
		return nil, fmt.Errorf("product %q not found: %w", lic.PlanCode, err)
	}

	// 4. Generate renewal order number
	order := &domain.Order{
		OutTradeNo: fmt.Sprintf("RNL-%d-%s", time.Now().UnixNano(), licenseKey),
		LicenseKey: licenseKey,
		Amount:     product.Price,
		Currency:   "CNY",
		Status:     "pending",
		OrderType:  domain.OrderTypeRenewal,
	}

	// 5. Execute in transaction
	uow := s.uowFactory()
	if err := uow.Begin(ctx); err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			uow.Rollback()
		}
	}()

	repos := uow.Repos()
	if err = repos.OrderRepo.Create(ctx, order); err != nil {
		return nil, fmt.Errorf("create renewal order: %w", err)
	}

	if err = uow.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	metrics.OrdersCreatedTotal.Inc()
	return order, nil
}
