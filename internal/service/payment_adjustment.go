package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrInvalidPaymentAdjustment      = errors.New("invalid payment adjustment")
	ErrPaymentAdjustmentManualReview = errors.New("payment adjustment requires manual review")
	ErrPaymentAdjustmentRejected     = errors.New("payment adjustment rejected by policy")
)

// PaymentAdjustmentService applies Walnut-owned compensation policy for payment
// events such as refunds and subscription cancellations. Provider webhooks only
// supply facts; this service decides how grants and credits are adjusted.
type PaymentAdjustmentService interface {
	Apply(ctx context.Context, event *domain.PaymentEventInbox) (*PaymentAdjustmentResult, error)
}

type PaymentAdjustmentResult struct {
	Order              *domain.Order                   `json:"order"`
	PolicyDecision     PaymentAdjustmentPolicyDecision `json:"policy_decision"`
	RiskFlag           *domain.PaymentRiskFlag         `json:"risk_flag,omitempty"`
	RevokedGrantIDs    []string                        `json:"revoked_grant_ids,omitempty"`
	ClawbackCredits    int64                           `json:"clawback_credits,omitempty"`
	ClawbackTx         *domain.CreditTransaction       `json:"clawback_transaction,omitempty"`
	AffectedExecutions []domain.FulfillmentExecution   `json:"affected_executions,omitempty"`
	Note               string                          `json:"note,omitempty"`
}

type PaymentAdjustmentRepositories struct {
	Orders                repository.OrderRepository
	EntitlementGrants     repository.EntitlementGrantRepository
	CreditAccounts        repository.CreditAccountRepository
	CreditTransactions    repository.CreditTransactionRepository
	FulfillmentExecutions repository.FulfillmentExecutionRepository
	PaymentRiskFlags      repository.PaymentRiskFlagRepository
}

type PaymentAdjustmentDependencies struct {
	Repositories      PaymentAdjustmentRepositories
	Policy            PaymentAdjustmentPolicy
	UnitOfWorkFactory func() repository.UnitOfWork
}

type paymentAdjustmentServiceImpl struct {
	repos      PaymentAdjustmentRepositories
	policy     PaymentAdjustmentPolicy
	uowFactory func() repository.UnitOfWork
}

func NewPaymentAdjustmentService(deps PaymentAdjustmentDependencies) PaymentAdjustmentService {
	policy := deps.Policy
	if policy == nil {
		policy = NewConfigurablePaymentAdjustmentPolicy(DefaultPaymentAdjustmentPolicyConfig())
	}
	return &paymentAdjustmentServiceImpl{repos: deps.Repositories, policy: policy, uowFactory: deps.UnitOfWorkFactory}
}

func (s *paymentAdjustmentServiceImpl) Apply(ctx context.Context, event *domain.PaymentEventInbox) (*PaymentAdjustmentResult, error) {
	if s == nil || event == nil || strings.TrimSpace(event.OutTradeNo) == "" || !s.hasRequiredRepos(s.repos) {
		return nil, ErrInvalidPaymentAdjustment
	}
	if event.EventType != domain.PaymentEventTypeRefunded && event.EventType != domain.PaymentEventTypeDisputed && event.EventType != domain.PaymentEventTypeCancelled {
		return nil, ErrInvalidPaymentAdjustment
	}
	return s.withAdjustmentTransaction(ctx, func(repos PaymentAdjustmentRepositories) (*PaymentAdjustmentResult, error) {
		return s.applyWithRepos(ctx, repos, event)
	})
}

