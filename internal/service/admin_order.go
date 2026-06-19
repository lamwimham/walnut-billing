package service

import (
	"context"
	"errors"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var ErrInvalidAdminOrderQuery = errors.New("invalid admin order query")

const (
	defaultAdminOrderLimit = 50
	maxAdminOrderLimit     = 100
)

type AdminOrderService interface {
	ListOrders(ctx context.Context, query AdminOrderQuery) (*AdminOrderList, error)
}

type AdminOrderQuery struct {
	UserID     string
	SKUCode    string
	Status     string
	Provider   string
	OrderType  string
	OutTradeNo string
	Limit      int
	Offset     int
}

type AdminOrderList struct {
	Total  int64              `json:"total"`
	Orders []AdminOrderRecord `json:"orders"`
}

type AdminOrderRecord struct {
	OutTradeNo              string                 `json:"out_trade_no"`
	UserID                  string                 `json:"user_id"`
	SKUCode                 string                 `json:"sku_code"`
	Status                  string                 `json:"status"`
	Provider                string                 `json:"provider"`
	OrderType               string                 `json:"order_type"`
	Amount                  int64                  `json:"amount"`
	Currency                string                 `json:"currency"`
	PaidAt                  string                 `json:"paid_at,omitempty"`
	FulfilledAt             string                 `json:"fulfilled_at,omitempty"`
	HasCheckoutSession      bool                   `json:"has_checkout_session"`
	HasProviderCustomer     bool                   `json:"has_provider_customer"`
	HasProviderSubscription bool                   `json:"has_provider_subscription"`
	HasMetadata             bool                   `json:"has_metadata"`
	PaymentEventCount       int                    `json:"payment_event_count"`
	LatestPaymentEvent      AdminOrderPaymentEvent `json:"latest_payment_event,omitempty"`
	FulfillmentCount        int                    `json:"fulfillment_count"`
	FailedFulfillmentCount  int                    `json:"failed_fulfillment_count"`
	OpenRiskFlagCount       int                    `json:"open_risk_flag_count"`
}

type AdminOrderPaymentEvent struct {
	ID          string `json:"id,omitempty"`
	Provider    string `json:"provider,omitempty"`
	EventType   string `json:"event_type,omitempty"`
	Status      string `json:"status,omitempty"`
	PayloadHash string `json:"payload_hash,omitempty"`
	ReceivedAt  string `json:"received_at,omitempty"`
	ProcessedAt string `json:"processed_at,omitempty"`
}

type adminOrderService struct {
	orders repository.AdminOrderReadRepository
}

func NewAdminOrderService(orders repository.AdminOrderReadRepository) AdminOrderService {
	return &adminOrderService{orders: orders}
}

func (s *adminOrderService) ListOrders(ctx context.Context, query AdminOrderQuery) (*AdminOrderList, error) {
	if s == nil || s.orders == nil {
		return nil, ErrInvalidAdminOrderQuery
	}
	repoQuery := repository.AdminOrderQuery{
		UserID:     strings.TrimSpace(query.UserID),
		SKUCode:    strings.TrimSpace(query.SKUCode),
		Status:     strings.TrimSpace(query.Status),
		Provider:   strings.TrimSpace(query.Provider),
		OrderType:  strings.TrimSpace(query.OrderType),
		OutTradeNo: strings.TrimSpace(query.OutTradeNo),
		Limit:      normalizeAdminOrderQueryLimit(query.Limit),
		Offset:     maxInt(query.Offset, 0),
	}
	records, total, err := s.orders.List(ctx, repoQuery)
	if err != nil {
		return nil, err
	}
	orders := make([]AdminOrderRecord, 0, len(records))
	for _, record := range records {
		orders = append(orders, projectAdminOrder(record))
	}
	return &AdminOrderList{Total: total, Orders: orders}, nil
}

func projectAdminOrder(record repository.AdminOrderRecord) AdminOrderRecord {
	order := record.Order
	return AdminOrderRecord{
		OutTradeNo:              order.OutTradeNo,
		UserID:                  order.UserID,
		SKUCode:                 order.SKUCode,
		Status:                  order.Status,
		Provider:                order.Provider,
		OrderType:               defaultString(order.OrderType, domain.OrderTypeNew),
		Amount:                  order.Amount,
		Currency:                order.Currency,
		PaidAt:                  formatOptionalTime(order.PaidAt),
		FulfilledAt:             formatOptionalTime(order.FulfilledAt),
		HasCheckoutSession:      strings.TrimSpace(order.CheckoutURL) != "" || strings.TrimSpace(order.ProviderCheckoutID) != "",
		HasProviderCustomer:     strings.TrimSpace(order.ProviderCustomerID) != "",
		HasProviderSubscription: orderMetadataHasSubscription(order.Metadata),
		HasMetadata:             strings.TrimSpace(order.Metadata) != "",
		PaymentEventCount:       record.PaymentEventCount,
		LatestPaymentEvent:      projectAdminOrderPaymentEvent(record.LatestPaymentEvent),
		FulfillmentCount:        record.FulfillmentCount,
		FailedFulfillmentCount:  record.FailedFulfillmentCount,
		OpenRiskFlagCount:       record.OpenRiskFlagCount,
	}
}

func projectAdminOrderPaymentEvent(event *domain.PaymentEventInbox) AdminOrderPaymentEvent {
	if event == nil {
		return AdminOrderPaymentEvent{}
	}
	return AdminOrderPaymentEvent{
		ID:          event.ID,
		Provider:    event.Provider,
		EventType:   event.EventType,
		Status:      event.Status,
		PayloadHash: event.PayloadHash,
		ReceivedAt:  formatTime(event.ReceivedAt),
		ProcessedAt: formatOptionalTime(event.ProcessedAt),
	}
}

func normalizeAdminOrderQueryLimit(limit int) int {
	if limit <= 0 {
		return defaultAdminOrderLimit
	}
	if limit > maxAdminOrderLimit {
		return maxAdminOrderLimit
	}
	return limit
}

func orderMetadataHasSubscription(metadata string) bool {
	return strings.Contains(metadata, "walnut_provider_subscription_id") || strings.Contains(metadata, "provider_subscription_id")
}
