package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidSubscriptionRenewal = errors.New("invalid subscription renewal event")
)

type SubscriptionRenewalService interface {
	Apply(ctx context.Context, event *domain.PaymentEventInbox) (*SubscriptionRenewalResult, error)
}

type SubscriptionRenewalResult struct {
	Order           *domain.Order                     `json:"order,omitempty"`
	PolicyDecision  SubscriptionRenewalPolicyDecision `json:"policy_decision"`
	Fulfillment     *FulfillmentResult                `json:"fulfillment,omitempty"`
	GraceGrant      *domain.EntitlementGrant          `json:"grace_grant,omitempty"`
	ExpiredGrantIDs []string                          `json:"expired_grant_ids,omitempty"`
	AffectedGrants  []domain.EntitlementGrant         `json:"affected_grants,omitempty"`
	Note            string                            `json:"note,omitempty"`
}

type SubscriptionRenewalRepositories struct {
	Orders            repository.OrderRepository
	Users             repository.UserRepository
	EntitlementGrants repository.EntitlementGrantRepository
}

type SubscriptionRenewalDependencies struct {
	Repositories       SubscriptionRenewalRepositories
	Fulfillment        FulfillmentService
	Policy             SubscriptionRenewalPolicy
	EntitlementCatalog EntitlementCatalog
	UnitOfWorkFactory  func() repository.UnitOfWork
}

type subscriptionRenewalServiceImpl struct {
	repos       SubscriptionRenewalRepositories
	fulfillment FulfillmentService
	policy      SubscriptionRenewalPolicy
	catalog     EntitlementCatalog
	uowFactory  func() repository.UnitOfWork
}

func NewSubscriptionRenewalService(deps SubscriptionRenewalDependencies) SubscriptionRenewalService {
	policy := deps.Policy
	if policy == nil {
		policy = NewConfigurableSubscriptionRenewalPolicy(DefaultSubscriptionRenewalPolicyConfig())
	}
	catalog := deps.EntitlementCatalog
	if catalog == nil {
		catalog = DefaultEntitlementCatalog()
	}
	return &subscriptionRenewalServiceImpl{
		repos:       deps.Repositories,
		fulfillment: deps.Fulfillment,
		policy:      policy,
		catalog:     catalog,
		uowFactory:  deps.UnitOfWorkFactory,
	}
}

func (s *subscriptionRenewalServiceImpl) Apply(ctx context.Context, event *domain.PaymentEventInbox) (*SubscriptionRenewalResult, error) {
	if s == nil || event == nil || strings.TrimSpace(event.OutTradeNo) == "" || !s.hasRequiredRepos(s.repos) {
		return nil, ErrInvalidSubscriptionRenewal
	}
	if !isSubscriptionRenewalEventType(event.EventType) {
		return nil, ErrInvalidSubscriptionRenewal
	}
	if event.EventType == domain.PaymentEventTypeRenewalPaid && !isCheckoutBackedRenewalEvent(ctx, s.repos.Orders, event) {
		return s.applyWithRepos(ctx, s.repos, event)
	}
	return s.withSubscriptionRenewalTransaction(ctx, func(repos SubscriptionRenewalRepositories) (*SubscriptionRenewalResult, error) {
		return s.applyWithRepos(ctx, repos, event)
	})
}

