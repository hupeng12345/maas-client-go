package tripper

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/big"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hupeng12345/maas-client-go/logit"
)

// ═══════════════════════════════════════════════════════════════════
// LogitRoundTripper — HTTP request/response logging middleware
// ═══════════════════════════════════════════════════════════════════

const (
	// DefaultMaxBodyLogSize is the maximum size of a request/response body
	// recorded in logs (100KB).
	DefaultMaxBodyLogSize = 100 * 1024

	// RequestIDKey is the name of the log field that holds the request ID.
	RequestIDKey = "req_rel_id"

	// DefaultBinaryHexPreview is the default number of binary body bytes
	// rendered as a hex preview (0 disables the hex preview).
	DefaultBinaryHexPreview = 0

	// binaryPeekSize is how many bytes the heuristic inspects.
	binaryPeekSize = 512

	// binaryPrintableThreshold is the printable-ratio threshold used by the
	// heuristic; anything below it is treated as binary.
	binaryPrintableThreshold = 0.7
)

// LogitRoundTripper provides structured logging for HTTP requests. As the
// outermost middleware it observes the final request state and the total
// latency.
//
// Features:
//   - Generates a unique request ID per request and correlates the start and
//     completion log entries with it.
//   - Truncates large response bodies automatically, avoiding memory pressure
//     and log bloat.
//   - Detects binary bodies and prints a compact summary instead of garbage.
//   - Records request body sizes without logging the full content, so the
//     request stream is never disturbed.
type LogitRoundTripper struct {
	next         http.RoundTripper
	logger       logit.Logger
	maxBodySize  int  // maximum body size recorded in logs (bytes)
	binaryMaxHex int  // hex preview bytes for binary bodies; 0 = summary only
	skipBody     bool // never read the body, log Content-Length only
}

// LogOption configures a LogitRoundTripper.
type LogOption func(*LogitRoundTripper)

// LogMaxBodySize sets the maximum size of a request/response body recorded in
// logs. Bodies above it are truncated and marked with an ellipsis. The default
// is 100KB.
func LogMaxBodySize(size int) LogOption {
	return func(l *LogitRoundTripper) {
		l.maxBodySize = size
	}
}

// LogBinaryHexPreview sets how many bytes of a binary body are shown as a hex
// preview.
//
//	0  = show only [binary: image/png, 24501 bytes] (the default)
//	16 = show the first 16 bytes as hex, for example
//	     [binary: image/png, 24501 bytes, hex: 89 50 4E 47 ...]
func LogBinaryHexPreview(n int) LogOption {
	return func(l *LogitRoundTripper) {
		l.binaryMaxHex = n
	}
}

// LogSkipBody turns body logging off: the body is neither unwrapped nor
// previewed, and only its size from Content-Length is recorded.
//
// It is required for streaming scenarios:
//   - Continuous downstreams such as speech synthesis, where buffering the body
//     would delay the first byte and break real-time playback.
//   - Large file uploads, where copying data just for logging is wasteful.
func LogSkipBody() LogOption {
	return func(l *LogitRoundTripper) {
		l.skipBody = true
	}
}

// ── Sensitive data redaction ─────────────────────────────────
//
// Logs travel through a pipeline that may be retained for a long time and read
// across teams, so credentials and cookies must never reach them. Redaction
// happens centrally inside this middleware instead of relying on callers to
// remember it.

// sensitiveHeaders lists the headers (lowercase) that must be redacted in
// logs.
var sensitiveHeaders = map[string]struct{}{
	"authorization":           {},
	"proxy-authorization":     {},
	"cookie":                  {},
	"set-cookie":              {},
	"x-api-key":               {},
	"x-control-authorization": {},
}

// redactHeaders returns a copy of the headers with sensitive values replaced
// by ***. It never mutates the argument, so the real request is unaffected.
func redactHeaders(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, vs := range h {
		if _, ok := sensitiveHeaders[strings.ToLower(k)]; ok {
			out[k] = []string{"***"}
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// sensitiveQueryKeys lists the URL query keys (lowercase) whose values must be
// redacted.
var sensitiveQueryKeys = map[string]struct{}{
	"authorization": {},
	"api_key":       {},
	"apikey":        {},
	"access_token":  {},
	"token":         {},
}

// safeURL returns a redacted URL string: userinfo is dropped and sensitive
// query parameter values are replaced with ***.
func safeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	cp := *u
	if cp.User != nil {
		cp.User = url.User("***")
	}
	if cp.RawQuery != "" {
		q := cp.Query()
		changed := false
		for k := range q {
			if _, ok := sensitiveQueryKeys[strings.ToLower(k)]; ok {
				q.Set(k, "***")
				changed = true
			}
		}
		if changed {
			cp.RawQuery = q.Encode()
		}
	}
	return cp.String()
}

// NewLogitRoundTripper creates a logging middleware decorator.
func NewLogitRoundTripper(logger logit.Logger, opts ...LogOption) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		rt := &LogitRoundTripper{
			next:         next,
			logger:       logger,
			maxBodySize:  DefaultMaxBodyLogSize,
			binaryMaxHex: DefaultBinaryHexPreview,
		}
		for _, opt := range opts {
			opt(rt)
		}
		return rt
	}
}

