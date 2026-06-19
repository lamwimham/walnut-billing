package gorm_repo

import (
	"context"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AdminSubscriptionReadRepository = (*AdminSubscriptionReadRepo)(nil)

// AdminSubscriptionReadRepo builds Walnut-owned subscription candidate facts.
// Provider payloads remain in payment-event storage and are only surfaced as
// payload hashes by service projections.
type AdminSubscriptionReadRepo struct {
	DB *gorm.DB
}

func (r *AdminSubscriptionReadRepo) List(ctx context.Context, query repository.AdminSubscriptionQuery) (*repository.AdminSubscriptionReadModel, error) {
	if r == nil || r.DB == nil {
		return nil, repository.ErrNotFound
	}
	candidateRows, err := r.listCandidates(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(candidateRows) == 0 {
		return &repository.AdminSubscriptionReadModel{Records: []repository.AdminSubscriptionRecord{}}, nil
	}

	userIDs := adminSubscriptionUserIDs(candidateRows)
	usersByID, err := adminSubscriptionUsersByID(ctx, r.DB, userIDs)
	if err != nil {
		return nil, err
	}
	grantsByUser, err := adminSubscriptionGrantsByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, err
	}
	cancellationsByUserSKU, err := adminSubscriptionLatestCancellations(ctx, r.DB, userIDs)
	if err != nil {
		return nil, err
	}
	ordersByUserSKU, err := adminSubscriptionLatestOrders(ctx, r.DB, candidateRows, query)
	if err != nil {
		return nil, err
	}
	outTradeNos := adminSubscriptionOutTradeNos(ordersByUserSKU)
	eventsByOrder, err := adminSubscriptionEventsByOrder(ctx, r.DB, outTradeNos)
	if err != nil {
		return nil, err
	}

	records := make([]repository.AdminSubscriptionRecord, 0, len(candidateRows))
	for _, candidate := range candidateRows {
		user, ok := usersByID[candidate.UserID]
		if !ok {
			continue
		}
		key := adminSubscriptionKey(candidate.UserID, candidate.SKUCode)
		order := ordersByUserSKU[key]
		var events []domain.PaymentEventInbox
		if order != nil {
			events = eventsByOrder[order.OutTradeNo]
		}
		records = append(records, repository.AdminSubscriptionRecord{
			User:               user,
			SKUCode:            candidate.SKUCode,
			Grants:             grantsByUser[candidate.UserID],
			LatestOrder:        order,
			LatestCancellation: cancellationsByUserSKU[key],
			PaymentEvents:      events,
		})
	}
	return &repository.AdminSubscriptionReadModel{Records: records}, nil
}

type adminSubscriptionCandidate struct {
	UserID  string
	SKUCode string
	SortAt  string
}

func (r *AdminSubscriptionReadRepo) listCandidates(ctx context.Context, query repository.AdminSubscriptionQuery) ([]adminSubscriptionCandidate, error) {
	candidates := map[string]adminSubscriptionCandidate{}
	orderCandidates, err := adminSubscriptionCandidatesFromOrders(ctx, r.DB, query)
	if err != nil {
		return nil, err
	}
	for _, candidate := range orderCandidates {
		mergeAdminSubscriptionCandidate(candidates, candidate)
	}
	grantCandidates, err := adminSubscriptionCandidatesFromGrants(ctx, r.DB, query)
	if err != nil {
		return nil, err
	}
	for _, candidate := range grantCandidates {
		mergeAdminSubscriptionCandidate(candidates, candidate)
	}
	cancellationCandidates, err := adminSubscriptionCandidatesFromCancellations(ctx, r.DB, query)
	if err != nil {
		return nil, err
	}
	for _, candidate := range cancellationCandidates {
		mergeAdminSubscriptionCandidate(candidates, candidate)
	}
	result := make([]adminSubscriptionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate)
	}
	sortAdminSubscriptionCandidates(result)
	return result, nil
}

func adminSubscriptionCandidatesFromOrders(ctx context.Context, db *gorm.DB, query repository.AdminSubscriptionQuery) ([]adminSubscriptionCandidate, error) {
	q := db.WithContext(ctx).Model(&domain.Order{}).
		Where("user_id <> ''").
		Where("order_type IN ?", []string{domain.OrderTypeCheckout, domain.OrderTypeRenewal})
	q = applyAdminSubscriptionOrderFilters(q, query)
	var orders []domain.Order
	if err := q.Order("paid_at DESC, fulfilled_at DESC, id DESC").Find(&orders).Error; err != nil {
		return nil, err
	}
	result := make([]adminSubscriptionCandidate, 0, len(orders))
	for _, order := range orders {
		if strings.TrimSpace(order.UserID) == "" || !adminSubscriptionSKUAllowed(order.SKUCode, query.SKUCode) {
			continue
		}
		result = append(result, adminSubscriptionCandidate{UserID: order.UserID, SKUCode: order.SKUCode, SortAt: adminSubscriptionOrderSortAt(order)})
	}
	return result, nil
}

