package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AccessAccountReadRepository = (*AccessAccountReadRepo)(nil)

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
