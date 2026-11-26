package maas

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Kind classifies errors so callers can branch on category instead of strings.
type Kind uint8

const (
	// KindUnknown is an unclassified error.
	KindUnknown Kind = iota
	// KindConfig is a local configuration error raised while building a Client.
	KindConfig
	// KindLocal is a local validation failure (missing file, oversized payload,
	// missing required parameter). The request was never sent.
	KindLocal
	// KindTransport is a network failure: DNS, TCP, TLS or connect timeout.
	KindTransport
	// KindProtocol means the response did not match the protocol: not JSON,
	// missing envelope fields, malformed frame.
	KindProtocol
	// KindAPI means the platform returned a business error code.
	KindAPI
	// KindTask means an asynchronous task reached a failed terminal state
	// (FAILED / CANCELED).
	KindTask
)

// String returns a readable name for the kind.
func (k Kind) String() string {
	switch k {
	case KindConfig:
		return "config"
	case KindLocal:
		return "local"
	case KindTransport:
		return "transport"
	case KindProtocol:
		return "protocol"
	case KindAPI:
		return "api"
	case KindTask:
		return "task"
	default:
		return "unknown"
	}
}

// Error is the SDK's unified error type.
//
// It carries four layers of information so callers can handle failures at the
// right level:
//   - attribution: Kind / Code / HTTPStatus
//   - troubleshooting: RequestID / TaskUUID / RawBody / Op
//   - decision: Retryable / RetryAfter
//   - presentation: Message (platform-provided) / Hint (SDK-provided next step)
type Error struct {
	Kind       Kind
	Code       string
	Message    string
	Hint       string
	HTTPStatus int
	RequestID  string
	TaskUUID   string
	RetryAfter time.Duration
	Retryable  bool
	Op         string
	RawBody    []byte
	Err        error
}

// Error implements the error interface.
//
// Example output:
//
//	maas: ocr.ParseDocument: [402001] insufficient balance (HTTP 402, request_id=req-x)
//	  hint: account balance is insufficient; top up or check postpaid credit
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	var sb strings.Builder
	sb.WriteString("maas")
	if e.Op != "" {
		sb.WriteString(": ")
		sb.WriteString(e.Op)
	}
	if e.Code != "" {
		sb.WriteString(": [")
		sb.WriteString(e.Code)
		sb.WriteByte(']')
		if e.Message != "" {
			sb.WriteByte(' ')
			sb.WriteString(e.Message)
		}
	} else if e.Message != "" {
		sb.WriteString(": ")
		sb.WriteString(e.Message)
	}
	if e.HTTPStatus != 0 {
		fmt.Fprintf(&sb, " (HTTP %d", e.HTTPStatus)
		if e.RequestID != "" {
			sb.WriteString(", request_id=")
			sb.WriteString(e.RequestID)
		}
		sb.WriteByte(')')
	} else if e.RequestID != "" {
		sb.WriteString(" (request_id=")
		sb.WriteString(e.RequestID)
		sb.WriteByte(')')
	}
	if e.TaskUUID != "" {
		sb.WriteString(" (task=")
		sb.WriteString(e.TaskUUID)
		sb.WriteByte(')')
	}
	if e.Hint != "" {
		sb.WriteString("\n  hint: ")
		sb.WriteString(e.Hint)
	}
	if e.Err != nil {
		sb.WriteString("\n  cause: ")
		sb.WriteString(e.Err.Error())
	}
	return sb.String()
}

// Unwrap exposes the root cause so errors.Is / errors.As keep working.
func (e *Error) Unwrap() error { return e.Err }

