package ocr_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/ocr"
)

// newClient builds a client pointed at a test server.
func newClient(t *testing.T, baseURL string) *maas.Client {
	t.Helper()
	c, err := maas.New(maas.WithAPIKey("sk_test"), maas.WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// envelope writes a JSON envelope.
func envelope(w http.ResponseWriter, code, message string, data any) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{"code": code, "message": message, "data": data}
	_ = json.NewEncoder(w).Encode(body)
}

// taskServer serves a submission followed by a scripted sequence of poll states.
func taskServer(t *testing.T, states []string, finalTask string) (*httptest.Server, *int32) {
	t.Helper()
	var polls int32

	mux := http.NewServeMux()
	mux.HandleFunc("/vision/v1/document/parse/general/async", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if _, _, err := r.FormFile("file"); err != nil {
			t.Errorf("missing file part: %v", err)
		}
		w.Header().Set("X-Request-ID", "submit-id")
		envelope(w, maas.CodeSuccess, "ok", map[string]any{
			"task_uuid": "task-1", "task_status": "PENDING", "request_id": "submit-id",
		})
	})
	mux.HandleFunc("/vision/v1/result", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("task_uuid"); got != "task-1" {
			t.Errorf("unexpected task_uuid %q", got)
		}
		n := int(atomic.AddInt32(&polls, 1))
		status := finalTask
		if n <= len(states) {
			status = states[n-1]
		}
		w.Header().Set("X-Request-ID", "poll-id")
		envelope(w, maas.CodeSuccess, "ok", map[string]any{
			"task_uuid":        "task-1",
			"task_status":      status,
			"start_time":       100,
			"end_time":         103,
			"markdown_content": "# title",
			"pages": []map[string]any{{
				"page_index":    1,
				"page_markdown": "# title",
				"layout_result": []map[string]any{{
					"type": "text", "page_idx": 1, "content": "hello",
				}},
			}},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &polls
}

func TestParseDocumentPollsUntilSuccess(t *testing.T) {
	srv, polls := taskServer(t, []string{"PROCESSING", "PENDING"}, "SUCCESS")

	res, err := ocr.New(newClient(t, srv.URL)).ParseDocument(
		context.Background(),
		maas.FileFromBytes("a.pdf", []byte("%PDF-1.4")),
		ocr.EnableTOC(true),
		ocr.WithWaitOptions(ocr.WithPollInterval(time.Millisecond)),
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := atomic.LoadInt32(polls); got != 3 {
		t.Fatalf("expected 3 polls, got %d", got)
	}
	if !res.Succeeded() {
		t.Fatalf("want success, got %q", res.TaskStatus)
	}
	if res.MarkdownContent != "# title" {
		t.Fatalf("content mismatch: %q", res.MarkdownContent)
	}
	if len(res.Pages) != 1 || res.Pages[0].Index != 1 {
		t.Fatalf("pages mismatch: %+v", res.Pages)
	}
	if len(res.Pages[0].Layout) != 1 || res.Pages[0].Layout[0].Content != "hello" {
		t.Fatalf("layout mismatch: %+v", res.Pages[0].Layout)
	}
	if res.Meta.RequestID != "poll-id" {
		t.Fatalf("request id must come from the last response: %q", res.Meta.RequestID)
	}
	if res.Duration() != 3*time.Second {
		t.Fatalf("duration mismatch: %v", res.Duration())
	}
}

// A task that ends in a failed terminal state must surface as ErrTaskFailed even
// though the query itself returned the success code.
func TestParseDocumentTerminalStates(t *testing.T) {
	cases := map[string]bool{ // final status -> expect task failure
		"FAILED":   true,
		"CANCELED": true,
	}
	for status, wantFail := range cases {
		t.Run(status, func(t *testing.T) {
			srv, _ := taskServer(t, nil, status)
			_, err := ocr.New(newClient(t, srv.URL)).ParseDocument(
				context.Background(),
				maas.FileFromBytes("a.pdf", []byte("x")),
				ocr.WithWaitOptions(ocr.WithPollInterval(time.Millisecond)),
			)
			if got := err != nil; got != wantFail {
				t.Fatalf("status %s: got err=%v, want failure=%v", status, err, wantFail)
			}
			if err != nil && !maas.IsTaskFailed(err) {
				t.Fatalf("status %s: want ErrTaskFailed, got %v", status, err)
			}
		})
	}
}

func TestRecognizeCardModes(t *testing.T) {
	cases := []struct {
		name     string
		async    bool
		wantPath string
	}{
		{"default uses sync", false, "/vision/v1/card/idcard/sync"},
		{"async option", true, "/vision/v1/card/idcard/async"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			mux := http.NewServeMux()
			mux.HandleFunc("/vision/v1/card/idcard/sync", func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				envelope(w, maas.CodeSuccess, "ok", map[string]any{
					"task_status": "SUCCESS",
					"pages": []map[string]any{{
						"page_index": 0,
						"ie_result":  []map[string]any{{"recognized": true, "fields": map[string]any{"name": "Alice"}}},
					}},
				})
			})
			mux.HandleFunc("/vision/v1/card/idcard/async", func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				envelope(w, maas.CodeSuccess, "ok", map[string]any{"task_uuid": "t1", "task_status": "PENDING"})
			})
			mux.HandleFunc("/vision/v1/result", func(w http.ResponseWriter, r *http.Request) {
				envelope(w, maas.CodeSuccess, "ok", map[string]any{"task_status": "SUCCESS"})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			opts := []ocr.RecognizeOption{ocr.WithWaitOptions(ocr.WithPollInterval(time.Millisecond))}
			if tc.async {
				opts = append(opts, ocr.WithAsyncSubmit())
			}
			if _, err := ocr.New(newClient(t, srv.URL)).Recognize(context.Background(), ocr.CardIDCard,
				maas.FileFromBytes("id.png", []byte("png")), opts...); err != nil {
				t.Fatalf("recognize: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Fatalf("path mismatch: got %q want %q", gotPath, tc.wantPath)
			}
		})
	}
}

// Every card kind must map to its own path.
func TestCardKindPaths(t *testing.T) {
	kinds := ocr.CardKinds()
	if len(kinds) != 13 {
		t.Fatalf("expected 13 card kinds, got %d", len(kinds))
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				envelope(w, maas.CodeSuccess, "ok", map[string]any{"task_status": "SUCCESS"})
			}))
			defer srv.Close()

			_, err := ocr.New(newClient(t, srv.URL)).Recognize(context.Background(), kind,
				maas.FileFromBytes("a.png", []byte("png")))
			if err != nil {
				t.Fatalf("recognize: %v", err)
			}
			want := "/vision/v1/card/" + string(kind) + "/sync"
			if gotPath != want {
				t.Fatalf("path mismatch: got %q want %q", gotPath, want)
			}
		})
	}
}

