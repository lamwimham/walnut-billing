package service

import (
	"context"
	"errors"
	"testing"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
	gorm_repo "walnut-billing/internal/repository/gorm_repo"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFulfillmentService_UnitOfWorkRollsBackPartialSideEffects(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:fulfillment_uow?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(
		&domain.User{},
		&domain.Order{},
		&domain.EntitlementGrant{},
		&domain.CreditAccount{},
		&domain.CreditTransaction{},
		&domain.FulfillmentExecution{},
	); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	userRepo := &gorm_repo.UserRepo{DB: db}
	orderRepo := &gorm_repo.OrderRepo{DB: db}
	grantRepo := &gorm_repo.EntitlementGrantRepo{DB: db}
	creditAccountRepo := &gorm_repo.CreditAccountRepo{DB: db}
	creditTransactionRepo := &gorm_repo.CreditTransactionRepo{DB: db}
	fulfillmentRepo := &gorm_repo.FulfillmentExecutionRepo{DB: db}

	ctx := context.Background()
	if err := userRepo.Create(ctx, &domain.User{ID: "usr_1", Email: "writer@example.com", Status: domain.UserStatusActive}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	paidAt := time.Now().UTC()
	order := &domain.Order{
		OutTradeNo: "CHK-TX",
		UserID:     "usr_1",
		SKUCode:    "mixed_bundle",
		Amount:     1900,
		Currency:   "CNY",
		Status:     domain.OrderStatusPaid,
		OrderType:  domain.OrderTypeCheckout,
		PaidAt:     &paidAt,
	}
	if err := orderRepo.Create(ctx, order); err != nil {
		t.Fatalf("create order: %v", err)
	}
	catalog, err := NewStaticFulfillmentCatalog(
		FulfillmentRule{ID: "mixed:credits", SKUCode: "mixed_bundle", Type: FulfillmentRuleGrantCredits, CreditsAmount: 600},
		FulfillmentRule{ID: "mixed:bad-entitlement", SKUCode: "mixed_bundle", Type: FulfillmentRuleGrantEntitlement, EntitlementID: "unknown.feature"},
	)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	svc := NewFulfillmentService(FulfillmentDependencies{
		Repositories: FulfillmentRepositories{
			Orders:                orderRepo,
			Users:                 userRepo,
			EntitlementGrants:     grantRepo,
			CreditAccounts:        creditAccountRepo,
			CreditTransactions:    creditTransactionRepo,
			FulfillmentExecutions: fulfillmentRepo,
		},
		Catalog:            catalog,
		EntitlementCatalog: DefaultEntitlementCatalog(),
		UnitOfWorkFactory: func() repository.UnitOfWork {
			return gorm_repo.NewUnitOfWork(db)
		},
	})

	_, err = svc.FulfillOrder(ctx, order)
	if !errors.Is(err, ErrUnknownEntitlement) {
		t.Fatalf("expected unknown entitlement failure, got %v", err)
	}

	var creditTransactions int64
	if err := db.Model(&domain.CreditTransaction{}).Count(&creditTransactions).Error; err != nil {
		t.Fatalf("count transactions: %v", err)
	}
	if creditTransactions != 0 {
		t.Fatalf("expected credit transaction rollback, got %d", creditTransactions)
	}
	var grants int64
	if err := db.Model(&domain.EntitlementGrant{}).Count(&grants).Error; err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grants != 0 {
		t.Fatalf("expected grant rollback, got %d", grants)
	}
	storedOrder, err := orderRepo.GetByOutTradeNo(ctx, "CHK-TX")
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if storedOrder.Status != domain.OrderStatusPaid || storedOrder.FulfilledAt != nil {
		t.Fatalf("expected order to remain paid after rollback, got %#v", storedOrder)
	}
	executions, err := fulfillmentRepo.List(ctx, repository.FulfillmentExecutionQuery{OutTradeNo: "CHK-TX"})
	if err != nil {
		t.Fatalf("list executions: %v", err)
	}
	if len(executions) != 1 || executions[0].Status != domain.FulfillmentExecutionStatusFailed {
		t.Fatalf("expected one durable failed execution for reprocess diagnostics, got %#v", executions)
	}
}
