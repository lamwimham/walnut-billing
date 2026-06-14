package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type mockPaymentRiskFlagRepo struct {
	flags map[string]*domain.PaymentRiskFlag
}

type mockPaymentAdjustmentUnitOfWork struct {
	orders       *mockTxOrderRepo
	grants       *mockGrantRepo
	accounts     *mockCreditAccountRepo
	transactions *mockCreditTransactionRepo
	buckets      *mockCreditBucketRepo
	executions   *mockFulfillmentExecutionRepo
	risks        *mockPaymentRiskFlagRepo
	begins       int
	commits      int
	rollbacks    int
}

func newMockPaymentRiskFlagRepo() *mockPaymentRiskFlagRepo {
	return &mockPaymentRiskFlagRepo{flags: make(map[string]*domain.PaymentRiskFlag)}
}

func (m *mockPaymentRiskFlagRepo) Create(ctx context.Context, flag *domain.PaymentRiskFlag) error {
	m.flags[flag.ID] = flag
	return nil
}

func (m *mockPaymentRiskFlagRepo) GetByID(ctx context.Context, id string) (*domain.PaymentRiskFlag, error) {
	flag, ok := m.flags[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return flag, nil
}

func (m *mockPaymentRiskFlagRepo) GetByProviderEventID(ctx context.Context, provider string, providerEventID string) (*domain.PaymentRiskFlag, error) {
	for _, flag := range m.flags {
		if flag.Provider == provider && flag.ProviderEventID == providerEventID {
			return flag, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockPaymentRiskFlagRepo) List(ctx context.Context, query repository.PaymentRiskFlagQuery) ([]domain.PaymentRiskFlag, error) {
	var result []domain.PaymentRiskFlag
	for _, flag := range m.flags {
		if query.UserID != "" && flag.UserID != query.UserID {
			continue
		}
		if query.OutTradeNo != "" && flag.OutTradeNo != query.OutTradeNo {
			continue
		}
		if query.Provider != "" && flag.Provider != query.Provider {
			continue
		}
		if query.Reason != "" && flag.Reason != query.Reason {
			continue
		}
		if query.Severity != "" && flag.Severity != query.Severity {
			continue
		}
		if query.Status != "" && flag.Status != query.Status {
			continue
		}
		result = append(result, *flag)
	}
	return result, nil
}

func (m *mockPaymentRiskFlagRepo) Update(ctx context.Context, flag *domain.PaymentRiskFlag) error {
	m.flags[flag.ID] = flag
	return nil
}

func (m *mockPaymentAdjustmentUnitOfWork) Begin(ctx context.Context) error {
	m.begins++
	return nil
}

func (m *mockPaymentAdjustmentUnitOfWork) Repos() repository.TransactionalRepositories {
	return repository.TransactionalRepositories{
		OrderRepo:                m.orders,
		EntitlementGrantRepo:     m.grants,
		CreditAccountRepo:        m.accounts,
		CreditTransactionRepo:    m.transactions,
		CreditBucketRepo:         m.buckets,
		FulfillmentExecutionRepo: m.executions,
		PaymentRiskFlagRepo:      m.risks,
	}
}

func (m *mockPaymentAdjustmentUnitOfWork) Commit() error {
	m.commits++
	return nil
}

func (m *mockPaymentAdjustmentUnitOfWork) Rollback() error {
	m.rollbacks++
	return nil
}

func newPaymentAdjustmentTestService() (PaymentAdjustmentService, *mockTxOrderRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockCreditTransactionRepo, *mockFulfillmentExecutionRepo, *mockPaymentRiskFlagRepo) {
	return newPaymentAdjustmentTestServiceWithPolicy(paymentAdjustmentTestAutoRefundPolicy())
}

func newPaymentAdjustmentTestServiceWithPolicy(policy PaymentAdjustmentPolicy) (PaymentAdjustmentService, *mockTxOrderRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockCreditTransactionRepo, *mockFulfillmentExecutionRepo, *mockPaymentRiskFlagRepo) {
	svc, orders, grants, accounts, transactions, executions, risks, _ := newPaymentAdjustmentTestServiceWithPolicyAndBuckets(policy)
	return svc, orders, grants, accounts, transactions, executions, risks
}

func newPaymentAdjustmentTestServiceWithPolicyAndBuckets(policy PaymentAdjustmentPolicy) (PaymentAdjustmentService, *mockTxOrderRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockCreditTransactionRepo, *mockFulfillmentExecutionRepo, *mockPaymentRiskFlagRepo, *mockCreditBucketRepo) {
	orders := newMockTxOrderRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	transactions := newMockCreditTransactionRepo()
	buckets := newMockCreditBucketRepo()
	executions := newMockFulfillmentExecutionRepo()
	risks := newMockPaymentRiskFlagRepo()
	svc := NewPaymentAdjustmentService(PaymentAdjustmentDependencies{
		Repositories: PaymentAdjustmentRepositories{
			Orders:                orders,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			CreditBuckets:         buckets,
			FulfillmentExecutions: executions,
			PaymentRiskFlags:      risks,
		},
		Policy: policy,
	})
	return svc, orders, grants, accounts, transactions, executions, risks, buckets
}

func paymentAdjustmentTestAutoRefundPolicy() PaymentAdjustmentPolicy {
	return NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		DisputeAction:           PaymentAdjustmentActionAutoRefund,
		CancelAction:            PaymentAdjustmentActionKeepCurrentPeriod,
		Now:                     func() time.Time { return time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC) },
	})
}

