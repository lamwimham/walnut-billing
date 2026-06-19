package gorm_repo

import (
	"context"
	"walnut-billing/internal/repository"

	"gorm.io/gorm"
)

var _ repository.UnitOfWork = (*GormUnitOfWork)(nil)

// GormUnitOfWork implements the UnitOfWork pattern for GORM.
// It wraps a single database transaction and provides transactional repositories.
type GormUnitOfWork struct {
	db        *gorm.DB
	tx        *gorm.DB
	committed bool
}

// NewUnitOfWork creates a new UnitOfWork bound to the given database.
func NewUnitOfWork(db *gorm.DB) *GormUnitOfWork {
	return &GormUnitOfWork{db: db}
}

// Begin starts a new transaction.
func (u *GormUnitOfWork) Begin(ctx context.Context) error {
	if u.tx != nil && !u.committed {
		return nil // Already in a transaction
	}
	u.tx = u.db.WithContext(ctx).Begin()
	if u.tx.Error != nil {
		return u.tx.Error
	}
	u.committed = false
	return nil
}

// Repos returns repositories bound to the current transaction.
func (u *GormUnitOfWork) Repos() repository.TransactionalRepositories {
	if u.tx == nil {
		return repository.TransactionalRepositories{
			OrderRepo:                    &OrderRepo{DB: u.db},
			LicenseRepo:                  &LicenseRepo{DB: u.db},
			UserRepo:                     &UserRepo{DB: u.db},
			EntitlementGrantRepo:         &EntitlementGrantRepo{DB: u.db},
			CreditAccountRepo:            &CreditAccountRepo{DB: u.db},
			CreditReservationRepo:        &CreditReservationRepo{DB: u.db},
			CreditTransactionRepo:        &CreditTransactionRepo{DB: u.db},
			CreditBucketRepo:             &CreditBucketRepo{DB: u.db},
			FulfillmentExecutionRepo:     &FulfillmentExecutionRepo{DB: u.db},
			PaymentEventRepo:             &PaymentEventRepo{DB: u.db},
			PaymentRiskFlagRepo:          &PaymentRiskFlagRepo{DB: u.db},
			SubscriptionCancellationRepo: &SubscriptionCancellationRepo{DB: u.db},
			UserDeviceRepo:               &UserDeviceRepo{DB: u.db},
			TrialGrantRepo:               &TrialGrantRepo{DB: u.db},
			AccessLoginChallengeRepo:     &AccessLoginChallengeRepo{DB: u.db},
			CloudProjectRepo:             &CloudProjectRepo{DB: u.db},
			CloudSyncSessionRepo:         &CloudSyncSessionRepo{DB: u.db},
			CloudManifestRepo:            &CloudManifestRepo{DB: u.db},
			CloudObjectRepo:              &CloudObjectRepo{DB: u.db},
		}
	}
	return repository.TransactionalRepositories{
		OrderRepo:                    (&OrderRepo{DB: u.db}).WithTx(u.tx),
		LicenseRepo:                  (&LicenseRepo{DB: u.db}).WithTx(u.tx),
		UserRepo:                     (&UserRepo{DB: u.db}).WithTx(u.tx),
		EntitlementGrantRepo:         (&EntitlementGrantRepo{DB: u.db}).WithTx(u.tx),
		CreditAccountRepo:            (&CreditAccountRepo{DB: u.db}).WithTx(u.tx),
		CreditReservationRepo:        (&CreditReservationRepo{DB: u.db}).WithTx(u.tx),
		CreditTransactionRepo:        (&CreditTransactionRepo{DB: u.db}).WithTx(u.tx),
		CreditBucketRepo:             (&CreditBucketRepo{DB: u.db}).WithTx(u.tx),
		FulfillmentExecutionRepo:     (&FulfillmentExecutionRepo{DB: u.db}).WithTx(u.tx),
		PaymentEventRepo:             (&PaymentEventRepo{DB: u.db}).WithTx(u.tx),
		PaymentRiskFlagRepo:          (&PaymentRiskFlagRepo{DB: u.db}).WithTx(u.tx),
		SubscriptionCancellationRepo: (&SubscriptionCancellationRepo{DB: u.db}).WithTx(u.tx),
		UserDeviceRepo:               (&UserDeviceRepo{DB: u.db}).WithTx(u.tx),
		TrialGrantRepo:               (&TrialGrantRepo{DB: u.db}).WithTx(u.tx),
		AccessLoginChallengeRepo:     (&AccessLoginChallengeRepo{DB: u.db}).WithTx(u.tx),
		CloudProjectRepo:             (&CloudProjectRepo{DB: u.db}).WithTx(u.tx),
		CloudSyncSessionRepo:         (&CloudSyncSessionRepo{DB: u.db}).WithTx(u.tx),
		CloudManifestRepo:            (&CloudManifestRepo{DB: u.db}).WithTx(u.tx),
		CloudObjectRepo:              (&CloudObjectRepo{DB: u.db}).WithTx(u.tx),
	}
}

// Commit commits the transaction.
func (u *GormUnitOfWork) Commit() error {
	if u.tx == nil {
		return nil
	}
	err := u.tx.Commit().Error
	if err == nil {
		u.committed = true
	}
	return err
}

// Rollback rolls back the transaction.
func (u *GormUnitOfWork) Rollback() error {
	if u.tx == nil {
		return nil
	}
	return u.tx.Rollback().Error
}
