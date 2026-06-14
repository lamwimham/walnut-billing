package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

func newCreditBucketFromGrant(account *domain.CreditAccount, input CreditGrantInput, key string, now time.Time) (*domain.CreditBucket, error) {
	bucketID, err := generateEntityID("crb_")
	if err != nil {
		return nil, err
	}
	bucketType := normalizeCreditBucketType(input)
	expiresAt := cloneTimeUTC(input.ExpiresAt)
	periodEnd := cloneTimeUTC(input.PeriodEndAt)
	if bucketType == domain.CreditBucketTypeTopup {
		expiresAt = nil
	}
	if bucketType == domain.CreditBucketTypeSubscriptionPeriod && expiresAt == nil && periodEnd != nil {
		expiresAt = cloneTimeUTC(periodEnd)
	}
	return &domain.CreditBucket{
		ID:             bucketID,
		AccountID:      account.ID,
		UserID:         account.UserID,
		Type:           bucketType,
		Source:         defaultString(strings.TrimSpace(input.Source), "admin"),
		SourceOrderNo:  strings.TrimSpace(input.SourceOrderNo),
		PeriodStartAt:  cloneTimeUTC(input.PeriodStartAt),
		PeriodEndAt:    periodEnd,
		ExpiresAt:      expiresAt,
		Granted:        input.Amount,
		Remaining:      input.Amount,
		Reserved:       0,
		Status:         domain.CreditBucketStatusActive,
		IdempotencyKey: key,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

func normalizeCreditBucketType(input CreditGrantInput) string {
	bucketType := strings.TrimSpace(input.BucketType)
	switch bucketType {
	case domain.CreditBucketTypeAdmin, domain.CreditBucketTypeLegacy, domain.CreditBucketTypeTopup, domain.CreditBucketTypeSubscriptionPeriod:
		return bucketType
	}
	if strings.TrimSpace(input.Source) == domain.GrantSourceFulfillment {
		if input.ExpiresAt != nil || input.PeriodEndAt != nil {
			return domain.CreditBucketTypeSubscriptionPeriod
		}
		return domain.CreditBucketTypeTopup
	}
	return domain.CreditBucketTypeAdmin
}

func cloneTimeUTC(value *time.Time) *time.Time {
	if value == nil || value.IsZero() {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func encodeCreditBucketAllocations(allocations []CreditBucketAllocation) string {
	allocations = compactCreditBucketAllocations(allocations)
	if len(allocations) == 0 {
		return ""
	}
	encoded, err := json.Marshal(allocations)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodeCreditBucketAllocations(raw string) []CreditBucketAllocation {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var allocations []CreditBucketAllocation
	if err := json.Unmarshal([]byte(raw), &allocations); err != nil {
		return nil
	}
	return compactCreditBucketAllocations(allocations)
}

func compactCreditBucketAllocations(allocations []CreditBucketAllocation) []CreditBucketAllocation {
	compacted := make([]CreditBucketAllocation, 0, len(allocations))
	for _, allocation := range allocations {
		allocation.BucketID = strings.TrimSpace(allocation.BucketID)
		if allocation.BucketID == "" || allocation.Amount <= 0 {
			continue
		}
		compacted = append(compacted, allocation)
	}
	return compacted
}

func singleAllocationBucketID(allocations []CreditBucketAllocation) string {
	allocations = compactCreditBucketAllocations(allocations)
	if len(allocations) != 1 {
		return ""
	}
	return allocations[0].BucketID
}

type earliestExpiringBucketAllocationStrategy struct{}

func (earliestExpiringBucketAllocationStrategy) Allocate(ctx context.Context, buckets []domain.CreditBucket, amount int64, now time.Time) ([]CreditBucketAllocation, error) {
	if amount <= 0 {
		return nil, nil
	}
	eligible := make([]domain.CreditBucket, 0, len(buckets))
	for _, bucket := range buckets {
		if !creditBucketAvailableAt(bucket, now) {
			continue
		}
		eligible = append(eligible, bucket)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left := eligible[i]
		right := eligible[j]
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

	remaining := amount
	allocations := make([]CreditBucketAllocation, 0, len(eligible))
	for _, bucket := range eligible {
		if remaining <= 0 {
			break
		}
		used := minInt64(bucket.Remaining, remaining)
		if used <= 0 {
			continue
		}
		allocations = append(allocations, CreditBucketAllocation{BucketID: bucket.ID, Amount: used})
		remaining -= used
	}
	return allocations, nil
}

func creditBucketAvailableAt(bucket domain.CreditBucket, now time.Time) bool {
	if bucket.Status != domain.CreditBucketStatusActive || bucket.Remaining <= 0 {
		return false
	}
	return bucket.ExpiresAt == nil || bucket.ExpiresAt.After(now)
}

func (s *creditServiceImpl) reserveBucketCredits(ctx context.Context, repos creditRepos, account *domain.CreditAccount, amount int64, now time.Time) ([]CreditBucketAllocation, error) {
	if repos.buckets == nil || account == nil {
		return nil, nil
	}
	if _, err := expireCreditBucketsForAccount(ctx, repos, account, now, 0); err != nil {
		return nil, err
	}
	if account.Balance < amount {
		return nil, ErrInsufficientCredits
	}
	buckets, err := repos.buckets.List(ctx, repository.CreditBucketQuery{
		AccountID:         account.ID,
		Status:            domain.CreditBucketStatusActive,
		ActiveAt:          &now,
		PositiveRemaining: true,
	})
	if err != nil {
		return nil, err
	}
	strategy := s.allocationStrategy
	if strategy == nil {
		strategy = earliestExpiringBucketAllocationStrategy{}
	}
	allocations, err := strategy.Allocate(ctx, buckets, amount, now)
	if err != nil {
		return nil, err
	}
	allocations = compactCreditBucketAllocations(allocations)
	for _, allocation := range compactCreditBucketAllocations(allocations) {
		bucket, err := repos.buckets.GetByID(ctx, allocation.BucketID)
		if err != nil {
			return nil, err
		}
		if bucket.Remaining < allocation.Amount {
			return nil, ErrInsufficientCredits
		}
		bucket.Remaining -= allocation.Amount
		bucket.Reserved += allocation.Amount
		if bucket.Remaining == 0 && bucket.Reserved == 0 {
			bucket.Status = domain.CreditBucketStatusDepleted
		}
		bucket.UpdatedAt = now
		if err := repos.buckets.Update(ctx, bucket); err != nil {
			return nil, err
		}
	}
	return compactCreditBucketAllocations(allocations), nil
}

func totalCreditBucketAllocationAmount(allocations []CreditBucketAllocation) int64 {
	var total int64
	for _, allocation := range compactCreditBucketAllocations(allocations) {
		total += allocation.Amount
	}
	return total
}

func finalizeBucketAllocations(ctx context.Context, buckets repository.CreditBucketRepository, allocations []CreditBucketAllocation, finalStatus string, now time.Time) (int64, error) {
	restored := int64(0)
	for _, allocation := range compactCreditBucketAllocations(allocations) {
		bucket, err := buckets.GetByID(ctx, allocation.BucketID)
		if err != nil {
			return restored, err
		}
		if bucket.Reserved < allocation.Amount {
			return restored, ErrReservationNotPending
		}
		bucket.Reserved -= allocation.Amount
		if finalStatus == domain.CreditReservationStatusReleased {
			if bucket.ExpiresAt == nil || bucket.ExpiresAt.After(now) {
				bucket.Remaining += allocation.Amount
				restored += allocation.Amount
				bucket.Status = domain.CreditBucketStatusActive
			}
		}
		if bucket.Remaining == 0 && bucket.Reserved == 0 {
			if bucket.ExpiresAt != nil && !bucket.ExpiresAt.After(now) {
				bucket.Status = domain.CreditBucketStatusExpired
			} else {
				bucket.Status = domain.CreditBucketStatusDepleted
			}
		}
		bucket.UpdatedAt = now
		if err := buckets.Update(ctx, bucket); err != nil {
			return restored, err
		}
	}
	return restored, nil
}

func expireCreditBucketsWithRepos(ctx context.Context, repos creditRepos, now time.Time, limit int, accountID string) (*CreditBucketExpiryResult, error) {
	result := &CreditBucketExpiryResult{}
	if repos.buckets == nil {
		return result, nil
	}
	buckets, err := repos.buckets.List(ctx, repository.CreditBucketQuery{
		AccountID:         strings.TrimSpace(accountID),
		Status:            domain.CreditBucketStatusActive,
		ExpiresAtOrBefore: &now,
		PositiveRemaining: true,
		Limit:             limit,
	})
	if err != nil {
		return nil, err
	}
	accountsByID := make(map[string]*domain.CreditAccount)
	for idx := range buckets {
		bucket := buckets[idx]
		account := accountsByID[bucket.AccountID]
		if account == nil {
			account, err = repos.accounts.GetByID(ctx, bucket.AccountID)
			if err != nil {
				return nil, err
			}
			accountsByID[bucket.AccountID] = account
		}
		transaction, expiredAmount, err := expireCreditBucket(ctx, repos, account, &bucket, now)
		if err != nil {
			return nil, err
		}
		result.ExpiredBuckets = append(result.ExpiredBuckets, bucket)
		result.Transactions = append(result.Transactions, *transaction)
		result.ExpiredAmount += expiredAmount
	}
	return result, nil
}

func expireCreditBucketsForAccount(ctx context.Context, repos creditRepos, account *domain.CreditAccount, now time.Time, limit int) (*CreditBucketExpiryResult, error) {
	result := &CreditBucketExpiryResult{}
	if repos.buckets == nil || account == nil {
		return result, nil
	}
	buckets, err := repos.buckets.List(ctx, repository.CreditBucketQuery{
		AccountID:         account.ID,
		Status:            domain.CreditBucketStatusActive,
		ExpiresAtOrBefore: &now,
		PositiveRemaining: true,
		Limit:             limit,
	})
	if err != nil {
		return nil, err
	}
	for idx := range buckets {
		bucket := buckets[idx]
		transaction, expiredAmount, err := expireCreditBucket(ctx, repos, account, &bucket, now)
		if err != nil {
			return nil, err
		}
		result.ExpiredBuckets = append(result.ExpiredBuckets, bucket)
		result.Transactions = append(result.Transactions, *transaction)
		result.ExpiredAmount += expiredAmount
	}
	return result, nil
}

func expireCreditBucket(ctx context.Context, repos creditRepos, account *domain.CreditAccount, bucket *domain.CreditBucket, now time.Time) (*domain.CreditTransaction, int64, error) {
	expiredAmount := bucket.Remaining
	if account.Balance < expiredAmount {
		expiredAmount = account.Balance
	}
	if expiredAmount < 0 {
		expiredAmount = 0
	}
	if expiredAmount > 0 {
		account.Balance -= expiredAmount
		account.UpdatedAt = now
		if err := repos.accounts.Update(ctx, account); err != nil {
			return nil, 0, err
		}
	}
	bucket.Remaining = 0
	bucket.Status = domain.CreditBucketStatusExpired
	bucket.UpdatedAt = now
	if err := repos.buckets.Update(ctx, bucket); err != nil {
		return nil, 0, err
	}
	transaction, err := newCreditTransaction(ctx, repos.transactions, creditTransactionInput{
		Account:         account,
		BucketID:        bucket.ID,
		TransactionType: domain.CreditTransactionTypeExpire,
		Amount:          -expiredAmount,
		IdempotencyKey:  creditBucketExpireKey(*bucket),
		Source:          "credit_bucket_expiry",
		Description:     "expire credit bucket",
		CreatedAt:       now,
	})
	if err != nil {
		return nil, 0, err
	}
	return transaction, expiredAmount, nil
}

func creditBucketExpireKey(bucket domain.CreditBucket) string {
	expiresAt := "none"
	if bucket.ExpiresAt != nil {
		expiresAt = bucket.ExpiresAt.UTC().Format("20060102150405")
	}
	return "expire:" + strings.TrimSpace(bucket.ID) + ":" + expiresAt
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
