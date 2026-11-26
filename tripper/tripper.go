// Package tripper builds HTTP transports and middleware chains in one place.
//
// A single package covers the full set of HTTP client needs: proxy
// configuration, TLS, timeouts, logging, and retries. Callers never have to
// work out whether a problem belongs to the transport layer or the middleware
// layer; every option sits flat on one config value and the build stage sorts
// them into layers internally.
//
// The recommended middleware order (outermost to innermost) is:
//
//	request → Custom → Logger → Retry → Transport
//
// Example usage:
//
//	// Build a complete http.Client in one call.
//	client := tripper.New(
//	    tripper.Proxy(proxyURL),
//	    tripper.Logger(logger),
//	    tripper.Retry(3),
//	    tripper.ClientTimeout(30*time.Second),
//	)
//	resp, _ := client.Get("https://api.example.com/data")
//
//	// Build only the RoundTripper (for a custom http.Client).
//	rt := tripper.Build(
//	    tripper.Logger(logger),
//	    tripper.Retry(3),
//	)
//
//	// Build only the transport layer (no middleware).
//	transport := tripper.TransportOnly(
//	    tripper.Proxy(proxyURL),
//	    tripper.TLSInsecure(),
//	)
package tripper

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hupeng12345/maas-client-go/logit"
)

// ═══════════════════════════════════════════════════════════════════
// Option — the single configuration entry point
// ═══════════════════════════════════════════════════════════════════

// Option is a tripper configuration function. Every transport-layer and
// middleware option shares this type.
type Option func(*config)

// config collects every option and is split into layers internally at build
// time (transport → middleware → client).
type config struct {
	// ---- Transport layer ----
	base                http.RoundTripper // Base transport; nil means one is created automatically.
	proxyURL            *url.URL
	insecureSkipVerify  bool
	connectTimeout      time.Duration
	tlsHandshakeTimeout time.Duration
	maxIdleConns        int
	idleConnTimeout     time.Duration

	// ---- Client layer ----
	clientTimeout time.Duration
	noRedirect    bool

	// ---- Middleware layer ----
	logger     logit.Logger
	logOpts    []LogOption
	maxRetries int
	retryOpts  []RetryOption
	customMW   []func(http.RoundTripper) http.RoundTripper
}

// ═══════════════════════════════════════════════════════════════════
// Constructors
// ═══════════════════════════════════════════════════════════════════

// Build builds an http.RoundTripper (transport layer plus middleware chain).
//
// The order of the options does not matter; they are assembled in the
// recommended order. With no options, http.DefaultTransport is returned.
func Build(opts ...Option) http.RoundTripper {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	transport := c.buildTransport()
	return c.wrapMiddleware(transport)
}

