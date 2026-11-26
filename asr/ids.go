package asr

import (
	"context"

	maas "github.com/hupeng12345/maas-client-go"
)

// sessionRequestIDs resolves the two identifiers used during the handshake.
//
// X-Api-Request-Id must be a UUID. When the caller supplied an ID through
// maas.WithRequestID and it already is a UUID, both headers share it; otherwise a
// fresh UUID is generated for the protocol while the caller's value is still sent
// as X-Request-ID.
func sessionRequestIDs(ctx context.Context) (context.Context, string, string) {
	ctx, generic := ensureRequestID(ctx)
	apiRequestID := generic
	if !isUUID(apiRequestID) {
		apiRequestID = maas.NewUUID()
	}
	return ctx, apiRequestID, generic
}

// ensureRequestID returns the caller's request ID, generating and storing one
// when absent.
func ensureRequestID(ctx context.Context) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id := maas.RequestIDFromContext(ctx); id != "" {
		return ctx, id
	}
	id := maas.NewRequestID()
	return maas.WithRequestID(ctx, id), id
}

// isUUID reports whether s has the canonical 36-character UUID form.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// clipPayload copies at most n bytes so a large frame is never retained whole.
func clipPayload(b []byte, n int) []byte {
	if len(b) <= n {
		return append([]byte(nil), b...)
	}
	out := make([]byte, n)
	copy(out, b[:n])
	return out
}
