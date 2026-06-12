package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/generator"
	"walnut-billing/internal/repository"
)

// --- Mocks ---

type mockProductRepo struct {
	products map[string]*domain.Product
}

func newMockProductRepo() *mockProductRepo {
	return &mockProductRepo{
		products: make(map[string]*domain.Product),
	}
}

func (m *mockProductRepo) Create(ctx context.Context, p *domain.Product) error {
	m.products[p.Code] = p
	return nil
}

func (m *mockProductRepo) GetByCode(ctx context.Context, code string) (*domain.Product, error) {
	p, ok := m.products[code]
	if !ok {
		return nil, errors.New("product not found")
	}
	return p, nil
}

func (m *mockProductRepo) List(ctx context.Context, visibleOnly bool) ([]domain.Product, error) {
	var result []domain.Product
	for _, p := range m.products {
		if !visibleOnly || p.IsVisible {
			result = append(result, *p)
		}
	}
	return result, nil
}

type mockTxOrderRepo struct {
	orders map[string]*domain.Order
}

func newMockTxOrderRepo() *mockTxOrderRepo {
	return &mockTxOrderRepo{orders: make(map[string]*domain.Order)}
}

func (m *mockTxOrderRepo) Create(ctx context.Context, order *domain.Order) error {
	m.orders[order.OutTradeNo] = order
	return nil
}

func (m *mockTxOrderRepo) GetByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.Order, error) {
	o, ok := m.orders[outTradeNo]
	if !ok {
		return nil, errors.New("not found")
	}
	return o, nil
}

