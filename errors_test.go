package maas

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClassifyCode(t *testing.T) {
	cases := []struct {
		code      string
		status    int
		kind      Kind
		retryable bool
	}{
		{CodeSuccess, 200, KindAPI, false},
		{CodeBadRequest, 400, KindAPI, false},
		{CodeMissingAPIKey, 401, KindAPI, false},
		{CodeInvalidAPIKey, 401, KindAPI, false},
		{CodeInsufficientBalance, 402, KindAPI, false},
		{CodeRateLimited, 429, KindAPI, true},
		{CodeNoRoute, 404, KindAPI, false},
		{CodeBodyTooLarge, 413, KindAPI, false},
		{CodeInternalError, 500, KindAPI, true},
		{CodeBackendUnavailable, 502, KindAPI, true},
		{CodeBackendTimeout, 504, KindAPI, true},
		{CodeUnsupportedFileType, 400, KindAPI, false},
		{CodeTaskNotFound, 404, KindAPI, false},
		{CodeTTSUnavailable, 422, KindAPI, false},
		{"650099", 500, KindAPI, false},
		{"999999", 503, KindAPI, true},
		{"999999", 400, KindAPI, false},
	}
	for _, c := range cases {
		got := classifyCode(c.code, c.status)
		if got.kind != c.kind || got.retryable != c.retryable {
			t.Errorf("code=%s status=%d: want kind=%v retryable=%v, got kind=%v retryable=%v",
				c.code, c.status, c.kind, c.retryable, got.kind, got.retryable)
		}
		if got.hint == "" {
			t.Errorf("code=%s: hint must not be empty", c.code)
		}
	}
}

func TestErrorSentinels(t *testing.T) {
	cases := []struct {
		code  string
		want  error
		match func(error) bool
	}{
		{CodeMissingAPIKey, ErrAuth, IsAuth},
		{CodeInvalidAPIKey, ErrAuth, IsAuth},
		{CodeInsufficientBalance, ErrInsufficient, IsInsufficient},
		{CodeRateLimited, ErrRateLimited, IsRateLimited},
		{CodeNoRoute, ErrNotFound, IsNotFound},
		{CodeTaskNotFound, ErrNotFound, IsNotFound},
		{CodeBodyTooLarge, ErrTooLarge, IsTooLarge},
		{CodeUnsupportedFileType, ErrUnsupported, IsUnsupported},
		{CodeBackendTimeout, ErrTimeout, IsTimeout},
		{CodeAccessDenied, ErrForbidden, IsForbidden},
	}
	for _, c := range cases {
		err := NewAPIError("op", c.code, "msg", 400)
		if !errors.Is(err, c.want) {
			t.Errorf("code=%s: errors.Is(%v) failed", c.code, c.want)
		}
		if !c.match(err) {
			t.Errorf("code=%s: predicate failed", c.code)
		}
		// The predicate must still match after wrapping.
		wrapped := fmt.Errorf("wrap: %w", err)
		if !c.match(wrapped) {
			t.Errorf("code=%s: predicate failed after wrapping", c.code)
		}
	}
}

func TestErrorString(t *testing.T) {
	e := &Error{
		Kind:       KindAPI,
		Code:       CodeInsufficientBalance,
		Message:    "insufficient balance",
		Hint:       "insufficient balance",
		HTTPStatus: 402,
		RequestID:  "req-1",
		TaskUUID:   "task-1",
	}
	s := e.Error()
	for _, want := range []string{"402001", "insufficient balance", "HTTP 402", "req-1", "task-1"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() missing %q: %s", want, s)
		}
	}
}

func TestConfigError(t *testing.T) {
	ce := &ConfigError{Field: "APIKey", Reason: "must not be empty"}
	if !errors.Is(ce, ErrConfig) {
		t.Fatal("ConfigError should match ErrConfig")
	}
	got, ok := AsConfigError(fmt.Errorf("wrap: %w", ce))
	if !ok || got.Field != "APIKey" {
		t.Fatalf("AsConfigError failed: %v %v", got, ok)
	}
}

func TestTransportErrorMapping(t *testing.T) {
	e := classifyTransportError("op", context.Background(), context.DeadlineExceeded)
	if !IsTimeout(e) {
		t.Fatalf("deadline should map to ErrTimeout: %v", e)
	}
	if !e.Retryable {
		t.Fatal("timeout should be retryable")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	e = classifyTransportError("op", canceled, context.Canceled)
	if e.Retryable {
		t.Fatal("canceled must not be retryable")
	}
}

func TestParseRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "3")
	if d := parseRetryAfter(h); d != 3*time.Second {
		t.Fatalf("want 3s, got %v", d)
	}

	h.Set("Retry-After", time.Now().Add(5*time.Second).UTC().Format(http.TimeFormat))
	if d := parseRetryAfter(h); d <= 0 || d > 6*time.Second {
		t.Fatalf("http-date parse failed: %v", d)
	}

	h.Set("Retry-After", "garbage")
	if d := parseRetryAfter(h); d != 0 {
		t.Fatalf("want 0, got %v", d)
	}
}

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"short":               "****",
		"sk_12345678":         "sk_****78",
		"sk_123456789":        "sk_****89",
		"sk_abcdef1234567890": "sk_abc****7890",
	}
	for in, want := range cases {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q)=%q, want %q", in, got, want)
		}
	}
}
