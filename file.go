package maas

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
)

// FileSource is the abstraction for an uploadable file.
//
// The SDK only depends on these four methods, so callers can implement their
// own source (for example streaming straight from object storage).
type FileSource interface {
	// Name returns the file name used for the multipart filename and for
	// server-side type inference.
	Name() string
	// Size returns the byte size, or -1 when unknown.
	Size() int64
	// Open returns a fresh reader. For replayable sources every call restarts
	// from the beginning.
	Open() (io.ReadCloser, error)
	// Replayable reports whether Open can be called again safely, which
	// determines whether the request may be retried.
	Replayable() bool
}

// errReaderConsumed indicates that a one-shot reader was already consumed.
var errReaderConsumed = errors.New("maas: file reader already consumed")

// File converts common values into a FileSource.
//
// Supported inputs:
//
//	string              → opened by path, replayable
//	[]byte              → in-memory bytes, replayable
//	io.ReadSeeker       → Seek(0,0) on each Open, replayable
//	io.Reader           → one-shot, not replayable
//	FileSource          → returned unchanged
//
// Any other type returns nil, which callers should treat as an invalid argument.
func File(v any) FileSource {
	switch x := v.(type) {
	case nil:
		return nil
	case FileSource:
		return x
	case string:
		return FileFromPath(x)
	case []byte:
		return FileFromBytes("upload.bin", x)
	case io.ReadSeeker:
		return FileFromReaderSeeker("upload.bin", x)
	case io.Reader:
		return FileFromReader("upload.bin", x)
	default:
		return nil
	}
}

// pathSource is backed by a local file path.
type pathSource struct {
	path string
}

// FileFromPath builds a FileSource from a local path (replayable).
func FileFromPath(path string) FileSource { return &pathSource{path: path} }

func (s *pathSource) Name() string { return filepath.Base(s.path) }

func (s *pathSource) Size() int64 {
	st, err := os.Stat(s.path)
	if err != nil {
		return -1
	}
	return st.Size()
}

func (s *pathSource) Open() (io.ReadCloser, error) { return os.Open(s.path) }

func (s *pathSource) Replayable() bool { return true }

// bytesSource is backed by in-memory bytes.
type bytesSource struct {
	name string
	data []byte
}

// FileFromBytes builds a FileSource from in-memory bytes (replayable).
func FileFromBytes(name string, data []byte) FileSource {
	if name == "" {
		name = "upload.bin"
	}
	return &bytesSource{name: name, data: data}
}

func (s *bytesSource) Name() string { return s.name }
func (s *bytesSource) Size() int64  { return int64(len(s.data)) }
func (s *bytesSource) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (s *bytesSource) Replayable() bool { return true }

// readerSource is backed by a one-shot io.Reader.
type readerSource struct {
	name     string
	r        io.Reader
	consumed atomic.Bool
}

// FileFromReader builds a one-shot FileSource from an io.Reader.
//
// Because the stream cannot be rewound, the SDK never retries requests using
// this source. If you need retries, use FileFromPath, FileFromBytes or
// FileFromReaderSeeker instead.
func FileFromReader(name string, r io.Reader) FileSource {
	if name == "" {
		name = "upload.bin"
	}
	return &readerSource{name: name, r: r}
}

func (s *readerSource) Name() string { return s.name }
func (s *readerSource) Size() int64  { return -1 }
func (s *readerSource) Open() (io.ReadCloser, error) {
	if s.consumed.Swap(true) {
		return nil, errReaderConsumed
	}
	return io.NopCloser(s.r), nil
}
func (s *readerSource) Replayable() bool { return false }

// seekerSource is backed by an io.ReadSeeker.
type seekerSource struct {
	name string
	rs   io.ReadSeeker
}

// FileFromReaderSeeker builds a replayable FileSource from an io.ReadSeeker.
func FileFromReaderSeeker(name string, rs io.ReadSeeker) FileSource {
	if name == "" {
		name = "upload.bin"
	}
	return &seekerSource{name: name, rs: rs}
}

func (s *seekerSource) Name() string { return s.name }

func (s *seekerSource) Size() int64 {
	cur, err := s.rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return -1
	}
	end, err := s.rs.Seek(0, io.SeekEnd)
	if err != nil {
		return -1
	}
	if _, err := s.rs.Seek(cur, io.SeekStart); err != nil {
		return -1
	}
	return end
}

func (s *seekerSource) Open() (io.ReadCloser, error) {
	if _, err := s.rs.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.NopCloser(s.rs), nil
}

func (s *seekerSource) Replayable() bool { return true }
