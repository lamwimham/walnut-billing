package gorm_repo

import (
	"context"
	"strings"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.AdminTestScenarioResetRepository = (*AdminTestScenarioResetRepo)(nil)

// AdminTestScenarioResetRepo owns destructive dev/test cleanup. It is kept out
// of handlers and guarded by service policy plus admin permission checks.
type AdminTestScenarioResetRepo struct {
	DB *gorm.DB
}

func (r *AdminTestScenarioResetRepo) ResetUserControlPlane(ctx context.Context, query repository.AdminTestScenarioResetQuery) (*repository.AdminTestScenarioResetRecord, error) {
	if r == nil || r.DB == nil {
		return nil, repository.ErrNotFound
	}
	query.Email = strings.TrimSpace(strings.ToLower(query.Email))
	query.UserID = strings.TrimSpace(query.UserID)

	user, err := adminResetFindUser(ctx, r.DB, query)
	if err != nil {
		return nil, err
	}
	email := query.Email
	userIDs := []string{}
	if user != nil {
		userIDs = append(userIDs, user.ID)
		if email == "" {
			email = strings.TrimSpace(strings.ToLower(user.Email))
		}
	}

	counts := map[string]int64{}
	exec := func(db *gorm.DB) error {
		orderNos, err := adminResetOrderNos(ctx, db, userIDs)
		if err != nil {
			return err
		}
		operations := adminResetOperations(userIDs, email, orderNos)
		for _, operation := range operations {
			count, err := adminResetCount(ctx, db, operation)
			if err != nil {
				return err
			}
			counts[operation.name] = count
			if query.DryRun || count == 0 {
				continue
			}
			if err := adminResetDelete(ctx, db, operation); err != nil {
				return err
			}
		}
		return nil
	}

	if query.DryRun {
		if err := exec(r.DB); err != nil {
			return nil, err
		}
	} else if err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return exec(tx)
	}); err != nil {
		return nil, err
	}

	if adminResetTotal(counts) == 0 {
		return nil, repository.ErrNotFound
	}
	return &repository.AdminTestScenarioResetRecord{
		Scenario:       query.Scenario,
		User:           user,
		Email:          email,
		AffectedCounts: counts,
	}, nil
}

