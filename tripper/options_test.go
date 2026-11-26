package tripper_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hupeng12345/maas-client-go/tripper"
)

// Building a client must never panic and must apply the documented defaults.
func TestBuildVariants(t *testing.T) {
	proxy, err := url.Parse("http://proxy.test:8080")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		opts []tripper.Option
	}{
		{"no options", nil},
		{"proxy", []tripper.Option{tripper.Proxy(proxy)}},
		{"connect timeout", []tripper.Option{tripper.ConnectTimeout(time.Second)}},
		{"client timeout", []tripper.Option{tripper.ClientTimeout(time.Second)}},
		{"idle conns", []tripper.Option{tripper.MaxIdleConns(7), tripper.IdleConnTimeout(time.Minute)}},
		{"no redirect", []tripper.Option{tripper.NoRedirect()}},
		{"retry", []tripper.Option{tripper.Retry(2)}},
		{"retry opts", []tripper.Option{tripper.RetryOpts(
			tripper.RetryMaxRetries(4),
			tripper.RetryExponentialBackoff(10*time.Millisecond, time.Second),
		)}},
		{"tls insecure", []tripper.Option{tripper.TLSInsecure()}},
		{"base transport", []tripper.Option{tripper.BaseTransport(http.DefaultTransport)}},
		{"custom middleware", []tripper.Option{tripper.Custom(func(next http.RoundTripper) http.RoundTripper {
			return next
		})}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rt := tripper.Build(tc.opts...); rt == nil {
				t.Fatal("Build returned nil")
			}
			if c := tripper.New(tc.opts...); c == nil {
				t.Fatal("New returned nil")
			}
			if tr := tripper.TransportOnly(tc.opts...); tr == nil {
				t.Fatal("TransportOnly returned nil")
			}
		})
	}
}

func TestFromConfig(t *testing.T) {
	cfg := tripper.Config{
		ProxyURL:       "http://proxy.test:8080",
		ConnectTimeout: time.Second,
		ClientTimeout:  time.Second,
		MaxRetries:     2,
		NoRedirect:     true,
	}
	c := tripper.FromConfig(cfg)
	if c == nil {
		t.Fatal("FromConfig returned nil")
	}
	if c.Timeout != time.Second {
		t.Fatalf("timeout not applied: %v", c.Timeout)
	}
}

func TestNoRedirectBehaviour(t *testing.T) {
	c := tripper.New(tripper.NoRedirect())
	if c.CheckRedirect == nil {
		t.Fatal("NoRedirect must set CheckRedirect")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err := c.CheckRedirect(req, nil); err == nil {
		t.Fatal("CheckRedirect should return http.ErrUseLastResponse")
	}
}
