package sdk

import "time"

// VerifyResult contains the outcome of a license verification.
type VerifyResult struct {
	// IsValid indicates whether the license is considered valid by the SDK.
	// It is true if the server returned a valid status, OR if offline grace
	// fallback is triggered and the local cache hasn't expired.
	IsValid bool

	// ServerStatus is the raw status from the server ("active", "expired", etc.).
	// Empty if offline fallback was used.
	ServerStatus string

	// ExpiresAt is the license expiry time from the server.
	// Zero time if offline fallback was used.
	ExpiresAt time.Time

	// IsOffline is true when the result came from local cache due to
	// network/server failure (offline grace period).
	IsOffline bool

	// Error is populated if verification failed (e.g., invalid license,
	// expired grace period, or unexpected server error).
	Error string
}

// ActivateResult contains the outcome of a license activation.
type ActivateResult struct {
	Success bool
	Error   string
	// DeviceID is the device bound to the license.
	DeviceID string
	// MaxSeats is the maximum allowed seats for this license.
	MaxSeats int
}

// verifyResponse is the server-side response for POST /api/v1/verify
type verifyResponse struct {
	Status    string `json:"status"`
	Valid     bool   `json:"valid"`
	Message   string `json:"message,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	MaxSeats  int    `json:"max_seats,omitempty"`
}

// activateResponse is the server-side response for POST /api/v1/activate
type activateResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	MaxSeats int    `json:"max_seats,omitempty"`
}
