package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"
)

var (
	ErrCreditAccountNotFound = errors.New("credit account not found")
	ErrInvalidCreditAmount   = errors.New("invalid credit amount")
	ErrInsufficientCredits   = errors.New("insufficient credits")
	ErrReservationNotFound   = errors.New("credit reservation not found")
	ErrReservationNotPending = errors.New("credit reservation is not pending")
	ErrReservationExpired    = errors.New("credit reservation has expired")
	ErrIdempotencyRequired   = errors.New("idempotency key is required")
)

type CreditGrantInput struct {
	UserID         string
	Amount         int64
	IdempotencyKey string
	Source         string
	Description    string
}

type CreditReservationInput struct {
	UserID         string
	Operation      string
	Amount         int64
	IdempotencyKey string
	FeatureID      string
	ExecutionID    string
	Metadata       map[string]any
	ExpiresAt      *time.Time
}

type CreditFinalizationInput struct {
	ReservationID  string
	IdempotencyKey string
}

type CreditMutationResult struct {
	Account     *domain.CreditAccount     `json:"account"`
	Reservation *domain.CreditReservation `json:"reservation,omitempty"`
	Transaction *domain.CreditTransaction `json:"transaction,omitempty"`
}

type UsageRecordQuery struct {
	UserID      string
	FeatureID   string
	Operation   string
	ExecutionID string
	Status      string
	Limit       int
	Offset      int
}

// CreditService owns the Walnut Credits account, reservation, and ledger flow.
type CreditService interface {
	AccountForUser(ctx context.Context, userID string) (*domain.CreditAccount, error)
	Grant(ctx context.Context, input CreditGrantInput) (*CreditMutationResult, error)
	Reserve(ctx context.Context, input CreditReservationInput) (*CreditMutationResult, error)
	Commit(ctx context.Context, input CreditFinalizationInput) (*CreditMutationResult, error)
	Release(ctx context.Context, input CreditFinalizationInput) (*CreditMutationResult, error)
	ListTransactions(ctx context.Context, userID string, limit int, offset int) ([]domain.CreditTransaction, error)
	ListUsageRecords(ctx context.Context, query UsageRecordQuery) ([]domain.UsageRecord, error)
}

type creditServiceImpl struct {
	users        repository.UserRepository
	accounts     repository.CreditAccountRepository
	reservations repository.CreditReservationRepository
	transactions repository.CreditTransactionRepository
	uowFactory   func() repository.UnitOfWork
}

func NewCreditService(
	users repository.UserRepository,
	accounts repository.CreditAccountRepository,
	reservations repository.CreditReservationRepository,
	transactions repository.CreditTransactionRepository,
	uowFactory func() repository.UnitOfWork,
) CreditService {
	return &creditServiceImpl{
		users:        users,
		accounts:     accounts,
		reservations: reservations,
		transactions: transactions,
		uowFactory:   uowFactory,
	}
}

func (s *creditServiceImpl) AccountForUser(ctx context.Context, userID string) (*domain.CreditAccount, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrUserNotFound
	}
	account, err := s.accounts.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrCreditAccountNotFound
		}
		return nil, err
	}
	return account, nil
}

func (s *creditServiceImpl) Grant(ctx context.Context, input CreditGrantInput) (*CreditMutationResult, error) {
	userID := strings.TrimSpace(input.UserID)
	key := strings.TrimSpace(input.IdempotencyKey)
	if userID == "" {
		return nil, ErrUserNotFound
	}
	if input.Amount <= 0 {
		return nil, ErrInvalidCreditAmount
	}
	if key == "" {
		return nil, ErrIdempotencyRequired
	}
	if err := s.ensureUser(ctx, userID); err != nil {
		return nil, err
	}

	var result *CreditMutationResult
	err := s.withCreditTransaction(ctx, func(repos creditRepos) error {
		mutation, err := grantCreditsWithRepos(ctx, repos, input)
		if err != nil {
			return err
		}
		result = mutation
		return nil
	})
	return result, err
}