func adminSubscriptionCandidatesFromGrants(ctx context.Context, db *gorm.DB, query repository.AdminSubscriptionQuery) ([]adminSubscriptionCandidate, error) {
	if strings.TrimSpace(query.Provider) != "" || strings.TrimSpace(query.OutTradeNo) != "" {
		return nil, nil
	}
	q := db.WithContext(ctx).Model(&domain.EntitlementGrant{}).
		Where("user_id <> ''").
		Where("source = ?", domain.GrantSourceFulfillment)
	if strings.TrimSpace(query.UserID) != "" {
		q = q.Where("user_id = ?", strings.TrimSpace(query.UserID))
	}
	var grants []domain.EntitlementGrant
	if err := q.Order("starts_at DESC, created_at DESC").Find(&grants).Error; err != nil {
		return nil, err
	}
	result := make([]adminSubscriptionCandidate, 0, len(grants))
	for _, grant := range grants {
		if !adminSubscriptionEntitlement(grant.EntitlementID) {
			continue
		}
		skuCode := adminSubscriptionSKUFromGrant(grant)
		if !adminSubscriptionSKUAllowed(skuCode, query.SKUCode) {
			continue
		}
		result = append(result, adminSubscriptionCandidate{
			UserID:  grant.UserID,
			SKUCode: skuCode,
			SortAt:  grant.StartsAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return result, nil
}

func adminSubscriptionCandidatesFromCancellations(ctx context.Context, db *gorm.DB, query repository.AdminSubscriptionQuery) ([]adminSubscriptionCandidate, error) {
	if strings.TrimSpace(query.Provider) != "" || strings.TrimSpace(query.OutTradeNo) != "" {
		return nil, nil
	}
	q := db.WithContext(ctx).Model(&domain.SubscriptionCancellation{}).Where("user_id <> ''")
	if strings.TrimSpace(query.UserID) != "" {
		q = q.Where("user_id = ?", strings.TrimSpace(query.UserID))
	}
	if strings.TrimSpace(query.SKUCode) != "" {
		q = q.Where("sku_code = ?", strings.TrimSpace(query.SKUCode))
	}
	var cancellations []domain.SubscriptionCancellation
	if err := q.Order("updated_at DESC, created_at DESC").Find(&cancellations).Error; err != nil {
		return nil, err
	}
	result := make([]adminSubscriptionCandidate, 0, len(cancellations))
	for _, cancellation := range cancellations {
		if !adminSubscriptionSKUAllowed(cancellation.SKUCode, query.SKUCode) {
			continue
		}
		result = append(result, adminSubscriptionCandidate{
			UserID:  cancellation.UserID,
			SKUCode: cancellation.SKUCode,
			SortAt:  cancellation.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return result, nil
}

func applyAdminSubscriptionOrderFilters(q *gorm.DB, query repository.AdminSubscriptionQuery) *gorm.DB {
	if strings.TrimSpace(query.UserID) != "" {
		q = q.Where("user_id = ?", strings.TrimSpace(query.UserID))
	}
	if strings.TrimSpace(query.SKUCode) != "" {
		q = q.Where("sku_code = ?", strings.TrimSpace(query.SKUCode))
	}
	if strings.TrimSpace(query.Provider) != "" {
		q = q.Where("provider = ?", strings.TrimSpace(query.Provider))
	}
	if strings.TrimSpace(query.OutTradeNo) != "" {
		q = q.Where("out_trade_no = ?", strings.TrimSpace(query.OutTradeNo))
	}
	return q
}

func adminSubscriptionUsersByID(ctx context.Context, db *gorm.DB, userIDs []string) (map[string]domain.User, error) {
	var users []domain.User
	if err := db.WithContext(ctx).Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}
	result := make(map[string]domain.User, len(users))
	for _, user := range users {
		result[user.ID] = user
	}
	return result, nil
}

func adminSubscriptionGrantsByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string][]domain.EntitlementGrant, error) {
	var grants []domain.EntitlementGrant
	if err := db.WithContext(ctx).
		Where("user_id IN ?", userIDs).
		Where("source = ?", domain.GrantSourceFulfillment).
		Order("starts_at DESC, created_at DESC").
		Find(&grants).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.EntitlementGrant, len(userIDs))
	for _, grant := range grants {
		result[grant.UserID] = append(result[grant.UserID], grant)
	}
	return result, nil
}

func adminSubscriptionLatestCancellations(ctx context.Context, db *gorm.DB, userIDs []string) (map[string]*domain.SubscriptionCancellation, error) {
	var cancellations []domain.SubscriptionCancellation
	if err := db.WithContext(ctx).
		Where("user_id IN ?", userIDs).
		Order("updated_at DESC, created_at DESC").
		Find(&cancellations).Error; err != nil {
		return nil, err
	}
	result := make(map[string]*domain.SubscriptionCancellation, len(cancellations))
	for _, cancellation := range cancellations {
		key := adminSubscriptionKey(cancellation.UserID, cancellation.SKUCode)
		if _, ok := result[key]; ok {
			continue
		}
		copy := cancellation
		result[key] = &copy
	}
	return result, nil
}

func adminSubscriptionLatestOrders(ctx context.Context, db *gorm.DB, candidates []adminSubscriptionCandidate, query repository.AdminSubscriptionQuery) (map[string]*domain.Order, error) {
	userIDs := adminSubscriptionUserIDs(candidates)
	q := db.WithContext(ctx).Model(&domain.Order{}).
		Where("user_id IN ?", userIDs).
		Where("order_type IN ?", []string{domain.OrderTypeCheckout, domain.OrderTypeRenewal})
	q = applyAdminSubscriptionOrderFilters(q, query)
	var orders []domain.Order
	if err := q.Order("paid_at DESC, fulfilled_at DESC, id DESC").Find(&orders).Error; err != nil {
		return nil, err
	}
	result := make(map[string]*domain.Order, len(candidates))
	for _, order := range orders {
		key := adminSubscriptionKey(order.UserID, order.SKUCode)
		if _, ok := result[key]; ok {
			continue
		}
		copy := order
		result[key] = &copy
	}
	return result, nil
}

func adminSubscriptionEventsByOrder(ctx context.Context, db *gorm.DB, outTradeNos []string) (map[string][]domain.PaymentEventInbox, error) {
	if len(outTradeNos) == 0 {
		return map[string][]domain.PaymentEventInbox{}, nil
	}
	var events []domain.PaymentEventInbox
	if err := db.WithContext(ctx).
		Where("out_trade_no IN ?", outTradeNos).
		Order("received_at DESC").
		Find(&events).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.PaymentEventInbox, len(outTradeNos))
	for _, event := range events {
		result[event.OutTradeNo] = append(result[event.OutTradeNo], event)
	}
	return result, nil
}

func mergeAdminSubscriptionCandidate(candidates map[string]adminSubscriptionCandidate, candidate adminSubscriptionCandidate) {
	if strings.TrimSpace(candidate.UserID) == "" || strings.TrimSpace(candidate.SKUCode) == "" {
		return
	}
	key := adminSubscriptionKey(candidate.UserID, candidate.SKUCode)
	existing, ok := candidates[key]
	if !ok || candidate.SortAt > existing.SortAt {
		candidates[key] = candidate
	}
}

func sortAdminSubscriptionCandidates(candidates []adminSubscriptionCandidate) {
	for i := 1; i < len(candidates); i++ {
		current := candidates[i]
		j := i - 1
		for j >= 0 && candidates[j].SortAt < current.SortAt {
			candidates[j+1] = candidates[j]
			j--
		}
		candidates[j+1] = current
	}
}

func adminSubscriptionUserIDs(candidates []adminSubscriptionCandidate) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.UserID) == "" || seen[candidate.UserID] {
			continue
		}
		seen[candidate.UserID] = true
		ids = append(ids, candidate.UserID)
	}
	return ids
}

