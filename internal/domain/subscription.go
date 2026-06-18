package domain

import "time"

const (
	SubscriptionStatusCancelAtPeriodEnd = "cancelled_at_period_end"
	SubscriptionStatusActive            = "active"
)

// SubscriptionCancellation records a Walnut-owned subscription control fact.
// Payment/fulfillment orders remain immutable facts; cancellation controls future renewal only.
type SubscriptionCancellation struct {
	ID                   string     `json:"id" gorm:"primaryKey;size:40"`
	UserID               string     `json:"user_id" gorm:"size:40;index"`
	SKUCode              string     `json:"sku_code" gorm:"size:64;index"`
	Status               string     `json:"status" gorm:"size:32;index"`
	CancelAtPeriodEnd    bool       `json:"cancel_at_period_end" gorm:"default:true"`
	CurrentPeriodEndsAt  time.Time  `json:"current_period_ends_at" gorm:"index"`
	SourceOrderNo        string     `json:"source_order_no" gorm:"size:64;index"`
	Reason               string     `json:"reason" gorm:"size:128"`
	Source               string     `json:"source" gorm:"size:32;index"`
	IdempotencyKey       string     `json:"idempotency_key" gorm:"uniqueIndex;size:160"`
	ResumeIdempotencyKey string     `json:"resume_idempotency_key" gorm:"size:160;index"`
	ResumedAt            *time.Time `json:"resumed_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}
