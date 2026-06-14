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

type mockCreditAccountRepo struct {
	accounts map[string]*domain.CreditAccount
}

func newMockCreditAccountRepo() *mockCreditAccountRepo {
	return &mockCreditAccountRepo{accounts: make(map[string]*domain.CreditAccount)}
}

func (m *mockCreditAccountRepo) Create(ctx context.Context, account *domain.CreditAccount) error {
	m.accounts[account.ID] = account
	return nil
}

func (m *mockCreditAccountRepo) GetByID(ctx context.Context, id string) (*domain.CreditAccount, error) {
	account, ok := m.accounts[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return account, nil
}

func (m *mockCreditAccountRepo) GetByUserID(ctx context.Context, userID string) (*domain.CreditAccount, error) {
	for _, account := range m.accounts {
		if account.UserID == userID {
			return account, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockCreditAccountRepo) Update(ctx context.Context, account *domain.CreditAccount) error {
	m.accounts[account.ID] = account
	return nil
}

type mockCreditReservationRepo struct {
	reservations map[string]*domain.CreditReservation
}

func newMockCreditReservationRepo() *mockCreditReservationRepo {
	return &mockCreditReservationRepo{reservations: make(map[string]*domain.CreditReservation)}
}

func (m *mockCreditReservationRepo) Create(ctx context.Context, reservation *domain.CreditReservation) error {
	m.reservations[reservation.ID] = reservation
	return nil
}

func (m *mockCreditReservationRepo) GetByID(ctx context.Context, id string) (*domain.CreditReservation, error) {
	reservation, ok := m.reservations[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return reservation, nil
}

func (m *mockCreditReservationRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditReservation, error) {
	for _, reservation := range m.reservations {
		if reservation.IdempotencyKey == key {
			return reservation, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockCreditReservationRepo) List(ctx context.Context, query repository.CreditReservationQuery) ([]domain.CreditReservation, error) {
	var result []domain.CreditReservation
	for _, reservation := range m.reservations {
		if query.UserID != "" && reservation.UserID != query.UserID {
			continue
		}
		if query.FeatureID != "" && reservation.FeatureID != query.FeatureID {
			continue
		}
		if query.Operation != "" && reservation.Operation != query.Operation {
			continue
		}
		if query.ExecutionID != "" && reservation.ExecutionID != query.ExecutionID {
			continue
		}
		if query.Status != "" && reservation.Status != query.Status {
			continue
		}
		result = append(result, *reservation)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if query.Offset > 0 {
		if query.Offset >= len(result) {
			return []domain.CreditReservation{}, nil
		}
		result = result[query.Offset:]
	}
	if query.Limit > 0 && query.Limit < len(result) {
		result = result[:query.Limit]
	}
	return result, nil
}

func (m *mockCreditReservationRepo) Update(ctx context.Context, reservation *domain.CreditReservation) error {
	m.reservations[reservation.ID] = reservation
	return nil
}

type mockCreditTransactionRepo struct {
	transactions map[string]*domain.CreditTransaction
}

func newMockCreditTransactionRepo() *mockCreditTransactionRepo {
	return &mockCreditTransactionRepo{transactions: make(map[string]*domain.CreditTransaction)}
}

func (m *mockCreditTransactionRepo) Create(ctx context.Context, transaction *domain.CreditTransaction) error {
	m.transactions[transaction.ID] = transaction
	return nil
}

func (m *mockCreditTransactionRepo) GetByID(ctx context.Context, id string) (*domain.CreditTransaction, error) {
	transaction, ok := m.transactions[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return transaction, nil
}

func (m *mockCreditTransactionRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditTransaction, error) {
	for _, transaction := range m.transactions {
		if transaction.IdempotencyKey == key {
			return transaction, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockCreditTransactionRepo) ListByUser(ctx context.Context, userID string, limit int, offset int) ([]domain.CreditTransaction, error) {
	var result []domain.CreditTransaction
	for _, transaction := range m.transactions {
		if transaction.UserID == userID {
			result = append(result, *transaction)
		}
	}
	return result, nil
}

func (m *mockCreditTransactionRepo) ListByReservationIDs(ctx context.Context, reservationIDs []string) ([]domain.CreditTransaction, error) {
	allowed := make(map[string]bool)
	for _, id := range reservationIDs {
		allowed[id] = true
	}
	var result []domain.CreditTransaction
	for _, transaction := range m.transactions {
		if allowed[transaction.ReservationID] {
			result = append(result, *transaction)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

type mockCreditBucketRepo struct {
	buckets map[string]*domain.CreditBucket
}

func newMockCreditBucketRepo() *mockCreditBucketRepo {
	return &mockCreditBucketRepo{buckets: make(map[string]*domain.CreditBucket)}
}

func (m *mockCreditBucketRepo) Create(ctx context.Context, bucket *domain.CreditBucket) error {
	m.buckets[bucket.ID] = bucket
	return nil
}

func (m *mockCreditBucketRepo) GetByID(ctx context.Context, id string) (*domain.CreditBucket, error) {
	bucket, ok := m.buckets[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	return bucket, nil
}

func (m *mockCreditBucketRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditBucket, error) {
	for _, bucket := range m.buckets {
		if bucket.IdempotencyKey == key {
			return bucket, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (m *mockCreditBucketRepo) List(ctx context.Context, query repository.CreditBucketQuery) ([]domain.CreditBucket, error) {
	var result []domain.CreditBucket
	for _, bucket := range m.buckets {
		if query.AccountID != "" && bucket.AccountID != query.AccountID {
			continue
		}
		if query.UserID != "" && bucket.UserID != query.UserID {
			continue
		}
		if query.Type != "" && bucket.Type != query.Type {
			continue
		}
		if query.Status != "" && bucket.Status != query.Status {
			continue
		}
		if query.SourceOrderNo != "" && bucket.SourceOrderNo != query.SourceOrderNo {
			continue
		}
		if query.SourceTransactionID != "" && bucket.SourceTransactionID != query.SourceTransactionID {
			continue
		}
		if query.ActiveAt != nil && bucket.ExpiresAt != nil && !bucket.ExpiresAt.After(*query.ActiveAt) {
			continue
		}
		if query.ExpiresAtOrBefore != nil {
			if bucket.ExpiresAt == nil || bucket.ExpiresAt.After(*query.ExpiresAtOrBefore) {
				continue
			}
		}
		if query.PositiveRemaining && bucket.Remaining <= 0 {
			continue
		}
		result = append(result, *bucket)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := result[i]
		right := result[j]
		if left.ExpiresAt == nil && right.ExpiresAt != nil {
			return false
		}
		if left.ExpiresAt != nil && right.ExpiresAt == nil {
			return true
		}
		if left.ExpiresAt != nil && right.ExpiresAt != nil && !left.ExpiresAt.Equal(*right.ExpiresAt) {
			return left.ExpiresAt.Before(*right.ExpiresAt)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	if query.Offset > 0 {
		if query.Offset >= len(result) {
			return []domain.CreditBucket{}, nil
		}
		result = result[query.Offset:]
	}
	if query.Limit > 0 && query.Limit < len(result) {
		result = result[:query.Limit]
	}
	return result, nil
}

func (m *mockCreditBucketRepo) Update(ctx context.Context, bucket *domain.CreditBucket) error {
	m.buckets[bucket.ID] = bucket
	return nil
}

func newCreditTestService() (CreditService, *mockEntitlementUserRepo, *mockCreditAccountRepo, *mockCreditReservationRepo, *mockCreditTransactionRepo) {
	users := newMockEntitlementUserRepo()
	accounts := newMockCreditAccountRepo()
	reservations := newMockCreditReservationRepo()
	transactions := newMockCreditTransactionRepo()
	return NewCreditService(users, accounts, reservations, transactions, nil), users, accounts, reservations, transactions
}

func newCreditTestServiceWithBuckets() (CreditService, *mockEntitlementUserRepo, *mockCreditAccountRepo, *mockCreditReservationRepo, *mockCreditTransactionRepo, *mockCreditBucketRepo) {
	users := newMockEntitlementUserRepo()
	accounts := newMockCreditAccountRepo()
	reservations := newMockCreditReservationRepo()
	transactions := newMockCreditTransactionRepo()
	buckets := newMockCreditBucketRepo()
	return NewCreditServiceWithBuckets(users, accounts, reservations, transactions, buckets, nil), users, accounts, reservations, transactions, buckets
}

func TestCreditService_GrantCreatesAccountAndIsIdempotent(t *testing.T) {
	svc, users, accounts, _, transactions := newCreditTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}

	first, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-1",
		Source:         "admin",
	})
	if err != nil {
		t.Fatalf("expected grant, got %v", err)
	}
	second, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-1",
		Source:         "admin",
	})
	if err != nil {
		t.Fatalf("expected idempotent grant, got %v", err)
	}

	if first.Account.Balance != 100 || second.Account.Balance != 100 {
		t.Fatalf("expected stable balance 100, got first=%d second=%d", first.Account.Balance, second.Account.Balance)
	}
	if len(accounts.accounts) != 1 || len(transactions.transactions) != 1 {
		t.Fatalf("expected one account and one transaction")
	}
}

func TestCreditService_ReserveCommitAndReleaseMaintainBalances(t *testing.T) {
	svc, users, _, _, _ := newCreditTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	_, err := svc.Grant(context.Background(), CreditGrantInput{UserID: "usr_1", Amount: 100, IdempotencyKey: "grant-1"})
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	reserved, err := svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		Operation:      "editorial.studio.run",
		Amount:         30,
		IdempotencyKey: "reserve-1",
	})
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	if reserved.Account.Balance != 70 || reserved.Account.Reserved != 30 {
		t.Fatalf("expected balance=70 reserved=30, got balance=%d reserved=%d", reserved.Account.Balance, reserved.Account.Reserved)
	}

	committed, err := svc.Commit(context.Background(), CreditFinalizationInput{
		ReservationID:  reserved.Reservation.ID,
		IdempotencyKey: "commit-1",
	})
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if committed.Account.Balance != 70 || committed.Account.Reserved != 0 {
		t.Fatalf("expected commit balance=70 reserved=0, got balance=%d reserved=%d", committed.Account.Balance, committed.Account.Reserved)
	}
	if committed.Transaction.Amount != 0 {
		t.Fatalf("expected commit transaction to be confirmation amount 0, got %d", committed.Transaction.Amount)
	}

	reservedAgain, err := svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		Operation:      "editorial.studio.run",
		Amount:         20,
		IdempotencyKey: "reserve-2",
	})
	if err != nil {
		t.Fatalf("second reserve failed: %v", err)
	}
	released, err := svc.Release(context.Background(), CreditFinalizationInput{
		ReservationID:  reservedAgain.Reservation.ID,
		IdempotencyKey: "release-1",
	})
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if released.Account.Balance != 70 || released.Account.Reserved != 0 {
		t.Fatalf("expected release restore balance=70 reserved=0, got balance=%d reserved=%d", released.Account.Balance, released.Account.Reserved)
	}
	if released.Transaction.Amount != 20 {
		t.Fatalf("expected release transaction amount 20, got %d", released.Transaction.Amount)
	}
}

func TestCreditService_ReserveRejectsInsufficientCredits(t *testing.T) {
	svc, users, _, _, _ := newCreditTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	_, err := svc.Grant(context.Background(), CreditGrantInput{UserID: "usr_1", Amount: 10, IdempotencyKey: "grant-1"})
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	_, err = svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		Operation:      "editorial.studio.run",
		Amount:         11,
		IdempotencyKey: "reserve-1",
	})
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("expected insufficient credits, got %v", err)
	}
}

func TestCreditService_CommitRejectsExpiredReservation(t *testing.T) {
	svc, users, _, _, _ := newCreditTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	_, err := svc.Grant(context.Background(), CreditGrantInput{UserID: "usr_1", Amount: 50, IdempotencyKey: "grant-1"})
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	reserved, err := svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		Operation:      "editorial.studio.run",
		Amount:         10,
		IdempotencyKey: "reserve-1",
		ExpiresAt:      &expiresAt,
	})
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	reserved.Reservation.ExpiresAt = &past

	_, err = svc.Commit(context.Background(), CreditFinalizationInput{
		ReservationID:  reserved.Reservation.ID,
		IdempotencyKey: "commit-1",
	})
	if !errors.Is(err, ErrReservationExpired) {
		t.Fatalf("expected expired reservation, got %v", err)
	}
}

func TestCreditService_ListUsageRecordsProjectsReservationsAndTransactions(t *testing.T) {
	svc, users, _, _, _ := newCreditTestService()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	_, err := svc.Grant(context.Background(), CreditGrantInput{UserID: "usr_1", Amount: 100, IdempotencyKey: "grant-1"})
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}

	reserved, err := svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		FeatureID:      domain.EntitlementEditorialStudio,
		Operation:      "editorial.studio.run",
		ExecutionID:    "exec-1",
		Amount:         30,
		IdempotencyKey: "reserve-1",
		Metadata: map[string]any{
			"project_id":  "project-a",
			"document_id": "doc-1",
			"raw_prompt":  "must not leak",
		},
	})
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	_, err = svc.Commit(context.Background(), CreditFinalizationInput{
		ReservationID:  reserved.Reservation.ID,
		IdempotencyKey: "commit-1",
	})
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	records, err := svc.ListUsageRecords(context.Background(), UsageRecordQuery{
		UserID:    "usr_1",
		Operation: "editorial.studio.run",
	})
	if err != nil {
		t.Fatalf("list usage records failed: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected one usage record, got %d", len(records))
	}
	record := records[0]
	if record.ReservationID != reserved.Reservation.ID || record.Status != domain.CreditReservationStatusCommitted {
		t.Fatalf("unexpected usage record: %+v", record)
	}
	if record.FeatureID != domain.EntitlementEditorialStudio || record.ExecutionID != "exec-1" {
		t.Fatalf("expected feature/execution metadata, got feature=%q execution=%q", record.FeatureID, record.ExecutionID)
	}
	if record.Metadata["project_id"] != "project-a" || record.Metadata["document_id"] != "doc-1" {
		t.Fatalf("expected metadata projection, got %+v", record.Metadata)
	}
	if _, ok := record.Metadata["raw_prompt"]; ok {
		t.Fatalf("usage metadata must be sanitized, got %+v", record.Metadata)
	}
	if len(record.Transactions) != 2 {
		t.Fatalf("expected reserve+commit transactions, got %d", len(record.Transactions))
	}
}

func TestCreditService_GrantCreatesBucketAndIsIdempotent(t *testing.T) {
	svc, users, _, _, transactions, buckets := newCreditTestServiceWithBuckets()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	periodStart := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	first, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         600,
		IdempotencyKey: "grant-bucket-1",
		Source:         domain.GrantSourceFulfillment,
		BucketType:     domain.CreditBucketTypeSubscriptionPeriod,
		SourceOrderNo:  "CHK-1",
		PeriodStartAt:  &periodStart,
		PeriodEndAt:    &periodEnd,
	})
	if err != nil {
		t.Fatalf("expected bucketed grant, got %v", err)
	}
	second, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         600,
		IdempotencyKey: "grant-bucket-1",
		Source:         domain.GrantSourceFulfillment,
	})
	if err != nil {
		t.Fatalf("expected idempotent bucketed grant, got %v", err)
	}

	if first.Bucket == nil || second.Bucket == nil {
		t.Fatalf("expected bucket in grant result, first=%#v second=%#v", first.Bucket, second.Bucket)
	}
	if first.Transaction.BucketID != first.Bucket.ID || first.Bucket.SourceTransactionID != first.Transaction.ID {
		t.Fatalf("expected transaction and bucket to cross-reference, tx=%#v bucket=%#v", first.Transaction, first.Bucket)
	}
	if first.Bucket.Type != domain.CreditBucketTypeSubscriptionPeriod || first.Bucket.ExpiresAt == nil || !first.Bucket.ExpiresAt.Equal(periodEnd) {
		t.Fatalf("expected subscription-period bucket expiring at %s, got %#v", periodEnd, first.Bucket)
	}
	if second.Bucket.ID != first.Bucket.ID || len(buckets.buckets) != 1 || len(transactions.transactions) != 1 {
		t.Fatalf("expected idempotent bucket reuse, first=%s second=%s buckets=%d txs=%d", first.Bucket.ID, second.Bucket.ID, len(buckets.buckets), len(transactions.transactions))
	}
}

