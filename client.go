// Package maas is a Go client for the MaaS (Model as a Service) platform.
//
// It covers three capability families that differ substantially in transport:
//
//   - OCR: HTTP multipart upload with submit-and-poll for async endpoints
//   - TTS: HTTP JSON request streaming raw PCM audio back
//   - ASR: WebSocket session carrying binary frames (gzip payloads)
//
// All three share one core: explicit configuration, a unified error model, a
// pluggable logger, and request-ID propagation that reaches your logs and the
// platform's.
//
// Quick start:
//
//	client, err := maas.New(
//	    maas.WithAPIKey(apiKey), // the SDK never reads environment variables
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	res, err := client.OCR.ParseDocument(ctx, maas.File("contract.pdf"))
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(res.MarkdownContent)
package maas

import (
	"context"
	"net/http"
	"strings"

	"github.com/hupeng12345/maas-client-go/logit"
	"github.com/hupeng12345/maas-client-go/tripper"
)

// Client is the entry point of the MaaS client.
//
// A Client holds an immutable configuration and is safe for concurrent use.
// Capabilities are derived from it: OCR(), TTS() and ASR().
type Client struct {
	cfg    Config
	unary  *http.Client
	stream *http.Client
}

// New builds a Client.
//
// It only validates locally and never performs network I/O. Invalid arguments
// produce a *ConfigError.
//
//	// apiKey and baseURL come from your own configuration source:
//	// the SDK reads no environment variables and embeds no endpoint.
//	client, err := maas.New(
//	    maas.WithAPIKey(apiKey),
//	    maas.WithBaseURL("https://your-endpoint.example.com"),
//	)
func New(opts ...Option) (*Client, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return newClient(cfg)
}

// MustNew is like New but panics on invalid configuration. Useful in main or
// init where a bad configuration should abort startup.
func MustNew(opts ...Option) *Client {
	c, err := New(opts...)
	if err != nil {
		panic(err)
	}
	return c
}

// newClient assembles the transport layer from the configuration.
func newClient(cfg Config) (*Client, error) {
	c := &Client{cfg: cfg}

	if cfg.HTTPClient != nil {
		// The caller owns the transport: both views share the same client.
		c.unary = cfg.HTTPClient
		c.stream = cfg.HTTPClient
		if cfg.HTTPClient.Timeout > 0 {
			cfg.Logger.Warn(context.Background(), "custom http.Client has a global timeout; streaming responses (TTS) may be truncated",
				logit.StringField("timeout", cfg.HTTPClient.Timeout.String()),
				logit.StringField("hint", "use a client without Timeout for streaming, or switch to WithTripperOptions"))
		}
		return c, nil
	}

	// Non-streaming view: overall timeout plus retries (idempotent methods only).
	unaryOpts := []tripper.Option{
		tripper.ClientTimeout(cfg.Timeout),
		tripper.LoggerOpts(cfg.Logger),
	}
	if n := cfg.Retry.MaxAttempts - 1; n > 0 {
		unaryOpts = append(unaryOpts, tripper.Retry(n))
	}
	unaryOpts = append(unaryOpts, cfg.tripperOptions...)
	c.unary = tripper.New(unaryOpts...)

	// Streaming view: no overall timeout (otherwise long audio gets cut off) and
	// no response body pre-reading.
	streamOpts := []tripper.Option{
		tripper.ClientTimeout(0),
		tripper.LoggerOpts(cfg.Logger, tripper.LogSkipBody()),
	}
	streamOpts = append(streamOpts, cfg.tripperOptions...)
	c.stream = tripper.New(streamOpts...)

	return c, nil
}

// BaseURL returns the configured service endpoint, or "" when none was set.
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// RequireBaseURL reports whether an HTTP endpoint is configured.
//
// Capability packages call it before building a request so a missing endpoint
// fails fast with an actionable message instead of a malformed URL.
func (c *Client) RequireBaseURL(op string) error {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return NewLocalError(op, "no endpoint configured", "pass maas.WithBaseURL(...) when building the client")
	}
	return nil
}

// Logger returns the configured logging implementation.
func (c *Client) Logger() logit.Logger { return c.cfg.Logger }

// Config returns a copy of the configuration for troubleshooting.
func (c *Client) Config() Config { return c.cfg }

// PollPolicy returns the configured asynchronous polling policy.
func (c *Client) PollPolicy() PollPolicy { return c.cfg.Poll }

// Hooks returns the configured lifecycle callbacks.
func (c *Client) Hooks() Hooks { return c.cfg.Hooks }

// MaxUploadSize returns the configured local upload cap (0 means unlimited).
func (c *Client) MaxUploadSize() int64 { return c.cfg.MaxUploadSize }

// KeepRaw reports whether raw JSON is retained on results.
func (c *Client) KeepRaw() bool { return c.cfg.KeepRaw }

// AuthHeaders returns the default authentication headers for this client.
//
// Capability packages use it for protocols that cannot share the HTTP transport,
// such as the ASR WebSocket handshake. Callers may override the value with their
// own credentials.
func (c *Client) AuthHeaders() http.Header {
	h := http.Header{}
	if c.cfg.APIKey != "" {
		h.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	return h
}
