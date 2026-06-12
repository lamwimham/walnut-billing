package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidFulfillmentOrder  = errors.New("invalid fulfillment order")
	ErrFulfillmentOrderNotPaid  = errors.New("order is not paid")
	ErrFulfillmentRulesNotFound = errors.New("fulfillment rules not found")
	ErrInvalidFulfillmentRule   = errors.New("invalid fulfillment rule")
)

type FulfillmentRuleType string

const (
	FulfillmentRuleGrantEntitlement FulfillmentRuleType = "grant_entitlement"
	FulfillmentRuleGrantCredits     FulfillmentRuleType = "grant_credits"
)

// FulfillmentRule maps one Walnut SKU to one delivery effect. The rule is
// intentionally provider-agnostic: checkout providers only produce paid orders.
type FulfillmentRule struct {
	ID            string              `json:"id"`
	SKUCode       string              `json:"sku_code"`
	Type          FulfillmentRuleType `json:"type"`
	EntitlementID string              `json:"entitlement_id,omitempty"`
	CreditsAmount int64               `json:"credits_amount,omitempty"`
	Duration      string              `json:"duration,omitempty"`
}

type FulfillmentCatalog interface {
	RulesForSKU(skuCode string) ([]FulfillmentRule, error)
}

type StaticFulfillmentCatalog struct {
	rulesBySKU map[string][]FulfillmentRule
}

func NewStaticFulfillmentCatalog(rules ...FulfillmentRule) (*StaticFulfillmentCatalog, error) {
	catalog := &StaticFulfillmentCatalog{rulesBySKU: make(map[string][]FulfillmentRule)}
	for _, rule := range rules {
		normalized, err := normalizeFulfillmentRule(rule)
		if err != nil {
			return nil, err
		}
		catalog.rulesBySKU[normalized.SKUCode] = append(catalog.rulesBySKU[normalized.SKUCode], normalized)
	}
	return catalog, nil
}

func NewFulfillmentCatalogFromJSON(raw string, fallback []FulfillmentRule) (FulfillmentCatalog, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return NewStaticFulfillmentCatalog(fallback...)
	}
	rules, err := decodeFulfillmentRules(raw)
	if err != nil {
		return nil, err
	}
	return NewStaticFulfillmentCatalog(rules...)
}

func DefaultFulfillmentRules() []FulfillmentRule {
	return []FulfillmentRule{
		{
			ID:            "editorial_studio_monthly:entitlement",
			SKUCode:       "editorial_studio_monthly",
			Type:          FulfillmentRuleGrantEntitlement,
			EntitlementID: domain.EntitlementEditorialStudio,
			Duration:      "monthly",
		},
		{
			ID:            "editorial_studio_monthly:credits_600",
			SKUCode:       "editorial_studio_monthly",
			Type:          FulfillmentRuleGrantCredits,
			CreditsAmount: 600,
		},
		{
			ID:            "credits_600:credits",
			SKUCode:       "credits_600",
			Type:          FulfillmentRuleGrantCredits,
			CreditsAmount: 600,
		},
	}
}

func (c *StaticFulfillmentCatalog) RulesForSKU(skuCode string) ([]FulfillmentRule, error) {
	if c == nil {
		return nil, ErrFulfillmentRulesNotFound
	}
	skuCode = strings.TrimSpace(skuCode)
	rules := c.rulesBySKU[skuCode]
	if len(rules) == 0 {
		return nil, ErrFulfillmentRulesNotFound
	}
	result := make([]FulfillmentRule, len(rules))
	copy(result, rules)
	return result, nil
}

type FulfillmentTargetResult struct {
	TargetType string
	TargetID   string
	ResultRef  string
}

type FulfillmentRepositories struct {
	Orders                repository.OrderRepository
	Users                 repository.UserRepository
	EntitlementGrants     repository.EntitlementGrantRepository
	CreditAccounts        repository.CreditAccountRepository
	CreditTransactions    repository.CreditTransactionRepository
	FulfillmentExecutions repository.FulfillmentExecutionRepository
}

type FulfillmentDependencies struct {
	Repositories       FulfillmentRepositories
	Catalog            FulfillmentCatalog
	EntitlementCatalog EntitlementCatalog
	UnitOfWorkFactory  func() repository.UnitOfWork
}

type FulfillmentRuleExecutor interface {
	RuleType() FulfillmentRuleType
	Execute(ctx context.Context, repos FulfillmentRepositories, order *domain.Order, rule FulfillmentRule) (*FulfillmentTargetResult, error)
}

