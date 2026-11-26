package tripper_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/hupeng12345/maas-client-go/logit"
	"github.com/hupeng12345/maas-client-go/tripper"
)

func TestLogitTripperIntegration(t *testing.T) {
	var buf strings.Builder
	logger := logit.NewSimpleWithConfig(&buf, &logit.Config{PodName: "maas-client", Level: logit.Debug, CallerSkip: logit.CallerSkipConfig{List: []logit.CallerSkipConfigItem{{Value: "net/http", Mode: logit.CallerSkipContains}}}})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":"000000","message":"ok","data":{}}`)
	}))
	defer srv.Close()

	client := tripper.New(
		tripper.Logger(logger),
		tripper.Retry(2),
		tripper.ClientTimeout(5e9),
	)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/vision/v1/result?task_uuid=abc", nil)
	req.Header.Set("Authorization", "Bearer sk_secret_should_not_appear")
	req.Header.Set("X-Request-ID", "trace-1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	out := buf.String()
	if strings.Contains(out, "sk_secret_should_not_appear") {
		t.Fatalf("Authorization leaked into logs:\n%s", out)
	}
	if !strings.Contains(out, "http request completed") {
		t.Fatalf("missing completion log:\n%s", out)
	}
	t.Logf("log sample:\n%s", out)
}

// errOnRead records that it was read and returns an error, which lets tests
// assert that middleware did not consume the body early.
type errOnRead struct {
	called bool
}

func (e *errOnRead) Read(p []byte) (int, error) {
	e.called = true
	return 0, io.ErrUnexpectedEOF
}

// TestRetryDoesNotBufferNonIdempotentBody verifies that non-idempotent methods
// such as POST are not read into memory as a whole by the retry middleware when
// retries are enabled; otherwise a large streaming upload would degrade into
// full buffering.
func TestRetryDoesNotBufferNonIdempotentBody(t *testing.T) {
	body := &errOnRead{}
	rt := tripper.NewRetryRoundTripper(tripper.RetryMaxRetries(3))(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	}))

	req, err := http.NewRequest(http.MethodPost, "http://example.test/upload", body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if body.called {
		t.Fatal("retry middleware consumed a non-idempotent body; streaming upload would be buffered")
	}
}

// TestLogSkipBodyKeepsStreaming verifies that the logging middleware does not
// pre-read the body in streaming scenarios.
func TestLogSkipBodyKeepsStreaming(t *testing.T) {
	var buf strings.Builder
	logger := logit.NewSimpleWithConfig(&buf, &logit.Config{Level: logit.Debug})

	payload := "audio-bytes-should-not-be-in-log"
	rt := tripper.NewLogitRoundTripper(logger, tripper.LogSkipBody())(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Body:          io.NopCloser(strings.NewReader(payload)),
			ContentLength: int64(len(payload)),
			Header:        http.Header{"Content-Type": []string{"audio/pcm"}},
		}, nil
	}))

	req, err := http.NewRequest(http.MethodPost, "http://example.test/tts/v1/speech", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if string(got) != payload {
		t.Fatalf("body not passed through: %q", got)
	}
	out := buf.String()
	if strings.Contains(out, payload) {
		t.Fatalf("body captured into log despite LogSkipBody:\n%s", out)
	}
	if !strings.Contains(out, "response_body_size["+strconv.Itoa(len(payload))+"]") {
		t.Fatalf("expected size from Content-Length in log:\n%s", out)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
