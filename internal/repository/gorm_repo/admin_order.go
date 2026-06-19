package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AdminOrderReadRepository = (*AdminOrderReadRepo)(nil)

// AdminOrderReadRepo builds a commerce operator projection without exposing
// checkout URLs, provider customer IDs, or raw webhook payloads.
type AdminOrderReadRepo struct {
	DB *gorm.DB
}

func (r *AdminOrderReadRepo) List(ctx context.Context, query repository.AdminOrderQuery) ([]repository.AdminOrderRecord, int64, error) {
	if r == nil || r.DB == nil {
		return nil, 0, repository.ErrNotFound
	}
	limit := normalizeAdminOrderLimit(query.Limit)
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	q := r.DB.WithContext(ctx).Model(&domain.Order{})
	q = applyAdminOrderFilters(q, query)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var orders []domain.Order
	if err := q.Order("paid_at DESC, fulfilled_at DESC, id DESC").Limit(limit).Offset(offset).Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	if len(orders) == 0 {
		return []repository.AdminOrderRecord{}, total, nil
	}
	outTradeNos := outTradeNosFromOrders(orders)
	eventsByOrder, latestEvents, err := adminPaymentEventStats(ctx, r.DB, outTradeNos)
	if err != nil {
		return nil, 0, err
	}
	fulfillmentStats, err := adminFulfillmentStats(ctx, r.DB, outTradeNos)
	if err != nil {
		return nil, 0, err
	}
	openRiskCounts, err := adminOpenRiskCounts(ctx, r.DB, outTradeNos)
	if err != nil {
		return nil, 0, err
	}
	records := make([]repository.AdminOrderRecord, 0, len(orders))
	for _, order := range orders {
		stats := fulfillmentStats[order.OutTradeNo]
		records = append(records, repository.AdminOrderRecord{
			Order:                  order,
			PaymentEventCount:      eventsByOrder[order.OutTradeNo],
			LatestPaymentEvent:     latestEvents[order.OutTradeNo],
			FulfillmentCount:       stats.total,
			FailedFulfillmentCount: stats.failed,
			OpenRiskFlagCount:      openRiskCounts[order.OutTradeNo],
		})
	}
	return records, total, nil
}

func applyAdminOrderFilters(q *gorm.DB, query repository.AdminOrderQuery) *gorm.DB {
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.SKUCode != "" {
		q = q.Where("sku_code = ?", query.SKUCode)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	if query.Provider != "" {
		q = q.Where("provider = ?", query.Provider)
	}
	if query.OrderType != "" {
		q = q.Where("order_type = ?", query.OrderType)
	}
	if query.OutTradeNo != "" {
		q = q.Where("out_trade_no = ?", query.OutTradeNo)
	}
	return q
}

func normalizeAdminOrderLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func adminPaymentEventStats(ctx context.Context, db *gorm.DB, outTradeNos []string) (map[string]int, map[string]*domain.PaymentEventInbox, error) {
	var events []domain.PaymentEventInbox
	if err := db.WithContext(ctx).Where("out_trade_no IN ?", outTradeNos).Order("received_at DESC").Find(&events).Error; err != nil {
		return nil, nil, err
	}
	counts := make(map[string]int, len(outTradeNos))
	latest := make(map[string]*domain.PaymentEventInbox, len(outTradeNos))
	for _, event := range events {
		counts[event.OutTradeNo]++
		if _, ok := latest[event.OutTradeNo]; !ok {
			copy := event
			latest[event.OutTradeNo] = &copy
		}
	}
	return counts, latest, nil
}

type adminFulfillmentStat struct {
	total  int
	failed int
}

func adminFulfillmentStats(ctx context.Context, db *gorm.DB, outTradeNos []string) (map[string]adminFulfillmentStat, error) {
	var executions []domain.FulfillmentExecution
	if err := db.WithContext(ctx).Where("out_trade_no IN ?", outTradeNos).Find(&executions).Error; err != nil {
		return nil, err
	}
	result := make(map[string]adminFulfillmentStat, len(outTradeNos))
	for _, execution := range executions {
		stat := result[execution.OutTradeNo]
		stat.total++
		if execution.Status == domain.FulfillmentExecutionStatusFailed {
			stat.failed++
		}
		result[execution.OutTradeNo] = stat
	}
	return result, nil
}

func adminOpenRiskCounts(ctx context.Context, db *gorm.DB, outTradeNos []string) (map[string]int, error) {
	var flags []domain.PaymentRiskFlag
	if err := db.WithContext(ctx).
		Where("out_trade_no IN ? AND status = ?", outTradeNos, domain.PaymentRiskStatusOpen).
		Find(&flags).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int, len(outTradeNos))
	for _, flag := range flags {
		result[flag.OutTradeNo]++
	}
	return result, nil
}