type FulfillmentResult struct {
	Order            *domain.Order                 `json:"order"`
	Executions       []domain.FulfillmentExecution `json:"executions"`
	AlreadyFulfilled bool                          `json:"already_fulfilled"`
}

type FulfillmentService interface {
	FulfillOrder(ctx context.Context, order *domain.Order) (*FulfillmentResult, error)
	ListExecutions(ctx context.Context, query repository.FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error)
}

type fulfillmentServiceImpl struct {
	repos      FulfillmentRepositories
	catalog    FulfillmentCatalog
	executors  map[FulfillmentRuleType]FulfillmentRuleExecutor
	uowFactory func() repository.UnitOfWork
}

func NewFulfillmentService(deps FulfillmentDependencies) FulfillmentService {
	return NewFulfillmentServiceWithExecutors(deps, nil)
}

func NewFulfillmentServiceWithExecutors(deps FulfillmentDependencies, executors []FulfillmentRuleExecutor) FulfillmentService {
	if deps.EntitlementCatalog == nil {
		deps.EntitlementCatalog = DefaultEntitlementCatalog()
	}
	if len(executors) == 0 {
		executors = []FulfillmentRuleExecutor{
			&entitlementFulfillmentExecutor{catalog: deps.EntitlementCatalog},
			&creditFulfillmentExecutor{},
		}
	}
	byType := make(map[FulfillmentRuleType]FulfillmentRuleExecutor)
	for _, executor := range executors {
		if executor != nil {
			byType[executor.RuleType()] = executor
		}
	}
	return &fulfillmentServiceImpl{
		repos:      deps.Repositories,
		catalog:    deps.Catalog,
		executors:  byType,
		uowFactory: deps.UnitOfWorkFactory,
	}
}

func (s *fulfillmentServiceImpl) FulfillOrder(ctx context.Context, order *domain.Order) (*FulfillmentResult, error) {
	if order == nil || s == nil || s.catalog == nil || !s.hasRequiredRepos(s.repos) {
		return nil, ErrInvalidFulfillmentOrder
	}
	result, err := s.withFulfillmentTransaction(ctx, func(repos FulfillmentRepositories) (*FulfillmentResult, error) {
		return s.fulfillOrderWithRepos(ctx, repos, order)
	})
	if err != nil && result != nil {
		s.persistFailedExecutions(ctx, result.Executions)
	}
	return result, err
}

func (s *fulfillmentServiceImpl) ListExecutions(ctx context.Context, query repository.FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error) {
	if s == nil || s.repos.FulfillmentExecutions == nil {
		return nil, ErrInvalidFulfillmentOrder
	}
	return s.repos.FulfillmentExecutions.List(ctx, query)
}

func (s *fulfillmentServiceImpl) fulfillOrderWithRepos(ctx context.Context, repos FulfillmentRepositories, order *domain.Order) (*FulfillmentResult, error) {
	if order.OutTradeNo != "" {
		latest, err := repos.Orders.GetByOutTradeNo(ctx, order.OutTradeNo)
		if err != nil {
			return nil, err
		}
		order = latest
	}
	if strings.TrimSpace(order.UserID) == "" || strings.TrimSpace(order.SKUCode) == "" || order.OrderType != domain.OrderTypeCheckout {
		return nil, ErrInvalidFulfillmentOrder
	}
	if order.Status == domain.OrderStatusFulfilled {
		executions, err := repos.FulfillmentExecutions.List(ctx, repository.FulfillmentExecutionQuery{OutTradeNo: order.OutTradeNo})
		if err != nil {
			return nil, err
		}
		return &FulfillmentResult{Order: order, Executions: executions, AlreadyFulfilled: true}, nil
	}
	if order.Status != domain.OrderStatusPaid {
		return nil, ErrFulfillmentOrderNotPaid
	}
	if err := ensureFulfillmentUser(ctx, repos.Users, order.UserID); err != nil {
		return nil, err
	}

	rules, err := s.catalog.RulesForSKU(order.SKUCode)
	if err != nil {
		return nil, err
	}
	result := &FulfillmentResult{Order: order, Executions: make([]domain.FulfillmentExecution, 0, len(rules))}
	for _, rule := range rules {
		execution, err := s.applyRule(ctx, repos, order, rule)
		if execution != nil {
			result.Executions = append(result.Executions, *execution)
		}
		if err != nil {
			return result, err
		}
	}

	now := time.Now().UTC()
	order.Status = domain.OrderStatusFulfilled
	if order.FulfilledAt == nil {
		order.FulfilledAt = &now
	}
	if err := repos.Orders.Update(ctx, order); err != nil {
		return result, err
	}
	result.Order = order
	return result, nil
}

