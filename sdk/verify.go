package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// verifyRequest is the JSON payload for POST /api/v1/verify
type verifyRequest struct {
	LicenseKey string `json:"license_key"`
	DeviceID   string `json:"device_id"`
}

// verifyWithRetry calls the server's verify endpoint with retries.
func (c *Client) verifyWithRetry(ctx context.Context) (*VerifyResult, error) {
	req := verifyRequest{
		LicenseKey: c.cfg.LicenseKey,
		DeviceID:   c.cfg.DeviceID,
	}

	var resp verifyResponse
	err := c.requestWithRetry(ctx, "POST", "/api/v1/verify", req, &resp)
	if err != nil {
		return nil, err
	}

	result := &VerifyResult{
		IsValid:      resp.Valid,
		ServerStatus: resp.Status,
		Error:        resp.Message,
	}

	if resp.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
			result.ExpiresAt = t
		}
	}

	if !resp.Valid && resp.Message != "" {
		result.Error = resp.Message
	}

	return result, nil
}

// verifyOffline checks the local cache for a valid offline verification.
func (c *Client) verifyOffline() (*VerifyResult, error) {
	if c.cache == nil || c.cachedAt.IsZero() {
		return &VerifyResult{
			IsValid: false,
			Error:   "sdk: no cached verification available",
		}, fmt.Errorf("no cache")
	}

	// Check if we're within the offline grace period
	elapsed := time.Since(c.cachedAt)
	if elapsed > c.cfg.OfflineGracePeriod {
		return &VerifyResult{
			IsValid:   false,
			IsOffline: true,
			Error:     "sdk: offline grace period expired",
		}, fmt.Errorf("grace period expired")
	}

	// Return cached result with offline flag
	return &VerifyResult{
		IsValid:      true, // Trust the last known good state within grace period
		ServerStatus: c.cache.ServerStatus,
		ExpiresAt:    c.cache.ExpiresAt,
		IsOffline:    true,
		Error:        "",
	}, nil
}

// activateRequest is the JSON payload for POST /api/v1/activate
type activateRequest struct {
	LicenseKey string `json:"license_key"`
	DeviceID   string `json:"device_id"`
}

// activateWithRetry calls the server's activate endpoint with retries.
func (c *Client) activateWithRetry(ctx context.Context) (*ActivateResult, error) {
	req := activateRequest{
		LicenseKey: c.cfg.LicenseKey,
		DeviceID:   c.cfg.DeviceID,
	}

	var resp activateResponse
	err := c.requestWithRetry(ctx, "POST", "/api/v1/activate", req, &resp)
	if err != nil {
		// Activation failed, but we still have offline cache
		return &ActivateResult{
			Success:  false,
			Error:    "sdk: activation failed (server unreachable)",
			DeviceID: c.cfg.DeviceID,
		}, err
	}

	result := &ActivateResult{
		Success:  resp.Status == "active",
		Error:    resp.Message,
		DeviceID: resp.DeviceID,
		MaxSeats: resp.MaxSeats,
	}

	return result, nil
}

// MarshalJSON implements json.Marshaler for VerifyResult.
func (r *VerifyResult) MarshalJSON() ([]byte, error) {
	type Alias VerifyResult
	return json.Marshal(&struct {
		*Alias
		CachedAt *time.Time `json:"cached_at,omitempty"`
	}{
		Alias:    (*Alias)(r),
		CachedAt: nil, // Don't expose internal cache time by default
	})
}
