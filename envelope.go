package maas

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// defaultMaxResponseBytes is the parse limit for a single response body.
//
// Document parsing results can be large (full markdown plus per-page layout),
// so the limit is 64 MiB; exceeding it returns a clear error rather than
// silently truncating.
const defaultMaxResponseBytes int64 = 64 << 20

// Envelope is the standard gateway response envelope.
//
// Capability packages decode it through DecodeEnvelope; application code normally
// never needs it because results embed the decoded payload.
//
// Only this shape is accepted: a six-digit string code, a string message, and
// arbitrary JSON data. Anything else is a protocol error (see
// ErrNonStandardResponse in errors.go).
type Envelope struct {
	Code    string
	Message string
	Data    json.RawMessage
}

// IsSuccess reports whether the business code is the success code.
func (e *Envelope) IsSuccess() bool { return e.Code == CodeSuccess }

// rawEnvelope validates field types during parsing so encoding/json never
// coerces values silently (for example turning the number 200 into a string).
type rawEnvelope struct {
	Code    json.RawMessage `json:"code"`
	Message json.RawMessage `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// parseEnvelope reads and parses one HTTP response.
//
// A non-nil envelope only means the body matched the standard envelope; business
// failures are decided by the caller from envelope.Code. When the body does not
// match, an *Error with Kind KindProtocol is returned.
func parseEnvelope(op string, resp *http.Response, limit int64) (*Envelope, error) {
	if limit <= 0 {
		limit = defaultMaxResponseBytes
	}
	body, err := readLimited(resp.Body, limit)
	if err != nil {
		if err == errResponseTooLarge {
			return nil, &Error{
				Kind:       KindProtocol,
				Message:    "response body exceeds the parse limit",
				Hint:       "response too large to parse; contact the platform for very large documents",
				HTTPStatus: resp.StatusCode,
				Op:         op,
				RequestID:  RequestIDFromHeader(resp.Header),
			}
		}
		e := newTransportError(op, err)
		e.HTTPStatus = resp.StatusCode
		e.RequestID = RequestIDFromHeader(resp.Header)
		return nil, e
	}

	if len(bytes.TrimSpace(body)) == 0 {
		e := NewProtocolError(op, "empty response body", resp.StatusCode, body)
		e.RequestID = RequestIDFromHeader(resp.Header)
		return nil, e
	}

	var raw rawEnvelope
	if err := json.Unmarshal(body, &raw); err != nil {
		e := NewProtocolError(op, "response is not a JSON object", resp.StatusCode, body)
		e.RequestID = RequestIDFromHeader(resp.Header)
		return nil, e
	}

	// code must be a string (numbers are rejected) and six digits long.
	code, ok := asJSONString(raw.Code)
	if !ok || !isSixDigitCode(code) {
		e := NewProtocolError(op, "response code is missing or not a six-digit string", resp.StatusCode, body)
		e.RequestID = RequestIDFromHeader(resp.Header)
		return nil, e
	}

	// message may be absent or null; when present it must be a string.
	msg, ok := asJSONString(raw.Message)
	if !ok {
		e := NewProtocolError(op, "response message is not a string", resp.StatusCode, body)
		e.RequestID = RequestIDFromHeader(resp.Header)
		return nil, e
	}

	return &Envelope{Code: code, Message: msg, Data: raw.Data}, nil
}

// asJSONString decodes a JSON value as a string.
//
// Absent and null yield ("", true); strings yield their value; anything else
// (numbers, objects, ...) yields false.
func asJSONString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return "", false
	}
	return s, true
}

// isSixDigitCode reports whether code is exactly six ASCII digits.
func isSixDigitCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// Error converts a failed business envelope into *Error.
//
// It is exported for capability packages; pass the response so status code and
// retry headers are preserved.
func (e *Envelope) Error(op string, resp *http.Response) *Error {
	out := NewAPIError(op, e.Code, e.Message, resp.StatusCode)
	out.RequestID = RequestIDFromHeader(resp.Header)
	out.RetryAfter = parseRetryAfter(resp.Header)
	if b, err := json.Marshal(e.Data); err == nil && len(b) > 0 && !bytes.Equal(b, []byte("null")) {
		out.RawBody = clip(b, 4096)
	}
	return out
}

// errResponseTooLarge indicates the response body exceeded the parse limit.
var errResponseTooLarge = &Error{Kind: KindProtocol, Message: "response body too large"}

// DecodeEnvelope reads and validates one response envelope.
//
// It is exported for capability packages. A nil error only means the body matched
// the standard envelope; business failures are reported by IsSuccess.
func DecodeEnvelope(op string, resp *http.Response) (*Envelope, error) {
	return parseEnvelope(op, resp, 0)
}

// readLimited reads the whole body, failing when it exceeds limit bytes.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	buf, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, errResponseTooLarge
	}
	return buf, nil
}
