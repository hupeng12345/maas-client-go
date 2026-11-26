package maas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSendInjectsHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"000000","message":"ok","data":{}}`))
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	resp, meta, err := c.send(context.Background(), requestSpec{
		op:     "test.op",
		method: http.MethodGet,
		url:    c.buildURL("/vision/v1/result"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer drainAndClose(resp.Body)

	if got.Get("Authorization") != "Bearer sk_test" {
		t.Fatalf("authorization mismatch: %q", got.Get("Authorization"))
	}
	if got.Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID must be generated when absent")
	}
	if got.Get("User-Agent") == "" {
		t.Fatal("User-Agent must not be empty")
	}
	if got.Get("Accept") != "application/json" {
		t.Fatalf("accept mismatch: %q", got.Get("Accept"))
	}
	if meta.RequestID == "" || meta.RequestID != got.Get(HeaderRequestID) {
		t.Fatalf("meta request id mismatch: meta=%q header=%q", meta.RequestID, got.Get(HeaderRequestID))
	}
	if meta.HTTPStatus != http.StatusOK {
		t.Fatalf("meta status mismatch: %d", meta.HTTPStatus)
	}
	if meta.Duration <= 0 {
		t.Fatal("meta duration should be positive")
	}
}

func TestSendUsesContextRequestID(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("X-Request-ID")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithRequestID(context.Background(), "trace-abc")
	resp, _, err := c.send(ctx, requestSpec{op: "op", method: http.MethodGet, url: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer drainAndClose(resp.Body)

	if seen != "trace-abc" {
		t.Fatalf("request id not propagated: %q", seen)
	}
	if RequestIDFromContext(ctx) != "trace-abc" {
		t.Fatal("context request id changed")
	}
}

func TestSendHooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var reqEv RequestEvent
	var respEv ResponseEvent
	c, err := New(
		WithAPIKey("sk_test"),
		WithBaseURL(srv.URL),
		WithHooks(Hooks{
			OnRequest:  func(_ context.Context, e RequestEvent) { reqEv = e },
			OnResponse: func(_ context.Context, e ResponseEvent) { respEv = e },
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, _, err := c.send(context.Background(), requestSpec{op: "op", method: http.MethodGet, url: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer drainAndClose(resp.Body)

	if reqEv.Op != "op" || reqEv.APIKeyMasked == "" {
		t.Fatalf("request hook not fired properly: %+v", reqEv)
	}
	if reqEv.APIKeyMasked == "sk_test" {
		t.Fatal("hook must receive masked key")
	}
	if respEv.Status != http.StatusOK {
		t.Fatalf("response hook status mismatch: %d", respEv.Status)
	}
}

func TestSendMapsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(
		WithAPIKey("sk_test"),
		WithBaseURL(srv.URL),
		WithTimeout(30*time.Millisecond),
		WithoutRetry(),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = c.send(context.Background(), requestSpec{op: "op", method: http.MethodGet, url: srv.URL})
	if err == nil {
		t.Fatal("want timeout error")
	}
	if !IsTimeout(err) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
	if !IsRetryable(err) {
		t.Fatal("timeout should be retryable")
	}
}

// The streaming view must ignore WithTimeout, otherwise long TTS audio is truncated.
func TestStreamingIgnoresGlobalTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write([]byte("pcm"))
	}))
	defer srv.Close()

	c, err := New(
		WithAPIKey("sk_test"),
		WithBaseURL(srv.URL),
		WithTimeout(20*time.Millisecond),
		WithoutRetry(),
	)
	if err != nil {
		t.Fatal(err)
	}

	resp, _, err := c.send(context.Background(), requestSpec{
		op: "tts", method: http.MethodPost, url: srv.URL, streaming: true,
	})
	if err != nil {
		t.Fatalf("streaming request should not time out: %v", err)
	}
	defer drainAndClose(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status mismatch: %d", resp.StatusCode)
	}
}

func TestBuildURL(t *testing.T) {
	c, err := New(WithAPIKey("sk_test"), WithBaseURL("https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.buildURL("vision/v1/x"); got != "https://example.com/vision/v1/x" {
		t.Fatalf("relative path mismatch: %s", got)
	}
	if got := c.buildURL("https://other.test/a"); got != "https://other.test/a" {
		t.Fatalf("absolute url mismatch: %s", got)
	}
}

func TestNewUUID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := NewUUID()
		if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
			t.Fatalf("malformed uuid: %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate uuid: %q", id)
		}
		seen[id] = true
	}
}

// The request ID echoed by the server must win over the locally generated one so
// logs can be correlated with the platform.
func TestSendPrefersServerRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "server-side-id")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	resp, meta, err := c.send(WithRequestID(context.Background(), "client-side-id"),
		requestSpec{op: "op", method: http.MethodGet, url: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer drainAndClose(resp.Body)

	if meta.RequestID != "server-side-id" {
		t.Fatalf("server request id should win: %q", meta.RequestID)
	}
}