func grantCreditsWithRepos(ctx context.Context, repos creditRepos, input CreditGrantInput) (*CreditMutationResult, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	existing, err := repos.transactions.GetByIdempotencyKey(ctx, key)
	if err == nil {
		account, err := repos.accounts.GetByID(ctx, existing.AccountID)
		if err != nil {
			return nil, err
		}
		return &CreditMutationResult{Account: account, Transaction: existing}, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	account, err := ensureCreditAccount(ctx, repos.accounts, strings.TrimSpace(input.UserID))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	account.Balance += input.Amount
	account.UpdatedAt = now
	if err := repos.accounts.Update(ctx, account); err != nil {
		return nil, err
	}

	transaction, err := newCreditTransaction(ctx, repos.transactions, creditTransactionInput{
		Account:         account,
		TransactionType: domain.CreditTransactionTypeGrant,
		Amount:          input.Amount,
		IdempotencyKey:  key,
		Source:          defaultString(strings.TrimSpace(input.Source), "admin"),
		Description:     strings.TrimSpace(input.Description),
		CreatedAt:       now,
	})
	if err != nil {
		return nil, err
	}
	return &CreditMutationResult{Account: account, Transaction: transaction}, nil
}

func (s *creditServiceImpl) Reserve(ctx context.Context, input CreditReservationInput) (*CreditMutationResult, error) {
	userID := strings.TrimSpace(input.UserID)
	key := strings.TrimSpace(input.IdempotencyKey)
	if userID == "" {
		return nil, ErrUserNotFound
	}
	if input.Amount <= 0 {
		return nil, ErrInvalidCreditAmount
	}
	if key == "" {
		return nil, ErrIdempotencyRequired
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now().UTC()) {
		return nil, ErrReservationExpired
	}
	if err := s.ensureUser(ctx, userID); err != nil {
		return nil, err
	}

	var result *CreditMutationResult
	err := s.withCreditTransaction(ctx, func(repos creditRepos) error {
		existing, err := repos.reservations.GetByIdempotencyKey(ctx, key)
		if err == nil {
			account, err := repos.accounts.GetByID(ctx, existing.AccountID)
			if err != nil {
				return err
			}
			result = &CreditMutationResult{Account: account, Reservation: existing}
			return nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return err
		}

		account, err := s.ensureAccount(ctx, repos.accounts, userID)
		if err != nil {
			return err
		}
		if account.Balance < input.Amount {
			return ErrInsufficientCredits
		}
		now := time.Now().UTC()
		account.Balance -= input.Amount
		account.Reserved += input.Amount
		account.UpdatedAt = now
		if err := repos.accounts.Update(ctx, account); err != nil {
			return err
		}

		reservationID, err := generateEntityID("crr_")
		if err != nil {
			return err
		}
		reservation := &domain.CreditReservation{
			ID:             reservationID,
			AccountID:      account.ID,
			UserID:         userID,
			Operation:      strings.TrimSpace(input.Operation),
			Amount:         input.Amount,
			Status:         domain.CreditReservationStatusPending,
			IdempotencyKey: key,
			FeatureID:      strings.TrimSpace(input.FeatureID),
			ExecutionID:    strings.TrimSpace(input.ExecutionID),
			Metadata:       encodeUsageMetadata(sanitizedUsageMetadata(input.Metadata)),
			ExpiresAt:      input.ExpiresAt,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := repos.reservations.Create(ctx, reservation); err != nil {
			return err
		}

		transaction, err := s.newTransaction(ctx, repos.transactions, creditTransactionInput{
			Account:         account,
			ReservationID:   reservation.ID,
			TransactionType: domain.CreditTransactionTypeReserve,
			Amount:          -input.Amount,
			IdempotencyKey:  "reserve:" + key,
			Source:          defaultString(strings.TrimSpace(input.Operation), "usage"),
			Description:     "reserve credits",
			CreatedAt:       now,
		})
		if err != nil {
			return err
		}
		result = &CreditMutationResult{Account: account, Reservation: reservation, Transaction: transaction}
		return nil
	})
	return result, err
}

func (s *creditServiceImpl) Commit(ctx context.Context, input CreditFinalizationInput) (*CreditMutationResult, error) {
	return s.finalizeReservation(ctx, input, domain.CreditReservationStatusCommitted, domain.CreditTransactionTypeCommit)
}

func (s *creditServiceImpl) Release(ctx context.Context, input CreditFinalizationInput) (*CreditMutationResult, error) {
	return s.finalizeReservation(ctx, input, domain.CreditReservationStatusReleased, domain.CreditTransactionTypeRelease)
}

func (s *creditServiceImpl) ListTransactions(ctx context.Context, userID string, limit int, offset int) ([]domain.CreditTransaction, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrUserNotFound
	}
	return s.transactions.ListByUser(ctx, userID, limit, offset)
}

func (s *creditServiceImpl) ListUsageRecords(ctx context.Context, query UsageRecordQuery) ([]domain.UsageRecord, error) {
	userID := strings.TrimSpace(query.UserID)
	if userID == "" {
		return nil, ErrUserNotFound
	}
	reservations, err := s.reservations.List(ctx, repository.CreditReservationQuery{
		UserID:      userID,
		FeatureID:   strings.TrimSpace(query.FeatureID),
		Operation:   strings.TrimSpace(query.Operation),
		ExecutionID: strings.TrimSpace(query.ExecutionID),
		Status:      strings.TrimSpace(query.Status),
		Limit:       query.Limit,
		Offset:      query.Offset,
	})
	if err != nil {
		return nil, err
	}
	reservationIDs := make([]string, 0, len(reservations))
	for _, reservation := range reservations {
		reservationIDs = append(reservationIDs, reservation.ID)
	}
	transactions, err := s.transactions.ListByReservationIDs(ctx, reservationIDs)
	if err != nil {
		return nil, err
	}
	transactionsByReservation := make(map[string][]domain.CreditTransaction)
	for _, transaction := range transactions {
		transactionsByReservation[transaction.ReservationID] = append(
			transactionsByReservation[transaction.ReservationID],
			transaction,
		)
	}
	records := make([]domain.UsageRecord, 0, len(reservations))
	for _, reservation := range reservations {
		records = append(records, usageRecordFromReservation(
			reservation,
			transactionsByReservation[reservation.ID],
		))
	}
	return records, nil
}

func (s *creditServiceImpl) finalizeReservation(ctx context.Context, input CreditFinalizationInput, finalStatus string, transactionType string) (*CreditMutationResult, error) {
	reservationID := strings.TrimSpace(input.ReservationID)
	key := strings.TrimSpace(input.IdempotencyKey)
	if reservationID == "" {
		return nil, ErrReservationNotFound
	}
	if key == "" {
		return nil, ErrIdempotencyRequired
	}

	var result *CreditMutationResult
	err := s.withCreditTransaction(ctx, func(repos creditRepos) error {
		existing, err := repos.transactions.GetByIdempotencyKey(ctx, key)
		if err == nil {
			reservation, err := repos.reservations.GetByID(ctx, existing.ReservationID)
			if err != nil {
				return err
			}
			account, err := repos.accounts.GetByID(ctx, existing.AccountID)
			if err != nil {
				return err
			}
			result = &CreditMutationResult{Account: account, Reservation: reservation, Transaction: existing}
			return nil
		}
		if !errors.Is(err, repository.ErrNotFound) {
			return err
		}

		reservation, err := repos.reservations.GetByID(ctx, reservationID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrReservationNotFound
			}
			return err
		}
		if reservation.Status != domain.CreditReservationStatusPending {
			return ErrReservationNotPending
		}
		now := time.Now().UTC()
		if finalStatus == domain.CreditReservationStatusCommitted && reservation.ExpiresAt != nil && !reservation.ExpiresAt.After(now) {
			return ErrReservationExpired
		}

		account, err := repos.accounts.GetByID(ctx, reservation.AccountID)
		if err != nil {
			return err
		}
		if account.Reserved < reservation.Amount {
			return ErrReservationNotPending
		}
		account.Reserved -= reservation.Amount
		if finalStatus == domain.CreditReservationStatusReleased {
			account.Balance += reservation.Amount
		}
		account.UpdatedAt = now
		if err := repos.accounts.Update(ctx, account); err != nil {
			return err
		}

		reservation.Status = finalStatus
		reservation.UpdatedAt = now
		if finalStatus == domain.CreditReservationStatusCommitted {
			reservation.CommittedAt = &now
		} else {
			reservation.ReleasedAt = &now
		}
		if err := repos.reservations.Update(ctx, reservation); err != nil {
			return err
		}

		amount := int64(0)
		description := "commit reserved credits"
		if transactionType == domain.CreditTransactionTypeRelease {
			amount = reservation.Amount
			description = "release reserved credits"
		}
		transaction, err := s.newTransaction(ctx, repos.transactions, creditTransactionInput{
			Account:         account,
			ReservationID:   reservation.ID,
			TransactionType: transactionType,
			Amount:          amount,
			IdempotencyKey:  key,
			Source:          defaultString(strings.TrimSpace(reservation.Operation), "usage"),
			Description:     description,
			CreatedAt:       now,
		})
		if err != nil {
			return err
		}
		result = &CreditMutationResult{Account: account, Reservation: reservation, Transaction: transaction}
		return nil
	})
	return result, err
}

