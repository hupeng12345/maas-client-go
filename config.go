package maas

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hupeng12345/maas-client-go/logit"
	"github.com/hupeng12345/maas-client-go/tripper"
)

// SDK constants.
const (
	// Version is the SDK version, aligned with git tags.
	Version = "0.1.0"

	// DefaultTimeout is the overall timeout for a single non-streaming request.
	DefaultTimeout = 60 * time.Second

	// DefaultMaxUploadSize is a local pre-check threshold that only catches
	// obviously oversized uploads.
	DefaultMaxUploadSize int64 = 100 << 20

	// DefaultUserAgentPrefix is the User-Agent prefix.
	DefaultUserAgentPrefix = "maas-client-go/"
)

// RetryPolicy controls retry behaviour.
//
// The SDK only retries idempotent requests (GET result queries). Submission
// POSTs are not retried by default because the platform has no idempotency key:
// a retry would create a duplicate task and be billed twice.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts including the first.
	// 1 disables retries.
	MaxAttempts int
	// BaseDelay is the initial backoff.
	BaseDelay time.Duration
	// MaxDelay caps a single backoff.
	MaxDelay time.Duration
	// Jitter is the jitter ratio; 0.2 means ±20%.
	Jitter float64
}

// DefaultRetryPolicy returns the default retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   300 * time.Millisecond,
		MaxDelay:    3 * time.Second,
		Jitter:      0.2,
	}
}

// PollPolicy controls the polling cadence for asynchronous tasks.
type PollPolicy struct {
	// InitialDelay is the wait before the first poll.
	InitialDelay time.Duration
	// MaxDelay caps the interval between two polls.
	MaxDelay time.Duration
	// Multiplier is the backoff factor.
	Multiplier float64
	// Jitter is the jitter ratio.
	Jitter float64
	// Timeout is the total time to wait for completion.
	Timeout time.Duration
	// QueryTimeout is the timeout of a single poll.
	QueryTimeout time.Duration
}

// DefaultPollPolicy returns the default polling policy.
func DefaultPollPolicy() PollPolicy {
	return PollPolicy{
		InitialDelay: 800 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   1.5,
		Jitter:       0.2,
		Timeout:      5 * time.Minute,
		QueryTimeout: 30 * time.Second,
	}
}

// Config is the complete client configuration. The zero value is not usable;
// build one through Option values instead.
type Config struct {
	// BaseURL is the service endpoint (required).
	//
	// The SDK embeds no endpoint: every deployment address must come from the
	// caller, either here or per call where a capability allows an override.
	BaseURL string
	// APIKey is the platform credential.
	APIKey string
	// HTTPClient replaces the transport entirely. Once set, the built-in
	// tripper chain (logging and retries) is no longer used.
	HTTPClient *http.Client
	// Timeout is the overall timeout for a single non-streaming request.
	Timeout time.Duration
	// Retry is the retry policy.
	Retry RetryPolicy
	// Poll is the asynchronous polling policy.
	Poll PollPolicy
	// MaxUploadSize caps local file size; 0 disables the check.
	MaxUploadSize int64
	// UserAgent is the User-Agent suffix.
	UserAgent string
	// Logger is the logging implementation; nil is replaced by a silent logger.
	Logger logit.Logger
	// Hooks are the lifecycle callbacks.
	Hooks Hooks
	// KeepRaw controls whether raw JSON is retained on results.
	KeepRaw bool

	tripperOptions    []tripper.Option
	tripperOptionsSet bool
}

// defaultConfig returns a usable zero-configuration baseline.
func defaultConfig() Config {
	return Config{
		Timeout:       DefaultTimeout,
		Retry:         DefaultRetryPolicy(),
		Poll:          DefaultPollPolicy(),
		MaxUploadSize: DefaultMaxUploadSize,
		UserAgent:     DefaultUserAgentPrefix + Version,
		Logger:        logit.Discard(),
	}
}

// normalize validates and canonicalises the configuration.
func (c *Config) normalize() error {
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.APIKey == "" {
		return &ConfigError{Field: "APIKey", Reason: "must not be empty; pass it with WithAPIKey"}
	}

	c.BaseURL = strings.TrimSpace(c.BaseURL)
	if c.BaseURL == "" {
		return &ConfigError{Field: "BaseURL", Reason: "must not be empty; pass your deployment domain with WithBaseURL"}
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return &ConfigError{Field: "BaseURL", Reason: fmt.Sprintf("cannot parse: %v", err)}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &ConfigError{Field: "BaseURL", Reason: "scheme must be http or https"}
	}
	if u.Host == "" {
		return &ConfigError{Field: "BaseURL", Reason: "missing host"}
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")

	if c.Timeout <= 0 {
		return &ConfigError{Field: "Timeout", Reason: "must be greater than 0"}
	}
	if c.Retry.MaxAttempts < 1 {
		return &ConfigError{Field: "Retry.MaxAttempts", Reason: "must be at least 1"}
	}
	if c.Retry.Jitter < 0 || c.Retry.Jitter > 1 {
		return &ConfigError{Field: "Retry.Jitter", Reason: "must be within [0,1]"}
	}
	if c.Poll.InitialDelay <= 0 || c.Poll.MaxDelay <= 0 || c.Poll.Timeout <= 0 || c.Poll.QueryTimeout <= 0 {
		return &ConfigError{Field: "Poll", Reason: "durations must be greater than 0"}
	}
	if c.Poll.Multiplier < 1 {
		return &ConfigError{Field: "Poll.Multiplier", Reason: "must be >= 1"}
	}
	if c.MaxUploadSize < 0 {
		return &ConfigError{Field: "MaxUploadSize", Reason: "must not be negative"}
	}
	if c.Logger == nil {
		c.Logger = logit.Discard()
	}
	if c.HTTPClient != nil && c.tripperOptionsSet {
		return &ConfigError{
			Field:  "HTTPClient",
			Reason: "mutually exclusive with WithTripperOptions: once you own the http.Client, you own the middleware chain",
		}
	}
	return nil
}
