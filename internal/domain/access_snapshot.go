package domain

// AccessSnapshotV2 is the signed software-access contract consumed by Walnut
// clients. It intentionally separates software access from AI compute readiness.
type AccessSnapshotV2 struct {
	Version           int                     `json:"version"`
	User              AccessSnapshotUserV2    `json:"user"`
	License           AccessSnapshotLicenseV2 `json:"license"`
	Device            AccessSnapshotDeviceV2  `json:"device"`
	Entitlements      map[string]bool         `json:"entitlements"`
	Features          map[string]any          `json:"features"`
	Credits           map[string]int64        `json:"credits"`
	IssuedAt          string                  `json:"issued_at"`
	ExpiresAt         string                  `json:"expires_at"`
	OfflineGraceUntil string                  `json:"offline_grace_until"`
	Source            string                  `json:"source"`
	Signature         string                  `json:"signature"`
	SignatureKeyID    string                  `json:"signature_key_id,omitempty"`
	SignatureAlg      string                  `json:"signature_algorithm,omitempty"`
}

type AccessSnapshotUserV2 struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	Status        string `json:"status"`
	EmailVerified bool   `json:"email_verified"`
}

type AccessSnapshotLicenseV2 struct {
	State               string `json:"state"`
	Plan                string `json:"plan"`
	AIMode              string `json:"ai_mode"`
	TrialEndsAt         string `json:"trial_ends_at,omitempty"`
	SubscriptionEndsAt  string `json:"subscription_ends_at,omitempty"`
	SubscriptionStatus  string `json:"subscription_status,omitempty"`
	CancelAtPeriodEnd   bool   `json:"cancel_at_period_end,omitempty"`
	CurrentPeriodEndsAt string `json:"current_period_ends_at,omitempty"`
	GraceUntil          string `json:"grace_until,omitempty"`
}

type AccessSnapshotDeviceV2 struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	Status     string `json:"status"`
	MaxDevices int    `json:"max_devices"`
}