func TestCreditService_ReserveAllocatesEarliestExpiringBucketFirst(t *testing.T) {
	svc, users, _, _, _, buckets := newCreditTestServiceWithBuckets()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	periodEnd := now.AddDate(0, 1, 0)
	subscriptionGrant, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-subscription",
		Source:         domain.GrantSourceFulfillment,
		BucketType:     domain.CreditBucketTypeSubscriptionPeriod,
		PeriodStartAt:  &now,
		PeriodEndAt:    &periodEnd,
	})
	if err != nil {
		t.Fatalf("subscription grant failed: %v", err)
	}
	topupGrant, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-topup",
		Source:         domain.GrantSourceFulfillment,
		BucketType:     domain.CreditBucketTypeTopup,
	})
	if err != nil {
		t.Fatalf("topup grant failed: %v", err)
	}

	reserved, err := svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		Operation:      "editorial.studio.run",
		Amount:         120,
		IdempotencyKey: "reserve-fefo",
	})
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}

	allocations := decodeCreditBucketAllocations(reserved.Reservation.BucketAllocations)
	if len(allocations) != 2 || allocations[0].BucketID != subscriptionGrant.Bucket.ID || allocations[0].Amount != 100 || allocations[1].BucketID != topupGrant.Bucket.ID || allocations[1].Amount != 20 {
		t.Fatalf("expected FEFO allocation subscription->topup, got %+v", allocations)
	}
	subscriptionBucket := buckets.buckets[subscriptionGrant.Bucket.ID]
	topupBucket := buckets.buckets[topupGrant.Bucket.ID]
	if subscriptionBucket.Remaining != 0 || subscriptionBucket.Reserved != 100 || topupBucket.Remaining != 80 || topupBucket.Reserved != 20 {
		t.Fatalf("unexpected bucket balances, subscription=%#v topup=%#v", subscriptionBucket, topupBucket)
	}
}