// Is lets errors.Is(err, ErrAuth) style checks work.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrAuth:
		return e.Code == CodeMissingAPIKey || e.Code == CodeInvalidAPIKey ||
			e.Code == CodeMissingControlAuth || e.Code == CodeInvalidControlAuth
	case ErrForbidden:
		return e.Code == CodeAccessDenied || e.Code == CodeIPNotAllowed
	case ErrInsufficient:
		return e.Code == CodeInsufficientBalance
	case ErrRateLimited:
		return e.Code == CodeRateLimited
	case ErrNotFound:
		return e.Code == CodeNoRoute || e.Code == CodeModelNotFound ||
			e.Code == CodeResourceNotFound || e.Code == CodeTaskNotFound
	case ErrTooLarge:
		return e.Code == CodeBodyTooLarge || e.Code == CodeFileTooLarge ||
			e.Code == CodeSyncFileTooLarge
	case ErrUnsupported:
		return e.Code == CodeUnsupportedFileType
	case ErrTimeout:
		return e.Code == CodeBackendTimeout ||
			errors.Is(e.Err, context.DeadlineExceeded)
	case ErrTaskFailed:
		return e.Kind == KindTask
	case ErrNonStandardResponse:
		return e.Kind == KindProtocol
	}
	return false
}

// Sentinel errors, used together with errors.Is and the IsXxx helpers.
var (
	// ErrAuth means the API key is missing, invalid or expired.
	ErrAuth = errors.New("maas: authentication failed")
	// ErrForbidden means the key is valid but has no access to the resource.
	ErrForbidden = errors.New("maas: access denied")
	// ErrInsufficient means the balance is too low and postpaid is not allowed.
	ErrInsufficient = errors.New("maas: insufficient balance")
	// ErrRateLimited means the request was throttled.
	ErrRateLimited = errors.New("maas: rate limited")
	// ErrNotFound means the path, model, task or resource does not exist.
	ErrNotFound = errors.New("maas: not found")
	// ErrTooLarge means the request body or file exceeds the limit.
	ErrTooLarge = errors.New("maas: payload too large")
	// ErrUnsupported means the file type is not supported.
	ErrUnsupported = errors.New("maas: unsupported file type")
	// ErrTimeout means the upstream timed out.
	ErrTimeout = errors.New("maas: deadline exceeded")
	// ErrTaskFailed means an asynchronous task failed.
	ErrTaskFailed = errors.New("maas: task failed")
	// ErrNonStandardResponse means the response is not the gateway envelope.
	ErrNonStandardResponse = errors.New("maas: non-standard response")
	// ErrConfig means the construction parameters are invalid.
	ErrConfig = errors.New("maas: invalid configuration")
)

// Predicates. All are based on errors.Is and therefore work through wrapping.
func IsAuth(err error) bool         { return errors.Is(err, ErrAuth) }
func IsForbidden(err error) bool    { return errors.Is(err, ErrForbidden) }
func IsInsufficient(err error) bool { return errors.Is(err, ErrInsufficient) }
func IsRateLimited(err error) bool  { return errors.Is(err, ErrRateLimited) }
func IsNotFound(err error) bool     { return errors.Is(err, ErrNotFound) }
func IsTooLarge(err error) bool     { return errors.Is(err, ErrTooLarge) }
func IsUnsupported(err error) bool  { return errors.Is(err, ErrUnsupported) }
func IsTimeout(err error) bool      { return errors.Is(err, ErrTimeout) }
func IsTaskFailed(err error) bool   { return errors.Is(err, ErrTaskFailed) }

// IsNonStandardResponse reports whether the response was not a gateway envelope.
func IsNonStandardResponse(err error) bool { return errors.Is(err, ErrNonStandardResponse) }

// IsRetryable reports whether the error is worth retrying.
func IsRetryable(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Retryable
	}
	return false
}

// RetryAfter returns the suggested wait time, or 0 when unavailable.
func RetryAfter(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) {
		return e.RetryAfter
	}
	return 0
}

// ConfigError reports an invalid Client configuration.
type ConfigError struct {
	Field  string
	Reason string
}

func (e *ConfigError) Error() string {
	if e.Field == "" {
		return "maas: invalid configuration: " + e.Reason
	}
	return "maas: invalid configuration: " + e.Field + ": " + e.Reason
}

