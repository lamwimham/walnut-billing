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
}

func NewPaymentFulfillmentEventProcessor(
	orders repository.OrderRepository,
	orderEvents PaymentEventProcessor,
	fulfillment FulfillmentService,
) *PaymentFulfillmentEventProcessor {
	return &PaymentFulfillmentEventProcessor{orders: orders, orderEvents: orderEvents, fulfillment: fulfillment}
}

func (p *PaymentFulfillmentEventProcessor) ProcessPaymentEvent(ctx context.Context, event *domain.PaymentEventInbox) error {
	if p == nil || p.orderEvents == nil {
		return ErrPaymentEventNotProcessable
	}
	if err := p.orderEvents.ProcessPaymentEvent(ctx, event); err != nil {
		return err
	}
	if event == nil || event.EventType != domain.PaymentEventTypePaid {
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