func TestCreditService_ReleaseRestoresBucketAllocation(t *testing.T) {
	svc, users, _, _, _, buckets := newCreditTestServiceWithBuckets()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	grant, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-release-bucket",
		BucketType:     domain.CreditBucketTypeTopup,
	})
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}
	reserved, err := svc.Reserve(context.Background(), CreditReservationInput{
		UserID:         "usr_1",
		Operation:      "editorial.studio.run",
		Amount:         40,
		IdempotencyKey: "reserve-release-bucket",
	})
	if err != nil {
		t.Fatalf("reserve failed: %v", err)
	}
	released, err := svc.Release(context.Background(), CreditFinalizationInput{
		ReservationID:  reserved.Reservation.ID,
		IdempotencyKey: "release-bucket",
	})
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	bucket := buckets.buckets[grant.Bucket.ID]
	if released.Account.Balance != 100 || released.Account.Reserved != 0 || bucket.Remaining != 100 || bucket.Reserved != 0 || bucket.Status != domain.CreditBucketStatusActive {
		t.Fatalf("expected release to restore account and bucket, account=%#v bucket=%#v", released.Account, bucket)
	}
}

func TestCreditService_ExpireBucketsExpiresOnlySubscriptionBuckets(t *testing.T) {
	svc, users, accounts, _, _, buckets := newCreditTestServiceWithBuckets()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	periodStart := now.AddDate(0, -1, 0)
	periodEnd := now.Add(-time.Hour)
	subscriptionGrant, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-expiring-subscription",
		Source:         domain.GrantSourceFulfillment,
		BucketType:     domain.CreditBucketTypeSubscriptionPeriod,
		PeriodStartAt:  &periodStart,
		PeriodEndAt:    &periodEnd,
	})
	if err != nil {
		t.Fatalf("subscription grant failed: %v", err)
	}
	topupGrant, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         50,
		IdempotencyKey: "grant-non-expiring-topup",
		Source:         domain.GrantSourceFulfillment,
		BucketType:     domain.CreditBucketTypeTopup,
		ExpiresAt:      &periodEnd,
	})
	if err != nil {
		t.Fatalf("topup grant failed: %v", err)
	}

	result, err := svc.ExpireBuckets(context.Background(), CreditBucketExpiryInput{Now: now})
	if err != nil {
		t.Fatalf("expire failed: %v", err)
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	subscriptionBucket := buckets.buckets[subscriptionGrant.Bucket.ID]
	topupBucket := buckets.buckets[topupGrant.Bucket.ID]
	if result.ExpiredAmount != 100 || len(result.ExpiredBuckets) != 1 || len(result.Transactions) != 1 {
		t.Fatalf("expected one expired subscription bucket, got %#v", result)
	}
	if account.Balance != 50 || subscriptionBucket.Status != domain.CreditBucketStatusExpired || subscriptionBucket.Remaining != 0 {
		t.Fatalf("expected subscription credits removed from aggregate balance, account=%#v bucket=%#v", account, subscriptionBucket)
	}
	if topupBucket.Status != domain.CreditBucketStatusActive || topupBucket.Remaining != 50 || topupBucket.ExpiresAt != nil {
		t.Fatalf("top-up bucket must not expire with subscription period, got %#v", topupBucket)
	}
	if result.Transactions[0].BucketID != subscriptionBucket.ID || result.Transactions[0].Amount != -100 || result.Transactions[0].Type != domain.CreditTransactionTypeExpire {
		t.Fatalf("unexpected expiry transaction: %#v", result.Transactions[0])
	}
}