// Is makes errors.Is(err, ErrConfig) work.
func (e *ConfigError) Is(target error) bool { return target == ErrConfig }

// AsConfigError extracts a *ConfigError.
func AsConfigError(err error) (*ConfigError, bool) {
	var ce *ConfigError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// ── Error code catalogue ───────────────────────────────────
//
// Mirrors the public platform error codes. Unlisted codes fall back to
// "API error, retryable only for 5xx".

const (
	CodeSuccess = "000000"

	CodeBadRequest             = "400001"
	CodeUnknownProvider        = "400002"
	CodeUnknownCapability      = "400003"
	CodeCapabilityNotSupported = "400004"

	CodeMissingAPIKey      = "401001"
	CodeInvalidAPIKey      = "401002"
	CodeMissingControlAuth = "401003"
	CodeInvalidControlAuth = "401004"

	CodeInsufficientBalance = "402001"

	CodeAccessDenied             = "403001"
	CodeIPNotAllowed             = "403002"
	CodeHotwordTableNotPermitted = "403003"

	CodeNoRoute          = "404001"
	CodeModelNotFound    = "404002"
	CodeResourceNotFound = "404003"

	CodeBodyTooLarge     = "413001"
	CodeFileTooLarge     = "413002"
	CodeSyncFileTooLarge = "640003"

	CodeRateLimited = "429001"

	CodeInternalError            = "500001"
	CodeBackendUnavailable       = "502001"
	CodeObjectStorageUnavailable = "502002"
	CodeCircuitOpen              = "503001"
	CodeServerOverloaded         = "503002"
	CodeResourceNotReady         = "503003"
	CodeBackendTimeout           = "504001"

	CodeUnsupportedFileType = "640001"
	CodeTaskNotFound        = "640402"
	CodeRequestValidation   = "642203"

	CodeTTSUnavailable = "700000"
)

// codeSpec describes the fixed semantics of one business code.
type codeSpec struct {
	kind      Kind
	retryable bool
	hint      string
}

// codeTable maps business codes to their semantics.
var codeTable = map[string]codeSpec{
	CodeBadRequest:             {KindAPI, false, "Check the request parameters and file format"},
	CodeUnknownProvider:        {KindAPI, false, "Provider is not registered; ask the platform to confirm the model"},
	CodeUnknownCapability:      {KindAPI, false, "Capability not recognised; verify the request path"},
	CodeCapabilityNotSupported: {KindAPI, false, "This provider does not support the capability"},

	CodeMissingAPIKey:      {KindAPI, false, "No API key sent: pass WithAPIKey when building the client"},
	CodeInvalidAPIKey:      {KindAPI, false, "API key is invalid, disabled or expired; check its status in the console"},
	CodeMissingControlAuth: {KindAPI, false, "Missing user session credential"},
	CodeInvalidControlAuth: {KindAPI, false, "User session is invalid or expired; sign in again"},

	CodeInsufficientBalance: {KindAPI, false, "Insufficient balance; top up or confirm postpaid credit in the console"},

	CodeAccessDenied:             {KindAPI, false, "The key is valid but has no access to this resource; ask the platform to enable it"},
	CodeIPNotAllowed:             {KindAPI, false, "Source IP rejected by policy; ask the platform to allowlist it"},
	CodeHotwordTableNotPermitted: {KindAPI, false, "Hotword table is not authorised or disabled; check data.forbidden_table_ids"},

	CodeNoRoute:          {KindAPI, false, "No route matches this path and method; verify BaseURL and the endpoint path"},
	CodeModelNotFound:    {KindAPI, false, "Model is not registered or the alias did not match"},
	CodeResourceNotFound: {KindAPI, false, "No registered resource matched; confirm the model is enabled for the account"},

	CodeBodyTooLarge:     {KindAPI, false, "Request body too large; shrink the file or split the work"},
	CodeFileTooLarge:     {KindAPI, false, "File exceeds the per-file limit"},
	CodeSyncFileTooLarge: {KindAPI, false, "Sync recognition images are capped at 10 MiB; use the async endpoint or compress the image"},

	CodeRateLimited: {KindAPI, true, "Rate limited; back off according to Retry-After and retry"},

	CodeInternalError:            {KindAPI, true, "Internal platform error; retry later"},
	CodeBackendUnavailable:       {KindAPI, true, "Upstream unavailable; retry later"},
	CodeObjectStorageUnavailable: {KindAPI, true, "Object storage unavailable; retry later"},
	CodeCircuitOpen:              {KindAPI, true, "Upstream circuit breaker is open; retry later"},
	CodeServerOverloaded:         {KindAPI, true, "Gateway concurrency is saturated; retry later"},
	CodeResourceNotReady:         {KindAPI, true, "Resource catalogue is not ready; retry later"},
	CodeBackendTimeout:           {KindAPI, true, "Upstream timed out; retry or send a smaller file"},

	CodeUnsupportedFileType: {KindAPI, false, "Unsupported file type; check the supported formats"},
	CodeTaskNotFound:        {KindAPI, false, "task_uuid does not exist or has expired; submit the task again"},
	CodeRequestValidation:   {KindAPI, false, "Validation failed; check form fields and values"},

	CodeTTSUnavailable: {KindAPI, false, "TTS model not published, parameter out of range, or no route for the voice; check model/voice/sample"},
}

// classifyCode returns the semantics for a business code, falling back to the
// HTTP status for unlisted codes.
func classifyCode(code string, httpStatus int) codeSpec {
	if spec, ok := codeTable[code]; ok {
		return spec
	}
	if strings.HasPrefix(code, "6") || strings.HasPrefix(code, "7") {
		return codeSpec{kind: KindAPI, retryable: false, hint: "Business processing failed; check the request or contact the platform"}
	}
	if httpStatus >= 500 {
		return codeSpec{kind: KindAPI, retryable: true, hint: "Server-side error; retry later"}
	}
	return codeSpec{kind: KindAPI, retryable: false, hint: "Request failed; check the parameters and retry"}
}

// NewAPIError builds an *Error from a business code.
func NewAPIError(op, code, message string, httpStatus int) *Error {
	spec := classifyCode(code, httpStatus)
	return &Error{
		Kind:       spec.kind,
		Code:       code,
		Message:    message,
		Hint:       spec.hint,
		HTTPStatus: httpStatus,
		Retryable:  spec.retryable,
		Op:         op,
	}
}

// NewProtocolError builds an error for a response that does not match the protocol.
func NewProtocolError(op, reason string, httpStatus int, raw []byte) *Error {
	return &Error{
		Kind:       KindProtocol,
		Code:       "",
		Message:    reason,
		Hint:       "Response is not a standard gateway envelope: confirm BaseURL and gateway version, then report the request_id",
		HTTPStatus: httpStatus,
		Op:         op,
		RawBody:    clip(raw, 4096),
		Err:        ErrNonStandardResponse,
	}
}

// newTransportError builds a network-layer error.
func newTransportError(op string, err error) *Error {
	return &Error{
		Kind:      KindTransport,
		Message:   "request failed",
		Hint:      "Check network, proxy and timeout settings",
		Retryable: true,
		Op:        op,
		Err:       err,
	}
}

// NewLocalError builds a local validation error.
func NewLocalError(op, reason, hint string) *Error {
	return &Error{
		Kind:    KindLocal,
		Message: reason,
		Hint:    hint,
		Op:      op,
	}
}

// clip returns a copy of the first n bytes of b so a huge body never stays
// attached to an error.
func clip(b []byte, n int) []byte {
	if len(b) <= n {
		return append([]byte(nil), b...)
	}
	out := make([]byte, n)
	copy(out, b[:n])
	return out
}

// parseRetryAfter parses the Retry-After header, accepting either seconds or an
// HTTP date.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	var secs int
	if _, err := fmt.Sscanf(v, "%d", &secs); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
