package service

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

// PaymentFulfillmentEventProcessor decorates payment-order state transitions
// with commerce fulfillment. Webhook transport and payment providers remain
// unaware of entitlement grants or credit ledger mutations.
type PaymentFulfillmentEventProcessor struct {
	orders      repository.OrderRepository
	orderEvents PaymentEventProcessor
	fulfillment FulfillmentService
	adjustment  PaymentAdjustmentService
	renewal     SubscriptionRenewalService
}

func NewPaymentFulfillmentEventProcessor(
	orders repository.OrderRepository,
	orderEvents PaymentEventProcessor,
	fulfillment FulfillmentService,
) *PaymentFulfillmentEventProcessor {
	return NewPaymentFulfillmentEventProcessorWithAdjustments(orders, orderEvents, fulfillment, nil)
}

func NewPaymentFulfillmentEventProcessorWithAdjustments(
	orders repository.OrderRepository,
	orderEvents PaymentEventProcessor,
	fulfillment FulfillmentService,
	adjustment PaymentAdjustmentService,
) *PaymentFulfillmentEventProcessor {
	return NewPaymentFulfillmentEventProcessorWithPolicies(orders, orderEvents, fulfillment, adjustment, nil)
}

func NewPaymentFulfillmentEventProcessorWithPolicies(
	orders repository.OrderRepository,
	orderEvents PaymentEventProcessor,
	fulfillment FulfillmentService,
	adjustment PaymentAdjustmentService,
	renewal SubscriptionRenewalService,
) *PaymentFulfillmentEventProcessor {
	return &PaymentFulfillmentEventProcessor{orders: orders, orderEvents: orderEvents, fulfillment: fulfillment, adjustment: adjustment, renewal: renewal}
}

func (p *PaymentFulfillmentEventProcessor) ProcessPaymentEvent(ctx context.Context, event *domain.PaymentEventInbox) error {
	if p == nil {
		return ErrPaymentEventNotProcessable
	}
	if event == nil {
		return nil
	}
	if isSubscriptionRenewalEventType(event.EventType) {
		if p.renewal == nil {
			return ErrPaymentEventNotProcessable
		}
		_, err := p.renewal.Apply(ctx, event)
		return err
	}
	if p.orderEvents == nil {
		return ErrPaymentEventNotProcessable
	}
	if err := p.orderEvents.ProcessPaymentEvent(ctx, event); err != nil {
		return err
	}
	if event.EventType == domain.PaymentEventTypeRefunded || event.EventType == domain.PaymentEventTypeCancelled || event.EventType == domain.PaymentEventTypeDisputed {
		if p.adjustment == nil {
			return nil
		}
		_, err := p.adjustment.Apply(ctx, event)
		return err
	}
	if event.EventType != domain.PaymentEventTypePaid {
		return nil
	}
	if p.orders == nil || p.fulfillment == nil {
		return ErrPaymentEventNotProcessable
	}
	order, err := p.orders.GetByOutTradeNo(ctx, event.OutTradeNo)
	if err != nil {
		return err
	}
	if order.OrderType != domain.OrderTypeCheckout {
		return nil
	}
	_, err = p.fulfillment.FulfillOrder(ctx, order)
	return err
}