func (m *mockTxOrderRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	for _, order := range m.orders {
		if order.IdempotencyKey != nil && *order.IdempotencyKey == key {
			return order, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockTxOrderRepo) Update(ctx context.Context, order *domain.Order) error {
	m.orders[order.OutTradeNo] = order
	return nil
}

type mockTxLicenseRepo struct {
	licenses map[string]*domain.License
}

func newMockTxLicenseRepo() *mockTxLicenseRepo {
	return &mockTxLicenseRepo{licenses: make(map[string]*domain.License)}
}

func (m *mockTxLicenseRepo) Create(ctx context.Context, lic *domain.License) error {
	m.licenses[lic.Key] = lic
	return nil
}

func (m *mockTxLicenseRepo) GetByKey(ctx context.Context, key string) (*domain.License, error) {
	l, ok := m.licenses[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return l, nil
}

func (m *mockTxLicenseRepo) Update(ctx context.Context, lic *domain.License) error {
	m.licenses[lic.Key] = lic
	return nil
}

func (m *mockTxLicenseRepo) List(ctx context.Context, status string) ([]domain.License, error) {
	return nil, nil
}

type mockUnitOfWork struct {
	orderRepo   *mockTxOrderRepo
	licenseRepo *mockTxLicenseRepo
	commitErr   error
	beginErr    error
}

func (m *mockUnitOfWork) Begin(ctx context.Context) error {
	return m.beginErr
}

func (m *mockUnitOfWork) Repos() repository.TransactionalRepositories {
	return repository.TransactionalRepositories{
		OrderRepo:   m.orderRepo,
		LicenseRepo: m.licenseRepo,
	}
}

func (m *mockUnitOfWork) Commit() error {
	return m.commitErr
}

func (m *mockUnitOfWork) Rollback() error {
	return nil
}

// --- Tests ---

func TestOrderService_Create_Success(t *testing.T) {
	productRepo := newMockProductRepo()
	productRepo.products["pro"] = &domain.Product{
		Code:      "pro",
		Price:     3800,
		Validity:  "yearly",
		IsVisible: true,
	}

	orderRepo := newMockTxOrderRepo()
	licenseRepo := newMockTxLicenseRepo()
	keyFactory := generator.DefaultFactory()

	uow := &mockUnitOfWork{
		orderRepo:   newMockTxOrderRepo(),
		licenseRepo: newMockTxLicenseRepo(),
	}

	svc := NewOrderService(orderRepo, productRepo, licenseRepo, keyFactory, func() repository.UnitOfWork { return uow })

	order, err := svc.Create(context.Background(), "pro")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if order.Amount != 3800 {
		t.Errorf("expected amount 3800, got %d", order.Amount)
	}
	if order.Status != "pending" {
		t.Errorf("expected status 'pending', got %s", order.Status)
	}
	if order.Currency != "CNY" {
		t.Errorf("expected currency 'CNY', got %s", order.Currency)
	}
}

func TestOrderService_Create_ProductNotFound(t *testing.T) {
	productRepo := newMockProductRepo()
	orderRepo := newMockTxOrderRepo()
	licenseRepo := newMockTxLicenseRepo()
	keyFactory := generator.DefaultFactory()
	uow := &mockUnitOfWork{orderRepo: newMockTxOrderRepo(), licenseRepo: newMockTxLicenseRepo()}

	svc := NewOrderService(orderRepo, productRepo, licenseRepo, keyFactory, func() repository.UnitOfWork { return uow })

	_, err := svc.Create(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent product")
	}
}

func TestOrderService_Create_ProductNotVisible(t *testing.T) {
	productRepo := newMockProductRepo()
	productRepo.products["hidden"] = &domain.Product{
		Code:      "hidden",
		Price:     1000,
		IsVisible: false,
	}
	orderRepo := newMockTxOrderRepo()
	licenseRepo := newMockTxLicenseRepo()
	keyFactory := generator.DefaultFactory()
	uow := &mockUnitOfWork{orderRepo: newMockTxOrderRepo(), licenseRepo: newMockTxLicenseRepo()}

	svc := NewOrderService(orderRepo, productRepo, licenseRepo, keyFactory, func() repository.UnitOfWork { return uow })

	_, err := svc.Create(context.Background(), "hidden")
	if err == nil {
		t.Fatal("expected error for invisible product")
	}
}

func TestOrderService_GetByOutTradeNo(t *testing.T) {
	orderRepo := newMockTxOrderRepo()
	orderRepo.orders["ORD-TEST"] = &domain.Order{OutTradeNo: "ORD-TEST", Status: "pending"}

	svc := NewOrderService(orderRepo, nil, nil, nil, nil)

	order, err := svc.GetByOutTradeNo(context.Background(), "ORD-TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.OutTradeNo != "ORD-TEST" {
		t.Errorf("expected OutTradeNo 'ORD-TEST', got %s", order.OutTradeNo)
	}

	_, err = svc.GetByOutTradeNo(context.Background(), "NOT-FOUND")
	if err == nil {
		t.Error("expected error for missing order")
	}
}

func TestOrderService_CreateRenewal_Success(t *testing.T) {
	productRepo := newMockProductRepo()
	productRepo.products["sub_monthly"] = &domain.Product{
		Code:      "sub_monthly",
		Price:     1500,
		Validity:  "monthly",
		IsVisible: true,
	}

	licenseRepo := NewMockLicenseRepository()
	exp := time.Now().AddDate(0, 0, 7)
	licenseRepo.licenses["SM-SUB-0001-0001"] = &domain.License{
		Key:       "SM-SUB-0001-0001",
		PlanCode:  "sub_monthly",
		Status:    "active",
		Validity:  "monthly",
		ExpiresAt: &exp,
	}

	orderRepo := newMockTxOrderRepo()
	uow := &mockUnitOfWork{
		orderRepo:   newMockTxOrderRepo(),
		licenseRepo: newMockTxLicenseRepo(),
	}

	svc := NewOrderService(orderRepo, productRepo, licenseRepo, generator.DefaultFactory(), func() repository.UnitOfWork { return uow })

	order, err := svc.CreateRenewal(context.Background(), "SM-SUB-0001-0001")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if order.OrderType != domain.OrderTypeRenewal {
		t.Errorf("expected order type 'renewal', got %s", order.OrderType)
	}
	if order.Amount != 1500 {
		t.Errorf("expected amount 1500, got %d", order.Amount)
	}
	if order.LicenseKey != "SM-SUB-0001-0001" {
		t.Errorf("expected license key 'SM-SUB-0001-0001', got %s", order.LicenseKey)
	}
}

func TestOrderService_CreateRenewal_LifetimeLicense(t *testing.T) {
	productRepo := newMockProductRepo()
	licenseRepo := NewMockLicenseRepository()
	licenseRepo.licenses["SM-PRO-0001-0001"] = &domain.License{
		Key:      "SM-PRO-0001-0001",
		PlanCode: "pro",
		Status:   "active",
		Validity: "lifetime",
	}
	orderRepo := newMockTxOrderRepo()
	uow := &mockUnitOfWork{orderRepo: newMockTxOrderRepo(), licenseRepo: newMockTxLicenseRepo()}

	svc := NewOrderService(orderRepo, productRepo, licenseRepo, generator.DefaultFactory(), func() repository.UnitOfWork { return uow })

	_, err := svc.CreateRenewal(context.Background(), "SM-PRO-0001-0001")
	if err == nil {
		t.Fatal("expected error for lifetime license")
	}
}

func TestOrderService_CreateRenewal_LicenseNotFound(t *testing.T) {
	productRepo := newMockProductRepo()
	licenseRepo := NewMockLicenseRepository()
	orderRepo := newMockTxOrderRepo()
	uow := &mockUnitOfWork{orderRepo: newMockTxOrderRepo(), licenseRepo: newMockTxLicenseRepo()}

	svc := NewOrderService(orderRepo, productRepo, licenseRepo, generator.DefaultFactory(), func() repository.UnitOfWork { return uow })

	_, err := svc.CreateRenewal(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent license")
	}
}
