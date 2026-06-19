package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AccessAccountReadRepository = (*AccessAccountReadRepo)(nil)
var _ repository.AdminUserAccessSummaryReadRepository = (*AdminUserAccessSummaryReadRepo)(nil)

// AccessAccountReadRepo builds the admin access-account projection from the
// normalized write tables without exposing raw emails at the handler boundary.
type AccessAccountReadRepo struct {
	DB *gorm.DB
}

func (r *AccessAccountReadRepo) List(ctx context.Context, query repository.AccessAccountQuery) ([]repository.AccessAccountRecord, int64, error) {
	var users []domain.User
	q := r.DB.WithContext(ctx).Model(&domain.User{})
	if query.UserID != "" {
		q = q.Where("id = ?", query.UserID)
	}
	if query.Email != "" {
		q = q.Where("email = ?", query.Email)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if query.Limit > 0 {
		q = q.Limit(query.Limit)
	}
	if query.Offset > 0 {
		q = q.Offset(query.Offset)
	}
	if err := q.Order("created_at DESC").Find(&users).Error; err != nil {
		return nil, 0, err
	}
	if len(users) == 0 {
		return []repository.AccessAccountRecord{}, total, nil
	}

	userIDs := make([]string, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}

	devicesByUser, err := listUserDevicesByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, 0, err
	}
	trialsByUser, err := listTrialGrantsByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, 0, err
	}
	grantsByUser, err := listEntitlementGrantsByUser(ctx, r.DB, userIDs)
	if err != nil {
		return nil, 0, err
	}

	records := make([]repository.AccessAccountRecord, 0, len(users))
	for _, user := range users {
		records = append(records, repository.AccessAccountRecord{
			User:              user,
			Devices:           devicesByUser[user.ID],
			TrialGrants:       trialsByUser[user.ID],
			EntitlementGrants: grantsByUser[user.ID],
		})
	}
	return records, total, nil
}

func listUserDevicesByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string][]domain.UserDevice, error) {
	var devices []domain.UserDevice
	if err := db.WithContext(ctx).Where("user_id IN ?", userIDs).Order("last_seen_at DESC").Find(&devices).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.UserDevice, len(userIDs))
	for _, device := range devices {
		result[device.UserID] = append(result[device.UserID], device)
	}
	return result, nil
}

func listTrialGrantsByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string][]domain.TrialGrant, error) {
	var trials []domain.TrialGrant
	if err := db.WithContext(ctx).Where("user_id IN ?", userIDs).Order("created_at DESC").Find(&trials).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.TrialGrant, len(userIDs))
	for _, trial := range trials {
		result[trial.UserID] = append(result[trial.UserID], trial)
	}
	return result, nil
}

func listEntitlementGrantsByUser(ctx context.Context, db *gorm.DB, userIDs []string) (map[string][]domain.EntitlementGrant, error) {
	var grants []domain.EntitlementGrant
	if err := db.WithContext(ctx).Where("user_id IN ?", userIDs).Order("created_at DESC").Find(&grants).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.EntitlementGrant, len(userIDs))
	for _, grant := range grants {
		result[grant.UserID] = append(result[grant.UserID], grant)
	}
	return result, nil
}

// AdminUserAccessSummaryReadRepo builds a single-user operator read model from
// normalized module tables. It deliberately returns raw facts only to the
// service privacy projector; handlers never access this repository directly.
type AdminUserAccessSummaryReadRepo struct {
	DB *gorm.DB
}

func (r *AdminUserAccessSummaryReadRepo) Get(ctx context.Context, query repository.AdminUserAccessSummaryQuery) (*repository.AdminUserAccessSummaryRecord, error) {
	if r == nil || r.DB == nil || query.UserID == "" {
		return nil, repository.ErrNotFound
	}
	limit := normalizeAdminSummaryLimit(query.RecentLimit)
	var user domain.User
	if err := r.DB.WithContext(ctx).Where("id = ?", query.UserID).First(&user).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	record := &repository.AdminUserAccessSummaryRecord{User: user}

	var devices []domain.UserDevice
	if err := r.DB.WithContext(ctx).Where("user_id = ?", user.ID).Order("last_seen_at DESC").Find(&devices).Error; err != nil {
		return nil, err
	}
	record.Devices = devices

	var trials []domain.TrialGrant
	if err := r.DB.WithContext(ctx).Where("user_id = ?", user.ID).Order("created_at DESC").Limit(limit).Find(&trials).Error; err != nil {
		return nil, err
	}
	record.TrialGrants = trials

	var grants []domain.EntitlementGrant
	if err := r.DB.WithContext(ctx).Where("user_id = ?", user.ID).Order("created_at DESC").Find(&grants).Error; err != nil {
		return nil, err
	}
	record.EntitlementGrants = grants

	var orders []domain.Order
	if err := r.DB.WithContext(ctx).Where("user_id = ?", user.ID).Order("paid_at DESC, fulfilled_at DESC, id DESC").Limit(limit).Find(&orders).Error; err != nil {
		return nil, err
	}
	record.Orders = orders

	outTradeNos := outTradeNosFromOrders(orders)
	if len(outTradeNos) > 0 {
		var events []domain.PaymentEventInbox
		if err := r.DB.WithContext(ctx).Where("out_trade_no IN ?", outTradeNos).Order("received_at DESC").Limit(limit).Find(&events).Error; err != nil {
			return nil, err
		}
		record.PaymentEvents = events
	}

	var flags []domain.PaymentRiskFlag
	if err := r.DB.WithContext(ctx).Where("user_id = ?", user.ID).Order("created_at DESC").Find(&flags).Error; err != nil {
		return nil, err
	}
	record.RiskFlags = flags

	var projects []domain.CloudProject
	if err := r.DB.WithContext(ctx).Where("user_id = ?", user.ID).Order("updated_at DESC").Find(&projects).Error; err != nil {
		return nil, err
	}
	record.CloudProjects = projects

	var usedBytes int64
	if err := r.DB.WithContext(ctx).Model(&domain.CloudObject{}).
		Where("user_id = ? AND status = ?", user.ID, domain.CloudObjectStatusActive).
		Select("COALESCE(SUM(size_bytes), 0)").Scan(&usedBytes).Error; err != nil {
		return nil, err
	}
	record.CloudUsedBytes = usedBytes

	return record, nil
}

func normalizeAdminSummaryLimit(limit int) int {
	if limit <= 0 {
		return 10
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func outTradeNosFromOrders(orders []domain.Order) []string {
	seen := map[string]bool{}
	outTradeNos := make([]string, 0, len(orders))
	for _, order := range orders {
		if order.OutTradeNo == "" || seen[order.OutTradeNo] {
			continue
		}
		seen[order.OutTradeNo] = true
		outTradeNos = append(outTradeNos, order.OutTradeNo)
	}
	return outTradeNos
}