func (s *fulfillmentServiceImpl) applyRule(ctx context.Context, repos FulfillmentRepositories, order *domain.Order, rule FulfillmentRule) (*domain.FulfillmentExecution, error) {
	rule, err := normalizeFulfillmentRule(rule)
	if err != nil {
		return nil, err
	}
	key := fulfillmentRuleExecutionKey(order.OutTradeNo, rule.ID)
	existing, err := repos.FulfillmentExecutions.GetByIdempotencyKey(ctx, key)
	if err == nil && existing.Status == domain.FulfillmentExecutionStatusApplied {
		return existing, nil
	}
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if existing == nil && err == nil {
		return nil, ErrInvalidFulfillmentRule
	}
	executor := s.executors[rule.Type]
	if executor == nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidFulfillmentRule, rule.Type)
	}
	execution, err := newExecutionFromOrderRule(order, rule, key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		execution.ID = existing.ID
		execution.CreatedAt = existing.CreatedAt
	}

	target, execErr := executor.Execute(ctx, repos, order, rule)
	if target != nil {
		execution.TargetType = target.TargetType
		execution.TargetID = target.TargetID
		execution.ResultRef = target.ResultRef
	}
	if execErr != nil {
		execution.Status = domain.FulfillmentExecutionStatusFailed
		execution.LastError = execErr.Error()
		if saveErr := saveFulfillmentExecution(ctx, repos.FulfillmentExecutions, execution, existing != nil); saveErr != nil {
			return nil, saveErr
		}
		return execution, execErr
	}

	execution.Status = domain.FulfillmentExecutionStatusApplied
	execution.LastError = ""
	if saveErr := saveFulfillmentExecution(ctx, repos.FulfillmentExecutions, execution, existing != nil); saveErr != nil {
		latest, getErr := repos.FulfillmentExecutions.GetByIdempotencyKey(ctx, key)
		if getErr == nil && latest.Status == domain.FulfillmentExecutionStatusApplied {
			return latest, nil
		}
		return nil, saveErr
	}
	return execution, nil
}

