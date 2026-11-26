package maas

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := New()
	if err == nil {
		t.Fatal("want error for missing API key")
	}
	if !errors.Is(err, ErrConfig) {
		t.Fatalf("want ErrConfig, got %v", err)
	}
	ce, ok := AsConfigError(err)
	if !ok || ce.Field != "APIKey" {
		t.Fatalf("unexpected config error: %+v", ce)
	}
}

// The SDK reads no environment variables: even with env vars set, a missing
// explicit argument still fails.
func TestNewDoesNotReadEnv(t *testing.T) {
	t.Setenv("MAAS_API_KEY", "sk_from_env")
	t.Setenv("MAAS_BASE_URL", "https://example.invalid")

	if _, err := New(); err == nil {
		t.Fatal("SDK must not pick up API key from environment")
	}
}

func TestNewValidatesBaseURL(t *testing.T) {
	cases := []string{"ftp://example.com", "https://", "://bad"}
	for _, u := range cases {
		if _, err := New(WithAPIKey("sk_test"), WithBaseURL(u)); err == nil {
			t.Errorf("want error for base url %q", u)
		}
	}
}

func TestNewRejectsHTTPClientWithTripperOptions(t *testing.T) {
	_, err := New(
		WithAPIKey("sk_test"),
		WithHTTPClient(&http.Client{}),
		WithTripperOptions(),
	)
	if err == nil || !errors.Is(err, ErrConfig) {
		t.Fatalf("want conflict error, got %v", err)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	c, err := New(WithAPIKey("sk_test"), WithBaseURL("https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() == "" {
		t.Fatal("base url must be set by the caller")
	}
	if c.Config().Timeout != DefaultTimeout {
		t.Fatalf("timeout mismatch: %v", c.Config().Timeout)
	}
	if c.Logger() == nil {
		t.Fatal("logger must never be nil")
	}
	if c.Config().Retry.MaxAttempts != DefaultRetryPolicy().MaxAttempts {
		t.Fatal("retry defaults not applied")
	}
}

func TestNewNormalizesTrailingSlash(t *testing.T) {
	c, err := New(WithAPIKey("sk_test"), WithBaseURL("https://example.com/"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.buildURL("/vision/v1/x"); got != "https://example.com/vision/v1/x" {
		t.Fatalf("url mismatch: %s", got)
	}
}

func TestNewAcceptsCustomHTTPClient(t *testing.T) {
	hc := &http.Client{Timeout: 5 * time.Second}
	c, err := New(WithAPIKey("sk_test"), WithBaseURL("https://example.com"), WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	if c.unary != hc || c.stream != hc {
		t.Fatal("custom http client should be used for both views")
	}
}

func TestMustNewPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustNew should panic on invalid config")
		}
	}()
	MustNew()
}