func adminResetFindUser(ctx context.Context, db *gorm.DB, query repository.AdminTestScenarioResetQuery) (*domain.User, error) {
	q := db.WithContext(ctx).Model(&domain.User{})
	if query.UserID != "" {
		q = q.Where("id = ?", query.UserID)
	}
	if query.Email != "" {
		q = q.Where("email = ?", query.Email)
	}
	if query.UserID == "" && query.Email == "" {
		return nil, repository.ErrNotFound
	}
	var user domain.User
	if err := q.First(&user).Error; err != nil {
		if query.UserID != "" {
			return nil, mapGormNotFound(err)
		}
		if mapGormNotFound(err) == repository.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

type adminResetOperation struct {
	name  string
	model any
	where string
	args  []any
}

func adminResetOperations(userIDs []string, email string, orderNos []string) []adminResetOperation {
	operations := make([]adminResetOperation, 0, 20)
	add := func(name string, model any, where string, args ...any) {
		if strings.TrimSpace(where) == "" {
			return
		}
		operations = append(operations, adminResetOperation{name: name, model: model, where: where, args: args})
	}
	if where, args, ok := adminResetEmailWhere("email", email); ok {
		add("access_login_challenges", &domain.AccessLoginChallenge{}, where, args...)
	}
	if where, args, ok := adminResetUserOrEmailWhere(userIDs, email, "user_id", "email"); ok {
		add("registration_requests", &domain.RegistrationRequest{}, where, args...)
		add("trial_grants", &domain.TrialGrant{}, where, args...)
	}
	if where, args, ok := adminResetUserWhere(userIDs, "user_id"); ok {
		add("cloud_objects", &domain.CloudObject{}, where, args...)
		add("cloud_manifests", &domain.CloudManifest{}, where, args...)
		add("cloud_sync_sessions", &domain.CloudSyncSession{}, where, args...)
		add("cloud_projects", &domain.CloudProject{}, where, args...)
		add("subscription_cancellations", &domain.SubscriptionCancellation{}, where, args...)
		add("credit_transactions", &domain.CreditTransaction{}, where, args...)
		add("credit_reservations", &domain.CreditReservation{}, where, args...)
		add("credit_buckets", &domain.CreditBucket{}, where, args...)
		add("credit_accounts", &domain.CreditAccount{}, where, args...)
		add("user_devices", &domain.UserDevice{}, where, args...)
		add("entitlement_grants", &domain.EntitlementGrant{}, where, args...)
	}
	if where, args, ok := adminResetUserOrOrderWhere(userIDs, orderNos, "user_id", "out_trade_no"); ok {
		add("fulfillment_executions", &domain.FulfillmentExecution{}, where, args...)
		add("payment_risk_flags", &domain.PaymentRiskFlag{}, where, args...)
	}
	if where, args, ok := adminResetOrderWhere(orderNos, "out_trade_no"); ok {
		add("payment_event_inboxes", &domain.PaymentEventInbox{}, where, args...)
	}
	if where, args, ok := adminResetUserWhere(userIDs, "user_id"); ok {
		add("orders", &domain.Order{}, where, args...)
		add("users", &domain.User{}, "id IN ?", userIDs)
	}
	return operations
}

func adminResetCount(ctx context.Context, db *gorm.DB, operation adminResetOperation) (int64, error) {
	var count int64
	err := db.WithContext(ctx).Model(operation.model).Where(operation.where, operation.args...).Count(&count).Error
	return count, err
}

func adminResetDelete(ctx context.Context, db *gorm.DB, operation adminResetOperation) error {
	return db.WithContext(ctx).Where(operation.where, operation.args...).Delete(operation.model).Error
}

func adminResetOrderNos(ctx context.Context, db *gorm.DB, userIDs []string) ([]string, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	var outTradeNos []string
	if err := db.WithContext(ctx).Model(&domain.Order{}).
		Where("user_id IN ?", userIDs).
		Pluck("out_trade_no", &outTradeNos).Error; err != nil {
		return nil, err
	}
	return compactStrings(outTradeNos), nil
}

func adminResetUserWhere(userIDs []string, userColumn string) (string, []any, bool) {
	if len(userIDs) == 0 {
		return "", nil, false
	}
	return userColumn + " IN ?", []any{userIDs}, true
}

func adminResetEmailWhere(emailColumn string, email string) (string, []any, bool) {
	if strings.TrimSpace(email) == "" {
		return "", nil, false
	}
	return emailColumn + " = ?", []any{email}, true
}

func adminResetOrderWhere(orderNos []string, orderColumn string) (string, []any, bool) {
	if len(orderNos) == 0 {
		return "", nil, false
	}
	return orderColumn + " IN ?", []any{orderNos}, true
}

func adminResetUserOrEmailWhere(userIDs []string, email string, userColumn string, emailColumn string) (string, []any, bool) {
	hasUsers := len(userIDs) > 0
	hasEmail := strings.TrimSpace(email) != ""
	switch {
	case hasUsers && hasEmail:
		return "(" + userColumn + " IN ? OR " + emailColumn + " = ?)", []any{userIDs, email}, true
	case hasUsers:
		return userColumn + " IN ?", []any{userIDs}, true
	case hasEmail:
		return emailColumn + " = ?", []any{email}, true
	default:
		return "", nil, false
	}
}

func adminResetUserOrOrderWhere(userIDs []string, orderNos []string, userColumn string, orderColumn string) (string, []any, bool) {
	hasUsers := len(userIDs) > 0
	hasOrders := len(orderNos) > 0
	switch {
	case hasUsers && hasOrders:
		return "(" + userColumn + " IN ? OR " + orderColumn + " IN ?)", []any{userIDs, orderNos}, true
	case hasUsers:
		return userColumn + " IN ?", []any{userIDs}, true
	case hasOrders:
		return orderColumn + " IN ?", []any{orderNos}, true
	default:
		return "", nil, false
	}
}

func compactStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func adminResetTotal(counts map[string]int64) int64 {
	var total int64
	for _, count := range counts {
		total += count
	}
	return total
}
