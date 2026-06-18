package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/payment"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidSubscriptionCancellation = errors.New("invalid subscription cancellation request")
	ErrSubscriptionNotFound            = errors.New("subscription not found")
	ErrSubscriptionControlUnavailable  = errors.New("subscription control unavailable")
	ErrSubscriptionControlFailed       = errors.New("subscription control failed")
)

const (
	SubscriptionCancellationStatusCancelAtPeriodEnd = "cancelled_at_period_end"
	SubscriptionStatusActive                        = "active"
)

type SubscriptionCancellationService interface {
	Cancel(ctx context.Context, input SubscriptionCancellationInput) (*SubscriptionCancellationResult, error)
	Resume(ctx context.Context, input SubscriptionResumeInput) (*SubscriptionCancellationResult, error)
}

type SubscriptionCancellationInput struct {
	UserID         string
	SKUCode        string
	Reason         string
	Source         string
	IdempotencyKey string
}

type SubscriptionResumeInput struct {
	UserID         string
	SKUCode        string
	Source         string
	IdempotencyKey string
}

type SubscriptionCancellationResult struct {
	UserID              string                         `json:"user_id"`
	SKUCode             string                         `json:"sku_code"`
	Status              string                         `json:"status"`
	CancelAtPeriodEnd   bool                           `json:"cancel_at_period_end"`
	CurrentPeriodEndsAt string                         `json:"current_period_ends_at"`
	Subscription        SubscriptionCancellationRecord `json:"subscription"`
	Projection          SoftwareSubscriptionProjection `json:"projection"`
}

type SubscriptionCancellationRecord struct {
	UserID              string `json:"user_id"`
	SKUCode             string `json:"sku_code"`
	Status              string `json:"status"`
	CancelAtPeriodEnd   bool   `json:"cancel_at_period_end"`
	CurrentPeriodEndsAt string `json:"current_period_ends_at"`
	SourceOrderNo       string `json:"source_order_no"`
	ID                  string `json:"id"`
}

type SubscriptionCancellationRepositories struct {
	Orders            repository.OrderRepository
	Users             repository.UserRepository
	EntitlementGrants repository.EntitlementGrantRepository
	PaymentEvents     repository.PaymentEventRepository
	Cancellations     repository.SubscriptionCancellationRepository
}

// SubscriptionControlGateway is the commerce-owned provider boundary for hosted
// subscription lifecycle changes. payment.PaymentService implements it without
// leaking concrete provider adapters into subscription orchestration.
type SubscriptionControlGateway interface {
	CancelSubscription(ctx context.Context, providerName string, req payment.SubscriptionControlRequest) (*payment.SubscriptionControlResult, error)
	ResumeSubscription(ctx context.Context, providerName string, req payment.SubscriptionControlRequest) (*payment.SubscriptionControlResult, error)
}

type SubscriptionCancellationDependencies struct {
	Repositories      SubscriptionCancellationRepositories
	ProviderControl   SubscriptionControlGateway
	UnitOfWorkFactory func() repository.UnitOfWork
	Now               func() time.Time
}

type subscriptionCancellationServiceImpl struct {
	repos           SubscriptionCancellationRepositories
	providerControl SubscriptionControlGateway
	uowFactory      func() repository.UnitOfWork
	now             func() time.Time
}

func NewSubscriptionCancellationService(deps SubscriptionCancellationDependencies) SubscriptionCancellationService {
	return &subscriptionCancellationServiceImpl{
		repos:           deps.Repositories,
		providerControl: deps.ProviderControl,
		uowFactory:      deps.UnitOfWorkFactory,
		now:             deps.Now,
	}
}

