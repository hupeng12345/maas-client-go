package ocr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/ocr"
)

// WithEndpoint must let callers target a different path without waiting for an
// SDK release.
func TestEndpointOverride(t *testing.T) {
	cases := []struct {
		name        string
		defaultPath string
		override    string
		wantPath    string
	}{
		{
			name:        "default path",
			defaultPath: "/vision/v1/ocr/general/async",
			wantPath:    "/vision/v1/ocr/general/async",
		},
		{
			name:        "override wins",
			defaultPath: "/vision/v1/ocr/general/async",
			override:    "/custom/v2/ocr",
			wantPath:    "/custom/v2/ocr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			mux := http.NewServeMux()
			handler := func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				envelope(w, maas.CodeSuccess, "ok", map[string]any{
					"task_uuid": "t1", "task_status": "PENDING",
				})
			}
			mux.HandleFunc(tc.defaultPath, handler)
			if tc.wantPath != tc.defaultPath {
				mux.HandleFunc(tc.wantPath, handler)
			}
			mux.HandleFunc("/vision/v1/result", func(w http.ResponseWriter, r *http.Request) {
				envelope(w, maas.CodeSuccess, "ok", map[string]any{"task_status": "SUCCESS"})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			opts := []ocr.OCROption{ocr.WithWaitOptions(ocr.WithPollInterval(time.Millisecond))}
			if tc.override != "" {
				opts = append(opts, ocr.WithEndpoint(tc.override))
			}
			if _, err := ocr.New(newClient(t, srv.URL)).OCRText(
				context.Background(), maas.FileFromBytes("a.png", []byte("x")), opts...); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Fatalf("path: got %q want %q", gotPath, tc.wantPath)
			}
		})
	}
}
