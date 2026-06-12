package sdk

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Client is the main SDK client for interacting with the walnut Billing API.
// It provides offline-tolerant license verification with exponential backoff retry.
type Client struct {
	cfg        Config
	httpClient *http.Client
	// cache stores the last successful verification result.
	cache *VerifyResult
	// cachedAt is the time when the cache was populated.
	cachedAt time.Time
}

// NewClient creates a new walnut billing client.
func NewClient(licenseKey string, opts ...Option) (*Client, error) {
	cfg := defaultConfig(licenseKey)
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.LicenseKey == "" {
		return nil, fmt.Errorf("sdk: license key is required")
	}

	if cfg.DeviceID == "" {
		cfg.DeviceID = defaultDeviceID()
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}, nil
}

// Verify checks the license status against the billing server.
// If the server is unreachable, it falls back to the cached result
// within the offline grace period.
func (c *Client) Verify(ctx context.Context) (*VerifyResult, error) {
	result, err := c.verifyWithRetry(ctx)
	if err != nil {
		// Server error, check offline cache
		return c.verifyOffline()
	}

	// Cache the successful result
	c.cache = result
	c.cachedAt = time.Now()
	return result, nil
}

// Activate binds the current device to the license.
func (c *Client) Activate(ctx context.Context) (*ActivateResult, error) {
	return c.activateWithRetry(ctx)
}

// VerifyAndActivate is a convenience method that verifies the license,
// and if inactive but valid, attempts to activate automatically.
func (c *Client) VerifyAndActivate(ctx context.Context) (*VerifyResult, error) {
	result, err := c.Verify(ctx)
	if err != nil {
		return nil, err
	}

	// If already active or in grace, we're good
	if result.ServerStatus == "active" || result.ServerStatus == "grace" {
		return result, nil
	}

	// If offline, trust the cache
	if result.IsOffline && result.IsValid {
		return result, nil
	}

	// If inactive (valid key but not activated yet), try to activate
	if result.ServerStatus == "inactive" || (result.IsValid && !result.IsOffline) {
		_, actErr := c.Activate(ctx)
		if actErr != nil {
			// Activation failed, but we can still use offline cache if valid
			cached, offErr := c.verifyOffline()
			if offErr == nil && cached.IsValid {
				return cached, nil
			}
			return nil, fmt.Errorf("sdk: activation failed: %w", actErr)
		}

		// Re-verify after activation
		return c.Verify(ctx)
	}

	// Server explicitly says invalid/expired
	return result, nil
}
