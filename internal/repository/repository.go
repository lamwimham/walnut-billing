package repository

import (
	"context"
	"errors"
	"time"

	"walnut-billing/internal/domain"
)

var ErrNotFound = errors.New("repository: not found")

// LicenseRepository defines the interface for license data access.
type LicenseRepository interface {
	Create(ctx context.Context, license *domain.License) error
	GetByKey(ctx context.Context, key string) (*domain.License, error)
	Update(ctx context.Context, license *domain.License) error
	List(ctx context.Context, status string) ([]domain.License, error)
}

// OrderRepository defines the interface for order data access.
type OrderRepository interface {
	Create(ctx context.Context, order *domain.Order) error
	GetByOutTradeNo(ctx context.Context, outTradeNo string) (*domain.Order, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.Order, error)
	Update(ctx context.Context, order *domain.Order) error
}

// ProductRepository defines the interface for product data access.
type ProductRepository interface {
	Create(ctx context.Context, product *domain.Product) error
	GetByCode(ctx context.Context, code string) (*domain.Product, error)
	List(ctx context.Context, visibleOnly bool) ([]domain.Product, error)
}

// AuditRepository defines the interface for immutable audit log data access.
type AuditRepository interface {
	Create(ctx context.Context, entry *domain.AuditEntry) error
	List(ctx context.Context, query AuditQuery) ([]domain.AuditEntry, error)
	Count(ctx context.Context, query AuditQuery) (int64, error)
}

// AuditQuery defines filtering criteria for audit log queries.
type AuditQuery struct {
	Action    string
	Actor     string
	Target    string
	Success   *bool
	StartTime time.Time
	EndTime   time.Time
	Limit     int
	Offset    int
}

// TransactionalRepositories returns new repository instances bound to a transaction.
// All operations on these repos will be part of the same transaction.
type TransactionalRepositories struct {
	OrderRepo             OrderRepository
	LicenseRepo           LicenseRepository
	CreditAccountRepo     CreditAccountRepository
	CreditReservationRepo CreditReservationRepository
	CreditTransactionRepo CreditTransactionRepository
}

// UnitOfWork manages a database transaction and provides transactional repositories.
type UnitOfWork interface {
	// Begin starts a new transaction.
	Begin(ctx context.Context) error
	// Repos returns repositories bound to the current transaction.
	Repos() TransactionalRepositories
	// Commit commits the transaction.
	Commit() error
	// Rollback rolls back the transaction.
	Rollback() error
}

// UserRepository defines data access for stable user identities.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
}

// RegistrationQuery defines filtering criteria for registration requests.
type RegistrationQuery struct {
	Status string
	UserID string
	Email  string
	Limit  int
	Offset int
}

// RegistrationRepository defines data access for feature registration requests.
type RegistrationRepository interface {
	Create(ctx context.Context, registration *domain.RegistrationRequest) error
	GetByID(ctx context.Context, id string) (*domain.RegistrationRequest, error)
	List(ctx context.Context, query RegistrationQuery) ([]domain.RegistrationRequest, error)
	Update(ctx context.Context, registration *domain.RegistrationRequest) error
}

// EntitlementGrantQuery defines filtering criteria for entitlement grants.
type EntitlementGrantQuery struct {
	UserID         string
	EntitlementID  string
	Status         string
	IncludeExpired bool
	Limit          int
	Offset         int
}

// EntitlementGrantRepository defines data access for feature grants.
type EntitlementGrantRepository interface {
	Create(ctx context.Context, grant *domain.EntitlementGrant) error
	GetByID(ctx context.Context, id string) (*domain.EntitlementGrant, error)
	List(ctx context.Context, query EntitlementGrantQuery) ([]domain.EntitlementGrant, error)
	ListByUser(ctx context.Context, userID string) ([]domain.EntitlementGrant, error)
	Update(ctx context.Context, grant *domain.EntitlementGrant) error
}

// CreditAccountRepository defines data access for Walnut Credits accounts.
type CreditAccountRepository interface {
	Create(ctx context.Context, account *domain.CreditAccount) error
	GetByID(ctx context.Context, id string) (*domain.CreditAccount, error)
	GetByUserID(ctx context.Context, userID string) (*domain.CreditAccount, error)
	Update(ctx context.Context, account *domain.CreditAccount) error
}

// CreditReservationQuery defines filtering criteria for usage reservations.
type CreditReservationQuery struct {
	UserID      string
	FeatureID   string
	Operation   string
	ExecutionID string
	Status      string
	Limit       int
	Offset      int
}

// CreditReservationRepository defines data access for idempotent credit reservations.
type CreditReservationRepository interface {
	Create(ctx context.Context, reservation *domain.CreditReservation) error
	GetByID(ctx context.Context, id string) (*domain.CreditReservation, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditReservation, error)
	List(ctx context.Context, query CreditReservationQuery) ([]domain.CreditReservation, error)
	Update(ctx context.Context, reservation *domain.CreditReservation) error
}

// CreditTransactionRepository defines data access for immutable credit ledger entries.
type CreditTransactionRepository interface {
	Create(ctx context.Context, transaction *domain.CreditTransaction) error
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditTransaction, error)
	ListByUser(ctx context.Context, userID string, limit int, offset int) ([]domain.CreditTransaction, error)
	ListByReservationIDs(ctx context.Context, reservationIDs []string) ([]domain.CreditTransaction, error)
}

// PaymentEventQuery defines filtering criteria for payment webhook events.
type PaymentEventQuery struct {
	Provider   string
	Status     string
	EventType  string
	OutTradeNo string
	Limit      int
	Offset     int
}

// PaymentEventRepository defines data access for provider webhook inbox events.
type PaymentEventRepository interface {
	Create(ctx context.Context, event *domain.PaymentEventInbox) error
	GetByID(ctx context.Context, id string) (*domain.PaymentEventInbox, error)
	GetByProviderEventID(ctx context.Context, provider string, providerEventID string) (*domain.PaymentEventInbox, error)
	List(ctx context.Context, query PaymentEventQuery) ([]domain.PaymentEventInbox, error)
	Update(ctx context.Context, event *domain.PaymentEventInbox) error
}