func (s *subscriptionRenewalServiceImpl) applyWithRepos(ctx context.Context, repos SubscriptionRenewalRepositories, event *domain.PaymentEventInbox) (*SubscriptionRenewalResult, error) {
	order, err := repos.Orders.GetByOutTradeNo(ctx, strings.TrimSpace(event.OutTradeNo))
	if err != nil {
		return nil, err
	}
	checkoutBackedInitialPayment := isCheckoutBackedRenewalEventFromOrder(order, event)
	if checkoutBackedInitialPayment {
		decision := SubscriptionRenewalPolicyDecision{Action: SubscriptionRenewalActionFulfillCheckout, Reason: SubscriptionRenewalReasonInitialSubscriptionPaid}
		result := &SubscriptionRenewalResult{Order: order, PolicyDecision: decision, Note: decision.Reason}
		if s.fulfillment == nil {
			return result, ErrInvalidSubscriptionRenewal
		}
		if err := markInitialCheckoutPaid(ctx, repos.Orders, order, event); err != nil {
			return result, err
		}
		fulfillment, err := s.fulfillment.FulfillOrder(ctx, order)
		result.Fulfillment = fulfillment
		return result, err
	}
	order, err = s.resolveRenewalOrder(ctx, repos.Orders, event, order)
	if err != nil {
		return nil, err
	}
	if order.OrderType != domain.OrderTypeRenewal || strings.TrimSpace(order.UserID) == "" || strings.TrimSpace(order.SKUCode) == "" {
		decision := SubscriptionRenewalPolicyDecision{Action: SubscriptionRenewalActionIgnore, Reason: SubscriptionRenewalReasonUnsupported}
		return &SubscriptionRenewalResult{Order: order, PolicyDecision: decision, Note: "non_subscription_renewal_order_ignored"}, nil
	}
	if err := updateRenewalOrderStatus(ctx, repos.Orders, order, event); err != nil {
		return nil, err
	}
	decision := s.policy.Decide(ctx, SubscriptionRenewalPolicyInput{Event: event, Order: order})
	result := &SubscriptionRenewalResult{Order: order, PolicyDecision: decision, Note: decision.Reason}
	switch decision.Action {
	case SubscriptionRenewalActionFulfillRenewal:
		if s.fulfillment == nil {
			return result, ErrInvalidSubscriptionRenewal
		}
		fulfillment, err := s.fulfillment.FulfillOrder(ctx, order)
		result.Fulfillment = fulfillment
		return result, err
	case SubscriptionRenewalActionGrantGrace:
		grant, affected, err := s.ensureGraceGrant(ctx, repos, event, order, decision.GracePeriodDays)
		result.GraceGrant = grant
		result.AffectedGrants = affected
		return result, err
	case SubscriptionRenewalActionExpireGrace:
		expired, err := s.expireGraceGrants(ctx, repos, order, paymentEventReceivedAt(event), decision.GracePeriodDays)
		result.ExpiredGrantIDs = expired
		return result, err
	case SubscriptionRenewalActionNaturalExpiry, SubscriptionRenewalActionIgnore:
		return result, nil
	default:
		return result, ErrInvalidSubscriptionRenewal
	}
}

