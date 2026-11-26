package maas

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// DoOption customises a single Do call.
type DoOption func(*doOptions)

type doOptions struct {
	streaming bool
}

// Streaming selects the timeout-free transport view.
//
// Use it for responses that are consumed incrementally, such as audio or a long
// downstream stream. It disables the overall request timeout and stops the
// logging middleware from buffering the body.
func Streaming() DoOption {
	return func(o *doOptions) { o.streaming = true }
}

// NewRequest builds a request for path, resolved against BaseURL.
//
// Authentication and request IDs are applied by Do, not here, so callers may
// inspect or replace headers first.
func (c *Client) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.buildURL(path), body)
	if err != nil {
		return nil, &Error{
			Kind:    KindLocal,
			Message: "failed to build request",
			Hint:    "check the URL and request body",
			Op:      "http.NewRequest",
			Err:     err,
		}
	}
	return req, nil
}

// Do sends req and normalises transport failures into *Error.
//
// It injects the Authorization header, a request ID, User-Agent and Accept when
// they are not already set, fires hooks, measures latency, and returns response
// metadata including the request ID.
//
// Do is exported for capability packages and for callers that need an endpoint
// this SDK does not wrap yet.
func (c *Client) Do(req *http.Request, op string, opts ...DoOption) (*http.Response, ResponseMeta, error) {
	if req == nil {
		return nil, ResponseMeta{}, NewLocalError(op, "nil request", "pass a request built with NewRequest")
	}

	var o doOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}

	ctx, reqID := ensureRequestID(req.Context())
	req = req.WithContext(ctx)

	if req.Header.Get(HeaderRequestID) == "" {
		req.Header.Set(HeaderRequestID, reqID)
	}
	if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	c.cfg.Hooks.fireRequest(ctx, RequestEvent{
		Op:           op,
		Method:       req.Method,
		URL:          req.URL.String(),
		RequestID:    reqID,
		APIKeyMasked: maskKey(c.cfg.APIKey),
	})

	client := c.unary
	if o.streaming {
		client = c.stream
	}

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		e := classifyTransportError(op, ctx, err)
		e.RequestID = reqID
		c.cfg.Hooks.fireResponse(ctx, ResponseEvent{
			Op: op, Method: req.Method, URL: req.URL.String(),
			RequestID: reqID, Duration: elapsed, Err: e,
		})
		return nil, ResponseMeta{RequestID: reqID, Duration: elapsed}, e
	}

	// Prefer the ID echoed by the server; fall back to the locally generated one
	// so the caller always has a usable ID.
	respID := RequestIDFromHeader(resp.Header)
	if respID == "" {
		respID = reqID
		resp.Header.Set(HeaderRequestID, respID)
	}

	meta := ResponseMeta{
		RequestID:  respID,
		HTTPStatus: resp.StatusCode,
		Duration:   elapsed,
	}

	c.cfg.Hooks.fireResponse(ctx, ResponseEvent{
		Op: op, Method: req.Method, URL: req.URL.String(),
		Status: resp.StatusCode, RequestID: respID, Duration: elapsed,
	})

	return resp, meta, nil
}

// requestSpec describes one outgoing request inside this package.
type requestSpec struct {
	op            string
	method        string
	url           string
	body          io.Reader
	contentType   string
	contentLength int64
	headers       http.Header
	streaming     bool
	accept        string
}

// send is the internal shorthand used by the JSON helpers in this package.
func (c *Client) send(ctx context.Context, spec requestSpec) (*http.Response, ResponseMeta, error) {
	req, err := c.NewRequest(ctx, spec.method, spec.url, spec.body)
	if err != nil {
		return nil, ResponseMeta{}, err
	}
	if spec.contentType != "" {
		req.Header.Set("Content-Type", spec.contentType)
	}
	if spec.contentLength > 0 {
		req.ContentLength = spec.contentLength
	}
	if spec.accept != "" {
		req.Header.Set("Accept", spec.accept)
	}
	for k, vs := range spec.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	var opts []DoOption
	if spec.streaming {
		opts = append(opts, Streaming())
	}
	return c.Do(req, spec.op, opts...)
}

// classifyTransportError maps a net/http error into *Error.
func classifyTransportError(op string, ctx context.Context, err error) *Error {
	base := &Error{
		Kind:      KindTransport,
		Message:   "request failed",
		Retryable: true,
		Op:        op,
		Err:       err,
	}
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		base.Message = "request canceled"
		base.Hint = "the caller canceled the context"
		base.Retryable = false
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || isTimeoutErr(err):
		base.Message = "request timed out"
		base.Hint = "timed out: raise WithTimeout, or use the async endpoint for large files"
		base.Err = errors.Join(ErrTimeout, err)
	default:
		base.Hint = "network failure: check connectivity, proxy and TLS settings"
	}
	return base
}

// isTimeoutErr reports whether err is a network timeout, without depending on a
// concrete error type.
func isTimeoutErr(err error) bool {
	type timeout interface{ Timeout() bool }
	var t timeout
	if errors.As(err, &t) {
		return t.Timeout()
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

// buildURL resolves a path against BaseURL.
func (c *Client) buildURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.cfg.BaseURL + path
}

// URL resolves a path against BaseURL. It is exported for capability packages.
func (c *Client) URL(path string) string { return c.buildURL(path) }

// drainAndClose reads and closes a body so the connection can be reused.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

// maxDownloadBody is a safety cap for streaming downloads.
const maxDownloadBody = 512 << 20

// limitedReader wraps a body and applies the download cap.
func limitedReader(r io.Reader) io.Reader {
	return io.LimitReader(r, maxDownloadBody)
}