func (s *paymentAdjustmentServiceImpl) applyWithRepos(ctx context.Context, repos PaymentAdjustmentRepositories, event *domain.PaymentEventInbox) (*PaymentAdjustmentResult, error) {
	order, err := repos.Orders.GetByOutTradeNo(ctx, event.OutTradeNo)
	if err != nil {
		return nil, err
	}
	if order.OrderType != domain.OrderTypeCheckout || strings.TrimSpace(order.UserID) == "" {
		return &PaymentAdjustmentResult{Order: order, Note: "non_commerce_order_ignored"}, nil
	}
	executions, err := repos.FulfillmentExecutions.List(ctx, repository.FulfillmentExecutionQuery{
		OutTradeNo: order.OutTradeNo,
		Status:     domain.FulfillmentExecutionStatusApplied,
	})
	if err != nil {
		return nil, err
	}

	usage, err := paymentAdjustmentUsageSnapshot(ctx, repos, order, executions)
	if err != nil {
		return nil, err
	}
	decision := s.policy.Decide(ctx, PaymentAdjustmentPolicyInput{Event: event, Order: order, Usage: usage})
	result := &PaymentAdjustmentResult{Order: order, PolicyDecision: decision, AffectedExecutions: executions, Note: decision.Note}
	if decision.CreateRiskFlag {
		flag, err := createPaymentRiskFlag(ctx, repos.PaymentRiskFlags, order, event)
		if err != nil {
			return result, err
		}
		result.RiskFlag = flag
	}
	if !decision.ApplyCompensation {
		if decision.ManualReview {
			return result, newPaymentEventPolicyError(
				domain.PaymentEventStatusReviewRequired,
				fmt.Errorf("%w: %s", ErrPaymentAdjustmentManualReview, decision.Reason),
			)
		}
		if decision.Rejected {
			return result, newPaymentEventPolicyError(
				domain.PaymentEventStatusPolicyRejected,
				fmt.Errorf("%w: %s", ErrPaymentAdjustmentRejected, decision.Reason),
			)
		}
		return result, nil
	}
	for _, execution := range executions {
		switch execution.TargetType {
		case domain.FulfillmentTargetEntitlement:
			if !decision.RevokeEntitlements {
				continue
			}
			grantID, err := revokeFulfilledGrant(ctx, repos.EntitlementGrants, execution.ResultRef)
			if err != nil {
				return result, err
			}
			if grantID != "" {
				result.RevokedGrantIDs = append(result.RevokedGrantIDs, grantID)
			}
		case domain.FulfillmentTargetCredits:
			if !decision.ClawbackCredits {
				continue
			}
			amount, tx, err := clawbackFulfilledCredits(ctx, repos, order, execution)
			if err != nil {
				return result, err
			}
			result.ClawbackCredits += amount
			if tx != nil {
				result.ClawbackTx = tx
			}
		}
	}
	return result, nil
}

func paymentAdjustmentUsageSnapshot(ctx context.Context, repos PaymentAdjustmentRepositories, order *domain.Order, executions []domain.FulfillmentExecution) (PaymentAdjustmentUsageSnapshot, error) {
	usage := PaymentAdjustmentUsageSnapshot{Known: true}
	if order == nil {
		return usage, nil
	}
	remainingByAccount := map[string]int64{}
	for _, execution := range executions {
		if execution.TargetType != domain.FulfillmentTargetCredits || strings.TrimSpace(execution.ResultRef) == "" {
			continue
		}
		original, err := repos.CreditTransactions.GetByID(ctx, execution.ResultRef)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return usage, err
		}
		if original.Amount <= 0 || original.UserID != order.UserID {
			continue
		}
		usage.CreditsGranted += original.Amount
		remaining, ok := remainingByAccount[original.AccountID]
		if !ok {
			account, err := repos.CreditAccounts.GetByID(ctx, original.AccountID)
			if err != nil {
				return usage, err
			}
			remaining = account.Balance
		}
		available := minInt64(original.Amount, remaining)
		if available < 0 {
			available = 0
		}
		usage.CreditsAvailableForClawback += available
		remainingByAccount[original.AccountID] = remaining - available
	}
	if usage.CreditsGranted > usage.CreditsAvailableForClawback {
		usage.CreditsUsed = usage.CreditsGranted - usage.CreditsAvailableForClawback
	}
	return usage, nil
}

func revokeFulfilledGrant(ctx context.Context, grants repository.EntitlementGrantRepository, grantID string) (string, error) {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return "", nil
	}
	grant, err := grants.GetByID(ctx, grantID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	if grant.Status == domain.GrantStatusRevoked {
		return "", nil
	}
	now := time.Now().UTC()
	grant.Status = domain.GrantStatusRevoked
	grant.RevokedAt = &now
	grant.UpdatedAt = now
	if grant.ExpiresAt == nil || grant.ExpiresAt.After(now) {
		grant.ExpiresAt = &now
	}
	if err := grants.Update(ctx, grant); err != nil {
		return "", err
	}
	return grant.ID, nil
}

func clawbackFulfilledCredits(ctx context.Context, repos PaymentAdjustmentRepositories, order *domain.Order, execution domain.FulfillmentExecution) (int64, *domain.CreditTransaction, error) {
	if strings.TrimSpace(execution.ResultRef) == "" {
		return 0, nil, nil
	}
	key := refundClawbackKey(order.OutTradeNo, execution.RuleID)
	existing, err := repos.CreditTransactions.GetByIdempotencyKey(ctx, key)
	if err == nil {
		return -existing.Amount, existing, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return 0, nil, err
	}
	original, err := repos.CreditTransactions.GetByID(ctx, execution.ResultRef)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	if original.Amount <= 0 {
		return 0, nil, nil
	}
	if original.UserID != order.UserID {
		return 0, nil, ErrInvalidPaymentAdjustment
	}
	account, err := repos.CreditAccounts.GetByID(ctx, original.AccountID)
	if err != nil {
		return 0, nil, err
	}
	clawbackAmount := minInt64(original.Amount, account.Balance)
	now := time.Now().UTC()
	if clawbackAmount > 0 {
		account.Balance -= clawbackAmount
		account.UpdatedAt = now
		if err := repos.CreditAccounts.Update(ctx, account); err != nil {
			return 0, nil, err
		}
	}
	transaction, err := newCreditTransaction(ctx, repos.CreditTransactions, creditTransactionInput{
		Account:         account,
		TransactionType: domain.CreditTransactionTypeClawback,
		Amount:          -clawbackAmount,
		IdempotencyKey:  key,
		Source:          "refund_policy",
		Description:     fmt.Sprintf("refund clawback for %s (%s)", order.SKUCode, order.OutTradeNo),
		CreatedAt:       now,
	})
	if err != nil {
		return 0, nil, err
	}
	return clawbackAmount, transaction, nil
}