// NewLogger is a convenience alias for NewLogitRoundTripper.
func NewLogger(logger logit.Logger, opts ...LogOption) func(http.RoundTripper) http.RoundTripper {
	return NewLogitRoundTripper(logger, opts...)
}

// generateRequestID returns a unique request ID: the hex encoding of 16 random
// bytes.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// isBinaryContent reports whether data should be treated as binary content.
//
// The decision happens in three stages:
//  1. The content type is a known text type → false.
//  2. The content type is a known binary type → true.
//  3. The content type is missing or unknown → byte-level heuristic.
//
// Text allowlist: text/*, application/json, application/xml,
// application/javascript.
// Binary denylist: image/*, audio/*, video/*, application/octet-stream,
// application/protobuf, and the like.
func isBinaryContent(data []byte, contentType string) bool {
	// Parse the media type, dropping parameters such as charset.
	mediatype, _, _ := mime.ParseMediaType(contentType)

	// Rule 1: a known text type.
	if isTextMediaType(mediatype) {
		return false
	}

	// Rule 2: a known binary type.
	if isBinaryMediaType(mediatype) {
		return true
	}

	// Rule 3: the byte-level heuristic.
	return isBinaryByHeuristic(data)
}

// isTextMediaType reports whether the media type is a known text type.
func isTextMediaType(mediatype string) bool {
	if strings.HasPrefix(mediatype, "text/") {
		return true
	}
	switch mediatype {
	case "application/json", "application/xml", "application/javascript",
		"application/x-www-form-urlencoded", "application/x-yaml",
		"application/graphql":
		return true
	}
	return false
}

// isBinaryMediaType reports whether the media type is a known binary type.
func isBinaryMediaType(mediatype string) bool {
	// multipart/* counts as binary: it is a composite format (boundary plus
	// text parts plus file parts), the raw content may hold arbitrary binary
	// data such as audio or images, and the heuristic only inspects the leading
	// boundary and text parts. That makes it far too easy to misclassify the
	// payload as text and dump raw file bytes into the log.
	for _, prefix := range []string{"image/", "audio/", "video/", "multipart/"} {
		if strings.HasPrefix(mediatype, prefix) {
			return true
		}
	}
	switch mediatype {
	case "application/octet-stream", "application/protobuf",
		"application/grpc", "application/grpc+proto",
		"application/zip", "application/gzip", "application/pdf",
		"application/x-protobuf", "application/x-gzip",
		"application/x-msgpack", "application/x-www-form-urlencoded":
		return true
	}
	return false
}

// isBinaryByHeuristic reports whether data looks binary based on its bytes.
//
// It takes the first binaryPeekSize bytes and measures the printable ratio.
// Printable means ASCII 32-126, \t, \n, \r, and valid multi-byte UTF-8
// sequences. A ratio below binaryPrintableThreshold means binary.
func isBinaryByHeuristic(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	peek := data
	if len(peek) > binaryPeekSize {
		peek = peek[:binaryPeekSize]
	}

	printable := 0
	for i := 0; i < len(peek); {
		b := peek[i]
		// ASCII printable characters plus common whitespace.
		if b >= 32 && b <= 126 || b == '\t' || b == '\n' || b == '\r' {
			printable++
			i++
			continue
		}
		// A NUL byte is a strong signal for binary content.
		if b == 0 {
			return true
		}
		// Try a multi-byte UTF-8 sequence.
		r, size := utf8.DecodeRune(peek[i:])
		if r != utf8.RuneError && size > 1 {
			printable += size
			i += size
			continue
		}
		i++
	}

	return float64(printable)/float64(len(peek)) < binaryPrintableThreshold
}

