package domain

import "time"

// Product defines the billing product.
type Product struct {
	Code      string `gorm:"primaryKey;size:32"`
	Name      string `gorm:"size:64"`
	Price     int64  // Amount in cents
	Validity  string `gorm:"size:16"` // lifetime, monthly, yearly
	IsVisible bool   `gorm:"default:true"`
}

// License represents a generated license key.
type License struct {
	ID          uint   `gorm:"primaryKey"`
	Key         string `gorm:"uniqueIndex;size:50"` // e.g. SM-PRO-7738-9921
	PlanCode    string `gorm:"size:32;index"`       // Associated Product code
	Status      string `gorm:"size:16;default:'inactive';index"`
	Validity    string `gorm:"size:16;default:'lifetime'"`
	DeviceID    string `gorm:"size:64;index"` // Bound machine ID
	ActivatedAt *time.Time
	ExpiresAt   *time.Time
	MaxSeats    int `gorm:"default:1"`
}

// Order records a Walnut-owned payment transaction.
// Legacy license orders use LicenseKey; commerce checkout orders use UserID and
// SKUCode and are fulfilled later into entitlement grants and credit ledger rows.
type Order struct {
	ID                 uint    `gorm:"primaryKey"`
	OutTradeNo         string  `gorm:"uniqueIndex;size:64"`
	LicenseKey         string  `gorm:"size:50;index"`
	UserID             string  `gorm:"size:40;index"`
	SKUCode            string  `gorm:"size:64;index"`
	Amount             int64   // Amount in cents
	Currency           string  `gorm:"size:8;default:'CNY'"`
	Status             string  `gorm:"size:24;default:'pending';index"`
	Provider           string  `gorm:"size:32;index"` // wechat, alipay, mock, future hosted checkout providers
	TradeNo            string  `gorm:"size:64"`       // Third-party trade number
	ProviderCheckoutID string  `gorm:"size:128;index"`
	ProviderCustomerID string  `gorm:"size:128;index"`
	CheckoutURL        string  `gorm:"type:text"`
	IdempotencyKey     *string `gorm:"uniqueIndex;size:128"`
	PaidAt             *time.Time
	FulfilledAt        *time.Time
	Metadata           string `gorm:"type:text"`
	OrderType          string `gorm:"size:16;default:'new'"` // new, renewal, checkout
}

// TableName overrides the table name for Order.
func (Order) TableName() string {
	return "orders"
}

const (
	OrderTypeNew      = "new"
	OrderTypeRenewal  = "renewal"
	OrderTypeCheckout = "checkout"
	GracePeriodDays   = 3 // Grace period after expiry
)

const (
	OrderStatusPending         = "pending"
	OrderStatusCheckoutCreated = "checkout_created"
	OrderStatusPaid            = "paid"
	OrderStatusFulfilled       = "fulfilled"
	OrderStatusCancelled       = "cancelled"
	OrderStatusRefunded        = "refunded"
	OrderStatusFailed          = "failed"
)

const (
	PaymentEventStatusReceived   = "received"
	PaymentEventStatusProcessing = "processing"
	PaymentEventStatusProcessed  = "processed"
	PaymentEventStatusIgnored    = "ignored"
	PaymentEventStatusFailed     = "failed"
)

const (
	PaymentEventTypePaid      = "payment.paid"
	PaymentEventTypeCancelled = "payment.cancelled"
	PaymentEventTypeRefunded  = "payment.refunded"
)