func TestCreditService_ExpireBucketsNeverCreatesNegativeBalance(t *testing.T) {
	svc, users, accounts, _, _, buckets := newCreditTestServiceWithBuckets()
	users.users["usr_1"] = &domain.User{ID: "usr_1", Email: "writer@example.com"}
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	periodEnd := now.Add(-time.Hour)
	grant, err := svc.Grant(context.Background(), CreditGrantInput{
		UserID:         "usr_1",
		Amount:         100,
		IdempotencyKey: "grant-expiry-cap",
		Source:         domain.GrantSourceFulfillment,
		BucketType:     domain.CreditBucketTypeSubscriptionPeriod,
		PeriodEndAt:    &periodEnd,
	})
	if err != nil {
		t.Fatalf("grant failed: %v", err)
	}
	account, err := accounts.GetByUserID(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	account.Balance = 30

	result, err := svc.ExpireBuckets(context.Background(), CreditBucketExpiryInput{Now: now})
	if err != nil {
		t.Fatalf("expire failed: %v", err)
	}
	bucket := buckets.buckets[grant.Bucket.ID]
	if result.ExpiredAmount != 30 || result.Transactions[0].Amount != -30 || account.Balance != 0 {
		t.Fatalf("expected expiry capped by account balance, result=%#v account=%#v", result, account)
	}
	if bucket.Status != domain.CreditBucketStatusExpired || bucket.Remaining != 0 {
		t.Fatalf("expected bucket to be fully expired even when aggregate balance is lower, got %#v", bucket)
	}
}
