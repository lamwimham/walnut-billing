package sdk

import "time"

const (
	// DefaultOfflineGracePeriod is the default duration the client
	// will allow usage when the server is unreachable.
	DefaultOfflineGracePeriod = 24 * time.Hour

	// DefaultMaxRetries is the default number of retries for network requests.
	DefaultMaxRetries = 3

	// DefaultBaseURL is the default billing server URL.
	DefaultBaseURL = "http://localhost:8082"
)

// Config holds the SDK client configuration.
type Config struct {
	// BaseURL of the billing server.
	BaseURL string

	// LicenseKey is the user's license key.
	LicenseKey string

	// DeviceID is the unique identifier for this machine/device.
	// If empty, the SDK will attempt to generate one.
	DeviceID string

	// OfflineGracePeriod is how long the client remains valid
	// when it cannot reach the server.
	OfflineGracePeriod time.Duration

	// MaxRetries for network requests.
	MaxRetries int

	// Timeout for individual HTTP requests.
	Timeout time.Duration
}

// Option is a functional option for configuring the client.
type Option func(*Config)

// WithBaseURL sets the billing server URL.
func WithBaseURL(url string) Option {
	return func(c *Config) {
		c.BaseURL = url
	}
}

// WithDeviceID sets the device ID.
func WithDeviceID(id string) Option {
	return func(c *Config) {
		c.DeviceID = id
	}
}

// WithOfflineGracePeriod sets the offline grace period duration.
func WithOfflineGracePeriod(d time.Duration) Option {
	return func(c *Config) {
		c.OfflineGracePeriod = d
	}
}

// WithMaxRetries sets the max number of retries.
func WithMaxRetries(n int) Option {
	return func(c *Config) {
		c.MaxRetries = n
	}
}

// WithTimeout sets the HTTP request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.Timeout = d
	}
}

// defaultConfig returns a Config with sensible defaults.
func defaultConfig(licenseKey string) Config {
	return Config{
		BaseURL:            DefaultBaseURL,
		LicenseKey:         licenseKey,
		DeviceID:           "",
		OfflineGracePeriod: DefaultOfflineGracePeriod,
		MaxRetries:         DefaultMaxRetries,
		Timeout:            10 * time.Second,
	}
}