func fulfilledOrderForAdjustment() *domain.Order {
	paidAt := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	fulfilledAt := paidAt.Add(time.Minute)
	return &domain.Order{
		ID:          88,
		OutTradeNo:  "CHK-ADJ-1",
		UserID:      "usr_1",
		SKUCode:     "editorial_studio_monthly",
		Amount:      1900,
		Currency:    "USD",
		Status:      domain.OrderStatusFulfilled,
		OrderType:   domain.OrderTypeCheckout,
		PaidAt:      &paidAt,
		FulfilledAt: &fulfilledAt,
	}
}

func seedRefundableFulfillment(
	orders *mockTxOrderRepo,
	grants *mockGrantRepo,
	accounts *mockCreditAccountRepo,
	transactions *mockCreditTransactionRepo,
	executions *mockFulfillmentExecutionRepo,
	balance int64,
) {
	order := fulfilledOrderForAdjustment()
	orders.orders[order.OutTradeNo] = order
	expiresAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	grants.grants["grt_1"] = &domain.EntitlementGrant{
		ID:            "grt_1",
		UserID:        order.UserID,
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		Source:        domain.GrantSourceFulfillment,
		StartsAt:      time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		ExpiresAt:     &expiresAt,
	}
	accounts.accounts["acct_1"] = &domain.CreditAccount{
		ID:        "acct_1",
		UserID:    order.UserID,
		Balance:   balance,
		CreatedAt: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
	}
	transactions.transactions["ctx_grant"] = &domain.CreditTransaction{
		ID:             "ctx_grant",
		AccountID:      "acct_1",
		UserID:         order.UserID,
		Type:           domain.CreditTransactionTypeGrant,
		Amount:         600,
		BalanceAfter:   600,
		IdempotencyKey: "fulfillment:CHK-ADJ-1:studio:credits:credits",
		Source:         domain.GrantSourceFulfillment,
	}
	executions.executions["fulfillment:CHK-ADJ-1:studio:entitlement"] = &domain.FulfillmentExecution{
		ID:             "ful_ent",
		OrderID:        order.ID,
		OutTradeNo:     order.OutTradeNo,
		UserID:         order.UserID,
		SKUCode:        order.SKUCode,
		RuleID:         "studio:entitlement",
		TargetType:     domain.FulfillmentTargetEntitlement,
		TargetID:       domain.EntitlementEditorialStudio,
		ResultRef:      "grt_1",
		IdempotencyKey: "fulfillment:CHK-ADJ-1:studio:entitlement",
		Status:         domain.FulfillmentExecutionStatusApplied,
	}
	executions.executions["fulfillment:CHK-ADJ-1:studio:credits"] = &domain.FulfillmentExecution{
		ID:             "ful_credits",
		OrderID:        order.ID,
		OutTradeNo:     order.OutTradeNo,
		UserID:         order.UserID,
		SKUCode:        order.SKUCode,
		RuleID:         "studio:credits",
		TargetType:     domain.FulfillmentTargetCredits,
		TargetID:       domain.CreditMetricBalance,
		ResultRef:      "ctx_grant",
		IdempotencyKey: "fulfillment:CHK-ADJ-1:studio:credits",
		Status:         domain.FulfillmentExecutionStatusApplied,
	}
}

