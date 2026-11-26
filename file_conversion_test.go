package maas

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// File must accept every documented input form and report replayability
// correctly, because replayability decides whether a request may be retried.
func TestFileConversions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.pdf")
	if err := os.WriteFile(path, []byte("path-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		input      any
		wantNil    bool
		wantName   string
		wantSize   int64
		replayable bool
		wantBody   string
	}{
		{name: "path", input: path, wantName: "a.pdf", wantSize: 12, replayable: true, wantBody: "path-content"},
		{name: "bytes", input: []byte("abcd"), wantName: "upload.bin", wantSize: 4, replayable: true, wantBody: "abcd"},
		{name: "reader", input: io.LimitReader(strings.NewReader("xyz"), 3), wantName: "upload.bin", wantSize: -1, replayable: false, wantBody: "xyz"},
		{name: "seeker", input: bytes.NewReader([]byte("seek")), wantName: "upload.bin", wantSize: 4, replayable: true, wantBody: "seek"},
		{name: "nil", input: nil, wantNil: true},
		{name: "unsupported", input: 42, wantNil: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := File(tc.input)
			if tc.wantNil {
				if src != nil {
					t.Fatalf("want nil source, got %v", src)
				}
				return
			}
			if src == nil {
				t.Fatal("want a source")
			}
			if src.Name() != tc.wantName {
				t.Errorf("name: got %q want %q", src.Name(), tc.wantName)
			}
			if src.Size() != tc.wantSize {
				t.Errorf("size: got %d want %d", src.Size(), tc.wantSize)
			}
			if src.Replayable() != tc.replayable {
				t.Errorf("replayable: got %v want %v", src.Replayable(), tc.replayable)
			}

			rc, err := src.Open()
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			body, _ := io.ReadAll(rc)
			_ = rc.Close()
			if string(body) != tc.wantBody {
				t.Errorf("body: got %q want %q", body, tc.wantBody)
			}
		})
	}
}

// A FileSource passed to File must be returned unchanged.
func TestFilePassesThroughFileSource(t *testing.T) {
	src := FileFromBytes("x.bin", []byte("y"))
	if got := File(src); got != src {
		t.Fatal("FileSource should pass through unchanged")
	}
}

func TestFileFromReaderIsOneShot(t *testing.T) {
	src := FileFromReader("mic.pcm", strings.NewReader("pcm"))
	if src.Replayable() {
		t.Fatal("one-shot reader must not be replayable")
	}
	rc, err := src.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()

	if _, err := src.Open(); err == nil {
		t.Fatal("second Open should fail")
	}
}

func TestSeekerReplaysFromStart(t *testing.T) {
	src := FileFromReaderSeeker("a.bin", bytes.NewReader([]byte("abcdef")))
	for i := 0; i < 2; i++ {
		rc, err := src.Open()
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if string(b) != "abcdef" {
			t.Fatalf("open #%d body: %q", i, b)
		}
	}
}
