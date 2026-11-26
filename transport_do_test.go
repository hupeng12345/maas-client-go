package maas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewRequestResolvesURL(t *testing.T) {
	c, err := New(WithAPIKey("sk_test"), WithBaseURL("https://example.com/api"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"absolute path", "/vision/v1/x", "https://example.com/api/vision/v1/x"},
		{"relative path", "vision/v1/x", "https://example.com/api/vision/v1/x"},
		{"absolute url", "https://other.test/x", "https://other.test/x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := c.NewRequest(context.Background(), http.MethodGet, tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if got := req.URL.String(); got != tc.want {
				t.Fatalf("url: got %q want %q", got, tc.want)
			}
			if req.Context() == nil {
				t.Fatal("request must carry a context")
			}
		})
	}
}

func TestDoInjectsHeaders(t *testing.T) {
	cases := []struct {
		name      string
		ctx       context.Context
		preset    map[string]string
		wantAuth  string
		wantReqID string
	}{
		{
			name:      "defaults",
			ctx:       context.Background(),
			wantAuth:  "Bearer sk_test",
			wantReqID: "generated",
		},
		{
			name:      "context request id wins",
			ctx:       WithRequestID(context.Background(), "trace-1"),
			wantAuth:  "Bearer sk_test",
			wantReqID: "trace-1",
		},
		{
			name:      "existing authorization is preserved",
			ctx:       context.Background(),
			preset:    map[string]string{"Authorization": "Bearer other"},
			wantAuth:  "Bearer other",
			wantReqID: "generated",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL))
			if err != nil {
				t.Fatal(err)
			}

			req, err := c.NewRequest(tc.ctx, http.MethodGet, "/x", nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.preset {
				req.Header.Set(k, v)
			}

			resp, meta, err := c.Do(req, "test.op")
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			defer drainAndClose(resp.Body)

			if gotAuth := got.Get("Authorization"); gotAuth != tc.wantAuth {
				t.Fatalf("authorization: got %q want %q", gotAuth, tc.wantAuth)
			}
			reqID := got.Get(HeaderRequestID)
			if reqID == "" {
				t.Fatal("request id must be set")
			}
			if tc.wantReqID != "generated" && reqID != tc.wantReqID {
				t.Fatalf("request id: got %q want %q", reqID, tc.wantReqID)
			}
			if meta.RequestID == "" || meta.HTTPStatus != http.StatusOK {
				t.Fatalf("meta mismatch: %+v", meta)
			}
			if meta.Duration <= 0 {
				t.Fatal("duration must be positive")
			}
		})
	}
}

// The server-provided request ID must win over the local one.
func TestDoPrefersServerRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(HeaderRequestID, "server-id")
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.NewRequest(WithRequestID(context.Background(), "client-id"), http.MethodGet, "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, meta, err := c.Do(req, "op")
	if err != nil {
		t.Fatal(err)
	}
	defer drainAndClose(resp.Body)

	if meta.RequestID != "server-id" {
		t.Fatalf("server id should win: %q", meta.RequestID)
	}
}

func TestDoNilRequest(t *testing.T) {
	c, err := New(WithAPIKey("sk_test"), WithBaseURL("https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Do(nil, "op"); err == nil {
		t.Fatal("want error for nil request")
	}
}

func TestDoStreamingIgnoresGlobalTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL), WithTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.NewRequest(context.Background(), http.MethodGet, "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, _, err := c.Do(req, "op", Streaming())
	if err != nil {
		t.Fatalf("streaming must ignore the global timeout: %v", err)
	}
	defer drainAndClose(resp.Body)
}

func TestDoMapsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	c, err := New(WithAPIKey("sk_test"), WithBaseURL(srv.URL), WithTimeout(10*time.Millisecond), WithoutRetry())
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.NewRequest(context.Background(), http.MethodGet, "/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Do(req, "op"); !IsTimeout(err) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestClientAccessors(t *testing.T) {
	c, err := New(
		WithAPIKey("sk_test"),
		WithBaseURL("https://example.com"),
		WithMaxUploadSize(1234),
		WithRawResult(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	if got := c.BaseURL(); got != "https://example.com" {
		t.Errorf("BaseURL = %q", got)
	}
	if got := c.MaxUploadSize(); got != 1234 {
		t.Errorf("MaxUploadSize = %d", got)
	}
	if !c.KeepRaw() {
		t.Error("KeepRaw should be true")
	}
	if got := c.PollPolicy().Timeout; got != DefaultPollPolicy().Timeout {
		t.Errorf("PollPolicy.Timeout = %v", got)
	}
	if got := c.AuthHeaders().Get("Authorization"); got != "Bearer sk_test" {
		t.Errorf("AuthHeaders = %q", got)
	}
	if c.Logger() == nil || c.Config().APIKey != "sk_test" {
		t.Error("logger or config not populated")
	}
}