func TestPaymentAdjustmentService_RefundRevokesGrantAndClawsBackAvailableCredits(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions, _ := newPaymentAdjustmentTestService()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 400)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected refund adjustment, got %v", err)
	}
	if result.PolicyDecision.Action != PaymentAdjustmentActionAutoRefund || result.PolicyDecision.Reason != PaymentAdjustmentReasonRefundInWindow {
		t.Fatalf("expected in-window auto refund decision, got %#v", result.PolicyDecision)
	}
	if len(result.RevokedGrantIDs) != 1 || result.RevokedGrantIDs[0] != "grt_1" {
		t.Fatalf("expected grant revoked, got %#v", result.RevokedGrantIDs)
	}
	if grant := grants.grants["grt_1"]; grant.Status != domain.GrantStatusRevoked || grant.RevokedAt == nil {
		t.Fatalf("expected revoked grant, got %#v", grant)
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 0 || result.ClawbackCredits != 400 {
		t.Fatalf("expected clawback 400 and balance 0, result=%#v account=%#v", result, account)
	}
	clawback, err := transactions.GetByIdempotencyKey(context.Background(), "refund:clawback:CHK-ADJ-1:studio:credits")
	if err != nil {
		t.Fatalf("expected clawback transaction: %v", err)
	}
	if clawback.Type != domain.CreditTransactionTypeClawback || clawback.Amount != -400 || clawback.BalanceAfter != 0 {
		t.Fatalf("unexpected clawback tx: %#v", clawback)
	}
}

func TestPaymentAdjustmentService_RefundClawsBackOnlyOriginalBucketNotFutureTopup(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions, _, buckets := newPaymentAdjustmentTestServiceWithPolicyAndBuckets(paymentAdjustmentTestAutoRefundPolicy())
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 700)
	transactions.transactions["ctx_grant"].BucketID = "bucket_subscription"
	now := time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC)
	buckets.buckets["bucket_subscription"] = &domain.CreditBucket{
		ID:                  "bucket_subscription",
		AccountID:           "acct_1",
		UserID:              "usr_1",
		Type:                domain.CreditBucketTypeSubscriptionPeriod,
		SourceTransactionID: "ctx_grant",
		Granted:             600,
		Remaining:           200,
		Status:              domain.CreditBucketStatusActive,
		CreatedAt:           now.Add(-time.Hour),
		UpdatedAt:           now.Add(-time.Hour),
	}
	buckets.buckets["bucket_topup"] = &domain.CreditBucket{
		ID:        "bucket_topup",
		AccountID: "acct_1",
		UserID:    "usr_1",
		Type:      domain.CreditBucketTypeTopup,
		Granted:   500,
		Remaining: 500,
		Status:    domain.CreditBucketStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected refund adjustment, got %v", err)
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if result.ClawbackCredits != 200 || account.Balance != 500 {
		t.Fatalf("expected clawback capped to original bucket only, result=%#v account=%#v", result, account)
	}
	if buckets.buckets["bucket_subscription"].Remaining != 0 || buckets.buckets["bucket_subscription"].Status != domain.CreditBucketStatusDepleted {
		t.Fatalf("expected original subscription bucket depleted, got %#v", buckets.buckets["bucket_subscription"])
	}
	if buckets.buckets["bucket_topup"].Remaining != 500 || buckets.buckets["bucket_topup"].Status != domain.CreditBucketStatusActive {
		t.Fatalf("future top-up bucket must be untouched, got %#v", buckets.buckets["bucket_topup"])
	}
	clawback, err := transactions.GetByIdempotencyKey(context.Background(), "refund:clawback:CHK-ADJ-1:studio:credits")
	if err != nil {
		t.Fatalf("expected clawback transaction: %v", err)
	}
	if clawback.BucketID != "bucket_subscription" || clawback.Amount != -200 || clawback.BalanceAfter != 500 {
		t.Fatalf("unexpected bucketed clawback tx: %#v", clawback)
	}
}