func (s *subscriptionRenewalServiceImpl) resolveRenewalOrder(ctx context.Context, orders repository.OrderRepository, event *domain.PaymentEventInbox, order *domain.Order) (*domain.Order, error) {
	if order == nil || order.OrderType == domain.OrderTypeRenewal {
		return order, nil
	}
	if order.OrderType != domain.OrderTypeCheckout || strings.TrimSpace(order.UserID) == "" || strings.TrimSpace(order.SKUCode) == "" {
		return order, nil
	}
	idempotencyKey := subscriptionRenewalOrderIdempotencyKey(order.OutTradeNo, event)
	if existing, err := orders.GetByIdempotencyKey(ctx, idempotencyKey); err == nil {
		return existing, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	now := renewalPaidAnchor(event)
	derived := &domain.Order{
		OutTradeNo:         subscriptionRenewalOrderOutTradeNo(idempotencyKey),
		UserID:             order.UserID,
		SKUCode:            order.SKUCode,
		Amount:             firstPositiveAmount(event.Amount, order.Amount),
		Currency:           firstNonEmptyString(event.Currency, order.Currency),
		Status:             renewalOrderStatusFromEvent(event.EventType),
		Provider:           firstNonEmptyString(event.Provider, order.Provider),
		TradeNo:            event.ProviderTradeNo,
		ProviderCheckoutID: order.ProviderCheckoutID,
		ProviderCustomerID: order.ProviderCustomerID,
		IdempotencyKey:     &idempotencyKey,
		OrderType:          domain.OrderTypeRenewal,
		Metadata:           fmt.Sprintf(`{"source_out_trade_no":%q}`, strings.TrimSpace(order.OutTradeNo)),
	}
	if event.EventType == domain.PaymentEventTypeRenewalPaid {
		derived.PaidAt = &now
	}
	if err := orders.Create(ctx, derived); err != nil {
		if existing, getErr := orders.GetByIdempotencyKey(ctx, idempotencyKey); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return derived, nil
}

func updateRenewalOrderStatus(ctx context.Context, orders repository.OrderRepository, order *domain.Order, event *domain.PaymentEventInbox) error {
	if order == nil || event == nil || order.OrderType != domain.OrderTypeRenewal {
		return nil
	}
	switch event.EventType {
	case domain.PaymentEventTypeRenewalPaid:
		now := renewalPaidAnchor(event)
		if order.Status == domain.OrderStatusFulfilled {
			return nil
		}
		order.Status = domain.OrderStatusPaid
		order.Provider = firstNonEmptyString(event.Provider, order.Provider)
		order.TradeNo = strings.TrimSpace(event.ProviderTradeNo)
		if order.PaidAt == nil {
			order.PaidAt = &now
		}
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
		return nil
	}
	return orders.Update(ctx, order)
}

func markInitialCheckoutPaid(ctx context.Context, orders repository.OrderRepository, order *domain.Order, event *domain.PaymentEventInbox) error {
	if order == nil || event == nil || order.OrderType != domain.OrderTypeCheckout {
		return nil
	}
	if order.Status == domain.OrderStatusFulfilled {
		return nil
	}
	now := renewalPaidAnchor(event)
	order.Status = domain.OrderStatusPaid
	order.Provider = firstNonEmptyString(event.Provider, order.Provider)
	order.TradeNo = strings.TrimSpace(event.ProviderTradeNo)
	if order.PaidAt == nil {
		order.PaidAt = &now
	}
	return orders.Update(ctx, order)
}

func (s *subscriptionRenewalServiceImpl) ensureGraceGrant(
	ctx context.Context,
	repos SubscriptionRenewalRepositories,
	event *domain.PaymentEventInbox,
	order *domain.Order,
	gracePeriodDays int,
) (*domain.EntitlementGrant, []domain.EntitlementGrant, error) {
	if gracePeriodDays <= 0 {
		gracePeriodDays = domain.GracePeriodDays
	}
	startsAt := renewalGraceStart(event)
	graceEnd := startsAt.AddDate(0, 0, gracePeriodDays)
	graceGrantKey := subscriptionGraceGrantKey(order.OutTradeNo, domain.EntitlementEditorialStudio)
	grant, err := createGrantWithRepos(ctx, repos.Users, repos.EntitlementGrants, s.catalog, GrantInput{
		UserID:         order.UserID,
		EntitlementID:  domain.EntitlementEditorialStudio,
		CreatedBy:      "system",
		Source:         domain.GrantSourceSubscriptionGrace,
		StartsAt:       startsAt,
		ExpiresAt:      &graceEnd,
		IdempotencyKey: graceGrantKey,
	})
	if err != nil {
		return nil, nil, err
	}
	affected, err := repos.EntitlementGrants.List(ctx, repository.EntitlementGrantQuery{
		UserID:         order.UserID,
		EntitlementID:  domain.EntitlementEditorialStudio,
		Status:         domain.GrantStatusActive,
		IncludeExpired: true,
	})
	if err != nil {
		return nil, nil, err
	}
	return grant, affected, nil
}

func (s *subscriptionRenewalServiceImpl) expireGraceGrants(
	ctx context.Context,
	repos SubscriptionRenewalRepositories,
	order *domain.Order,
	eventTime time.Time,
	gracePeriodDays int,
) ([]string, error) {
	if gracePeriodDays <= 0 {
		gracePeriodDays = domain.GracePeriodDays
	}
	grants, err := repos.EntitlementGrants.List(ctx, repository.EntitlementGrantQuery{
		UserID:         order.UserID,
		EntitlementID:  domain.EntitlementEditorialStudio,
		Status:         domain.GrantStatusActive,
		IncludeExpired: true,
	})
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	anchor := eventTime.UTC()
	if anchor.IsZero() {
		anchor = now
	}
	cutoff := anchor
	var expired []string
	for idx := range grants {
		grant := grants[idx]
		if grant.Source != domain.GrantSourceSubscriptionGrace {
			continue
		}
		if !grant.StartsAt.IsZero() && grant.StartsAt.After(anchor) {
			continue
		}
		if grant.ExpiresAt == nil || grant.ExpiresAt.After(anchor) {
			continue
		}
		grant.Status = domain.GrantStatusExpired
		grant.ExpiresAt = &cutoff
		grant.UpdatedAt = now
		if err := repos.EntitlementGrants.Update(ctx, &grant); err != nil {
			return expired, err
		}
		expired = append(expired, grant.ID)
	}
	return expired, nil
}

func (s *subscriptionRenewalServiceImpl) withSubscriptionRenewalTransaction(
	ctx context.Context,
	fn func(SubscriptionRenewalRepositories) (*SubscriptionRenewalResult, error),
) (*SubscriptionRenewalResult, error) {
	if s.uowFactory == nil {
		return fn(s.repos)
	}
	uow := s.uowFactory()
	if err := uow.Begin(ctx); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback()
		}
	}()

	repos := subscriptionRenewalReposFromUOW(uow.Repos(), s.repos)
	result, err := fn(repos)
	if err != nil {
		return result, err
	}
	if err := uow.Commit(); err != nil {
		return result, err
	}
	committed = true
	return result, nil
}

func (s *subscriptionRenewalServiceImpl) hasRequiredRepos(repos SubscriptionRenewalRepositories) bool {
	return repos.Orders != nil && repos.Users != nil && repos.EntitlementGrants != nil
}

func subscriptionRenewalReposFromUOW(repos repository.TransactionalRepositories, fallback SubscriptionRenewalRepositories) SubscriptionRenewalRepositories {
	return SubscriptionRenewalRepositories{
		Orders:            firstOrderRepo(repos.OrderRepo, fallback.Orders),
		Users:             firstUserRepo(repos.UserRepo, fallback.Users),
		EntitlementGrants: firstEntitlementGrantRepo(repos.EntitlementGrantRepo, fallback.EntitlementGrants),
	}
}

