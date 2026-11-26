package maas

import (
	"fmt"
	"io"
	"mime/multipart"
	"sort"
	"strconv"
)

// MultipartBody is a streaming multipart/form-data request body.
//
// It is exported for capability packages; application code normally passes files
// through the capability methods instead.
//
// Reader is backed by io.Pipe and streams as it is read: memory usage is
// constant regardless of file size, so large uploads never risk an OOM.
type MultipartBody struct {
	// Reader is the request body.
	Reader io.ReadCloser
	// ContentType includes the boundary.
	ContentType string
	// ContentLength is the total size, or -1 when unknown (chunked transfer).
	ContentLength int64
	// Replayable reports whether the same body can be produced again, which
	// determines whether the request may be retried.
	Replayable bool
}

// newMultipartBody builds a streaming multipart body.
//
// file is the file field (required); fields are extra form fields rendered by
// type:
//
//	bool           → "true" / "false"
//	string         → as-is
//	int/uint/float → decimal
//	fmt.Stringer  → String()
//	nil            → skipped
//
// Fields are emitted in lexicographic key order so output is deterministic and
// easy to debug.
func newMultipartBody(file FileSource, fields map[string]any) (*MultipartBody, error) {
	if file == nil {
		return nil, NewLocalError("multipart", "no file provided", "pass maas.File(...) to specify the upload")
	}
	if file.Name() == "" {
		return nil, NewLocalError("multipart", "file name is empty", "provide a non-empty file name")
	}

	writer := multipart.NewWriter(io.Discard)
	boundary := writer.Boundary()
	_ = writer.Close()

	keys := make([]string, 0, len(fields))
	for k := range fields {
		if fields[k] == nil {
			continue
		}
		if k == "file" {
			// "file" is reserved for the actual upload and must not be shadowed.
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Pre-render every form field; these are tiny and safe to keep in memory.
	fieldParts := make([][]byte, 0, len(keys))
	var fieldSize int64
	for _, k := range keys {
		part, err := buildFieldPart(boundary, k, renderFieldValue(fields[k]))
		if err != nil {
			return nil, err
		}
		fieldParts = append(fieldParts, part)
		fieldSize += int64(len(part))
	}

	fileHeader, err := buildFileHeader(boundary, file.Name())
	if err != nil {
		return nil, err
	}
	closing := []byte("\r\n--" + boundary + "--\r\n")

	fileSize := file.Size()
	contentLength := fieldSize + int64(len(fileHeader)) + int64(len(closing))
	if fileSize >= 0 {
		contentLength += fileSize
	} else {
		contentLength = -1
	}

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = pw.Close() }()
		for _, part := range fieldParts {
			if _, err := pw.Write(part); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		if _, err := pw.Write(fileHeader); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		src, err := file.Open()
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(pw, src); err != nil {
			_ = src.Close()
			_ = pw.CloseWithError(err)
			return
		}
		if err := src.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := pw.Write(closing); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	return &MultipartBody{
		Reader:        pr,
		ContentType:   "multipart/form-data; boundary=" + boundary,
		ContentLength: contentLength,
		Replayable:    file.Replayable(),
	}, nil
}

// NewMultipart builds a streaming multipart body.
//
// The body streams as it is read, so memory usage does not grow with file size.
// The caller must close Reader when done (or when the request is cancelled).
func NewMultipart(file FileSource, fields map[string]any) (*MultipartBody, error) {
	return newMultipartBody(file, fields)
}

// buildFieldPart renders a complete regular form field including boundaries.
func buildFieldPart(boundary, name, value string) ([]byte, error) {
	var buf writeBuffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		return nil, err
	}
	if err := mw.WriteField(name, value); err != nil {
		return nil, err
	}
	// Do not call mw.Close(): it would append the closing boundary, while a
	// regular field only needs its own part plus a trailing CRLF.
	buf.buf = append(buf.buf, '\r', '\n')
	return buf.Bytes(), nil
}

// buildFileHeader renders the file field header (without content or trailing CRLF).
func buildFileHeader(boundary, filename string) ([]byte, error) {
	escaped := escapeQuotes(filename)
	return []byte("--" + boundary + "\r\n" +
		`Content-Disposition: form-data; name="file"; filename="` + escaped + `"` + "\r\n" +
		"Content-Type: application/octet-stream\r\n\r\n"), nil
}

// escapeQuotes escapes quotes and backslashes in a filename so it cannot break
// the Content-Disposition header.
func escapeQuotes(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			out = append(out, '\\', s[i])
		case '\r', '\n':
			// Header injection guard: newlines become spaces.
			out = append(out, ' ')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

// renderFieldValue renders any value as a form field string.
func renderFieldValue(v any) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

// writeBuffer is the minimal io.Writer multipart.NewWriter needs.
type writeBuffer struct {
	buf []byte
}

func (b *writeBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *writeBuffer) Bytes() []byte { return b.buf }
