package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

// PaymentOrderEventProcessor updates Walnut order state from normalized payment
// events. Fulfillment can later decorate or compose this processor without
// putting entitlement or credits logic into webhook handlers.
type PaymentOrderEventProcessor struct {
	orders repository.OrderRepository
}

func NewPaymentOrderEventProcessor(orders repository.OrderRepository) *PaymentOrderEventProcessor {
	return &PaymentOrderEventProcessor{orders: orders}
}

func (p *PaymentOrderEventProcessor) ProcessPaymentEvent(ctx context.Context, event *domain.PaymentEventInbox) error {
	if p == nil || p.orders == nil || event == nil || event.OutTradeNo == "" {
		return ErrPaymentEventNotProcessable
	}
	order, err := p.orders.GetByOutTradeNo(ctx, event.OutTradeNo)
	if err != nil {
		return fmt.Errorf("order %q not found: %w", event.OutTradeNo, err)
	}
	if isPaymentPaidEvent(event.EventType) && event.Amount > 0 && order.Amount > 0 && event.Amount != order.Amount {
		return fmt.Errorf("%w: order=%d event=%d", ErrPaymentAmountMismatch, order.Amount, event.Amount)
	}
	if isPaymentPaidEvent(event.EventType) && !paymentCurrencyMatches(order.Currency, event.Currency) {
		return fmt.Errorf("%w: order=%s event=%s", ErrPaymentCurrencyMismatch, order.Currency, event.Currency)
	}

	now := time.Now().UTC()
	switch event.EventType {
	case domain.PaymentEventTypePaid, domain.PaymentEventTypeRenewalPaid:
		if order.Status == domain.OrderStatusPaid || order.Status == domain.OrderStatusFulfilled {
			return nil
		}
		order.Status = domain.OrderStatusPaid
		order.TradeNo = event.ProviderTradeNo
		order.Provider = event.Provider
		if order.PaidAt == nil {
			order.PaidAt = &now
		}
	case domain.PaymentEventTypeCancelled:
		if order.Status == domain.OrderStatusPaid || order.Status == domain.OrderStatusFulfilled {
			return nil
		}
		order.Status = domain.OrderStatusCancelled
	case domain.PaymentEventTypeRefunded, domain.PaymentEventTypeDisputed:
		order.Status = domain.OrderStatusRefunded
	case domain.PaymentEventTypeRenewalFailed:
		if order.Status == domain.OrderStatusPaid || order.Status == domain.OrderStatusFulfilled {
			return nil
		}
		order.Status = domain.OrderStatusFailed
	case domain.PaymentEventTypeSubscriptionExpired:
		if order.Status == domain.OrderStatusPending || order.Status == domain.OrderStatusCheckoutCreated {
			order.Status = domain.OrderStatusFailed
		}
	default:
		return ErrPaymentEventNotProcessable
	}
	return p.orders.Update(ctx, order)
}

func isPaymentPaidEvent(eventType string) bool {
	return eventType == domain.PaymentEventTypePaid || eventType == domain.PaymentEventTypeRenewalPaid
}

func paymentCurrencyMatches(orderCurrency string, eventCurrency string) bool {
	eventCurrency = strings.TrimSpace(eventCurrency)
	if eventCurrency == "" {
		return true
	}
	orderCurrency = strings.TrimSpace(orderCurrency)
	if orderCurrency == "" {
		return true
	}
	return strings.EqualFold(orderCurrency, eventCurrency)
}