func TestRecognizeRejectsUnknownKind(t *testing.T) {
	c, err := maas.New(maas.WithAPIKey("sk_test"), maas.WithBaseURL("https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = ocr.New(c).Recognize(context.Background(), ocr.CardKind("nope"), maas.FileFromBytes("a", []byte("x")))
	if err == nil {
		t.Fatalf("want local error, got %v", err)
	}
	var e *maas.Error
	if !errors.As(err, &e) || e.Kind != maas.KindLocal {
		t.Fatalf("want KindLocal, got %v", err)
	}
}

// The API must reject response shapes that are not the gateway envelope.
func TestEnvelopeRejection(t *testing.T) {
	cases := []struct {
		name string
		body string
		want func(error) bool
	}{
		{"numeric code", `{"code":200,"message":"ok","data":{}}`, maas.IsNonStandardResponse},
		{"missing code", `{"message":"ok"}`, maas.IsNonStandardResponse},
		{"plain text", `ok`, maas.IsNonStandardResponse},
		{"unauthorized", `{"code":"401001","message":"missing api key","data":null}`, maas.IsAuth},
		{"insufficient", `{"code":"402001","message":"insufficient balance","data":null}`, maas.IsInsufficient},
		{"not found", `{"code":"404003","message":"resource not found","data":null}`, maas.IsNotFound},
		{"too large", `{"code":"413001","message":"request body too large","data":null}`, maas.IsTooLarge},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := ocr.New(newClient(t, srv.URL)).ParseDocument(
				context.Background(), maas.FileFromBytes("a.pdf", []byte("x")),
				ocr.WithWaitOptions(ocr.WithPollInterval(time.Millisecond)),
			)
			if err == nil {
				t.Fatal("want error")
			}
			if !tc.want(err) {
				t.Fatalf("error predicate failed: %v", err)
			}
		})
	}
}