// formatBinaryBody formats a binary body for log output.
//
// hexPreviewLen = 0: [binary: image/png, 24501 bytes]
// hexPreviewLen > 0: [binary: image/png, 24501 bytes, hex: 89 50 4E 47 ...]
func formatBinaryBody(data []byte, contentType string, totalSize int64, hexPreviewLen int) []byte {
	mediatype := contentType
	if mediatype == "" {
		mediatype = "application/octet-stream"
	}

	var buf bytes.Buffer
	_, _ = fmt.Fprintf(&buf, "[binary: %s, %d bytes", mediatype, totalSize)

	if hexPreviewLen > 0 && len(data) > 0 {
		preview := data
		if len(preview) > hexPreviewLen {
			preview = preview[:hexPreviewLen]
		}
		hexStr := hex.EncodeToString(preview)
		// Insert a space between every two hex characters for readability.
		var spaced string
		for i := 0; i < len(hexStr); i += 2 {
			if i > 0 {
				spaced += " "
			}
			spaced += hexStr[i : i+2]
		}
		_, _ = fmt.Fprintf(&buf, ", hex: %s", strings.ToUpper(spaced))
		if len(data) > hexPreviewLen {
			buf.WriteString("...")
		}
	}

	buf.WriteString("]")
	return buf.Bytes()
}

// readAndTruncateBody reads the first maxSize+1 bytes of body for logging and
// returns a restored reader from which the caller can keep reading.
//
// Design principles:
//  1. Read only what logging needs (the leading maxSize+1 bytes), never loading
//     the whole body into memory.
//  2. Do the logging work on a separate copy, so polluting restored is
//     physically impossible.
//  3. For large bodies (> maxSize+1), restored is a zero-copy pass-through made
//     of a prefix replay followed by the live body stream.
//
// Return values:
//   - truncated: the body content for logging, already truncated and formatted.
//   - restored:  an io.ReadCloser the caller can read in full.
//   - total:     the total body size in bytes, or -1 when unknown for large
//     bodies.
func readAndTruncateBody(body io.ReadCloser, maxSize int, contentType string, binaryHexPreview int) (truncated []byte, restored io.ReadCloser, total int64, err error) {
	if body == nil {
		return nil, nil, 0, nil
	}

	// Stage 1: read only the leading maxSize+1 bytes (constant memory,
	// independent of body size).
	var head bytes.Buffer
	head.Grow(maxSize + 1)
	headN, copyErr := io.CopyN(&head, body, int64(maxSize+1))

	if copyErr != nil && copyErr != io.EOF {
		_ = body.Close()
		return nil, nil, 0, copyErr
	}

	consumed := head.Bytes() // len = headN

	if copyErr == io.EOF {
		// Body < maxSize+1: everything was read, the body is exhausted.
		_ = body.Close()
		total = headN
		restored = io.NopCloser(bytes.NewBuffer(consumed))
	} else {
		// copyErr == nil means exactly maxSize+1 bytes were read.
		// Peek one more byte to confirm the body really ended (CopyN returns nil,
		// not io.EOF, once it has filled N bytes).
		var peek [1]byte
		_, peekErr := body.Read(peek[:])
		if peekErr == io.EOF {
			// The body is exactly maxSize+1 bytes.
			_ = body.Close()
			total = int64(len(consumed))
			restored = io.NopCloser(bytes.NewBuffer(consumed))
		} else if peekErr != nil {
			_ = body.Close()
			return nil, nil, 0, peekErr
		} else {
			// Body > maxSize+1: push the peeked byte back.
			_ = head.WriteByte(peek[0]) // Grow guarantees the capacity.
			consumed = head.Bytes()
			total = -1
			restored = &streamingRestored{
				head: bytes.NewReader(consumed),
				body: body,
			}
		}
	}

	// Stage 2: make an independent copy for logging, which nothing can do to
	// affect restored.
	logData := make([]byte, len(consumed))
	copy(logData, consumed)

	// Stage 3: process the logging copy.
	if isBinaryContent(logData, contentType) {
		displaySize := total
		if displaySize < 0 {
			// For streams, use the bytes read so far as a lower bound.
			displaySize = int64(len(logData))
		}
		truncated = formatBinaryBody(logData, contentType, displaySize, binaryHexPreview)
		return truncated, restored, total, nil
	}

	if len(logData) > maxSize {
		if total >= 0 {
			omitted := total - int64(maxSize)
			marker := fmt.Sprintf("... [truncated %d bytes omitted]", omitted)
			truncated = make([]byte, 0, maxSize+len(marker))
			truncated = append(truncated, logData[:maxSize]...)
			truncated = append(truncated, marker...)
		} else {
			marker := "... [truncated, remaining size unknown]"
			truncated = make([]byte, 0, maxSize+len(marker))
			truncated = append(truncated, logData[:maxSize]...)
			truncated = append(truncated, marker...)
		}
	} else {
		truncated = logData
	}

	return truncated, restored, total, nil
}

