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
	FindLatestSubscriptionOrder(ctx context.Context, query SubscriptionOrderQuery) (*domain.Order, error)
	Update(ctx context.Context, order *domain.Order) error
}

// SubscriptionOrderQuery locates the Walnut-owned order anchoring a software subscription.
type SubscriptionOrderQuery struct {
	UserID  string
	SKUCode string
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

// CloudProjectRepository defines data access for cloud-backed Walnut projects.
type CloudProjectRepository interface {
	Create(ctx context.Context, project *domain.CloudProject) error
	GetByID(ctx context.Context, id string) (*domain.CloudProject, error)
	GetByUserAndClientProject(ctx context.Context, userID string, clientProjectID string) (*domain.CloudProject, error)
	ListByUser(ctx context.Context, userID string, status string, limit int, offset int) ([]domain.CloudProject, error)
	Update(ctx context.Context, project *domain.CloudProject) error
}

// CloudManifestRepository defines data access for immutable cloud sync manifests.
type CloudManifestRepository interface {
	Create(ctx context.Context, manifest *domain.CloudManifest) error
	GetByID(ctx context.Context, id string) (*domain.CloudManifest, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.CloudManifest, error)
	ListByProject(ctx context.Context, cloudProjectID string, limit int, offset int) ([]domain.CloudManifest, error)
}

// CloudObjectRepository defines data access for cloud object metadata.
type CloudObjectRepository interface {
	Upsert(ctx context.Context, object *domain.CloudObject) error
	Update(ctx context.Context, object *domain.CloudObject) error
	ListByProject(ctx context.Context, cloudProjectID string, status string) ([]domain.CloudObject, error)
	SumActiveBytesByUser(ctx context.Context, userID string) (int64, error)
}

// TransactionalRepositories returns new repository instances bound to a transaction.
// All operations on these repos will be part of the same transaction.
type TransactionalRepositories struct {
	OrderRepo                    OrderRepository
	LicenseRepo                  LicenseRepository
	UserRepo                     UserRepository
	EntitlementGrantRepo         EntitlementGrantRepository
	CreditAccountRepo            CreditAccountRepository
	CreditReservationRepo        CreditReservationRepository
	CreditTransactionRepo        CreditTransactionRepository
	CreditBucketRepo             CreditBucketRepository
	FulfillmentExecutionRepo     FulfillmentExecutionRepository
	PaymentEventRepo             PaymentEventRepository
	PaymentRiskFlagRepo          PaymentRiskFlagRepository
	SubscriptionCancellationRepo SubscriptionCancellationRepository
	UserDeviceRepo               UserDeviceRepository
	TrialGrantRepo               TrialGrantRepository
	AccessLoginChallengeRepo     AccessLoginChallengeRepository
	CloudProjectRepo             CloudProjectRepository
	CloudManifestRepo            CloudManifestRepository
	CloudObjectRepo              CloudObjectRepository
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

// SubscriptionCancellationQuery defines filters for subscription cancellation facts.
type SubscriptionCancellationQuery struct {
	UserID  string
	SKUCode string
	Status  string
}

// SubscriptionCancellationRepository defines data access for subscription control facts.
type SubscriptionCancellationRepository interface {
	Create(ctx context.Context, cancellation *domain.SubscriptionCancellation) error
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error)
	GetByResumeIdempotencyKey(ctx context.Context, key string) (*domain.SubscriptionCancellation, error)
	FindActive(ctx context.Context, query SubscriptionCancellationQuery) (*domain.SubscriptionCancellation, error)
	Update(ctx context.Context, cancellation *domain.SubscriptionCancellation) error
}

// AccessLoginChallengeRepository defines data access for email login/recovery challenges.
type AccessLoginChallengeRepository interface {
	Create(ctx context.Context, challenge *domain.AccessLoginChallenge) error
	GetByID(ctx context.Context, id string) (*domain.AccessLoginChallenge, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.AccessLoginChallenge, error)
	Count(ctx context.Context, query AccessLoginChallengeQuery) (int64, error)
	Update(ctx context.Context, challenge *domain.AccessLoginChallenge) error
	ConsumePending(ctx context.Context, id string, consumedAt time.Time) (bool, error)
}

type AccessLoginChallengeQuery struct {
	Email        string
	ClientIPHash string
	CreatedAfter time.Time
	Statuses     []string
}

// AccessAccountQuery defines filters for the admin access-account read model.
type AccessAccountQuery struct {
	UserID string
	Email  string
	Status string
	Limit  int
	Offset int
}

// AccessAccountRecord groups the persisted identity, trial, device, and grant
// facts required by the admin access-account view.
type AccessAccountRecord struct {
	User              domain.User
	Devices           []domain.UserDevice
	TrialGrants       []domain.TrialGrant
	EntitlementGrants []domain.EntitlementGrant
}

// AccessAccountReadRepository defines the privacy-safe admin read model source.
type AccessAccountReadRepository interface {
	List(ctx context.Context, query AccessAccountQuery) ([]AccessAccountRecord, int64, error)
}

// UserRepository defines data access for stable user identities.
type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
}