func (s *paymentAdjustmentServiceImpl) withAdjustmentTransaction(
	ctx context.Context,
	fn func(PaymentAdjustmentRepositories) (*PaymentAdjustmentResult, error),
) (*PaymentAdjustmentResult, error) {
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

	repos := adjustmentReposFromUOW(uow.Repos(), s.repos)
	result, err := fn(repos)
	if err != nil {
		if _, ok := paymentEventPolicyErrorFrom(err); ok {
			if commitErr := uow.Commit(); commitErr != nil {
				return result, commitErr
			}
			committed = true
		}
		return result, err
	}
	if err := uow.Commit(); err != nil {
		return result, err
	}
	committed = true
	return result, nil
}

func (s *paymentAdjustmentServiceImpl) hasRequiredRepos(repos PaymentAdjustmentRepositories) bool {
	return repos.Orders != nil &&
		repos.EntitlementGrants != nil &&
		repos.CreditAccounts != nil &&
		repos.CreditTransactions != nil &&
		repos.FulfillmentExecutions != nil
}

func adjustmentReposFromUOW(repos repository.TransactionalRepositories, fallback PaymentAdjustmentRepositories) PaymentAdjustmentRepositories {
	return PaymentAdjustmentRepositories{
		Orders:                firstOrderRepo(repos.OrderRepo, fallback.Orders),
		EntitlementGrants:     firstEntitlementGrantRepo(repos.EntitlementGrantRepo, fallback.EntitlementGrants),
		CreditAccounts:        firstCreditAccountRepo(repos.CreditAccountRepo, fallback.CreditAccounts),
		CreditTransactions:    firstCreditTransactionRepo(repos.CreditTransactionRepo, fallback.CreditTransactions),
		FulfillmentExecutions: firstFulfillmentExecutionRepo(repos.FulfillmentExecutionRepo, fallback.FulfillmentExecutions),
		PaymentRiskFlags:      firstPaymentRiskFlagRepo(repos.PaymentRiskFlagRepo, fallback.PaymentRiskFlags),
	}
}

func createPaymentRiskFlag(ctx context.Context, flags repository.PaymentRiskFlagRepository, order *domain.Order, event *domain.PaymentEventInbox) (*domain.PaymentRiskFlag, error) {
	if flags == nil || order == nil || event == nil {
		return nil, ErrInvalidPaymentAdjustment
	}
	providerEventID := strings.TrimSpace(event.ProviderEventID)
	if providerEventID == "" {
		providerEventID = "risk:" + strings.TrimSpace(event.Provider) + ":" + strings.TrimSpace(event.OutTradeNo) + ":" + strings.TrimSpace(event.EventType)
	}
	provider := strings.TrimSpace(event.Provider)
	existing, err := flags.GetByProviderEventID(ctx, provider, providerEventID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	flagID, err := generateEntityID("prf_")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	flag := &domain.PaymentRiskFlag{
		ID:              flagID,
		UserID:          strings.TrimSpace(order.UserID),
		OutTradeNo:      strings.TrimSpace(order.OutTradeNo),
		Provider:        provider,
		ProviderEventID: providerEventID,
		Reason:          domain.PaymentRiskReasonDispute,
		Severity:        domain.PaymentRiskSeverityCritical,
		Status:          domain.PaymentRiskStatusOpen,
		Note:            "provider dispute/chargeback event",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := flags.Create(ctx, flag); err != nil {
		return nil, err
	}
	return flag, nil
}

func firstPaymentRiskFlagRepo(primary repository.PaymentRiskFlagRepository, fallback repository.PaymentRiskFlagRepository) repository.PaymentRiskFlagRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func refundClawbackKey(outTradeNo string, ruleID string) string {
	return "refund:clawback:" + strings.TrimSpace(outTradeNo) + ":" + strings.TrimSpace(ruleID)
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