func TestPaymentAdjustmentService_RefundOutsideWindowRequiresManualReview(t *testing.T) {
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		Now:                     func() time.Time { return time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC) },
	})
	svc, orders, grants, accounts, transactions, executions, _ := newPaymentAdjustmentTestServiceWithPolicy(policy)
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 600)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if !errors.Is(err, ErrPaymentAdjustmentManualReview) {
		t.Fatalf("expected manual review error, result=%#v err=%v", result, err)
	}
	if result == nil || !result.PolicyDecision.ManualReview || result.PolicyDecision.Reason != PaymentAdjustmentReasonRefundOutOfWindow {
		t.Fatalf("expected out-of-window manual review decision, got %#v", result)
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusActive {
		t.Fatalf("manual review should not revoke grant")
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 600 {
		t.Fatalf("manual review should not claw back credits, balance=%d", account.Balance)
	}
	if _, err := transactions.GetByIdempotencyKey(context.Background(), "refund:clawback:CHK-ADJ-1:studio:credits"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("manual review should not create clawback marker, err=%v", err)
	}
}

func TestPaymentAdjustmentService_RefundPolicyDoesNotTreatFutureTopupAsOriginalBucketUsage(t *testing.T) {
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		LowUsagePolicyEnabled:   true,
		LowUsageMaxCreditsUsed:  100,
		LowUsageAction:          PaymentAdjustmentActionAutoRefund,
		HighUsageAction:         PaymentAdjustmentActionManualReview,
		Now:                     func() time.Time { return time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC) },
	})
	svc, orders, grants, accounts, transactions, executions, _, buckets := newPaymentAdjustmentTestServiceWithPolicyAndBuckets(policy)
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 600)
	transactions.transactions["ctx_grant"].BucketID = "missing_original_bucket"
	buckets.buckets["future_topup"] = &domain.CreditBucket{
		ID:        "future_topup",
		AccountID: "acct_1",
		UserID:    "usr_1",
		Type:      domain.CreditBucketTypeTopup,
		Granted:   600,
		Remaining: 600,
		Status:    domain.CreditBucketStatusActive,
		CreatedAt: time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
	}

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if !errors.Is(err, ErrPaymentAdjustmentManualReview) {
		t.Fatalf("expected manual review when original bucket is missing, result=%#v err=%v", result, err)
	}
	if result == nil || result.PolicyDecision.Reason != PaymentAdjustmentReasonRefundHighUsage {
		t.Fatalf("expected high-usage policy decision, got %#v", result)
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 600 || buckets.buckets["future_topup"].Remaining != 600 {
		t.Fatalf("future top-up must remain untouched, account=%#v bucket=%#v", account, buckets.buckets["future_topup"])
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusActive {
		t.Fatalf("manual review should not revoke grant")
	}
}

func TestPaymentAdjustmentService_RefundHighUsageRequiresManualReview(t *testing.T) {
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		RefundWindowDays:        7,
		RefundInWindowAction:    PaymentAdjustmentActionAutoRefund,
		RefundOutOfWindowAction: PaymentAdjustmentActionManualReview,
		LowUsagePolicyEnabled:   true,
		LowUsageMaxCreditsUsed:  100,
		LowUsageAction:          PaymentAdjustmentActionAutoRefund,
		HighUsageAction:         PaymentAdjustmentActionManualReview,
		Now:                     func() time.Time { return time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC) },
	})
	svc, orders, grants, accounts, transactions, executions, _ := newPaymentAdjustmentTestServiceWithPolicy(policy)
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 400)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if !errors.Is(err, ErrPaymentAdjustmentManualReview) {
		t.Fatalf("expected manual review for high usage, result=%#v err=%v", result, err)
	}
	if result == nil || result.PolicyDecision.Reason != PaymentAdjustmentReasonRefundHighUsage {
		t.Fatalf("expected high-usage decision, got %#v", result)
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusActive {
		t.Fatalf("high usage manual review should not revoke grant")
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 400 {
		t.Fatalf("high usage manual review should not claw back credits, balance=%d", account.Balance)
	}
}

func TestPaymentAdjustmentService_ManualReviewPolicyCommitsRiskFlagWithUnitOfWork(t *testing.T) {
	orders := newMockTxOrderRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	transactions := newMockCreditTransactionRepo()
	executions := newMockFulfillmentExecutionRepo()
	risks := newMockPaymentRiskFlagRepo()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 600)
	uow := &mockPaymentAdjustmentUnitOfWork{
		orders:       orders,
		grants:       grants,
		accounts:     accounts,
		transactions: transactions,
		executions:   executions,
		risks:        risks,
	}
	policy := NewConfigurablePaymentAdjustmentPolicy(PaymentAdjustmentPolicyConfig{
		DisputeAction: PaymentAdjustmentActionManualReview,
	})
	svc := NewPaymentAdjustmentService(PaymentAdjustmentDependencies{
		Repositories: PaymentAdjustmentRepositories{
			Orders:                orders,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			FulfillmentExecutions: executions,
			PaymentRiskFlags:      risks,
		},
		Policy:            policy,
		UnitOfWorkFactory: func() repository.UnitOfWork { return uow },
	})

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_dispute_manual_review",
		EventType:       domain.PaymentEventTypeDisputed,
		OutTradeNo:      "CHK-ADJ-1",
	})
	if !errors.Is(err, ErrPaymentAdjustmentManualReview) {
		t.Fatalf("expected dispute manual review error, result=%#v err=%v", result, err)
	}
	if uow.begins != 1 || uow.commits != 1 || uow.rollbacks != 0 {
		t.Fatalf("expected policy decision to commit risk flag without rollback, uow=%#v", uow)
	}
	if result == nil || result.RiskFlag == nil || len(risks.flags) != 1 {
		t.Fatalf("expected risk flag committed for manual-review dispute, result=%#v count=%d", result, len(risks.flags))
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusActive {
		t.Fatalf("manual-review dispute should not revoke grants before operator decision")
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 600 {
		t.Fatalf("manual-review dispute should not claw back credits before operator decision, balance=%d", account.Balance)
	}
}

func TestPaymentAdjustmentService_RefundNeverCreatesNegativeCreditBalance(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions, _ := newPaymentAdjustmentTestService()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 0)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected refund adjustment, got %v", err)
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 0 || result.ClawbackCredits != 0 {
		t.Fatalf("expected no negative balance, result=%#v account=%#v", result, account)
	}
	clawback, err := transactions.GetByIdempotencyKey(context.Background(), "refund:clawback:CHK-ADJ-1:studio:credits")
	if err != nil {
		t.Fatalf("expected zero clawback marker: %v", err)
	}
	if clawback.Amount != 0 || clawback.BalanceAfter != 0 {
		t.Fatalf("unexpected zero clawback marker: %#v", clawback)
	}
}

