package payment

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

// MockPaymentProvider implements PaymentProvider for testing.
type MockPaymentProvider struct {
	NameStr     string
	PaymentURL  string
	CreateError error
	VerifyError error
}

func (m *MockPaymentProvider) Name() string {
	return m.NameStr
}

func (m *MockPaymentProvider) CreatePaymentURL(ctx context.Context, outTradeNo string, amount int64, description string) (string, error) {
	if m.CreateError != nil {
		return "", m.CreateError
	}
	return fmt.Sprintf("%s-%s", m.PaymentURL, outTradeNo), nil
}

func (m *MockPaymentProvider) VerifyCallback(ctx context.Context, params map[string]string) (outTradeNo, providerTradeNo string, amount int64, err error) {
	if m.VerifyError != nil {
		return "", "", 0, m.VerifyError
	}
	return params["out_trade_no"], params["transaction_id"], 0, nil
}

func (m *MockPaymentProvider) BuildSuccessResponse() (contentType string, body string) {
	return "application/json", `{"status":"ok"}`
}

func (m *MockPaymentProvider) BuildFailureResponse() (contentType string, body string) {
	return "application/json", `{"status":"fail"}`
}

// Mock repositories for payment testing
type mockLicenseRepo struct {
	licenses map[string]*domain.License
}

func newMockLicenseRepo() *mockLicenseRepo {
	return &mockLicenseRepo{licenses: make(map[string]*domain.License)}
}

func (m *mockLicenseRepo) Create(ctx context.Context, license *domain.License) error {
	m.licenses[license.Key] = license
	return nil
}

func (m *mockLicenseRepo) GetByKey(ctx context.Context, key string) (*domain.License, error) {
	lic, ok := m.licenses[key]
	if !ok {
		return nil, fmt.Errorf("record not found")
	}
	return lic, nil
}

func (m *mockLicenseRepo) Update(ctx context.Context, license *domain.License) error {
	m.licenses[license.Key] = license
	return nil
}

func (m *mockLicenseRepo) List(ctx context.Context, status string) ([]domain.License, error) {
	var result []domain.License
	for _, lic := range m.licenses {
		if status == "" || lic.Status == status {
			result = append(result, *lic)
		}
	}
	return result, nil
}

func (m *mockLicenseRepo) WithTx(tx interface{}) repository.LicenseRepository {
	return m
}

type mockOrderRepo struct {
	orders map[string]*domain.Order
}

func newMockOrderRepo() *mockOrderRepo {
	return &mockOrderRepo{orders: make(map[string]*domain.Order)}
}

func (m *mockOrderRepo) Create(ctx context.Context, order *domain.Order) error {
	m.orders[order.OutTradeNo] = order
	return nil
}

func (m *mockOrderRepo) GetByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.Order, error) {
	order, ok := m.orders[outTradeNo]
	if !ok {
		return nil, fmt.Errorf("record not found")
	}
	return order, nil
}