func (s *fulfillmentServiceImpl) withFulfillmentTransaction(
	ctx context.Context,
	fn func(FulfillmentRepositories) (*FulfillmentResult, error),
) (*FulfillmentResult, error) {
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

	repos := fulfillmentReposFromUOW(uow.Repos(), s.repos)
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

func (s *fulfillmentServiceImpl) persistFailedExecutions(ctx context.Context, executions []domain.FulfillmentExecution) {
	if s == nil || s.repos.FulfillmentExecutions == nil {
		return
	}
	for idx := range executions {
		execution := executions[idx]
		if execution.Status != domain.FulfillmentExecutionStatusFailed || execution.IdempotencyKey == "" {
			continue
		}
		existing, err := s.repos.FulfillmentExecutions.GetByIdempotencyKey(ctx, execution.IdempotencyKey)
		if err == nil {
			execution.ID = existing.ID
			execution.CreatedAt = existing.CreatedAt
			_ = saveFulfillmentExecution(ctx, s.repos.FulfillmentExecutions, &execution, true)
			continue
		}
		if errors.Is(err, repository.ErrNotFound) {
			_ = saveFulfillmentExecution(ctx, s.repos.FulfillmentExecutions, &execution, false)
		}
	}
}

func (s *fulfillmentServiceImpl) hasRequiredRepos(repos FulfillmentRepositories) bool {
	return repos.Orders != nil &&
		repos.Users != nil &&
		repos.EntitlementGrants != nil &&
		repos.CreditAccounts != nil &&
		repos.CreditTransactions != nil &&
		repos.FulfillmentExecutions != nil
}

func fulfillmentReposFromUOW(repos repository.TransactionalRepositories, fallback FulfillmentRepositories) FulfillmentRepositories {
	return FulfillmentRepositories{
		Orders:                firstOrderRepo(repos.OrderRepo, fallback.Orders),
		Users:                 firstUserRepo(repos.UserRepo, fallback.Users),
		EntitlementGrants:     firstEntitlementGrantRepo(repos.EntitlementGrantRepo, fallback.EntitlementGrants),
		CreditAccounts:        firstCreditAccountRepo(repos.CreditAccountRepo, fallback.CreditAccounts),
		CreditTransactions:    firstCreditTransactionRepo(repos.CreditTransactionRepo, fallback.CreditTransactions),
		FulfillmentExecutions: firstFulfillmentExecutionRepo(repos.FulfillmentExecutionRepo, fallback.FulfillmentExecutions),
	}
}

func saveFulfillmentExecution(ctx context.Context, repo repository.FulfillmentExecutionRepository, execution *domain.FulfillmentExecution, exists bool) error {
	execution.UpdatedAt = time.Now().UTC()
	if exists {
		return repo.Update(ctx, execution)
	}
	return repo.Create(ctx, execution)
}

type entitlementFulfillmentExecutor struct {
	catalog EntitlementCatalog
}

func (e *entitlementFulfillmentExecutor) RuleType() FulfillmentRuleType {
	return FulfillmentRuleGrantEntitlement
}

func (e *entitlementFulfillmentExecutor) Execute(ctx context.Context, repos FulfillmentRepositories, order *domain.Order, rule FulfillmentRule) (*FulfillmentTargetResult, error) {
	if e == nil || strings.TrimSpace(rule.EntitlementID) == "" {
		return nil, ErrInvalidFulfillmentRule
	}
	anchor := entitlementFulfillmentAnchor(ctx, repos.EntitlementGrants, order.UserID, rule.EntitlementID, fulfillmentAnchorTime(order))
	expiresAt, err := fulfillmentRuleExpiresAt(rule, anchor)
	if err != nil {
		return nil, err
	}
	grant, err := createGrantWithRepos(ctx, repos.Users, repos.EntitlementGrants, e.catalog, GrantInput{
		UserID:         order.UserID,
		EntitlementID:  rule.EntitlementID,
		CreatedBy:      "system",
		Source:         domain.GrantSourceFulfillment,
		ExpiresAt:      expiresAt,
		IdempotencyKey: fulfillmentRuleTargetKey(order.OutTradeNo, rule.ID, "entitlement"),
	})
	if err != nil {
		return nil, err
	}
	return &FulfillmentTargetResult{TargetType: domain.FulfillmentTargetEntitlement, TargetID: rule.EntitlementID, ResultRef: grant.ID}, nil
}

type creditFulfillmentExecutor struct{}

func (e *creditFulfillmentExecutor) RuleType() FulfillmentRuleType {
	return FulfillmentRuleGrantCredits
}

func (e *creditFulfillmentExecutor) Execute(ctx context.Context, repos FulfillmentRepositories, order *domain.Order, rule FulfillmentRule) (*FulfillmentTargetResult, error) {
	if e == nil || rule.CreditsAmount <= 0 {
		return nil, ErrInvalidFulfillmentRule
	}
	mutation, err := grantCreditsWithRepos(ctx, creditRepos{
		accounts:     repos.CreditAccounts,
		transactions: repos.CreditTransactions,
	}, CreditGrantInput{
		UserID:         order.UserID,
		Amount:         rule.CreditsAmount,
		IdempotencyKey: fulfillmentRuleTargetKey(order.OutTradeNo, rule.ID, "credits"),
		Source:         domain.GrantSourceFulfillment,
		Description:    fmt.Sprintf("fulfillment for %s (%s)", order.SKUCode, order.OutTradeNo),
	})
	if err != nil {
		return nil, err
	}
	resultRef := ""
	if mutation != nil && mutation.Transaction != nil {
		resultRef = mutation.Transaction.ID
	}
	return &FulfillmentTargetResult{TargetType: domain.FulfillmentTargetCredits, TargetID: domain.CreditMetricBalance, ResultRef: resultRef}, nil
}

func decodeFulfillmentRules(raw string) ([]FulfillmentRule, error) {
	var rules []FulfillmentRule
	if err := json.Unmarshal([]byte(raw), &rules); err == nil {
		return rules, nil
	}
	var doc struct {
		Rules []FulfillmentRule `json:"rules"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	return doc.Rules, nil
}

func normalizeFulfillmentRule(rule FulfillmentRule) (FulfillmentRule, error) {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.SKUCode = strings.TrimSpace(rule.SKUCode)
	rule.Type = FulfillmentRuleType(strings.TrimSpace(string(rule.Type)))
	rule.EntitlementID = strings.TrimSpace(rule.EntitlementID)
	rule.Duration = strings.TrimSpace(rule.Duration)
	if rule.ID == "" || rule.SKUCode == "" || rule.Type == "" {
		return FulfillmentRule{}, ErrInvalidFulfillmentRule
	}
	switch rule.Type {
	case FulfillmentRuleGrantEntitlement:
		if rule.EntitlementID == "" {
			return FulfillmentRule{}, ErrInvalidFulfillmentRule
		}
	case FulfillmentRuleGrantCredits:
		if rule.CreditsAmount <= 0 {
			return FulfillmentRule{}, ErrInvalidFulfillmentRule
		}
	default:
		return FulfillmentRule{}, fmt.Errorf("%w: %s", ErrInvalidFulfillmentRule, rule.Type)
	}
	return rule, nil
}

func fulfillmentRuleExpiresAt(rule FulfillmentRule, anchor time.Time) (*time.Time, error) {
	duration := strings.ToLower(strings.TrimSpace(rule.Duration))
	switch duration {
	case "", "none", "lifetime", "permanent":
		return nil, nil
	case "monthly", "month", "1m":
		expiresAt := anchor.AddDate(0, 1, 0)
		return &expiresAt, nil
	case "yearly", "annual", "year", "1y":
		expiresAt := anchor.AddDate(1, 0, 0)
		return &expiresAt, nil
	}
	if strings.HasSuffix(duration, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(duration, "d"))
		if err != nil || days <= 0 {
			return nil, ErrInvalidFulfillmentRule
		}
		expiresAt := anchor.Add(time.Duration(days) * 24 * time.Hour)
		return &expiresAt, nil
	}
	parsed, err := time.ParseDuration(duration)
	if err != nil || parsed <= 0 {
		return nil, ErrInvalidFulfillmentRule
	}
	expiresAt := anchor.Add(parsed)
	return &expiresAt, nil
}

func fulfillmentAnchorTime(order *domain.Order) time.Time {
	if order != nil && order.PaidAt != nil {
		return order.PaidAt.UTC()
	}
	return time.Now().UTC()
}

func entitlementFulfillmentAnchor(ctx context.Context, grants repository.EntitlementGrantRepository, userID string, entitlementID string, fallback time.Time) time.Time {
	if grants == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(entitlementID) == "" {
		return fallback
	}
	existing, err := grants.List(ctx, repository.EntitlementGrantQuery{
		UserID:         strings.TrimSpace(userID),
		EntitlementID:  strings.TrimSpace(entitlementID),
		Status:         domain.GrantStatusActive,
		IncludeExpired: false,
	})
	if err != nil {
		return fallback
	}
	anchor := fallback
	for _, grant := range existing {
		if grant.ExpiresAt != nil && grant.ExpiresAt.After(anchor) {
			anchor = grant.ExpiresAt.UTC()
		}
	}
	return anchor
}

func newExecutionFromOrderRule(order *domain.Order, rule FulfillmentRule, key string) (*domain.FulfillmentExecution, error) {
	executionID, err := generateEntityID("ful_")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &domain.FulfillmentExecution{
		ID:             executionID,
		OrderID:        order.ID,
		OutTradeNo:     order.OutTradeNo,
		UserID:         order.UserID,
		SKUCode:        order.SKUCode,
		RuleID:         rule.ID,
		TargetType:     defaultFulfillmentTargetType(rule),
		TargetID:       defaultFulfillmentTargetID(rule),
		IdempotencyKey: key,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

func defaultFulfillmentTargetType(rule FulfillmentRule) string {
	switch rule.Type {
	case FulfillmentRuleGrantEntitlement:
		return domain.FulfillmentTargetEntitlement
	case FulfillmentRuleGrantCredits:
		return domain.FulfillmentTargetCredits
	default:
		return ""
	}
}

func defaultFulfillmentTargetID(rule FulfillmentRule) string {
	switch rule.Type {
	case FulfillmentRuleGrantEntitlement:
		return rule.EntitlementID
	case FulfillmentRuleGrantCredits:
		return domain.CreditMetricBalance
	default:
		return ""
	}
}

func ensureFulfillmentUser(ctx context.Context, users repository.UserRepository, userID string) error {
	if users == nil || strings.TrimSpace(userID) == "" {
		return ErrUserNotFound
	}
	_, err := users.GetByID(ctx, strings.TrimSpace(userID))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrUserNotFound
		}
		return err
	}
	return nil
}

func firstOrderRepo(primary repository.OrderRepository, fallback repository.OrderRepository) repository.OrderRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstUserRepo(primary repository.UserRepository, fallback repository.UserRepository) repository.UserRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstEntitlementGrantRepo(primary repository.EntitlementGrantRepository, fallback repository.EntitlementGrantRepository) repository.EntitlementGrantRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstFulfillmentExecutionRepo(primary repository.FulfillmentExecutionRepository, fallback repository.FulfillmentExecutionRepository) repository.FulfillmentExecutionRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func fulfillmentRuleExecutionKey(outTradeNo string, ruleID string) string {
	return "fulfillment:" + strings.TrimSpace(outTradeNo) + ":" + strings.TrimSpace(ruleID)
}

func fulfillmentRuleTargetKey(outTradeNo string, ruleID string, target string) string {
	return fulfillmentRuleExecutionKey(outTradeNo, ruleID) + ":" + strings.TrimSpace(target)
}
