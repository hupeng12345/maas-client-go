package maas

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

// Request ID headers used by the platform.
const (
	// HeaderRequestID is the generic correlation header sent with every HTTP request.
	HeaderRequestID = "X-Request-ID"
	// HeaderAPIRequestID is the request ID header required by the ASR protocol.
	// Compatible implementations also accept X-Request-ID; the SDK sends both.
	HeaderAPIRequestID = "X-Api-Request-Id"
)

// requestIDKey is the private context key for request IDs, preventing collisions
// with other packages.
type requestIDKey struct{}

// WithRequestID stores a request ID in the context.
//
// Outgoing requests carry this value (X-Request-ID over HTTP, X-Api-Request-Id
// for ASR). The value the server ultimately used is echoed back in response
// headers and surfaced on results and errors, making it easy to correlate with
// platform-side logs.
//
// When unset, the SDK generates one (32 hex characters).
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey{}, strings.TrimSpace(id))
}

// RequestIDFromContext returns the request ID stored in ctx, or "".
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(requestIDKey{}).(string)
	return s
}

// RequestIDFromHeader reads the request ID from response headers.
//
// The gateway always returns x-request-id; http.Header canonicalises header
// names, so casing variants resolve to the same value. X-Api-Request-Id is
// accepted as a fallback for ASR-compatible deployments.
func RequestIDFromHeader(h http.Header) string {
	if h == nil {
		return ""
	}
	if v := strings.TrimSpace(h.Get(HeaderRequestID)); v != "" {
		return v
	}
	return strings.TrimSpace(h.Get(HeaderAPIRequestID))
}

// NewRequestID generates a 32-character hex request ID.
func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failing entropy source only degrades traceability, not correctness.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// NewUUID generates a canonical UUID v4 string, as required by the ASR
// protocol's X-Api-Request-Id header.
func NewUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// requestIDsForASR computes the two IDs required for an ASR handshake.
//
// The ASR protocol requires X-Api-Request-Id to be a UUID. If the caller set a
// value via WithRequestID that is already a UUID, it is reused for both
// headers; otherwise a fresh UUID is generated for the protocol header while
// the caller's value is still sent as X-Request-ID. This keeps "search logs by
// business ID" and "satisfy the protocol" true at the same time.
func requestIDsForASR(ctx context.Context) (context.Context, string, string) {
	ctx, generic := ensureRequestID(ctx)
	apiRequestID := generic
	if !isUUID(apiRequestID) {
		apiRequestID = NewUUID()
	}
	return ctx, apiRequestID, generic
}

// ensureRequestID returns the request ID from ctx, generating and storing one
// when absent.
func ensureRequestID(ctx context.Context) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id := RequestIDFromContext(ctx); id != "" {
		return ctx, id
	}
	id := NewRequestID()
	return WithRequestID(ctx, id), id
}