func (s *subscriptionCancellationServiceImpl) Cancel(ctx context.Context, input SubscriptionCancellationInput) (*SubscriptionCancellationResult, error) {
	input = normalizeSubscriptionCancellationInput(input)
	if s == nil || !s.hasRequiredRepos(s.repos) || input.UserID == "" || input.SKUCode == "" {
		return nil, ErrInvalidSubscriptionCancellation
	}
	providerControl, err := s.prepareCancelProviderControl(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.withCancellationTransaction(ctx, func(repos SubscriptionCancellationRepositories) (*SubscriptionCancellationResult, error) {
		return s.cancelWithRepos(ctx, repos, input, providerControl)
	})
}

func (s *subscriptionCancellationServiceImpl) Resume(ctx context.Context, input SubscriptionResumeInput) (*SubscriptionCancellationResult, error) {
	input = normalizeSubscriptionResumeInput(input)
	if s == nil || !s.hasRequiredRepos(s.repos) || input.UserID == "" || input.SKUCode == "" {
		return nil, ErrInvalidSubscriptionCancellation
	}
	providerControl, err := s.prepareResumeProviderControl(ctx, input)
	if err != nil {
		return nil, err
	}
	return s.withCancellationTransaction(ctx, func(repos SubscriptionCancellationRepositories) (*SubscriptionCancellationResult, error) {
		return s.resumeWithRepos(ctx, repos, input, providerControl)
	})
}

func (s *subscriptionCancellationServiceImpl) cancelWithRepos(ctx context.Context, repos SubscriptionCancellationRepositories, input SubscriptionCancellationInput, providerControl *payment.SubscriptionControlResult) (*SubscriptionCancellationResult, error) {
	if _, err := repos.Users.GetByID(ctx, input.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	period, err := currentSubscriptionPeriod(ctx, repos.EntitlementGrants, input.UserID, input.SKUCode, s.currentTime())
	if err != nil {
		return nil, err
	}
	order, err := repos.Orders.FindLatestSubscriptionOrder(ctx, repository.SubscriptionOrderQuery{
		UserID:  input.UserID,
		SKUCode: input.SKUCode,
	})
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	order, err = markProviderSubscriptionCancellation(ctx, repos.Orders, repos.PaymentEvents, order, input, providerControl, s.currentTime())
	if err != nil {
		return nil, err
	}
	if repos.Cancellations == nil {
		return nil, ErrInvalidSubscriptionCancellation
	}
	cancellation, err := ensureSubscriptionCancellation(ctx, repos.Cancellations, input, order, period, s.currentTime())
	if err != nil {
		return nil, err
	}
	return subscriptionCancellationResult(input, order, period, cancellation), nil
}

func (s *subscriptionCancellationServiceImpl) resumeWithRepos(ctx context.Context, repos SubscriptionCancellationRepositories, input SubscriptionResumeInput, providerControl *payment.SubscriptionControlResult) (*SubscriptionCancellationResult, error) {
	if _, err := repos.Users.GetByID(ctx, input.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	period, err := currentSubscriptionPeriod(ctx, repos.EntitlementGrants, input.UserID, input.SKUCode, s.currentTime())
	if err != nil {
		return nil, err
	}
	order, err := repos.Orders.FindLatestSubscriptionOrder(ctx, repository.SubscriptionOrderQuery{
		UserID:  input.UserID,
		SKUCode: input.SKUCode,
	})
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if _, err := markProviderSubscriptionResumed(ctx, repos.Orders, repos.PaymentEvents, order, input, providerControl, s.currentTime()); err != nil {
		return nil, err
	}
	cancellation, err := resumeSubscriptionCancellation(ctx, repos.Cancellations, input, s.currentTime())
	if err != nil {
		return nil, err
	}
	return subscriptionResumeResult(input, period, cancellation), nil
}

func (s *subscriptionCancellationServiceImpl) hasRequiredRepos(repos SubscriptionCancellationRepositories) bool {
	return repos.Orders != nil && repos.Users != nil && repos.EntitlementGrants != nil && repos.Cancellations != nil
}

func (s *subscriptionCancellationServiceImpl) currentTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *subscriptionCancellationServiceImpl) prepareCancelProviderControl(ctx context.Context, input SubscriptionCancellationInput) (*payment.SubscriptionControlResult, error) {
	if s == nil || s.providerControl == nil {
		return nil, nil
	}
	if input.IdempotencyKey != "" {
		existing, err := s.repos.Cancellations.GetByIdempotencyKey(ctx, input.IdempotencyKey)
		if err == nil && existing.Status == SubscriptionCancellationStatusCancelAtPeriodEnd && existing.CancelAtPeriodEnd {
			return nil, nil
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if _, err := s.repos.Users.GetByID(ctx, input.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if _, err := currentSubscriptionPeriod(ctx, s.repos.EntitlementGrants, input.UserID, input.SKUCode, s.currentTime()); err != nil {
		return nil, err
	}
	order, err := s.repos.Orders.FindLatestSubscriptionOrder(ctx, repository.SubscriptionOrderQuery{
		UserID:  input.UserID,
		SKUCode: input.SKUCode,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return s.cancelProviderSubscription(ctx, s.repos.PaymentEvents, order, input)
}

func (s *subscriptionCancellationServiceImpl) prepareResumeProviderControl(ctx context.Context, input SubscriptionResumeInput) (*payment.SubscriptionControlResult, error) {
	if s == nil || s.providerControl == nil {
		return nil, nil
	}
	if _, err := s.repos.Users.GetByID(ctx, input.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	if _, err := currentSubscriptionPeriod(ctx, s.repos.EntitlementGrants, input.UserID, input.SKUCode, s.currentTime()); err != nil {
		return nil, err
	}
	if input.IdempotencyKey != "" {
		existing, err := s.repos.Cancellations.GetByResumeIdempotencyKey(ctx, input.IdempotencyKey)
		if err == nil && existing.Status != SubscriptionCancellationStatusCancelAtPeriodEnd && !existing.CancelAtPeriodEnd {
			return nil, nil
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if _, err := s.repos.Cancellations.FindActive(ctx, repository.SubscriptionCancellationQuery{
		UserID:  input.UserID,
		SKUCode: input.SKUCode,
		Status:  SubscriptionCancellationStatusCancelAtPeriodEnd,
	}); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	order, err := s.repos.Orders.FindLatestSubscriptionOrder(ctx, repository.SubscriptionOrderQuery{
		UserID:  input.UserID,
		SKUCode: input.SKUCode,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return s.resumeProviderSubscription(ctx, s.repos.PaymentEvents, order, input)
}

func (s *subscriptionCancellationServiceImpl) cancelProviderSubscription(
	ctx context.Context,
	events repository.PaymentEventRepository,
	order *domain.Order,
	input SubscriptionCancellationInput,
) (*payment.SubscriptionControlResult, error) {
	if s == nil || s.providerControl == nil || order == nil {
		return nil, nil
	}
	subscriptionID := providerSubscriptionIDForOrder(ctx, events, order)
	if subscriptionID == "" {
		return nil, nil
	}
	result, err := s.providerControl.CancelSubscription(ctx, order.Provider, payment.SubscriptionControlRequest{
		ProviderSubscriptionID: subscriptionID,
		UserID:                 input.UserID,
		SKUCode:                input.SKUCode,
		CancelAtPeriodEnd:      true,
		IdempotencyKey:         input.IdempotencyKey,
		Metadata: map[string]string{
			"walnut_out_trade_no": order.OutTradeNo,
			"walnut_action":       "subscription_cancel",
			"walnut_source":       input.Source,
		},
	})
	if err != nil {
		if errors.Is(err, payment.ErrSubscriptionControlUnsupported) {
			return nil, ErrSubscriptionControlUnavailable
		}
		return nil, errors.Join(ErrSubscriptionControlFailed, err)
	}
	return result, nil
}

func (s *subscriptionCancellationServiceImpl) resumeProviderSubscription(
	ctx context.Context,
	events repository.PaymentEventRepository,
	order *domain.Order,
	input SubscriptionResumeInput,
) (*payment.SubscriptionControlResult, error) {
	if s == nil || s.providerControl == nil || order == nil {
		return nil, nil
	}
	subscriptionID := providerSubscriptionIDForOrder(ctx, events, order)
	if subscriptionID == "" {
		return nil, nil
	}
	result, err := s.providerControl.ResumeSubscription(ctx, order.Provider, payment.SubscriptionControlRequest{
		ProviderSubscriptionID: subscriptionID,
		UserID:                 input.UserID,
		SKUCode:                input.SKUCode,
		IdempotencyKey:         input.IdempotencyKey,
		Metadata: map[string]string{
			"walnut_out_trade_no": order.OutTradeNo,
			"walnut_action":       "subscription_resume",
			"walnut_source":       input.Source,
		},
	})
	if err != nil {
		if errors.Is(err, payment.ErrSubscriptionControlUnsupported) {
			return nil, ErrSubscriptionControlUnavailable
		}
		return nil, errors.Join(ErrSubscriptionControlFailed, err)
	}
	return result, nil
}

func (s *subscriptionCancellationServiceImpl) withCancellationTransaction(
	ctx context.Context,
	fn func(SubscriptionCancellationRepositories) (*SubscriptionCancellationResult, error),
) (*SubscriptionCancellationResult, error) {
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
	repos := subscriptionCancellationReposFromUOW(uow.Repos(), s.repos)
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

type subscriptionPeriodProjection struct {
	EndsAt time.Time
}

func currentSubscriptionPeriod(ctx context.Context, grants repository.EntitlementGrantRepository, userID string, skuCode string, now time.Time) (subscriptionPeriodProjection, error) {
	if grants == nil || strings.TrimSpace(userID) == "" || !isCancellableSubscriptionSKU(skuCode) {
		return subscriptionPeriodProjection{}, ErrInvalidSubscriptionCancellation
	}
	userGrants, err := grants.List(ctx, repository.EntitlementGrantQuery{
		UserID:         strings.TrimSpace(userID),
		Status:         domain.GrantStatusActive,
		IncludeExpired: false,
	})
	if err != nil {
		return subscriptionPeriodProjection{}, err
	}
	var endsAt *time.Time
	for _, grant := range userGrants {
		if grant.Source != domain.GrantSourceFulfillment || !IsCurrentAdvancedEntitlementID(grant.EntitlementID) || grant.ExpiresAt == nil {
			continue
		}
		end := grant.ExpiresAt.UTC()
		if !end.After(now) {
			continue
		}
		if endsAt == nil || end.After(*endsAt) {
			endsAt = &end
		}
	}
	if endsAt == nil {
		return subscriptionPeriodProjection{}, ErrSubscriptionNotFound
	}
	return subscriptionPeriodProjection{EndsAt: *endsAt}, nil
}

func subscriptionCancellationReposFromUOW(repos repository.TransactionalRepositories, fallback SubscriptionCancellationRepositories) SubscriptionCancellationRepositories {
	return SubscriptionCancellationRepositories{
		Orders:            firstOrderRepo(repos.OrderRepo, fallback.Orders),
		Users:             firstUserRepo(repos.UserRepo, fallback.Users),
		EntitlementGrants: firstEntitlementGrantRepo(repos.EntitlementGrantRepo, fallback.EntitlementGrants),
		PaymentEvents:     firstPaymentEventRepo(repos.PaymentEventRepo, fallback.PaymentEvents),
		Cancellations:     firstSubscriptionCancellationRepo(repos.SubscriptionCancellationRepo, fallback.Cancellations),
	}
}

func normalizeSubscriptionCancellationInput(input SubscriptionCancellationInput) SubscriptionCancellationInput {
	input.UserID = strings.TrimSpace(input.UserID)
	input.SKUCode = strings.TrimSpace(input.SKUCode)
	if input.SKUCode == "" {
		input.SKUCode = domain.SKUProOwnAIMonthly
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "pc_core"
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input
}

func normalizeSubscriptionResumeInput(input SubscriptionResumeInput) SubscriptionResumeInput {
	input.UserID = strings.TrimSpace(input.UserID)
	input.SKUCode = strings.TrimSpace(input.SKUCode)
	if input.SKUCode == "" {
		input.SKUCode = domain.SKUProOwnAIMonthly
	}
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "pc_core"
	}
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input
}

func isCancellableSubscriptionSKU(skuCode string) bool {
	switch strings.TrimSpace(skuCode) {
	case domain.SKUProOwnAIMonthly:
		return true
	default:
		return false
	}
}

func ensureSubscriptionCancellation(
	ctx context.Context,
	repo repository.SubscriptionCancellationRepository,
	input SubscriptionCancellationInput,
	order *domain.Order,
	period subscriptionPeriodProjection,
	now time.Time,
) (*domain.SubscriptionCancellation, error) {
	if input.IdempotencyKey != "" {
		existing, err := repo.GetByIdempotencyKey(ctx, input.IdempotencyKey)
		if err == nil {
			if existing.Status != SubscriptionCancellationStatusCancelAtPeriodEnd || !existing.CancelAtPeriodEnd {
				return activateSubscriptionCancellation(ctx, repo, existing, input, period, now)
			}
			return existing, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	sourceOrderNo := ""
	if order != nil {
		sourceOrderNo = order.OutTradeNo
	}
	id, err := generateEntityID("sub_cancel_")
	if err != nil {
		return nil, err
	}
	cancellation := &domain.SubscriptionCancellation{
		ID:                  id,
		UserID:              input.UserID,
		SKUCode:             input.SKUCode,
		Status:              SubscriptionCancellationStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:   true,
		CurrentPeriodEndsAt: period.EndsAt.UTC(),
		SourceOrderNo:       sourceOrderNo,
		Reason:              input.Reason,
		Source:              input.Source,
		IdempotencyKey:      input.IdempotencyKey,
		CreatedAt:           now.UTC(),
		UpdatedAt:           now.UTC(),
	}
	if cancellation.IdempotencyKey == "" {
		cancellation.IdempotencyKey = "subscription_cancel:" + cancellation.UserID + ":" + cancellation.SKUCode + ":" + cancellation.CurrentPeriodEndsAt.Format("20060102150405")
	}
	if err := repo.Create(ctx, cancellation); err != nil {
		if existing, getErr := repo.GetByIdempotencyKey(ctx, cancellation.IdempotencyKey); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return cancellation, nil
}

func markProviderSubscriptionCancellation(
	ctx context.Context,
	orders repository.OrderRepository,
	events repository.PaymentEventRepository,
	order *domain.Order,
	input SubscriptionCancellationInput,
	providerControl *payment.SubscriptionControlResult,
	now time.Time,
) (*domain.Order, error) {
	if order == nil || orders == nil {
		return order, nil
	}
	metadata := orderMetadataMap(order.Metadata)
	changed := putOrderMetadata(metadata, "walnut_subscription_status", SoftwareSubscriptionStatusCancelAtPeriodEnd)
	changed = putOrderMetadata(metadata, "walnut_cancel_at_period_end", "true") || changed
	changed = putOrderMetadata(metadata, "walnut_subscription_cancel_source", input.Source) || changed
	changed = putOrderMetadata(metadata, "walnut_subscription_cancel_reason", input.Reason) || changed
	changed = putOrderMetadata(metadata, "walnut_subscription_cancelled_at", now.UTC().Format(time.RFC3339)) || changed
	if providerSubscriptionID := firstProviderSubscriptionID(providerControl, providerSubscriptionIDForOrder(ctx, events, order)); providerSubscriptionID != "" {
		changed = putOrderMetadata(metadata, "walnut_provider_subscription_id", providerSubscriptionID) || changed
	}
	changed = putProviderControlMetadata(metadata, providerControl) || changed
	if !changed {
		return order, nil
	}
	order.Metadata = encodeCheckoutMetadata(metadata)
	if err := orders.Update(ctx, order); err != nil {
		return order, err
	}
	return order, nil
}

func markProviderSubscriptionResumed(
	ctx context.Context,
	orders repository.OrderRepository,
	events repository.PaymentEventRepository,
	order *domain.Order,
	input SubscriptionResumeInput,
	providerControl *payment.SubscriptionControlResult,
	now time.Time,
) (*domain.Order, error) {
	if order == nil || orders == nil {
		return nil, nil
	}
	metadata := orderMetadataMap(order.Metadata)
	changed := putOrderMetadata(metadata, "walnut_subscription_status", SubscriptionStatusActive)
	changed = putOrderMetadata(metadata, "walnut_cancel_at_period_end", "false") || changed
	changed = putOrderMetadata(metadata, "walnut_subscription_resume_source", input.Source) || changed
	changed = putOrderMetadata(metadata, "walnut_subscription_resumed_at", now.UTC().Format(time.RFC3339)) || changed
	if providerSubscriptionID := firstProviderSubscriptionID(providerControl, providerSubscriptionIDForOrder(ctx, events, order)); providerSubscriptionID != "" {
		changed = putOrderMetadata(metadata, "walnut_provider_subscription_id", providerSubscriptionID) || changed
	}
	changed = putProviderControlMetadata(metadata, providerControl) || changed
	if !changed {
		return order, nil
	}
	order.Metadata = encodeCheckoutMetadata(metadata)
	if err := orders.Update(ctx, order); err != nil {
		return order, err
	}
	return order, nil
}

func currentProviderSubscriptionID(ctx context.Context, events repository.PaymentEventRepository, outTradeNo string) string {
	if events == nil || strings.TrimSpace(outTradeNo) == "" {
		return ""
	}
	rows, err := events.List(ctx, repository.PaymentEventQuery{OutTradeNo: strings.TrimSpace(outTradeNo), Limit: 20})
	if err != nil {
		return ""
	}
	for _, event := range rows {
		if id := providerSubscriptionIDFromRawPayload(event.RawPayload); id != "" {
			return id
		}
	}
	return ""
}

func providerSubscriptionIDForOrder(ctx context.Context, events repository.PaymentEventRepository, order *domain.Order) string {
	if order == nil {
		return ""
	}
	metadata := orderMetadataMap(order.Metadata)
	if id := metadata["walnut_provider_subscription_id"]; id != "" {
		return id
	}
	if id := metadata["provider_subscription_id"]; id != "" {
		return id
	}
	return currentProviderSubscriptionID(ctx, events, order.OutTradeNo)
}

func orderMetadataMap(raw string) map[string]string {
	metadata := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return metadata
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err == nil {
		return metadata
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return metadata
	}
	for key, value := range generic {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if item, ok := value.(string); ok {
			metadata[key] = strings.TrimSpace(item)
		}
	}
	return metadata
}

func putOrderMetadata(metadata map[string]string, key string, value string) bool {
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return false
	}
	if metadata[key] == value {
		return false
	}
	metadata[key] = value
	return true
}

func firstProviderSubscriptionID(result *payment.SubscriptionControlResult, fallback string) string {
	if result != nil && strings.TrimSpace(result.ProviderSubscriptionID) != "" {
		return strings.TrimSpace(result.ProviderSubscriptionID)
	}
	return strings.TrimSpace(fallback)
}

func putProviderControlMetadata(metadata map[string]string, result *payment.SubscriptionControlResult) bool {
	if result == nil {
		return false
	}
	changed := false
	changed = putOrderMetadata(metadata, "walnut_provider_subscription_status", result.Status) || changed
	changed = putOrderMetadata(metadata, "walnut_provider_subscription_raw_status", result.RawStatus) || changed
	if result.CurrentPeriodStartAt != nil {
		changed = putOrderMetadata(metadata, "walnut_provider_period_start_at", result.CurrentPeriodStartAt.UTC().Format(time.RFC3339)) || changed
	}
	if result.CurrentPeriodEndsAt != nil {
		changed = putOrderMetadata(metadata, "walnut_provider_period_end_at", result.CurrentPeriodEndsAt.UTC().Format(time.RFC3339)) || changed
	}
	return changed
}

func providerSubscriptionIDFromRawPayload(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		if values, parseErr := url.ParseQuery(raw); parseErr == nil {
			return firstSubscriptionID(
				values.Get("subscription_id"),
				values.Get("provider_subscription_id"),
				values.Get("walnut_provider_subscription_id"),
			)
		}
		return ""
	}
	if id := firstSubscriptionID(
		stringAtPath(payload, "subscription_id"),
		stringAtPath(payload, "provider_subscription_id"),
		stringAtPath(payload, "walnut_provider_subscription_id"),
		stringAtPath(payload, "object", "subscription", "id"),
		stringAtPath(payload, "object", "subscription_id"),
		stringAtPath(payload, "object", "subscription"),
		stringAtPath(payload, "object", "metadata", "provider_subscription_id"),
		stringAtPath(payload, "object", "metadata", "walnut_provider_subscription_id"),
	); id != "" {
		return id
	}
	if id := stringAtPath(payload, "object", "id"); looksLikeSubscriptionID(id) {
		return id
	}
	return ""
}

func firstSubscriptionID(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func looksLikeSubscriptionID(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "sub_") || strings.HasPrefix(value, "subscription_")
}

func stringAtPath(value any, path ...string) string {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	if result, ok := current.(string); ok {
		return strings.TrimSpace(result)
	}
	return ""
}

func activateSubscriptionCancellation(
	ctx context.Context,
	repo repository.SubscriptionCancellationRepository,
	cancellation *domain.SubscriptionCancellation,
	input SubscriptionCancellationInput,
	period subscriptionPeriodProjection,
	now time.Time,
) (*domain.SubscriptionCancellation, error) {
	if cancellation == nil {
		return nil, ErrSubscriptionNotFound
	}
	cancellation.Status = SubscriptionCancellationStatusCancelAtPeriodEnd
	cancellation.CancelAtPeriodEnd = true
	cancellation.CurrentPeriodEndsAt = period.EndsAt.UTC()
	cancellation.Reason = input.Reason
	cancellation.Source = input.Source
	cancellation.ResumedAt = nil
	cancellation.UpdatedAt = now.UTC()
	if err := repo.Update(ctx, cancellation); err != nil {
		return nil, err
	}
	return cancellation, nil
}

func resumeSubscriptionCancellation(
	ctx context.Context,
	repo repository.SubscriptionCancellationRepository,
	input SubscriptionResumeInput,
	now time.Time,
) (*domain.SubscriptionCancellation, error) {
	if input.IdempotencyKey != "" {
		existing, err := repo.GetByResumeIdempotencyKey(ctx, input.IdempotencyKey)
		if err == nil {
			if existing.Status == SubscriptionCancellationStatusCancelAtPeriodEnd || existing.CancelAtPeriodEnd {
				return deactivateSubscriptionCancellation(ctx, repo, existing, input, now)
			}
			return existing, nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	cancellation, err := repo.FindActive(ctx, repository.SubscriptionCancellationQuery{
		UserID:  input.UserID,
		SKUCode: input.SKUCode,
		Status:  SubscriptionCancellationStatusCancelAtPeriodEnd,
	})
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, err
	}
	return deactivateSubscriptionCancellation(ctx, repo, cancellation, input, now)
}

func deactivateSubscriptionCancellation(
	ctx context.Context,
	repo repository.SubscriptionCancellationRepository,
	cancellation *domain.SubscriptionCancellation,
	input SubscriptionResumeInput,
	now time.Time,
) (*domain.SubscriptionCancellation, error) {
	if cancellation == nil {
		return nil, ErrSubscriptionNotFound
	}
	cancellation.Status = SubscriptionStatusActive
	cancellation.CancelAtPeriodEnd = false
	cancellation.Source = input.Source
	cancellation.ResumeIdempotencyKey = input.IdempotencyKey
	resumedAt := now.UTC()
	cancellation.ResumedAt = &resumedAt
	cancellation.UpdatedAt = resumedAt
	if cancellation.ResumeIdempotencyKey == "" {
		cancellation.ResumeIdempotencyKey = "subscription_resume:" + cancellation.UserID + ":" + cancellation.SKUCode + ":" + cancellation.CurrentPeriodEndsAt.Format("20060102150405")
	}
	if err := repo.Update(ctx, cancellation); err != nil {
		if existing, getErr := repo.GetByResumeIdempotencyKey(ctx, cancellation.ResumeIdempotencyKey); getErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return cancellation, nil
}

func subscriptionCancellationResult(input SubscriptionCancellationInput, order *domain.Order, period subscriptionPeriodProjection, cancellation *domain.SubscriptionCancellation) *SubscriptionCancellationResult {
	sourceOrderNo := ""
	id := ""
	if order != nil {
		sourceOrderNo = order.OutTradeNo
	}
	if cancellation != nil {
		id = cancellation.ID
		if cancellation.SourceOrderNo != "" {
			sourceOrderNo = cancellation.SourceOrderNo
		}
	}
	periodEnd := period.EndsAt.UTC().Format(time.RFC3339)
	record := SubscriptionCancellationRecord{
		UserID:              input.UserID,
		SKUCode:             input.SKUCode,
		Status:              SoftwareSubscriptionStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:   true,
		CurrentPeriodEndsAt: periodEnd,
		SourceOrderNo:       sourceOrderNo,
		ID:                  id,
	}
	return &SubscriptionCancellationResult{
		UserID:              input.UserID,
		SKUCode:             input.SKUCode,
		Status:              SoftwareSubscriptionStatusCancelAtPeriodEnd,
		CancelAtPeriodEnd:   true,
		CurrentPeriodEndsAt: periodEnd,
		Subscription:        record,
		Projection: SoftwareSubscriptionProjection{
			UserID:              input.UserID,
			SKUCode:             input.SKUCode,
			Status:              SoftwareSubscriptionStatusCancelAtPeriodEnd,
			CancelAtPeriodEnd:   true,
			CurrentPeriodEndsAt: periodEnd,
		},
	}
}

func subscriptionResumeResult(input SubscriptionResumeInput, period subscriptionPeriodProjection, cancellation *domain.SubscriptionCancellation) *SubscriptionCancellationResult {
	sourceOrderNo := ""
	id := ""
	if cancellation != nil {
		id = cancellation.ID
		sourceOrderNo = cancellation.SourceOrderNo
	}
	periodEnd := period.EndsAt.UTC().Format(time.RFC3339)
	record := SubscriptionCancellationRecord{
		UserID:              input.UserID,
		SKUCode:             input.SKUCode,
		Status:              SubscriptionStatusActive,
		CancelAtPeriodEnd:   false,
		CurrentPeriodEndsAt: periodEnd,
		SourceOrderNo:       sourceOrderNo,
		ID:                  id,
	}
	return &SubscriptionCancellationResult{
		UserID:              input.UserID,
		SKUCode:             input.SKUCode,
		Status:              SubscriptionStatusActive,
		CancelAtPeriodEnd:   false,
		CurrentPeriodEndsAt: periodEnd,
		Subscription:        record,
		Projection: SoftwareSubscriptionProjection{
			UserID:              input.UserID,
			SKUCode:             input.SKUCode,
			Status:              SoftwareSubscriptionStatusActive,
			CancelAtPeriodEnd:   false,
			CurrentPeriodEndsAt: periodEnd,
		},
	}
}

func firstSubscriptionCancellationRepo(primary repository.SubscriptionCancellationRepository, fallback repository.SubscriptionCancellationRepository) repository.SubscriptionCancellationRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstPaymentEventRepo(primary repository.PaymentEventRepository, fallback repository.PaymentEventRepository) repository.PaymentEventRepository {
	if primary != nil {
		return primary
	}
	return fallback
}
