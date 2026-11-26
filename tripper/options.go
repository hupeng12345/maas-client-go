package tripper

import (
	"net/http"
	"net/url"
	"time"

	"github.com/hupeng12345/maas-client-go/logit"
)

// ═══════════════════════════════════════════════════════════════════
// Transport-layer options
// ═══════════════════════════════════════════════════════════════════

// Proxy sets the HTTP proxy URL.
func Proxy(u *url.URL) Option {
	return func(c *config) {
		c.proxyURL = u
	}
}

// TLSInsecure skips TLS certificate verification. Use it in test environments
// only.
func TLSInsecure() Option {
	return func(c *config) {
		c.insecureSkipVerify = true
	}
}

// ConnectTimeout sets the TCP connection timeout.
func ConnectTimeout(d time.Duration) Option {
	return func(c *config) {
		c.connectTimeout = d
	}
}

// TLSHandshakeTimeout sets the TLS handshake timeout.
func TLSHandshakeTimeout(d time.Duration) Option {
	return func(c *config) {
		c.tlsHandshakeTimeout = d
	}
}

// MaxIdleConns sets the maximum number of idle connections.
func MaxIdleConns(n int) Option {
	return func(c *config) {
		c.maxIdleConns = n
	}
}

// IdleConnTimeout sets how long an idle connection is kept alive.
func IdleConnTimeout(d time.Duration) Option {
	return func(c *config) {
		c.idleConnTimeout = d
	}
}

// BaseTransport sets the base transport, typically to pass in a
// pre-configured *http.Transport.
//
// Note: when the argument is an *http.Transport, options such as Proxy and
// TLSInsecure modify it directly. When it is any other RoundTripper, the
// transport-layer options are ignored.
func BaseTransport(rt http.RoundTripper) Option {
	return func(c *config) {
		c.base = rt
	}
}

// ═══════════════════════════════════════════════════════════════════
// Client-layer options
// ═══════════════════════════════════════════════════════════════════

// ClientTimeout sets the overall timeout of the http.Client, including
// reading the response body.
func ClientTimeout(d time.Duration) Option {
	return func(c *config) {
		c.clientTimeout = d
	}
}

// NoRedirect disables automatic redirect following. 3xx responses are
// returned to the caller unchanged.
func NoRedirect() Option {
	return func(c *config) {
		c.noRedirect = true
	}
}

// ═══════════════════════════════════════════════════════════════════
// Middleware-layer options
// ═══════════════════════════════════════════════════════════════════

// Logger enables request/response logging.
//
// The logging middleware sits in the outermost layer and records the
// end-to-end latency, the request method and URL, the status code, and the
// request/response body sizes; at debug level it also logs the full body
// contents. Large response bodies are truncated automatically, with a default
// threshold of 100KB.
func Logger(logger logit.Logger) Option {
	return func(c *config) {
		c.logger = logger
	}
}

// LoggerOpts enables request/response logging with additional options.
//
//	tripper.LoggerOpts(logger, tripper.LogMaxBodySize(64*1024))
func LoggerOpts(logger logit.Logger, opts ...LogOption) Option {
	return func(c *config) {
		c.logger = logger
		c.logOpts = opts
	}
}

// Retry enables request retries.
//
// Default behavior:
//   - Retry on network errors, 5xx, and 429.
//   - Retry only idempotent methods (GET/HEAD/PUT/DELETE/OPTIONS/TRACE).
//   - Exponential backoff plus random jitter: 100ms → 200ms → 400ms... up to
//     5s.
func Retry(maxRetries int) Option {
	return func(c *config) {
		c.maxRetries = maxRetries
	}
}

// RetryOpts enables request retries with additional options.
//
//	tripper.RetryOpts(
//	    tripper.RetryMaxRetries(5),
//	    tripper.RetryExponentialBackoff(200*time.Millisecond, 10*time.Second),
//	)
func RetryOpts(opts ...RetryOption) Option {
	return func(c *config) {
		c.retryOpts = append(c.retryOpts, opts...)
	}
}

// Custom adds custom middleware that wraps outside Logger, that is, in the
// outermost layer.
//
// Use it to inject behavior the built-in middleware does not provide, such as
// circuit breaking or rate limiting.
func Custom(decorators ...func(http.RoundTripper) http.RoundTripper) Option {
	return func(c *config) {
		c.customMW = append(c.customMW, decorators...)
	}
}
