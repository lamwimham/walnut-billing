package gorm_repo

import (
	"context"
	"walnut-billing/internal/domain"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.CreditAccountRepository = (*CreditAccountRepo)(nil)
var _ repository.CreditReservationRepository = (*CreditReservationRepo)(nil)
var _ repository.CreditTransactionRepository = (*CreditTransactionRepo)(nil)

type CreditAccountRepo struct {
	DB *gorm.DB
}

func (r *CreditAccountRepo) Create(ctx context.Context, account *domain.CreditAccount) error {
	return r.DB.WithContext(ctx).Create(account).Error
}

func (r *CreditAccountRepo) GetByID(ctx context.Context, id string) (*domain.CreditAccount, error) {
	var account domain.CreditAccount
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&account).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &account, nil
}

func (r *CreditAccountRepo) GetByUserID(ctx context.Context, userID string) (*domain.CreditAccount, error) {
	var account domain.CreditAccount
	if err := r.DB.WithContext(ctx).Where("user_id = ?", userID).First(&account).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &account, nil
}

func (r *CreditAccountRepo) Update(ctx context.Context, account *domain.CreditAccount) error {
	return r.DB.WithContext(ctx).Save(account).Error
}

func (r *CreditAccountRepo) WithTx(tx *gorm.DB) *CreditAccountRepo {
	return &CreditAccountRepo{DB: tx}
}

type CreditReservationRepo struct {
	DB *gorm.DB
}

func (r *CreditReservationRepo) Create(ctx context.Context, reservation *domain.CreditReservation) error {
	return r.DB.WithContext(ctx).Create(reservation).Error
}

func (r *CreditReservationRepo) GetByID(ctx context.Context, id string) (*domain.CreditReservation, error) {
	var reservation domain.CreditReservation
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(&reservation).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &reservation, nil
}

func (r *CreditReservationRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditReservation, error) {
	var reservation domain.CreditReservation
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&reservation).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &reservation, nil
}

func (r *CreditReservationRepo) List(ctx context.Context, query repository.CreditReservationQuery) ([]domain.CreditReservation, error) {
	var reservations []domain.CreditReservation
	q := r.DB.WithContext(ctx).Model(&domain.CreditReservation{})
	if query.UserID != "" {
		q = q.Where("user_id = ?", query.UserID)
	}
	if query.FeatureID != "" {
		q = q.Where("feature_id = ?", query.FeatureID)
	}
	if query.Operation != "" {
		q = q.Where("operation = ?", query.Operation)
	}
	if query.ExecutionID != "" {
		q = q.Where("execution_id = ?", query.ExecutionID)
	}
	if query.Status != "" {
		q = q.Where("status = ?", query.Status)
	}
	q = q.Order("created_at DESC")
	if query.Limit > 0 {
		q = q.Limit(query.Limit)
	}
	if query.Offset > 0 {
		q = q.Offset(query.Offset)
	}
	if err := q.Find(&reservations).Error; err != nil {
		return nil, err
	}
	return reservations, nil
}

func (r *CreditReservationRepo) Update(ctx context.Context, reservation *domain.CreditReservation) error {
	return r.DB.WithContext(ctx).Save(reservation).Error
}

func (r *CreditReservationRepo) WithTx(tx *gorm.DB) *CreditReservationRepo {
	return &CreditReservationRepo{DB: tx}
}

type CreditTransactionRepo struct {
	DB *gorm.DB
}

func (r *CreditTransactionRepo) Create(ctx context.Context, transaction *domain.CreditTransaction) error {
	return r.DB.WithContext(ctx).Create(transaction).Error
}

func (r *CreditTransactionRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditTransaction, error) {
	var transaction domain.CreditTransaction
	if err := r.DB.WithContext(ctx).Where("idempotency_key = ?", key).First(&transaction).Error; err != nil {
		return nil, mapGormNotFound(err)
	}
	return &transaction, nil
}

func (r *CreditTransactionRepo) ListByUser(ctx context.Context, userID string, limit int, offset int) ([]domain.CreditTransaction, error) {
	var transactions []domain.CreditTransaction
	q := r.DB.WithContext(ctx).Where("user_id = ?", userID).Order("created_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&transactions).Error; err != nil {
		return nil, err
	}
	return transactions, nil
}

func (r *CreditTransactionRepo) ListByReservationIDs(ctx context.Context, reservationIDs []string) ([]domain.CreditTransaction, error) {
	var transactions []domain.CreditTransaction
	if len(reservationIDs) == 0 {
		return transactions, nil
	}
	if err := r.DB.WithContext(ctx).
		Where("reservation_id IN ?", reservationIDs).
		Order("created_at ASC").
		Find(&transactions).Error; err != nil {
		return nil, err
	}
	return transactions, nil
}

func (r *CreditTransactionRepo) WithTx(tx *gorm.DB) *CreditTransactionRepo {
	return &CreditTransactionRepo{DB: tx}
}