// New builds a complete *http.Client in one call.
//
// It handles transport creation, middleware wrapping, timeout configuration,
// and the redirect policy automatically. This covers the vast majority of HTTP
// client use cases.
//
//	client := tripper.New(
//	    tripper.Logger(logger),
//	    tripper.Retry(3),
//	    tripper.ClientTimeout(30*time.Second),
//	)
func New(opts ...Option) *http.Client {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	transport := c.buildTransport()
	rt := c.wrapMiddleware(transport)

	client := &http.Client{
		Transport: rt,
		Timeout:   c.clientTimeout,
	}
	if c.noRedirect {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client
}

// TransportOnly builds only the *http.Transport (transport configuration, no
// middleware).
//
// Use it when only proxy, TLS, and connection-pool settings are needed and
// logging, retries, or metrics are not.
//
//	transport := tripper.TransportOnly(
//	    tripper.Proxy(proxyURL),
//	    tripper.TLSInsecure(),
//	)
func TransportOnly(opts ...Option) *http.Transport {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	rt := c.buildTransport()
	if t, ok := rt.(*http.Transport); ok {
		return t
	}
	return defaultTransport()
}

// ═══════════════════════════════════════════════════════════════════
// Internal build logic
// ═══════════════════════════════════════════════════════════════════

// buildTransport creates an http.RoundTripper from the config.
func (c *config) buildTransport() http.RoundTripper {
	// If a base transport was supplied, modify it in place.
	base := defaultTransport()
	if c.base != nil {
		if t, ok := c.base.(*http.Transport); ok {
			base = t
		}
		// Not an *http.Transport: return the RoundTripper as-is and skip transport configuration.
		if _, ok := c.base.(*http.Transport); !ok {
			return c.base
		}
	}

	if c.proxyURL != nil {
		base.Proxy = http.ProxyURL(c.proxyURL)
	}

	if c.insecureSkipVerify {
		if base.TLSClientConfig == nil {
			base.TLSClientConfig = &tls.Config{}
		}
		base.TLSClientConfig.InsecureSkipVerify = true
	}

	if c.connectTimeout > 0 {
		base.DialContext = (&net.Dialer{
			Timeout:   c.connectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}

	if c.tlsHandshakeTimeout > 0 {
		if base.TLSClientConfig == nil {
			base.TLSClientConfig = &tls.Config{}
		}
		base.TLSHandshakeTimeout = c.tlsHandshakeTimeout
	}

	if c.maxIdleConns > 0 {
		base.MaxIdleConns = c.maxIdleConns
	}

	if c.idleConnTimeout > 0 {
		base.IdleConnTimeout = c.idleConnTimeout
	}

	return base
}

// wrapMiddleware wraps the transport with middleware in the recommended order.
//
// Order (built from the inside out):
//
//	Transport → Retry → Logger → Custom
func (c *config) wrapMiddleware(transport http.RoundTripper) http.RoundTripper {
	rt := transport

	// Retry
	if c.maxRetries > 0 || len(c.retryOpts) > 0 {
		opts := c.retryOpts
		if c.maxRetries > 0 {
			opts = append([]RetryOption{RetryMaxRetries(c.maxRetries)}, opts...)
		}
		rt = NewRetryRoundTripper(opts...)(rt)
	}

	// Next layer out: Logger.
	if c.logger != nil {
		rt = NewLogitRoundTripper(c.logger, c.logOpts...)(rt)
	}

	// Outermost layer: Custom.
	for _, decorator := range c.customMW {
		rt = decorator(rt)
	}

	return rt
}

// defaultTransport returns a clone of http.DefaultTransport.
func defaultTransport() *http.Transport {
	return &http.Transport{
		// Explicit over implicit: environment variables such as HTTP_PROXY and
		// HTTPS_PROXY are deliberately ignored.
		// To use a proxy, pass tripper.Proxy(u) explicitly or inject your own
		// *http.Client.
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// ═══════════════════════════════════════════════════════════════════
// Config type (handy for deserializing from a configuration file)
// ═══════════════════════════════════════════════════════════════════

// Config is the complete tripper configuration. The zero value is usable.
// It fits reading settings from a YAML/JSON configuration file.
type Config struct {
	// Transport
	ProxyURL            string        `json:"proxy_url,omitempty"`
	InsecureSkipVerify  bool          `json:"insecure_skip_verify,omitempty"`
	ConnectTimeout      time.Duration `json:"connect_timeout,omitempty"`
	TLSHandshakeTimeout time.Duration `json:"tls_handshake_timeout,omitempty"`
	MaxIdleConns        int           `json:"max_idle_conns,omitempty"`
	IdleConnTimeout     time.Duration `json:"idle_conn_timeout,omitempty"`

	// Client
	ClientTimeout time.Duration `json:"client_timeout,omitempty"`
	NoRedirect    bool          `json:"no_redirect,omitempty"`

	// Middleware
	Logger     logit.Logger `json:"-"`
	MaxRetries int          `json:"max_retries,omitempty"`
}

// FromConfig builds an *http.Client from a Config value.
// It fits reading settings from a configuration file.
func FromConfig(cfg Config) *http.Client {
	var opts []Option
	if cfg.ProxyURL != "" {
		if u, err := url.Parse(cfg.ProxyURL); err == nil {
			opts = append(opts, Proxy(u))
		}
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, TLSInsecure())
	}
	if cfg.ConnectTimeout > 0 {
		opts = append(opts, ConnectTimeout(cfg.ConnectTimeout))
	}
	if cfg.TLSHandshakeTimeout > 0 {
		opts = append(opts, TLSHandshakeTimeout(cfg.TLSHandshakeTimeout))
	}
	if cfg.MaxIdleConns > 0 {
		opts = append(opts, MaxIdleConns(cfg.MaxIdleConns))
	}
	if cfg.IdleConnTimeout > 0 {
		opts = append(opts, IdleConnTimeout(cfg.IdleConnTimeout))
	}
	if cfg.ClientTimeout > 0 {
		opts = append(opts, ClientTimeout(cfg.ClientTimeout))
	}
	if cfg.NoRedirect {
		opts = append(opts, NoRedirect())
	}
	if cfg.Logger != nil {
		opts = append(opts, Logger(cfg.Logger))
	}
	if cfg.MaxRetries > 0 {
		opts = append(opts, Retry(cfg.MaxRetries))
	}
	return New(opts...)
}