func TestPaymentAdjustmentService_RefundIsIdempotentAcrossRetries(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions, _ := newPaymentAdjustmentTestService()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 600)
	event := &domain.PaymentEventInbox{EventType: domain.PaymentEventTypeRefunded, OutTradeNo: "CHK-ADJ-1"}

	if _, err := svc.Apply(context.Background(), event); err != nil {
		t.Fatalf("first adjustment failed: %v", err)
	}
	if _, err := svc.Apply(context.Background(), event); err != nil {
		t.Fatalf("second adjustment failed: %v", err)
	}

	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 0 {
		t.Fatalf("expected one clawback only, balance=%d", account.Balance)
	}
	var clawbacks int
	for _, tx := range transactions.transactions {
		if tx.Type == domain.CreditTransactionTypeClawback {
			clawbacks++
		}
	}
	if clawbacks != 1 {
		t.Fatalf("expected one clawback tx, got %d", clawbacks)
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusRevoked {
		t.Fatalf("expected grant to remain revoked")
	}
}

func TestPaymentAdjustmentService_CancelKeepsCurrentPaidPeriod(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions, _ := newPaymentAdjustmentTestService()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 600)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeCancelled,
		OutTradeNo: "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected cancel adjustment, got %v", err)
	}
	if result.Note != "cancel_keeps_current_paid_period" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusActive {
		t.Fatalf("cancel should not revoke current grant")
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 600 {
		t.Fatalf("cancel should not claw back credits, balance=%d", account.Balance)
	}
	if _, err := transactions.GetByIdempotencyKey(context.Background(), "refund:clawback:CHK-ADJ-1:studio:credits"); err != repository.ErrNotFound {
		t.Fatalf("cancel should not create clawback tx, err=%v", err)
	}
}