// streamingRestored replays the already-consumed head bytes first and then
// streams the rest of the body. It is used for large bodies so that the whole
// body never has to be loaded into memory.
type streamingRestored struct {
	head   *bytes.Reader
	body   io.ReadCloser
	closed bool
}

func (r *streamingRestored) Read(p []byte) (int, error) {
	if r.head.Len() > 0 {
		return r.head.Read(p)
	}
	return r.body.Read(p)
}

func (r *streamingRestored) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}

// RoundTrip implements http.RoundTripper.
func (l *LogitRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	ctx := req.Context()
	// Prefer the request ID injected by the caller so that middleware logs and
	// application logs can be correlated by the same ID.
	requestID := req.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = generateRequestID()
	}

	l.logger.Info(ctx, "http request started",
		logit.StringField(RequestIDKey, requestID),
		logit.StringField("method", req.Method),
		logit.StringField("url", safeURL(req.URL)),
		logit.StringField("host", req.URL.Host),
		logit.StringField("path", req.URL.Path),
	)

	var reqBodyForLog []byte
	var reqBodySize int64
	if req.Body != nil {
		if l.skipBody {
			reqBodySize = req.ContentLength
		} else {
			var err error
			var restored io.ReadCloser
			reqContentType := req.Header.Get("Content-Type")
			reqBodyForLog, restored, reqBodySize, err = readAndTruncateBody(req.Body, l.maxBodySize, reqContentType, l.binaryMaxHex)
			if err != nil {
				l.logger.Warn(ctx, "failed to read request body for logging",
					logit.StringField(RequestIDKey, requestID),
					logit.ErrorField("error", err),
				)
			}
			req.Body = restored
		}
	}

	resp, err := l.next.RoundTrip(req)
	duration := time.Since(start)

	if err != nil {
		l.logger.Error(ctx, err,
			logit.StringField(RequestIDKey, requestID),
			logit.StringField("method", req.Method),
			logit.StringField("url", safeURL(req.URL)),
			logit.StringField("host", req.URL.Host),
			logit.StringField("path", req.URL.Path),
			logit.TimeDurationField("duration", duration, logit.WithTimeDurationFormatMethod(logit.TimeFormatMSUnit)),
		)
		return nil, err
	}

	var respBodyForLog []byte
	var respBodySize int64
	if resp.Body != nil {
		if l.skipBody {
			respBodySize = resp.ContentLength
		} else {
			var err error
			var restored io.ReadCloser
			respContentType := resp.Header.Get("Content-Type")
			respBodyForLog, restored, respBodySize, err = readAndTruncateBody(resp.Body, l.maxBodySize, respContentType, l.binaryMaxHex)
			if err != nil {
				l.logger.Warn(ctx, "failed to read response body for logging",
					logit.StringField(RequestIDKey, requestID),
					logit.ErrorField("error", err),
				)
			}
			resp.Body = restored
		}
	}

	l.logger.Info(ctx, "http request completed",
		logit.StringField(RequestIDKey, requestID),
		logit.StringField("method", req.Method),
		logit.StringField("url", safeURL(req.URL)),
		logit.StringField("host", req.URL.Host),
		logit.StringField("path", req.URL.Path),
		logit.IntField("status", resp.StatusCode),
		logit.TimeDurationField("duration", duration, logit.WithTimeDurationFormatMethod(logit.TimeFormatMSUnit)),
		logit.Int64Field("request_body_size", reqBodySize),
		logit.Int64Field("response_body_size", respBodySize),
	)

	l.logger.Debug(ctx, "http request detail",
		logit.StringField(RequestIDKey, requestID),
		logit.StringField("method", req.Method),
		logit.StringField("url", safeURL(req.URL)),
		logit.IntField("status", resp.StatusCode),
		logit.JSONAbsField("request_headers", redactHeaders(req.Header)),
		logit.JSONAbsField("response_headers", redactHeaders(resp.Header)),
		logit.StringField("request_body", string(reqBodyForLog)),
		logit.StringField("response_body", string(respBodyForLog)),
	)

	return resp, nil
}

// ═══════════════════════════════════════════════════════════════════
// RetryRoundTripper — HTTP request retry middleware
// ═══════════════════════════════════════════════════════════════════

const (
	// DefaultMaxRetries is the default maximum number of retries.
	DefaultMaxRetries = 3
	// DefaultBaseDelay is the default base retry delay.
	DefaultBaseDelay = 100 * time.Millisecond
	// DefaultMaxDelay is the default maximum retry delay.
	DefaultMaxDelay = 5 * time.Second
)