func (m *mockOrderRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error) {
	for _, order := range m.orders {
		if order.IdempotencyKey != nil && *order.IdempotencyKey == key {
			return order, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockOrderRepo) FindLatestSubscriptionOrder(ctx context.Context, query repository.SubscriptionOrderQuery) (*domain.Order, error) {
	var selected *domain.Order
	for _, order := range m.orders {
		if order.UserID != query.UserID || order.SKUCode != query.SKUCode {
			continue
		}
		if order.OrderType != domain.OrderTypeCheckout && order.OrderType != domain.OrderTypeRenewal {
			continue
		}
		if selected == nil || order.ID > selected.ID {
			selected = order
		}
	}
	if selected == nil {
		return nil, repository.ErrNotFound
	}
	return selected, nil
}

func (m *mockOrderRepo) Update(ctx context.Context, order *domain.Order) error {
	m.orders[order.OutTradeNo] = order
	return nil
}

func TestPaymentService_CreatePayment(t *testing.T) {
	orderRepo := newMockOrderRepo()
	orderRepo.orders["ORD-TEST-001"] = &domain.Order{
		OutTradeNo: "ORD-TEST-001",
		LicenseKey: "SM-PRO-0001-0001",
		Amount:     12800,
		Status:     "pending",
	}

	licRepo := newMockLicenseRepo()
	licRepo.licenses["SM-PRO-0001-0001"] = &domain.License{
		Key:    "SM-PRO-0001-0001",
		Status: "inactive",
	}

	provider := &MockPaymentProvider{
		NameStr:    "mock",
		PaymentURL: "mock://pay",
	}

	registry := NewProviderRegistry()
	registry.Register("mock", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	payURL, err := svc.CreatePayment(context.Background(), "ORD-TEST-001", "mock")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if payURL != "mock://pay-ORD-TEST-001" {
		t.Errorf("expected mock://pay-ORD-TEST-001, got %s", payURL)
	}

	// Verify order provider was set
	order := orderRepo.orders["ORD-TEST-001"]
	if order.Provider != "mock" {
		t.Errorf("expected provider 'mock', got %s", order.Provider)
	}
}

func TestPaymentService_CreatePayment_OrderNotFound(t *testing.T) {
	orderRepo := newMockOrderRepo()
	licRepo := newMockLicenseRepo()
	provider := &MockPaymentProvider{NameStr: "mock", PaymentURL: "mock://pay"}
	registry := NewProviderRegistry()
	registry.Register("mock", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	_, err := svc.CreatePayment(context.Background(), "nonexistent", "mock")
	if err == nil {
		t.Fatal("expected error for nonexistent order")
	}
}

func TestPaymentService_CreatePayment_AlreadyPaid(t *testing.T) {
	orderRepo := newMockOrderRepo()
	orderRepo.orders["ORD-TEST-002"] = &domain.Order{
		OutTradeNo: "ORD-TEST-002",
		Status:     "paid",
	}

	licRepo := newMockLicenseRepo()
	provider := &MockPaymentProvider{NameStr: "mock", PaymentURL: "mock://pay"}
	registry := NewProviderRegistry()
	registry.Register("mock", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	_, err := svc.CreatePayment(context.Background(), "ORD-TEST-002", "mock")
	if err == nil {
		t.Fatal("expected error for already paid order")
	}
}

func TestPaymentService_HandleCallback_Success(t *testing.T) {
	orderRepo := newMockOrderRepo()
	orderRepo.orders["ORD-TEST-003"] = &domain.Order{
		OutTradeNo: "ORD-TEST-003",
		LicenseKey: "SM-PRO-0003-0003",
		Status:     "pending",
	}

	licRepo := newMockLicenseRepo()
	licRepo.licenses["SM-PRO-0003-0003"] = &domain.License{
		Key:    "SM-PRO-0003-0003",
		Status: "inactive",
	}

	provider := &MockPaymentProvider{
		NameStr:     "mock",
		VerifyError: nil,
	}

	registry := NewProviderRegistry()
	registry.Register("mock", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	params := map[string]string{
		"out_trade_no":   "ORD-TEST-003",
		"transaction_id": "TXN-003",
	}

	contentType, body, status := svc.HandleCallback(context.Background(), "mock", params)

	if status != 200 {
		t.Errorf("expected status 200, got %d", status)
	}
	if contentType != "application/json" {
		t.Errorf("expected content type application/json, got %s", contentType)
	}
	if body != `{"status":"ok"}` {
		t.Errorf("expected body {\"status\":\"ok\"}, got %s", body)
	}

	// Verify order status
	order := orderRepo.orders["ORD-TEST-003"]
	if order.Status != "paid" {
		t.Errorf("expected order status 'paid', got %s", order.Status)
	}
	if order.TradeNo != "TXN-003" {
		t.Errorf("expected TradeNo 'TXN-003', got %s", order.TradeNo)
	}

	// Verify license status
	lic := licRepo.licenses["SM-PRO-0003-0003"]
	if lic.Status != "active" {
		t.Errorf("expected license status 'active', got %s", lic.Status)
	}
}

func TestPaymentService_HandleCallback_UnknownProvider(t *testing.T) {
	orderRepo := newMockOrderRepo()
	licRepo := newMockLicenseRepo()
	registry := NewProviderRegistry() // Empty registry
	svc := NewPaymentService(orderRepo, licRepo, registry)

	_, _, status := svc.HandleCallback(context.Background(), "unknown", nil)
	if status != 400 {
		t.Errorf("expected status 400, got %d", status)
	}
}

func TestPaymentService_HandleCallback_Renewal(t *testing.T) {
	orderRepo := newMockOrderRepo()
	exp := time.Now().AddDate(0, 0, -1) // expired yesterday
	licRepo := &mockLicenseRepo{
		licenses: map[string]*domain.License{
			"SM-SUB-0030-0030": {
				Key:       "SM-SUB-0030-0030",
				Status:    "active",
				Validity:  "monthly",
				ExpiresAt: &exp,
			},
		},
	}
	provider := &MockPaymentProvider{NameStr: "mock", PaymentURL: "mock://pay"}
	registry := NewProviderRegistry()
	registry.Register("mock", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	orderRepo.orders["RNL-RENEWAL-001"] = &domain.Order{
		OutTradeNo: "RNL-RENEWAL-001",
		LicenseKey: "SM-SUB-0030-0030",
		Status:     "pending",
		OrderType:  domain.OrderTypeRenewal,
		Amount:     1500,
	}

	contentType, _, statusCode := svc.HandleCallback(context.Background(), "mock", map[string]string{
		"out_trade_no":   "RNL-RENEWAL-001",
		"transaction_id": "PROV-001",
	})
	if statusCode != 200 {
		t.Errorf("expected status 200, got %d", statusCode)
	}
	if contentType != "application/json" {
		t.Errorf("expected content type application/json, got %s", contentType)
	}

	lic := licRepo.licenses["SM-SUB-0030-0030"]
	if lic.Status != "active" {
		t.Errorf("expected status 'active', got %s", lic.Status)
	}
	if lic.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}
	// For expired licenses, renewal extends from now (not old expiry)
	// So expect expiry = now + 1 month
	now := time.Now()
	expectedMonth := now.AddDate(0, 1, 0)
	diff := lic.ExpiresAt.Sub(expectedMonth)
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("expected expiry around %s, got %s", expectedMonth, lic.ExpiresAt)
	}
}

func TestPaymentService_CreateCheckoutSession_UsesCheckoutProvider(t *testing.T) {
	orderRepo := newMockOrderRepo()
	licRepo := newMockLicenseRepo()
	provider := NewCheckoutMockAdapter("http://localhost/callbacks/mock")
	registry := NewProviderRegistry()
	registry.Register("mock", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	session, err := svc.CreateCheckoutSession(context.Background(), "mock", CheckoutRequest{
		OutTradeNo:     "CHK-TEST-001",
		Amount:         1900,
		Currency:       "CNY",
		Description:    "Walnut Editorial Studio",
		UserID:         "usr_1",
		SKUCode:        "editorial_studio_monthly",
		IdempotencyKey: "checkout:1",
	})
	if err != nil {
		t.Fatalf("expected checkout session, got %v", err)
	}
	if session.CheckoutURL == "" || session.ProviderCheckoutID == "" {
		t.Fatalf("expected hosted checkout session, got %#v", session)
	}
}

func TestPaymentService_CreateCheckoutSession_AdaptsLegacyPaymentProvider(t *testing.T) {
	orderRepo := newMockOrderRepo()
	licRepo := newMockLicenseRepo()
	provider := &MockPaymentProvider{NameStr: "legacy", PaymentURL: "legacy://pay"}
	registry := NewProviderRegistry()
	registry.Register("legacy", provider, ProviderStatus{IsMock: true})
	svc := NewPaymentService(orderRepo, licRepo, registry)

	session, err := svc.CreateCheckoutSession(context.Background(), "legacy", CheckoutRequest{
		OutTradeNo:  "CHK-LEGACY-001",
		Amount:      990,
		Currency:    "CNY",
		Description: "Walnut Credits",
	})
	if err != nil {
		t.Fatalf("expected adapted checkout session, got %v", err)
	}
	if session.CheckoutURL != "legacy://pay-CHK-LEGACY-001" {
		t.Fatalf("expected legacy payment URL, got %s", session.CheckoutURL)
	}
	if session.Status != "checkout_created" {
		t.Fatalf("expected checkout_created, got %s", session.Status)
	}
}

func TestCheckoutMockAdapter_UsesConfiguredBaseURLAndCheckoutContext(t *testing.T) {
	provider := NewCheckoutMockAdapterWithBaseURL("http://localhost/callbacks/mock", "http://127.0.0.1:8082")

	session, err := provider.CreateCheckoutSession(context.Background(), CheckoutRequest{
		OutTradeNo: "CHK-LOCAL-001",
		SuccessURL: "walnut://checkout/success",
		CancelURL:  "walnut://checkout/cancel",
		UserID:     "usr_1",
		SKUCode:    "pro_own_ai_monthly",
	})
	if err != nil {
		t.Fatalf("create checkout session: %v", err)
	}
	if session.CheckoutURL == "" || !strings.HasPrefix(session.CheckoutURL, "http://127.0.0.1:8082/checkout/CHK-LOCAL-001") {
		t.Fatalf("expected configured local checkout url, got %s", session.CheckoutURL)
	}
	if !strings.Contains(session.CheckoutURL, "success_url=walnut%3A%2F%2Fcheckout%2Fsuccess") || !strings.Contains(session.CheckoutURL, "sku_code=pro_own_ai_monthly") {
		t.Fatalf("expected checkout context query in url, got %s", session.CheckoutURL)
	}
}