func TestSubmissionWithoutTaskUUID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope(w, maas.CodeSuccess, "ok", map[string]any{"task_status": "PENDING"})
	}))
	defer srv.Close()

	_, err := ocr.New(newClient(t, srv.URL)).ParseDocument(
		context.Background(), maas.FileFromBytes("a.pdf", []byte("x")))
	if err == nil {
		t.Fatal("want error")
	}
	var e *maas.Error
	if !errors.As(err, &e) || e.Kind != maas.KindProtocol {
		t.Fatalf("want protocol error, got %v", err)
	}
}

func TestLocalValidation(t *testing.T) {
	c, err := maas.New(maas.WithAPIKey("sk_test"), maas.WithBaseURL("https://example.com"), maas.WithMaxUploadSize(4))
	if err != nil {
		t.Fatal(err)
	}
	svc := ocr.New(c)

	cases := []struct {
		name string
		file maas.FileSource
		want string
	}{
		{"nil file", nil, "no file provided"},
		{"too large", maas.FileFromBytes("a.pdf", []byte("12345")), "local size limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ParseDocument(context.Background(), tc.file)
			if err == nil {
				t.Fatal("want error")
			}
			var e *maas.Error
			if !errors.As(err, &e) || e.Kind != maas.KindLocal {
				t.Fatalf("want local error, got %v", err)
			}
			if !contains(e.Message, tc.want) {
				t.Fatalf("message %q does not mention %q", e.Message, tc.want)
			}
		})
	}
}

func TestWaitTimeout(t *testing.T) {
	srv, _ := taskServer(t, nil, "PROCESSING")
	_, err := ocr.New(newClient(t, srv.URL)).ParseDocument(
		context.Background(),
		maas.FileFromBytes("a.pdf", []byte("x")),
		ocr.WithWaitOptions(
			ocr.WithPollInterval(time.Millisecond),
			ocr.WithWaitTimeout(20*time.Millisecond),
		),
	)
	if err == nil {
		t.Fatal("want timeout error")
	}
	if !maas.IsRetryable(err) {
		t.Fatalf("timeout should be retryable: %v", err)
	}
}

func TestWaitCanceled(t *testing.T) {
	srv, _ := taskServer(t, nil, "PROCESSING")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := ocr.New(newClient(t, srv.URL)).ParseDocument(
		ctx, maas.FileFromBytes("a.pdf", []byte("x")),
		ocr.WithWaitOptions(ocr.WithPollInterval(5*time.Millisecond)))
	if err == nil {
		t.Fatal("want error for canceled context")
	}
	if !maas.IsTaskFailed(err) {
		t.Fatalf("want a task error while waiting, got %v", err)
	}
}

// A context canceled before submission fails in the transport layer, not in the
// task layer, and must not be reported as retryable.
func TestSubmitCanceled(t *testing.T) {
	srv, _ := taskServer(t, nil, "SUCCESS")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ocr.New(newClient(t, srv.URL)).ParseDocument(ctx, maas.FileFromBytes("a.pdf", []byte("x")))
	if err == nil {
		t.Fatal("want error")
	}
	if maas.IsRetryable(err) {
		t.Fatalf("cancelation must not be retryable: %v", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