func adminSubscriptionOutTradeNos(orders map[string]*domain.Order) []string {
	seen := map[string]bool{}
	outTradeNos := make([]string, 0, len(orders))
	for _, order := range orders {
		if order == nil || strings.TrimSpace(order.OutTradeNo) == "" || seen[order.OutTradeNo] {
			continue
		}
		seen[order.OutTradeNo] = true
		outTradeNos = append(outTradeNos, order.OutTradeNo)
	}
	return outTradeNos
}

func adminSubscriptionKey(userID string, skuCode string) string {
	return strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(skuCode)
}

func adminSubscriptionSKUAllowed(skuCode string, filter string) bool {
	skuCode = strings.TrimSpace(skuCode)
	if skuCode == "" {
		return false
	}
	filter = strings.TrimSpace(filter)
	return filter == "" || filter == skuCode
}

func adminSubscriptionEntitlement(entitlementID string) bool {
	switch entitlementID {
	case domain.EntitlementEditorialStudio, domain.EntitlementCloudStorage:
		return true
	default:
		return false
	}
}

func adminSubscriptionSKUFromGrant(grant domain.EntitlementGrant) string {
	if grant.ExpiresAt == nil {
		return domain.SKUProOwnAILifetime
	}
	return domain.SKUProOwnAIMonthly
}

func adminSubscriptionOrderSortAt(order domain.Order) string {
	switch {
	case order.PaidAt != nil:
		return order.PaidAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	case order.FulfilledAt != nil:
		return order.FulfilledAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	default:
		return ""
	}
}