func TestPaymentAdjustmentService_DisputeCreatesRiskFlagAndAppliesRefundPolicy(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions, risks := newPaymentAdjustmentTestService()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 600)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_dispute_1",
		EventType:       domain.PaymentEventTypeDisputed,
		OutTradeNo:      "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected dispute adjustment, got %v", err)
	}
	if result.RiskFlag == nil || result.RiskFlag.Status != domain.PaymentRiskStatusOpen || result.RiskFlag.Severity != domain.PaymentRiskSeverityCritical {
		t.Fatalf("expected open critical risk flag, got %#v", result.RiskFlag)
	}
	if result.RiskFlag.Reason != domain.PaymentRiskReasonDispute || result.RiskFlag.Provider != "creem" || result.RiskFlag.ProviderEventID != "evt_dispute_1" {
		t.Fatalf("unexpected risk flag: %#v", result.RiskFlag)
	}
	if len(risks.flags) != 1 {
		t.Fatalf("expected one risk flag, got %d", len(risks.flags))
	}
	if grants.grants["grt_1"].Status != domain.GrantStatusRevoked {
		t.Fatalf("expected dispute to revoke grant")
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 0 {
		t.Fatalf("expected dispute to claw back credits, balance=%d", account.Balance)
	}
	if _, err := transactions.GetByIdempotencyKey(context.Background(), "refund:clawback:CHK-ADJ-1:studio:credits"); err != nil {
		t.Fatalf("expected dispute clawback tx: %v", err)
	}

	second, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		Provider:        "creem",
		ProviderEventID: "evt_dispute_1",
		EventType:       domain.PaymentEventTypeDisputed,
		OutTradeNo:      "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected idempotent dispute adjustment, got %v", err)
	}
	if second.RiskFlag == nil || second.RiskFlag.ID != result.RiskFlag.ID || len(risks.flags) != 1 {
		t.Fatalf("expected same risk flag on retry, first=%#v second=%#v count=%d", result.RiskFlag, second.RiskFlag, len(risks.flags))
	}
}
