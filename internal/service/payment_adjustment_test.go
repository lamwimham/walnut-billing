package service

import (
	"context"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

func newPaymentAdjustmentTestService() (PaymentAdjustmentService, *mockTxOrderRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockCreditTransactionRepo, *mockFulfillmentExecutionRepo) {
	orders := newMockTxOrderRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	transactions := newMockCreditTransactionRepo()
	executions := newMockFulfillmentExecutionRepo()
	svc := NewPaymentAdjustmentService(PaymentAdjustmentDependencies{
		Repositories: PaymentAdjustmentRepositories{
			Orders:                orders,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			FulfillmentExecutions: executions,
		},
	})
	return svc, orders, grants, accounts, transactions, executions
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
	svc, orders, grants, accounts, transactions, executions := newPaymentAdjustmentTestService()
	seedRefundableFulfillment(orders, grants, accounts, transactions, executions, 400)

	result, err := svc.Apply(context.Background(), &domain.PaymentEventInbox{
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-ADJ-1",
	})
	if err != nil {
		t.Fatalf("expected refund adjustment, got %v", err)
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

func TestPaymentAdjustmentService_RefundNeverCreatesNegativeCreditBalance(t *testing.T) {
	svc, orders, grants, accounts, transactions, executions := newPaymentAdjustmentTestService()
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
	svc, orders, grants, accounts, transactions, executions := newPaymentAdjustmentTestService()
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
	svc, orders, grants, accounts, transactions, executions := newPaymentAdjustmentTestService()
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