// UserDeviceRepository defines data access for access-session device bindings.
type UserDeviceRepository interface {
	Create(ctx context.Context, device *domain.UserDevice) error
	GetByID(ctx context.Context, id string) (*domain.UserDevice, error)
	GetByUserAndDevice(ctx context.Context, userID string, deviceID string) (*domain.UserDevice, error)
	ListByUser(ctx context.Context, userID string, status string) ([]domain.UserDevice, error)
	Update(ctx context.Context, device *domain.UserDevice) error
}

// TrialGrantQuery defines filtering criteria for idempotent trial allocations.
type TrialGrantQuery struct {
	UserID    string
	Email     string
	GrantType string
	Status    string
	Limit     int
	Offset    int
}

// TrialGrantRepository defines data access for trial allocation ledgers.
type TrialGrantRepository interface {
	Create(ctx context.Context, grant *domain.TrialGrant) error
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.TrialGrant, error)
	List(ctx context.Context, query TrialGrantQuery) ([]domain.TrialGrant, error)
	Update(ctx context.Context, grant *domain.TrialGrant) error
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
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.EntitlementGrant, error)
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

// CreditBucketQuery defines filtering criteria for expirable credit sources.
type CreditBucketQuery struct {
	AccountID           string
	UserID              string
	Type                string
	Status              string
	SourceOrderNo       string
	SourceTransactionID string
	ActiveAt            *time.Time
	ExpiresAtOrBefore   *time.Time
	PositiveRemaining   bool
	Limit               int
	Offset              int
}

// CreditBucketRepository defines data access for source-level credit buckets.
type CreditBucketRepository interface {
	Create(ctx context.Context, bucket *domain.CreditBucket) error
	GetByID(ctx context.Context, id string) (*domain.CreditBucket, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.CreditBucket, error)
	List(ctx context.Context, query CreditBucketQuery) ([]domain.CreditBucket, error)
	Update(ctx context.Context, bucket *domain.CreditBucket) error
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
	GetByID(ctx context.Context, id string) (*domain.CreditTransaction, error)
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

// FulfillmentExecutionQuery defines filtering criteria for fulfillment effects.
type FulfillmentExecutionQuery struct {
	OrderID    uint
	OutTradeNo string
	UserID     string
	SKUCode    string
	RuleID     string
	TargetType string
	Status     string
	Limit      int
	Offset     int
}

// FulfillmentExecutionRepository defines data access for idempotent order fulfillment.
type FulfillmentExecutionRepository interface {
	Create(ctx context.Context, execution *domain.FulfillmentExecution) error
	GetByID(ctx context.Context, id string) (*domain.FulfillmentExecution, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*domain.FulfillmentExecution, error)
	List(ctx context.Context, query FulfillmentExecutionQuery) ([]domain.FulfillmentExecution, error)
	Update(ctx context.Context, execution *domain.FulfillmentExecution) error
}

type PaymentRiskFlagQuery struct {
	UserID     string
	OutTradeNo string
	Provider   string
	Reason     string
	Severity   string
	Status     string
	Limit      int
	Offset     int
}

// PaymentRiskFlagRepository defines data access for payment-risk audit flags.
type PaymentRiskFlagRepository interface {
	Create(ctx context.Context, flag *domain.PaymentRiskFlag) error
	GetByID(ctx context.Context, id string) (*domain.PaymentRiskFlag, error)
	GetByProviderEventID(ctx context.Context, provider string, providerEventID string) (*domain.PaymentRiskFlag, error)
	List(ctx context.Context, query PaymentRiskFlagQuery) ([]domain.PaymentRiskFlag, error)
	Update(ctx context.Context, flag *domain.PaymentRiskFlag) error
}
