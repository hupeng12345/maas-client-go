package maas

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func newResp(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestParseEnvelopeSuccess(t *testing.T) {
	env, err := parseEnvelope("op", newResp(200, "application/json",
		`{"code":"000000","message":"ok","data":{"task_uuid":"abc"}}`), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !env.IsSuccess() {
		t.Fatalf("want success, got %q", env.Code)
	}
	if string(env.Data) != `{"task_uuid":"abc"}` {
		t.Fatalf("data mismatch: %s", env.Data)
	}
}

func TestParseEnvelopeAcceptsNullDataAndMissingMessage(t *testing.T) {
	env, err := parseEnvelope("op", newResp(200, "application/json", `{"code":"000000","data":null}`), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Message != "" {
		t.Fatalf("want empty message, got %q", env.Message)
	}
}

// The standard gateway envelope requires a six-digit string code.
// Numeric forms are rejected outright for compatibility reasons.
func TestParseEnvelopeRejectsNonStandard(t *testing.T) {
	cases := map[string]string{
		"numeric code":       `{"code":200,"message":"ok"}`,
		"numeric zero":       `{"code":0,"message":"ok"}`,
		"missing code":       `{"message":"ok"}`,
		"short code":         `{"code":"200","message":"ok"}`,
		"non digit code":     `{"code":"00a000","message":"ok"}`,
		"null code":          `{"code":null,"message":"ok"}`,
		"message not string": `{"code":"000000","message":1}`,
		"not an object":      `["ok"]`,
		"plain text":         `ok`,
		"empty object":       `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseEnvelope("op", newResp(200, "application/json", body), 0)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !IsNonStandardResponse(err) {
				t.Fatalf("want ErrNonStandardResponse, got %v", err)
			}
		})
	}
}

func TestParseEnvelopeEmptyBody(t *testing.T) {
	_, err := parseEnvelope("op", newResp(200, "application/json", "  "), 0)
	if err == nil || !IsNonStandardResponse(err) {
		t.Fatalf("want non-standard error, got %v", err)
	}
}

func TestParseEnvelopeTooLarge(t *testing.T) {
	big := `{"code":"000000","message":"ok","data":"` + strings.Repeat("x", 100) + `"}`
	_, err := parseEnvelope("op", newResp(200, "application/json", big), 32)
	if err == nil {
		t.Fatal("want error for oversized body")
	}
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindProtocol {
		t.Fatalf("want protocol error, got %v", err)
	}
}

func TestEnvelopeErrorCarriesRetryAfterAndRequestID(t *testing.T) {
	resp := newResp(429, "application/json", `{"code":"429001","message":"rate limit exceeded","data":null}`)
	resp.Header.Set("Retry-After", "7")
	resp.Header.Set("X-Request-ID", "req-abc")

	env, err := parseEnvelope("op", resp, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := env.Error("op", resp)
	if e.Code != CodeRateLimited {
		t.Fatalf("code mismatch: %s", e.Code)
	}
	if !e.Retryable {
		t.Fatal("429 should be retryable")
	}
	if e.RetryAfter != 7_000_000_000 {
		t.Fatalf("retry after mismatch: %v", e.RetryAfter)
	}
	if e.RequestID != "req-abc" {
		t.Fatalf("request id mismatch: %s", e.RequestID)
	}
}