// PaymentEventInbox stores verified provider webhook events before processing.
// The composite provider + provider_event_id key makes webhook replay safe and
// keeps provider-specific payloads away from entitlement and credits decisions.
type PaymentEventInbox struct {
	ID                string     `json:"id" gorm:"primaryKey;size:40"`
	Provider          string     `json:"provider" gorm:"size:32;uniqueIndex:idx_payment_event_provider_event;index"`
	ProviderEventID   string     `json:"provider_event_id" gorm:"size:128;uniqueIndex:idx_payment_event_provider_event"`
	EventType         string     `json:"event_type" gorm:"size:64;index"`
	OutTradeNo        string     `json:"out_trade_no" gorm:"size:64;index"`
	ProviderTradeNo   string     `json:"provider_trade_no" gorm:"size:128;index"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency" gorm:"size:8"`
	SignatureVerified bool       `json:"signature_verified" gorm:"default:false"`
	PayloadHash       string     `json:"payload_hash" gorm:"size:64"`
	RawPayload        string     `json:"raw_payload" gorm:"type:text"`
	Status            string     `json:"status" gorm:"size:16;default:'received';index"`
	Attempts          int        `json:"attempts" gorm:"default:0"`
	LastError         string     `json:"last_error" gorm:"type:text"`
	ReceivedAt        time.Time  `json:"received_at" gorm:"index"`
	ProcessedAt       *time.Time `json:"processed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"

	RegistrationStatusPending  = "pending"
	RegistrationStatusApproved = "approved"
	RegistrationStatusRejected = "rejected"

	GrantStatusActive  = "active"
	GrantStatusRevoked = "revoked"
	GrantStatusExpired = "expired"

	GrantSourceManual = "manual"

	EntitlementEditorialStudio = "editorial.studio"
)

// User is the stable identity used by entitlement snapshots.
// Authentication can be introduced later without changing grant ownership.
type User struct {
	ID          string    `json:"id" gorm:"primaryKey;size:40"`
	Email       string    `json:"email" gorm:"uniqueIndex;size:255"`
	DisplayName string    `json:"display_name" gorm:"size:128"`
	Status      string    `json:"status" gorm:"size:16;default:'active';index"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RegistrationRequest records a user's request to unlock a capability.
// It is intentionally separate from grants so approval workflow and access
// control can evolve independently.
type RegistrationRequest struct {
	ID                   string     `json:"id" gorm:"primaryKey;size:40"`
	UserID               string     `json:"user_id" gorm:"size:40;index"`
	Email                string     `json:"email" gorm:"size:255;index"`
	DisplayName          string     `json:"display_name" gorm:"size:128"`
	RequestedEntitlement string     `json:"requested_entitlement" gorm:"size:64;index"`
	Status               string     `json:"status" gorm:"size:16;default:'pending';index"`
	Source               string     `json:"source" gorm:"size:32;index"`
	DeviceID             string     `json:"device_id" gorm:"size:128;index"`
	Note                 string     `json:"note" gorm:"type:text"`
	ReviewNote           string     `json:"review_note" gorm:"type:text"`
	ReviewedBy           string     `json:"reviewed_by" gorm:"size:64"`
	ReviewedAt           *time.Time `json:"reviewed_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// EntitlementGrant is the source of truth for feature access.
// Product names, VIP copy, subscriptions, and credits should project into this
// stable entitlement ID model instead of being checked directly by clients.
type EntitlementGrant struct {
	ID            string     `json:"id" gorm:"primaryKey;size:40"`
	UserID        string     `json:"user_id" gorm:"size:40;index"`
	EntitlementID string     `json:"entitlement_id" gorm:"size:64;index"`
	Status        string     `json:"status" gorm:"size:16;default:'active';index"`
	Source        string     `json:"source" gorm:"size:32;index"`
	StartsAt      time.Time  `json:"starts_at" gorm:"index"`
	ExpiresAt     *time.Time `json:"expires_at" gorm:"index"`
	CreatedBy     string     `json:"created_by" gorm:"size:64"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	RevokedAt     *time.Time `json:"revoked_at"`
}

// EntitlementSnapshotUser is the account projection consumed by app clients.
type EntitlementSnapshotUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

// EntitlementSnapshot matches the PC Core access-control projection contract.
type EntitlementSnapshot struct {
	User         EntitlementSnapshotUser `json:"user"`
	Entitlements map[string]bool         `json:"entitlements"`
	Features     map[string]any          `json:"features"`
	Credits      map[string]int64        `json:"credits"`
	UpdatedAt    string                  `json:"updated_at"`
	Source       string                  `json:"source"`
}

const (
	CreditReservationStatusPending   = "pending"
	CreditReservationStatusCommitted = "committed"
	CreditReservationStatusReleased  = "released"

	CreditTransactionTypeGrant   = "grant"
	CreditTransactionTypeReserve = "reserve"
	CreditTransactionTypeCommit  = "commit"
	CreditTransactionTypeRelease = "release"

	CreditMetricBalance  = "credits.balance"
	CreditMetricReserved = "credits.reserved"
)

// CreditAccount stores the user's available and reserved Walnut Credits.
type CreditAccount struct {
	ID        string    `json:"id" gorm:"primaryKey;size:40"`
	UserID    string    `json:"user_id" gorm:"uniqueIndex;size:40"`
	Balance   int64     `json:"balance" gorm:"default:0"`
	Reserved  int64     `json:"reserved" gorm:"default:0"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreditReservation represents an idempotent pre-deduction for one operation.
type CreditReservation struct {
	ID             string     `json:"id" gorm:"primaryKey;size:40"`
	AccountID      string     `json:"account_id" gorm:"size:40;index"`
	UserID         string     `json:"user_id" gorm:"size:40;index"`
	Operation      string     `json:"operation" gorm:"size:64;index"`
	Amount         int64      `json:"amount"`
	Status         string     `json:"status" gorm:"size:16;index"`
	IdempotencyKey string     `json:"idempotency_key" gorm:"uniqueIndex;size:128"`
	FeatureID      string     `json:"feature_id" gorm:"size:64;index"`
	ExecutionID    string     `json:"execution_id" gorm:"size:128;index"`
	Metadata       string     `json:"metadata" gorm:"type:text"`
	ExpiresAt      *time.Time `json:"expires_at" gorm:"index"`
	CommittedAt    *time.Time `json:"committed_at"`
	ReleasedAt     *time.Time `json:"released_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// CreditTransaction is the immutable ledger for all credit mutations.
type CreditTransaction struct {
	ID             string    `json:"id" gorm:"primaryKey;size:40"`
	AccountID      string    `json:"account_id" gorm:"size:40;index"`
	UserID         string    `json:"user_id" gorm:"size:40;index"`
	ReservationID  string    `json:"reservation_id" gorm:"size:40;index"`
	Type           string    `json:"type" gorm:"size:16;index"`
	Amount         int64     `json:"amount"`
	BalanceAfter   int64     `json:"balance_after"`
	ReservedAfter  int64     `json:"reserved_after"`
	IdempotencyKey string    `json:"idempotency_key" gorm:"uniqueIndex;size:128"`
	Source         string    `json:"source" gorm:"size:32;index"`
	Description    string    `json:"description" gorm:"type:text"`
	CreatedAt      time.Time `json:"created_at"`
}

// UsageRecord is a read model that projects wallet internals into product usage.
// It keeps support/debug views independent from reservation and ledger details.
type UsageRecord struct {
	ReservationID string              `json:"reservation_id"`
	UserID        string              `json:"user_id"`
	FeatureID     string              `json:"feature_id"`
	Operation     string              `json:"operation"`
	ExecutionID   string              `json:"execution_id"`
	Amount        int64               `json:"amount"`
	Status        string              `json:"status"`
	Metadata      map[string]any      `json:"metadata"`
	Transactions  []CreditTransaction `json:"transactions,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	CommittedAt   *time.Time          `json:"committed_at,omitempty"`
	ReleasedAt    *time.Time          `json:"released_at,omitempty"`
}
