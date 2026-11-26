package maas

import (
	"net/http"
	"time"

	"github.com/hupeng12345/maas-client-go/logit"
	"github.com/hupeng12345/maas-client-go/tripper"
)

// Option configures a Client.
//
// Everything must be passed explicitly: the SDK does not read environment
// variables or configuration files, which keeps behaviour identical across
// machines.
type Option func(*Config) error

// WithAPIKey sets the platform API key (required).
func WithAPIKey(key string) Option {
	return func(c *Config) error {
		c.APIKey = key
		return nil
	}
}

// WithBaseURL sets the service endpoint (required).
//
// There is no default: the SDK never embeds a deployment address.
//
// It does not affect ASR, whose endpoint comes from asr.Config.URL.
func WithBaseURL(rawURL string) Option {
	return func(c *Config) error {
		c.BaseURL = rawURL
		return nil
	}
}

// WithHTTPClient takes over the transport entirely.
//
// After this, the SDK no longer builds its tripper chain (logging and retries
// become your responsibility), and it cannot be combined with
// WithTripperOptions.
//
// Typical uses: corporate proxies, mTLS, OpenTelemetry instrumentation, packet
// capture, or mocks in tests.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Config) error {
		c.HTTPClient = hc
		return nil
	}
}

// WithTimeout sets the overall timeout for non-streaming requests.
//
// It does not apply to ASR sessions or to the streaming TTS download.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) error {
		c.Timeout = d
		return nil
	}
}

// WithRetryPolicy sets the retry policy.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *Config) error {
		c.Retry = p
		return nil
	}
}

// WithoutRetry disables retries.
func WithoutRetry() Option {
	return func(c *Config) error {
		c.Retry = RetryPolicy{MaxAttempts: 1}
		return nil
	}
}

// WithPollPolicy sets the asynchronous polling policy.
func WithPollPolicy(p PollPolicy) Option {
	return func(c *Config) error {
		c.Poll = p
		return nil
	}
}

// WithMaxUploadSize caps local file size; 0 disables the check.
func WithMaxUploadSize(n int64) Option {
	return func(c *Config) error {
		c.MaxUploadSize = n
		return nil
	}
}

// WithUserAgent sets the User-Agent.
func WithUserAgent(ua string) Option {
	return func(c *Config) error {
		c.UserAgent = ua
		return nil
	}
}

// WithLogger sets the logging implementation.
//
// The default is a silent logger writing to io.Discard. To see logs:
//
//	maas.WithLogger(logit.NewSimple(os.Stderr))
func WithLogger(l logit.Logger) Option {
	return func(c *Config) error {
		c.Logger = l
		return nil
	}
}

// WithHooks sets the lifecycle callbacks.
func WithHooks(h Hooks) Option {
	return func(c *Config) error {
		c.Hooks = h
		return nil
	}
}

// WithRawResult controls whether raw server JSON is retained on results.
// It is off by default to save memory.
func WithRawResult(keep bool) Option {
	return func(c *Config) error {
		c.KeepRaw = keep
		return nil
	}
}

// WithTripperOptions appends middleware options to the built-in transport chain.
//
// Use it to override logging or retry behaviour. It is mutually exclusive with
// WithHTTPClient.
func WithTripperOptions(opts ...tripper.Option) Option {
	return func(c *Config) error {
		c.tripperOptions = append(c.tripperOptions, opts...)
		c.tripperOptionsSet = true
		return nil
	}
}
