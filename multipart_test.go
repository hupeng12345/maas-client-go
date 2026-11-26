package maas

import (
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"sync/atomic"
	"testing"
)

// readAllMultipart parses the whole multipart body and returns fields plus file content.
func readAllMultipart(t *testing.T, body *MultipartBody) (fields map[string]string, file string) {
	t.Helper()
	_, params, err := mime.ParseMediaType(body.ContentType)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	mr := multipart.NewReader(body.Reader, params["boundary"])
	fields = map[string]string{}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "file" {
			file = string(data)
		} else {
			fields[part.FormName()] = string(data)
		}
	}
	return fields, file
}

func TestMultipartBodyFieldsAndFile(t *testing.T) {
	body, err := NewMultipart(
		FileFromBytes("a.pdf", []byte("FILE-CONTENT")),
		map[string]any{"enable_toc": true, "language": "ja", "nil_field": nil},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Reader.Close()

	fields, file := readAllMultipart(t, body)
	if file != "FILE-CONTENT" {
		t.Fatalf("file content mismatch: %q", file)
	}
	if fields["enable_toc"] != "true" {
		t.Fatalf("bool rendering mismatch: %q", fields["enable_toc"])
	}
	if fields["language"] != "ja" {
		t.Fatalf("string rendering mismatch: %q", fields["language"])
	}
	if _, ok := fields["nil_field"]; ok {
		t.Fatal("nil field must be skipped")
	}
}

// When the file size is known the Content-Length must be exact, avoiding chunked transfer.
func TestMultipartContentLengthMatchesActual(t *testing.T) {
	body, err := NewMultipart(
		FileFromBytes("a.pdf", []byte("0123456789")),
		map[string]any{"enable_toc": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Reader.Close()

	if body.ContentLength < 0 {
		t.Fatal("content length should be known")
	}
	if int64(len(data)) != body.ContentLength {
		t.Fatalf("content length mismatch: declared=%d actual=%d", body.ContentLength, len(data))
	}
}

func TestMultipartUnknownSizeUsesChunked(t *testing.T) {
	body, err := NewMultipart(FileFromReader("mic.pcm", strings.NewReader("abc")), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Reader.Close()
	if body.ContentLength != -1 {
		t.Fatalf("want -1 for unknown size, got %d", body.ContentLength)
	}
	if body.Replayable {
		t.Fatal("one-shot source must not be replayable")
	}
}

// Key constraint: building the body must not read the file eagerly, otherwise
// large uploads would be fully buffered.
func TestMultipartDoesNotReadFileEagerly(t *testing.T) {
	var opened atomic.Int32
	var read atomic.Int32

	src := &countingSource{open: &opened, read: &read}
	body, err := NewMultipart(src, map[string]any{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}

	if opened.Load() != 0 || read.Load() != 0 {
		t.Fatalf("file consumed eagerly: opened=%d read=%d", opened.Load(), read.Load())
	}

	// After draining the stream the source must have been opened exactly once.
	if _, err := io.Copy(io.Discard, body.Reader); err != nil {
		t.Fatal(err)
	}
	_ = body.Reader.Close()
	if opened.Load() != 1 {
		t.Fatalf("expected the source to be opened exactly once, got %d", opened.Load())
	}
}

type countingSource struct {
	open *atomic.Int32
	read *atomic.Int32
}

func (s *countingSource) Name() string { return "big.bin" }
func (s *countingSource) Size() int64  { return -1 }
func (s *countingSource) Open() (io.ReadCloser, error) {
	s.open.Add(1)
	return &countingReader{read: s.read}, nil
}
func (s *countingSource) Replayable() bool { return false }

type countingReader struct{ read *atomic.Int32 }

func (r *countingReader) Read(p []byte) (int, error) {
	r.read.Add(1)
	return 0, io.EOF
}
func (r *countingReader) Close() error { return nil }

func TestMultipartRejectsMissingFile(t *testing.T) {
	if _, err := NewMultipart(nil, nil); err == nil {
		t.Fatal("want error for nil file")
	}
}

// renderFieldValue must produce stable strings for every supported type.
func TestRenderFieldValue(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"string", "hi", "hi"},
		{"int", 42, "42"},
		{"int64", int64(-7), "-7"},
		{"uint", uint(8), "8"},
		{"float32", float32(1.5), "1.5"},
		{"float64", 2.25, "2.25"},
		{"stringer", namedStringer("sv"), "sv"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderFieldValue(tc.in); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// namedStringer exercises the fmt.Stringer branch.
type namedStringer string

func (n namedStringer) String() string { return string(n) }

// Filenames must not be able to break the Content-Disposition header.
func TestEscapeQuotes(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain.pdf`, `plain.pdf`},
		{`a"b.pdf`, `a\"b.pdf`},
		{`a\b.pdf`, `a\\b.pdf`},
		{"a\r\nb.pdf", "a  b.pdf"},
	}
	for _, tc := range cases {
		if got := escapeQuotes(tc.in); got != tc.want {
			t.Errorf("escapeQuotes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
