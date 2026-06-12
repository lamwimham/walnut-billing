package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidCheckoutRequest = errors.New("invalid checkout request")
	ErrCheckoutProviderFailed = errors.New("checkout provider failed")
)

// CheckoutPaymentGateway is the narrow provider boundary used by CheckoutService.
// payment.PaymentService implements it, keeping commerce orchestration separate
// from provider-specific adapters.
type CheckoutPaymentGateway interface {
	CreateCheckoutSession(ctx context.Context, providerName string, req payment.CheckoutRequest) (*payment.CheckoutSession, error)
}

type CheckoutInput struct {
	UserID         string
	SKUCode        string
	Provider       string
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
	Metadata       map[string]string
}

type CheckoutResult struct {
	Order       *domain.Order            `json:"order"`
	CheckoutURL string                   `json:"checkout_url"`
	Provider    string                   `json:"provider"`
	Session     *payment.CheckoutSession `json:"session,omitempty"`
}

// CheckoutService is the commerce facade for app clients. It owns Walnut order
// creation and delegates only the external checkout-session creation to payment
// providers.
type CheckoutService interface {
	CreateCheckoutSession(ctx context.Context, input CheckoutInput) (*CheckoutResult, error)
}

type checkoutServiceImpl struct {
	orders   repository.OrderRepository
	products repository.ProductRepository
	users    repository.UserRepository
	gateway  CheckoutPaymentGateway
}

func NewCheckoutService(
	orders repository.OrderRepository,
	products repository.ProductRepository,
	users repository.UserRepository,
	gateway CheckoutPaymentGateway,
) CheckoutService {
	return &checkoutServiceImpl{
		orders:   orders,
		products: products,
		users:    users,
		gateway:  gateway,
	}
}

func (s *checkoutServiceImpl) CreateCheckoutSession(ctx context.Context, input CheckoutInput) (*CheckoutResult, error) {
	input = normalizeCheckoutInput(input)
	if input.UserID == "" || input.SKUCode == "" || input.Provider == "" || input.IdempotencyKey == "" {
		return nil, ErrInvalidCheckoutRequest
	}
	if s.orders == nil || s.products == nil || s.users == nil || s.gateway == nil {
		return nil, ErrInvalidCheckoutRequest
	}

	var order *domain.Order
	if existing, err := s.orders.GetByIdempotencyKey(ctx, input.IdempotencyKey); err == nil {
		if existing.UserID != input.UserID || existing.SKUCode != input.SKUCode || existing.Provider != input.Provider {
			return nil, ErrInvalidCheckoutRequest
		}
		if checkoutOrderHasSession(existing) {
			return checkoutResultFromOrder(existing), nil
		}
		order = existing
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	user, err := s.users.GetByID(ctx, input.UserID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if user.Status != "" && user.Status != domain.UserStatusActive {
		return nil, ErrUserNotFound
	}

	product, err := s.products.GetByCode(ctx, input.SKUCode)
	if err != nil {
		return nil, fmt.Errorf("product %q not found: %w", input.SKUCode, err)
	}
	if !product.IsVisible {
		return nil, fmt.Errorf("product %q is not available for purchase", input.SKUCode)
	}

	if order == nil {
		order = &domain.Order{
			OutTradeNo:     fmt.Sprintf("CHK-%d-%s", time.Now().UnixNano(), input.SKUCode),
			UserID:         input.UserID,
			SKUCode:        input.SKUCode,
			Amount:         product.Price,
			Currency:       defaultCheckoutCurrency(product),
			Status:         domain.OrderStatusPending,
			Provider:       input.Provider,
			IdempotencyKey: &input.IdempotencyKey,
			OrderType:      domain.OrderTypeCheckout,
			Metadata:       encodeCheckoutMetadata(input.Metadata),
		}
		if err := s.orders.Create(ctx, order); err != nil {
			return nil, err
		}
	}

	session, err := s.gateway.CreateCheckoutSession(ctx, input.Provider, payment.CheckoutRequest{
		OutTradeNo:     order.OutTradeNo,
		Amount:         order.Amount,
		Currency:       order.Currency,
		Description:    fmt.Sprintf("Walnut %s", product.Name),
		SuccessURL:     input.SuccessURL,
		CancelURL:      input.CancelURL,
		UserID:         input.UserID,
		CustomerEmail:  user.Email,
		CustomerName:   user.DisplayName,
		SKUCode:        input.SKUCode,
		IdempotencyKey: input.IdempotencyKey,
		Metadata:       input.Metadata,
	})
	if err != nil {
		order.Status = domain.OrderStatusFailed
		_ = s.orders.Update(ctx, order)
		return nil, fmt.Errorf("%w: %v", ErrCheckoutProviderFailed, err)
	}

	order.Status = defaultString(strings.TrimSpace(session.Status), domain.OrderStatusCheckoutCreated)
	order.CheckoutURL = strings.TrimSpace(session.CheckoutURL)
	order.ProviderCheckoutID = strings.TrimSpace(session.ProviderCheckoutID)
	order.ProviderCustomerID = strings.TrimSpace(session.ProviderCustomerID)
	if err := s.orders.Update(ctx, order); err != nil {
		return nil, err
	}

	return &CheckoutResult{
		Order:       order,
		CheckoutURL: order.CheckoutURL,
		Provider:    order.Provider,
		Session:     session,
	}, nil
}

func checkoutOrderHasSession(order *domain.Order) bool {
	if order == nil {
		return false
	}
	return strings.TrimSpace(order.CheckoutURL) != "" || order.Status == domain.OrderStatusPaid || order.Status == domain.OrderStatusFulfilled
}

func checkoutResultFromOrder(order *domain.Order) *CheckoutResult {
	return &CheckoutResult{
		Order:       order,
		CheckoutURL: order.CheckoutURL,
		Provider:    order.Provider,
		Session: &payment.CheckoutSession{
			CheckoutURL:        order.CheckoutURL,
			ProviderCheckoutID: order.ProviderCheckoutID,
			ProviderCustomerID: order.ProviderCustomerID,
			Status:             order.Status,
		},
	}
}

func normalizeCheckoutInput(input CheckoutInput) CheckoutInput {
	input.UserID = strings.TrimSpace(input.UserID)
	input.SKUCode = strings.TrimSpace(input.SKUCode)
	input.Provider = strings.TrimSpace(input.Provider)
	input.SuccessURL = strings.TrimSpace(input.SuccessURL)
	input.CancelURL = strings.TrimSpace(input.CancelURL)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input
}

func defaultCheckoutCurrency(product *domain.Product) string {
	if product != nil && strings.TrimSpace(product.Currency) != "" {
		return strings.ToUpper(strings.TrimSpace(product.Currency))
	}
	return "CNY"
}

func encodeCheckoutMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	cleaned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		cleaned[key] = strings.TrimSpace(value)
	}
	if len(cleaned) == 0 {
		return ""
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return ""
	}
	return string(data)
}