type creditRepos struct {
	accounts     repository.CreditAccountRepository
	reservations repository.CreditReservationRepository
	transactions repository.CreditTransactionRepository
}

func (s *creditServiceImpl) withCreditTransaction(ctx context.Context, fn func(creditRepos) error) error {
	if s.uowFactory == nil {
		return fn(creditRepos{accounts: s.accounts, reservations: s.reservations, transactions: s.transactions})
	}
	uow := s.uowFactory()
	if err := uow.Begin(ctx); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback()
		}
	}()

	repos := uow.Repos()
	creditRepos := creditRepos{
		accounts:     firstCreditAccountRepo(repos.CreditAccountRepo, s.accounts),
		reservations: firstCreditReservationRepo(repos.CreditReservationRepo, s.reservations),
		transactions: firstCreditTransactionRepo(repos.CreditTransactionRepo, s.transactions),
	}
	if err := fn(creditRepos); err != nil {
		return err
	}
	if err := uow.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func usageRecordFromReservation(reservation domain.CreditReservation, transactions []domain.CreditTransaction) domain.UsageRecord {
	featureID := strings.TrimSpace(reservation.FeatureID)
	if featureID == "" {
		featureID = strings.TrimSpace(reservation.Operation)
	}
	return domain.UsageRecord{
		ReservationID: reservation.ID,
		UserID:        reservation.UserID,
		FeatureID:     featureID,
		Operation:     reservation.Operation,
		ExecutionID:   reservation.ExecutionID,
		Amount:        reservation.Amount,
		Status:        reservation.Status,
		Metadata:      decodeUsageMetadata(reservation.Metadata),
		Transactions:  transactions,
		CreatedAt:     reservation.CreatedAt,
		UpdatedAt:     reservation.UpdatedAt,
		CommittedAt:   reservation.CommittedAt,
		ReleasedAt:    reservation.ReleasedAt,
	}
}

func sanitizedUsageMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	sanitized := make(map[string]any)
	for _, key := range []string{"project_id", "document_id", "conversation_id", "client_message_id"} {
		value := strings.TrimSpace(toUsageMetadataString(metadata[key]))
		if value != "" {
			sanitized[key] = value
		}
	}
	return sanitized
}

func toUsageMetadataString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func encodeUsageMetadata(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func decodeUsageMetadata(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return map[string]any{}
	}
	return metadata
}

func firstCreditAccountRepo(primary repository.CreditAccountRepository, fallback repository.CreditAccountRepository) repository.CreditAccountRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstCreditReservationRepo(primary repository.CreditReservationRepository, fallback repository.CreditReservationRepository) repository.CreditReservationRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstCreditTransactionRepo(primary repository.CreditTransactionRepository, fallback repository.CreditTransactionRepository) repository.CreditTransactionRepository {
	if primary != nil {
		return primary
	}
	return fallback
}

func (s *creditServiceImpl) ensureUser(ctx context.Context, userID string) error {
	if s.users == nil {
		return nil
	}
	_, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrUserNotFound
		}
		return err
	}
	return nil
}

func (s *creditServiceImpl) ensureAccount(ctx context.Context, accounts repository.CreditAccountRepository, userID string) (*domain.CreditAccount, error) {
	return ensureCreditAccount(ctx, accounts, userID)
}

func ensureCreditAccount(ctx context.Context, accounts repository.CreditAccountRepository, userID string) (*domain.CreditAccount, error) {
	account, err := accounts.GetByUserID(ctx, userID)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	accountID, err := generateEntityID("cra_")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	account = &domain.CreditAccount{
		ID:        accountID,
		UserID:    userID,
		Balance:   0,
		Reserved:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := accounts.Create(ctx, account); err != nil {
		return nil, err
	}
	return account, nil
}

type creditTransactionInput struct {
	Account         *domain.CreditAccount
	ReservationID   string
	TransactionType string
	Amount          int64
	IdempotencyKey  string
	Source          string
	Description     string
	CreatedAt       time.Time
}

func (s *creditServiceImpl) newTransaction(ctx context.Context, transactions repository.CreditTransactionRepository, input creditTransactionInput) (*domain.CreditTransaction, error) {
	return newCreditTransaction(ctx, transactions, input)
}

func newCreditTransaction(ctx context.Context, transactions repository.CreditTransactionRepository, input creditTransactionInput) (*domain.CreditTransaction, error) {
	transactionID, err := generateEntityID("ctx_")
	if err != nil {
		return nil, err
	}
	transaction := &domain.CreditTransaction{
		ID:             transactionID,
		AccountID:      input.Account.ID,
		UserID:         input.Account.UserID,
		ReservationID:  input.ReservationID,
		Type:           input.TransactionType,
		Amount:         input.Amount,
		BalanceAfter:   input.Account.Balance,
		ReservedAfter:  input.Account.Reserved,
		IdempotencyKey: input.IdempotencyKey,
		Source:         input.Source,
		Description:    input.Description,
		CreatedAt:      input.CreatedAt,
	}
	if err := transactions.Create(ctx, transaction); err != nil {
		return nil, err
	}
	return transaction, nil
}
