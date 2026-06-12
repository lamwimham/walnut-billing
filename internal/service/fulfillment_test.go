package service

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

type mockFulfillmentExecutionRepo struct {
	executions map[string]*domain.FulfillmentExecution
}

func newMockFulfillmentExecutionRepo() *mockFulfillmentExecutionRepo {
	return &mockFulfillmentExecutionRepo{executions: make(map[string]*domain.FulfillmentExecution)}
}

func (m *mockFulfillmentExecutionRepo) Create(ctx context.Context, execution *domain.FulfillmentExecution) error {
	m.executions[execution.IdempotencyKey] = execution
	return nil
}

func (m *mockFulfillmentExecutionRepo) GetByID(ctx context.Context, id string) (*domain.FulfillmentExecution, error) {
	for _, execution := range m.executions {
		if execution.ID == id {
			return execution, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockFulfillmentExecutionRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.FulfillmentExecution, error) {
	execution, ok := m.executions[key]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return execution, nil
}

func (m *mockFulfillmentExecutionRepo) List(ctx context.Context, query repository.FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error) {
	var result []domain.FulfillmentExecution
	for _, execution := range m.executions {
		if query.OrderID > 0 && execution.OrderID != query.OrderID {
			continue
		}
		if query.OutTradeNo != "" && execution.OutTradeNo != query.OutTradeNo {
			continue
		}
		if query.UserID != "" && execution.UserID != query.UserID {
			continue
		}
		if query.SKUCode != "" && execution.SKUCode != query.SKUCode {
			continue
		}
		if query.RuleID != "" && execution.RuleID != query.RuleID {
			continue
		}
		if query.TargetType != "" && execution.TargetType != query.TargetType {
			continue
		}
		if query.Status != "" && execution.Status != query.Status {
			continue
		}
		result = append(result, *execution)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (m *mockFulfillmentExecutionRepo) Update(ctx context.Context, execution *domain.FulfillmentExecution) error {
	m.executions[execution.IdempotencyKey] = execution
	return nil
}

func newFulfillmentTestService(rules ...FulfillmentRule) (FulfillmentService, *mockTxOrderRepo, *mockEntitlementUserRepo, *mockGrantRepo, *mockCreditAccountRepo, *mockCreditTransactionRepo, *mockFulfillmentExecutionRepo) {
	orders := newMockTxOrderRepo()
	users := newMockEntitlementUserRepo()
	registrations := newMockRegistrationRepo()
	grants := newMockGrantRepo()
	accounts := newMockCreditAccountRepo()
	reservations := newMockCreditReservationRepo()
	transactions := newMockCreditTransactionRepo()
	executions := newMockFulfillmentExecutionRepo()
	entitlementSvc := NewEntitlementService(users, registrations, grants, DefaultEntitlementCatalog())
	creditSvc := NewCreditService(users, accounts, reservations, transactions, nil)
	catalog, err := NewStaticFulfillmentCatalog(rules...)
	if err != nil {
		panic(err)
	}
	_ = entitlementSvc
	_ = creditSvc
	return NewFulfillmentService(FulfillmentDependencies{
		Repositories: FulfillmentRepositories{
			Orders:                orders,
			Users:                 users,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			FulfillmentExecutions: executions,
		},
		Catalog:            catalog,
		EntitlementCatalog: DefaultEntitlementCatalog(),
	}), orders, users, grants, accounts, transactions, executions
}

func editorialStudioFulfillmentRules() []FulfillmentRule {
	return []FulfillmentRule{
		{ID: "studio:entitlement", SKUCode: "editorial_studio_monthly", Type: FulfillmentRuleGrantEntitlement, EntitlementID: domain.EntitlementEditorialStudio, Duration: "monthly"},
		{ID: "studio:credits", SKUCode: "editorial_studio_monthly", Type: FulfillmentRuleGrantCredits, CreditsAmount: 600},
	}
}

func paidCheckoutOrder() *domain.Order {
	paidAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	return &domain.Order{
		ID:         42,
		OutTradeNo: "CHK-1",
		UserID:     "usr_1",
		SKUCode:    "editorial_studio_monthly",
		Amount:     1900,
		Currency:   "CNY",
		Status:     domain.OrderStatusPaid,
		OrderType:  domain.OrderTypeCheckout,
		PaidAt:     &paidAt,
	}
}

func TestFulfillmentService_FulfillPaidOrderGrantsEntitlementAndCredits(t *testing.T) {
	svc, orders, users, grants, accounts, transactions, executions := newFulfillmentTestService(editorialStudioFulfillmentRules()...)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = paidCheckoutOrder()

	result, err := svc.FulfillOrder(context.Background(), orders.orders["CHK-1"])
	if err != nil {
		t.Fatalf("expected fulfillment success, got %v", err)
	}
	if result.Order.Status != domain.OrderStatusFulfilled || result.Order.FulfilledAt == nil {
		t.Fatalf("expected fulfilled order, got %#v", result.Order)
	}
	if len(result.Executions) != 2 || len(executions.executions) != 2 {
		t.Fatalf("expected two fulfillment executions, got result=%d stored=%d", len(result.Executions), len(executions.executions))
	}
	if len(grants.grants) != 1 {
		t.Fatalf("expected one entitlement grant, got %d", len(grants.grants))
	}
	for _, grant := range grants.grants {
		if grant.Source != domain.GrantSourceFulfillment || grant.IdempotencyKey == nil || *grant.IdempotencyKey == "" || grant.ExpiresAt == nil {
			t.Fatalf("expected fulfillment grant with expiry/idempotency, got %#v", grant)
		}
	}
	if len(transactions.transactions) != 1 {
		t.Fatalf("expected one credit transaction, got %d", len(transactions.transactions))
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil || account.Balance != 600 {
		t.Fatalf("expected 600 credit balance, account=%#v err=%v", account, err)
	}
}

func TestFulfillmentService_IsIdempotentAcrossRetries(t *testing.T) {
	svc, orders, users, grants, _, transactions, executions := newFulfillmentTestService(editorialStudioFulfillmentRules()...)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = paidCheckoutOrder()

	if _, err := svc.FulfillOrder(context.Background(), orders.orders["CHK-1"]); err != nil {
		t.Fatalf("first fulfillment failed: %v", err)
	}
	second, err := svc.FulfillOrder(context.Background(), orders.orders["CHK-1"])
	if err != nil {
		t.Fatalf("second fulfillment failed: %v", err)
	}
	if !second.AlreadyFulfilled {
		t.Fatalf("expected already_fulfilled on retry")
	}
	if len(grants.grants) != 1 || len(transactions.transactions) != 1 || len(executions.executions) != 2 {
		t.Fatalf("expected no duplicate side effects, grants=%d txs=%d executions=%d", len(grants.grants), len(transactions.transactions), len(executions.executions))
	}
}

func TestFulfillmentService_ExtendsEntitlementFromExistingExpiry(t *testing.T) {
	svc, orders, users, grants, _, _, _ := newFulfillmentTestService(FulfillmentRule{
		ID:            "studio:entitlement",
		SKUCode:       "editorial_studio_monthly",
		Type:          FulfillmentRuleGrantEntitlement,
		EntitlementID: domain.EntitlementEditorialStudio,
		Duration:      "monthly",
	})
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	paidAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	existingExpiry := paidAt.AddDate(0, 1, 0)
	grants.grants["existing"] = &domain.EntitlementGrant{
		ID:            "existing",
		UserID:        "usr_1",
		EntitlementID: domain.EntitlementEditorialStudio,
		Status:        domain.GrantStatusActive,
		StartsAt:      paidAt.Add(-time.Hour),
		ExpiresAt:     &existingExpiry,
	}
	orders.orders["CHK-1"] = paidCheckoutOrder()

	if _, err := svc.FulfillOrder(context.Background(), orders.orders["CHK-1"]); err != nil {
		t.Fatalf("expected renewal fulfillment success, got %v", err)
	}
	var renewed *domain.EntitlementGrant
	for _, grant := range grants.grants {
		if grant.ID != "existing" {
			renewed = grant
		}
	}
	if renewed == nil || renewed.ExpiresAt == nil {
		t.Fatalf("expected renewal grant with expiry, got %#v", renewed)
	}
	expectedExpiry := existingExpiry.AddDate(0, 1, 0)
	if !renewed.ExpiresAt.Equal(expectedExpiry) {
		t.Fatalf("expected renewal to extend from existing expiry %s, got %s", expectedExpiry, renewed.ExpiresAt)
	}
}

func TestFulfillmentService_MissingRulesFailsWithoutMarkingFulfilled(t *testing.T) {
	svc, orders, users, _, _, _, _ := newFulfillmentTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = paidCheckoutOrder()

	_, err := svc.FulfillOrder(context.Background(), orders.orders["CHK-1"])
	if !errors.Is(err, ErrFulfillmentRulesNotFound) {
		t.Fatalf("expected missing rules error, got %v", err)
	}
	if orders.orders["CHK-1"].Status != domain.OrderStatusPaid {
		t.Fatalf("order should remain paid for reprocessing, got %s", orders.orders["CHK-1"].Status)
	}
}

func TestFulfillmentService_FailedRuleRecordsFailedExecution(t *testing.T) {
	rules := []FulfillmentRule{{ID: "bad:entitlement", SKUCode: "editorial_studio_monthly", Type: FulfillmentRuleGrantEntitlement, EntitlementID: "unknown.feature"}}
	svc, orders, users, _, _, _, executions := newFulfillmentTestService(rules...)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	orders.orders["CHK-1"] = paidCheckoutOrder()

	_, err := svc.FulfillOrder(context.Background(), orders.orders["CHK-1"])
	if !errors.Is(err, ErrUnknownEntitlement) {
		t.Fatalf("expected unknown entitlement error, got %v", err)
	}
	if orders.orders["CHK-1"].Status != domain.OrderStatusPaid {
		t.Fatalf("failed fulfillment must not mark order fulfilled")
	}
	if len(executions.executions) != 1 {
		t.Fatalf("expected failed execution, got %d", len(executions.executions))
	}
	for _, execution := range executions.executions {
		if execution.Status != domain.FulfillmentExecutionStatusFailed || execution.LastError == "" {
			t.Fatalf("expected failed execution with error, got %#v", execution)
		}
	}
}

func TestPaymentFulfillmentEventProcessor_FulfillsPaidCheckoutOrder(t *testing.T) {
	svc, orders, users, _, _, transactions, _ := newFulfillmentTestService(editorialStudioFulfillmentRules()...)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	order := paidCheckoutOrder()
	order.Status = domain.OrderStatusCheckoutCreated
	orders.orders["CHK-1"] = order
	processor := NewPaymentFulfillmentEventProcessor(orders, NewPaymentOrderEventProcessor(orders), svc)

	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		Provider:        "mock",
		EventType:       domain.PaymentEventTypePaid,
		OutTradeNo:      "CHK-1",
		ProviderTradeNo: "txn_1",
		Amount:          1900,
	})
	if err != nil {
		t.Fatalf("expected paid event to fulfill order, got %v", err)
	}
	if orders.orders["CHK-1"].Status != domain.OrderStatusFulfilled {
		t.Fatalf("expected fulfilled checkout order, got %s", orders.orders["CHK-1"].Status)
	}
	if len(transactions.transactions) != 1 {
		t.Fatalf("expected credit transaction from fulfillment")
	}
}

func TestPaymentFulfillmentEventProcessor_AppliesRefundAdjustment(t *testing.T) {
	fulfillmentSvc, orders, users, grants, accounts, transactions, executions := newFulfillmentTestService(editorialStudioFulfillmentRules()...)
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}
	order := paidCheckoutOrder()
	orders.orders["CHK-1"] = order
	if _, err := fulfillmentSvc.FulfillOrder(context.Background(), order); err != nil {
		t.Fatalf("fulfillment failed: %v", err)
	}
	adjustments := NewPaymentAdjustmentService(PaymentAdjustmentDependencies{
		Repositories: PaymentAdjustmentRepositories{
			Orders:                orders,
			EntitlementGrants:     grants,
			CreditAccounts:        accounts,
			CreditTransactions:    transactions,
			FulfillmentExecutions: executions,
		},
	})
	processor := NewPaymentFulfillmentEventProcessorWithAdjustments(orders, NewPaymentOrderEventProcessor(orders), fulfillmentSvc, adjustments)

	err := processor.ProcessPaymentEvent(context.Background(), &domain.PaymentEventInbox{
		Provider:   "mock",
		EventType:  domain.PaymentEventTypeRefunded,
		OutTradeNo: "CHK-1",
		Amount:     1900,
	})
	if err != nil {
		t.Fatalf("expected refund adjustment, got %v", err)
	}
	if orders.orders["CHK-1"].Status != domain.OrderStatusRefunded {
		t.Fatalf("expected refunded order, got %s", orders.orders["CHK-1"].Status)
	}
	for _, grant := range grants.grants {
		if grant.Status != domain.GrantStatusRevoked {
			t.Fatalf("expected refunded order grant to be revoked, got %#v", grant)
		}
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if account.Balance != 0 {
		t.Fatalf("expected credits clawed back, balance=%d", account.Balance)
	}
}
