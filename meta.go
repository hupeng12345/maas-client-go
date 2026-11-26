package maas

import (
	"net/http"
	"time"
)

// ResponseMeta carries metadata about a single request.
//
// It is embedded in every capability result (OCR results, TTS audio, ASR
// session results) so callers receive the business data and the request ID in
// one place, ready for trace correlation and support tickets.
type ResponseMeta struct {
	// RequestID is the final correlation ID of this request, taken from the
	// response header. Quote it when contacting the platform: it maps directly
	// to gateway and upstream logs.
	RequestID string
	// HTTPStatus is the HTTP status code (for ASR, the handshake status).
	HTTPStatus int
	// Duration is the time from sending the request until the response headers
	// (or the first ASR frame) arrived.
	Duration time.Duration
}

// RequestID reads the request ID from response headers.
//
// This is only needed when working with the low-level HTTP layer; normal
// results already embed a ResponseMeta.
func RequestID(resp *http.Response) string {
	if resp == nil {
		return ""
	}
	return RequestIDFromHeader(resp.Header)
}

// String renders the metadata for logs.
func (m ResponseMeta) String() string {
	if m.RequestID == "" {
		return ""
	}
	return "request_id=" + m.RequestID
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