func isSubscriptionRenewalEventType(eventType string) bool {
	switch eventType {
	case domain.PaymentEventTypeRenewalPaid, domain.PaymentEventTypeRenewalFailed, domain.PaymentEventTypeSubscriptionExpired:
		return true
	default:
		return false
	}
}

func subscriptionGraceGrantKey(outTradeNo string, entitlementID string) string {
	return fmt.Sprintf("subscription_grace:%s:%s", strings.TrimSpace(outTradeNo), strings.TrimSpace(entitlementID))
}

func subscriptionRenewalOrderIdempotencyKey(sourceOutTradeNo string, event *domain.PaymentEventInbox) string {
	sourceOutTradeNo = strings.TrimSpace(sourceOutTradeNo)
	cycle := subscriptionRenewalCycleKey(event)
	if sourceOutTradeNo == "" && event != nil {
		sourceOutTradeNo = strings.TrimSpace(event.ProviderEventID)
	}
	return fmt.Sprintf("subscription_renewal:%s:%s", sourceOutTradeNo, cycle)
}

func subscriptionRenewalCycleKey(event *domain.PaymentEventInbox) string {
	if event != nil && (event.EventType == domain.PaymentEventTypeRenewalFailed || event.EventType == domain.PaymentEventTypeSubscriptionExpired) {
		if event.PeriodEndAt != nil && !event.PeriodEndAt.IsZero() {
			return event.PeriodEndAt.UTC().Format("20060102150405")
		}
	}
	if event != nil && event.PeriodStartAt != nil && !event.PeriodStartAt.IsZero() {
		return event.PeriodStartAt.UTC().Format("20060102150405")
	}
	if event != nil && event.PeriodEndAt != nil && !event.PeriodEndAt.IsZero() {
		return event.PeriodEndAt.UTC().Format("20060102150405")
	}
	return paymentEventReceivedAt(event).Format("200601")
}

func isCheckoutBackedRenewalEvent(ctx context.Context, orders repository.OrderRepository, event *domain.PaymentEventInbox) bool {
	if orders == nil || event == nil || event.EventType != domain.PaymentEventTypeRenewalPaid {
		return false
	}
	source, err := orders.GetByOutTradeNo(ctx, strings.TrimSpace(event.OutTradeNo))
	if err != nil || source == nil || source.OrderType != domain.OrderTypeCheckout || source.PaidAt == nil || event.PeriodStartAt == nil {
		return false
	}
	return isCheckoutBackedRenewalEventFromOrder(source, event)
}

func isCheckoutBackedRenewalEventFromOrder(order *domain.Order, event *domain.PaymentEventInbox) bool {
	if order == nil || event == nil || event.EventType != domain.PaymentEventTypeRenewalPaid {
		return false
	}
	if order.OrderType != domain.OrderTypeCheckout {
		return false
	}
	if order.PaidAt == nil {
		return true
	}
	if event.PeriodStartAt == nil {
		return false
	}
	return sameBillingInstant(*order.PaidAt, *event.PeriodStartAt)
}

func sameBillingInstant(left time.Time, right time.Time) bool {
	diff := left.UTC().Sub(right.UTC())
	if diff < 0 {
		diff = -diff
	}
	return diff <= 2*time.Minute
}

func renewalPaidAnchor(event *domain.PaymentEventInbox) time.Time {
	if event != nil && event.PeriodStartAt != nil && !event.PeriodStartAt.IsZero() {
		return event.PeriodStartAt.UTC()
	}
	return paymentEventReceivedAt(event)
}

func renewalGraceStart(event *domain.PaymentEventInbox) time.Time {
	if event != nil && event.PeriodEndAt != nil && !event.PeriodEndAt.IsZero() {
		return event.PeriodEndAt.UTC()
	}
	if event != nil && event.PeriodStartAt != nil && !event.PeriodStartAt.IsZero() {
		return event.PeriodStartAt.UTC()
	}
	return paymentEventReceivedAt(event)
}

func subscriptionRenewalOrderOutTradeNo(idempotencyKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(idempotencyKey)))
	return "RNL-" + hex.EncodeToString(sum[:])[:16]
}

func renewalOrderStatusFromEvent(eventType string) string {
	switch eventType {
	case domain.PaymentEventTypeRenewalPaid:
		return domain.OrderStatusPaid
	case domain.PaymentEventTypeRenewalFailed, domain.PaymentEventTypeSubscriptionExpired:
		return domain.OrderStatusFailed
	default:
		return domain.OrderStatusPending
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPositiveAmount(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func paymentEventReceivedAt(event *domain.PaymentEventInbox) time.Time {
	if event != nil && !event.ReceivedAt.IsZero() {
		return event.ReceivedAt.UTC()
	}
	return time.Now().UTC()
}
