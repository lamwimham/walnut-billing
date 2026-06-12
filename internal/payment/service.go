package payment

import (
	"context"
	"fmt"
	"log"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	"time"
)

// PaymentService orchestrates payment creation and callback processing.
type PaymentService struct {
	registry    *ProviderRegistry
	orderRepo   repository.OrderRepository
	licenseRepo repository.LicenseRepository
}

func NewPaymentService(
	orderRepo repository.OrderRepository,
	licenseRepo repository.LicenseRepository,
	registry *ProviderRegistry,
) *PaymentService {
	return &PaymentService{
		registry:    registry,
		orderRepo:   orderRepo,
		licenseRepo: licenseRepo,
	}
}

// GetProvider returns a payment provider by name.
func (s *PaymentService) GetProvider(name string) (PaymentProvider, error) {
	return s.registry.Get(name)
}

// Registry returns the underlying provider registry for hot-swap operations.
func (s *PaymentService) Registry() *ProviderRegistry {
	return s.registry
}

// ListProviders returns all registered provider names.
func (s *PaymentService) ListProviders() []string {
	return s.registry.List()
}

// GetProviderStatus returns the status of all registered providers.
func (s *PaymentService) GetProviderStatus() map[string]ProviderStatus {
	return s.registry.Status()
}

// CreatePayment generates a payment URL for an existing order.
func (s *PaymentService) CreatePayment(ctx context.Context, outTradeNo, providerName string) (string, error) {
	order, err := s.orderRepo.GetByOutTradeNo(ctx, outTradeNo)
	if err != nil {
		return "", fmt.Errorf("order %q not found: %w", outTradeNo, err)
	}
	if order.Status != "pending" {
		return "", fmt.Errorf("order %q is already %s", outTradeNo, order.Status)
	}

	provider, err := s.GetProvider(providerName)
	if err != nil {
		return "", err
	}

	order.Provider = providerName
	if err := s.orderRepo.Update(ctx, order); err != nil {
		return "", fmt.Errorf("update order provider: %w", err)
	}

	description := fmt.Sprintf("walnut License (%s)", order.LicenseKey)
	return provider.CreatePaymentURL(ctx, outTradeNo, order.Amount, description)
}

// HandleCallback processes a payment provider callback.
// Returns the success/failure response content type and body.
func (s *PaymentService) HandleCallback(ctx context.Context, providerName string, params map[string]string) (contentType string, body string, statusCode int) {
	provider, err := s.GetProvider(providerName)
	if err != nil {
		log.Printf("[payment] unknown provider: %s", providerName)
		return "text/plain", "bad request", 400
	}

	outTradeNo, providerTradeNo, _, err := provider.VerifyCallback(ctx, params)
	if err != nil {
		log.Printf("[payment] callback verification failed for %s: %v", providerName, err)
		ct, b := provider.BuildFailureResponse()
		return ct, b, 400
	}

	// Look up the order
	order, err := s.orderRepo.GetByOutTradeNo(ctx, outTradeNo)
	if err != nil {
		log.Printf("[payment] order %s not found: %v", outTradeNo, err)
		ct, b := provider.BuildFailureResponse()
		return ct, b, 400
	}

	if order.Status == "paid" {
		log.Printf("[payment] order %s already paid, idempotent", outTradeNo)
		ct, b := provider.BuildSuccessResponse()
		return ct, b, 200
	}

	// Mark order as paid
	order.Status = "paid"
	order.TradeNo = providerTradeNo
	now := time.Now()
	order.PaidAt = &now
	if err := s.orderRepo.Update(ctx, order); err != nil {
		log.Printf("[payment] failed to update order %s: %v", outTradeNo, err)
		ct, b := provider.BuildFailureResponse()
		return ct, b, 500
	}

	// Handle license based on order type
	if order.LicenseKey != "" {
		lic, err := s.licenseRepo.GetByKey(ctx, order.LicenseKey)
		if err != nil {
			log.Printf("[payment] license %s not found for order %s: %v", order.LicenseKey, outTradeNo, err)
		} else {
			if order.OrderType == domain.OrderTypeRenewal {
				// Renewal: extend expiry from now (or from current expiry if still valid)
				base := now
				if lic.ExpiresAt != nil && lic.ExpiresAt.After(now) {
					base = *lic.ExpiresAt
				}
				switch lic.Validity {
				case "monthly":
					newExp := base.AddDate(0, 1, 0)
					lic.ExpiresAt = &newExp
				case "yearly":
					newExp := base.AddDate(1, 0, 0)
					lic.ExpiresAt = &newExp
				}
				lic.Status = "active"
				log.Printf("[payment] license %s renewed, new expiry=%s", order.LicenseKey, lic.ExpiresAt.Format(time.RFC3339))
			} else {
				// New order: activate license
				lic.Status = "active"
				if err := s.licenseRepo.Update(ctx, lic); err != nil {
					log.Printf("[payment] failed to activate license %s: %v", order.LicenseKey, err)
				} else {
					log.Printf("[payment] license %s activated for order %s", order.LicenseKey, outTradeNo)
				}
			}
			if order.OrderType == domain.OrderTypeRenewal {
				if err := s.licenseRepo.Update(ctx, lic); err != nil {
					log.Printf("[payment] failed to update license %s: %v", order.LicenseKey, err)
				}
			}
		}
	}

	log.Printf("[payment] order %s marked as paid, provider=%s", outTradeNo, providerName)
	ct, b := provider.BuildSuccessResponse()
	return ct, b, 200
}