// RetryPolicy decides whether a request should be retried; returning true
// retries it.
type RetryPolicy func(resp *http.Response, err error) bool

// BackoffStrategy returns how long to wait before retry number attempt.
// attempt is 1 for the first retry.
type BackoffStrategy func(attempt int) time.Duration

// RetryRoundTripper adds retries to HTTP requests.
type RetryRoundTripper struct {
	next         http.RoundTripper
	maxRetries   int
	backoff      BackoffStrategy
	retryPolicy  RetryPolicy
	retryMethods map[string]bool
}

// RetryOption configures a RetryRoundTripper.
type RetryOption func(*RetryRoundTripper)

// RetryMaxRetries sets the maximum number of retries. The default is 3.
func RetryMaxRetries(n int) RetryOption {
	return func(r *RetryRoundTripper) {
		r.maxRetries = n
	}
}

// RetryBackoff sets a custom backoff strategy.
func RetryBackoff(strategy BackoffStrategy) RetryOption {
	return func(r *RetryRoundTripper) {
		r.backoff = strategy
	}
}

// RetryExponentialBackoff selects exponential backoff with ±25% jitter.
func RetryExponentialBackoff(baseDelay, maxDelay time.Duration) RetryOption {
	return func(r *RetryRoundTripper) {
		r.backoff = func(attempt int) time.Duration {
			delay := baseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
			if delay > maxDelay {
				delay = maxDelay
			}
			jitter := int64(delay) / 4
			if jitter > 0 {
				n, _ := rand.Int(rand.Reader, big.NewInt(jitter*2))
				delay = delay - time.Duration(jitter) + time.Duration(n.Int64())
			}
			return delay
		}
	}
}

// RetryPolicyOption sets a custom retry policy.
func RetryPolicyOption(policy RetryPolicy) RetryOption {
	return func(r *RetryRoundTripper) {
		r.retryPolicy = policy
	}
}

// RetryMethods sets which HTTP methods may be retried. The default is the
// idempotent methods.
func RetryMethods(methods ...string) RetryOption {
	return func(r *RetryRoundTripper) {
		r.retryMethods = make(map[string]bool)
		for _, m := range methods {
			r.retryMethods[m] = true
		}
	}
}

func defaultRetryPolicy(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
		return true
	}
	if resp.StatusCode == 429 {
		return true
	}
	return false
}

var defaultIdempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
	http.MethodOptions: true,
	http.MethodTrace:   true,
}

// NewRetryRoundTripper creates a retry middleware decorator.
func NewRetryRoundTripper(opts ...RetryOption) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		rt := &RetryRoundTripper{
			next:         next,
			maxRetries:   DefaultMaxRetries,
			retryPolicy:  defaultRetryPolicy,
			retryMethods: defaultIdempotentMethods,
		}
		RetryExponentialBackoff(DefaultBaseDelay, DefaultMaxDelay)(rt)
		for _, opt := range opts {
			opt(rt)
		}
		return rt
	}
}

// NewRetry is a convenience alias for NewRetryRoundTripper.
func NewRetry(maxRetries int, opts ...RetryOption) func(http.RoundTripper) http.RoundTripper {
	allOpts := append([]RetryOption{RetryMaxRetries(maxRetries)}, opts...)
	return NewRetryRoundTripper(allOpts...)
}

func (r *RetryRoundTripper) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
	if !r.retryMethods[req.Method] {
		return false
	}
	return r.retryPolicy(resp, err)
}

func drainBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// RoundTrip implements http.RoundTripper.
func (r *RetryRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	// Pass straight through for non-idempotent methods and when retries are
	// disabled. The key point: req.Body is never read here, otherwise a large
	// streaming upload would be buffered entirely in memory, contradicting the
	// design goal that upload memory usage is independent of file size.
	if r.maxRetries <= 0 || !r.retryMethods[req.Method] {
		return r.next.RoundTrip(req)
	}

	var reqBody []byte
	if req.Body != nil {
		var err error
		reqBody, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
	}

	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			delay := r.backoff(attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				drainBody(lastResp)
				return nil, ctx.Err()
			}
		}

		if reqBody != nil {
			req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
		}

		reqCopy := req.Clone(ctx)
		if reqBody != nil {
			reqCopy.Body = io.NopCloser(bytes.NewBuffer(reqBody))
		}

		resp, err := r.next.RoundTrip(reqCopy)

		if !r.shouldRetry(req, resp, err) {
			return resp, err
		}

		lastErr = err
		lastResp = resp
		drainBody(lastResp)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	if lastResp != nil {
		return lastResp, lastErr
	}
	return nil, lastErr
}
