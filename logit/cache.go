// Package logit provides structured text logging.
package logit

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// timePrefix is the timestamp prefix for a single second. It is never modified
// after being published (an immutable snapshot). Readers in
// appendFormattedTimestamp may be reading it concurrently, so a refresh must
// allocate a new slice and never reuse the old backing array.
type timePrefix struct {
	unix  int64  // Unix second this prefix belongs to
	bytes []byte // "2006-01-02 15:04:05"
}

// TimeCache caches timestamp prefixes on the logging hot path to reduce
// allocations. The "YYYY-MM-DD HH:MM:SS" part is cached once per wall-clock
// second, so emitting several lines within the same second avoids formatting
// the prefix again and only the milliseconds part has to be appended.
//
// It is safe for concurrent use: the hit path performs a single atomic load
// without locking, and the lock is taken only when the prefix is refreshed for
// a new second. Concurrent logging is therefore never serialized per line.
type TimeCache struct {
	prefix atomic.Pointer[timePrefix] // immutable prefix for the current second
	mu     sync.Mutex                 // guards the refresh path only
}

// appendFormattedTimestamp appends the full timestamp with milliseconds to the
// buffer. The format is "2006-01-02 15:04:05.mmm", where mmm is the 3-digit
// millisecond part. If the call falls in the same second as the previous one,
// the cached prefix is used. It is safe for concurrent use from multiple
// goroutines.
func (c *TimeCache) appendFormattedTimestamp(buf *bytes.Buffer) {
	now := time.Now()
	unix := now.Unix()
	p := c.prefix.Load()
	if p == nil || p.unix != unix {
		p = c.refresh(unix, now)
	}
	buf.Write(p.bytes)
	_, _ = fmt.Fprintf(buf, ".%03d", now.UnixMilli()%1000)
}

// refresh rebuilds the prefix for the given second. It re-checks the atomic value
// after taking the lock, so that multiple goroutines do not format the same
// second twice.
func (c *TimeCache) refresh(unix int64, now time.Time) *timePrefix {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := c.prefix.Load(); p != nil && p.unix == unix {
		return p
	}
	// Allocate a new slice on every refresh: the old prefix must stay immutable,
	// otherwise reusing its backing array would let other goroutines that are
	// still reading it observe overwritten bytes. This happens at most once per
	// second, so the cost is negligible.
	p := &timePrefix{unix: unix, bytes: now.AppendFormat(nil, "2006-01-02 15:04:05")}
	c.prefix.Store(p)
	return p
}

// timeCache is the global singleton used for timestamp caching.
var timeCache = &TimeCache{}

// callerCache is a concurrent cache that maps full file paths to shortened ones.
// The shortened form keeps only "dir/file.go" instead of the full path, which
// keeps log lines smaller. sync.Map provides good read performance under
// concurrent logging.
var callerCache sync.Map

// callerResolveCache maps (format + skip-item fingerprint + stack PC prefix) to
// the resolved caller string. An identical call chain with the same merged skip
// rules and the same format hits the cache, so the frames do not have to be
// walked again on the hot path.
var callerResolveCache sync.Map

// callerSkipPrefixesMatch reports whether the fully qualified symbol fn should be
// skipped. Each item carries its own Mode:
//   - CallerSkipPrefix: boundary-safe prefix matching; "a/b" matches "a/b.Foo" and
//     "a/b/sub.Pkg", but not "a/bextra.Foo".
//   - CallerSkipContains: substring matching; "logit" matches any frame whose
//     function name contains "logit".
func callerSkipPrefixesMatch(fn string, items []CallerSkipConfigItem) bool {
	for _, item := range items {
		if item.Value == "" {
			continue
		}
		switch item.Mode {
		case CallerSkipPrefix:
			if fn == item.Value || strings.HasPrefix(fn, item.Value+".") || strings.HasPrefix(fn, item.Value+"/") {
				return true
			}
		case CallerSkipContains:
			if strings.Contains(fn, item.Value) {
				return true
			}
		}
	}
	return false
}

// fingerprintCallerPrefixes hashes the merged skip-item list for use as a cache key.
func fingerprintCallerPrefixes(items []CallerSkipConfigItem) uint64 {
	h := fnv.New64a()
	for _, item := range items {
		_, _ = h.Write([]byte{byte(item.Mode)})
		_, _ = h.Write([]byte(item.Value))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

const (
	// maxCallerFrames is the maximum number of stack frames captured per caller
	// resolution. Only the first non-logger frame after skipping is really needed,
	// and the logger and SDK wrappers are usually only a few levels deep; the
	// fixed-size array keeps pcs on the stack (the former make([]uintptr, 64)
	// escaped to the heap at 512B per log entry).
	maxCallerFrames = 16
	// maxCallerKeyPC is the maximum number of stack PCs that take part in the cache key.
	maxCallerKeyPC = 16
	// callerUnknown is the placeholder caller used when resolution fails.
	callerUnknown = "unknown:0"
	// callerKeyBufSize is the size of the cache key scratch buffer; it has to hold 16
	// base-36 PCs.
	callerKeyBufSize = 256
)

// callerKeyBuf is a fixed-size scratch buffer for cache keys.
// It uses [N]byte plus strconv.AppendUint instead of strings.Builder, which saves
// the builder allocation and avoids introducing a sync.Pool just for reuse (the
// boxing overhead would cost more than the allocation itself).
type callerKeyBuf [callerKeyBufSize]byte

// appendKey encodes the format, the fingerprint, and the PC list into the buffer and
// returns a slice that is safe to use as a map key. The encoding looks like
// "0|1abc|1x,2y,...", with the numbers in base 36.
func (k *callerKeyBuf) appendKey(prefixFP uint64, pcs []uintptr, format CallerFormat) []byte {
	b := k[:0]
	b = append(b, byte('0'+format), '|')
	b = strconv.AppendUint(b, prefixFP, 36)
	b = append(b, '|')
	for i, pc := range pcs {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendUint(b, uint64(pc), 36)
	}
	return b
}

// callerStackCacheKey builds a stable key from the format, the prefix fingerprint,
// and the PC list (used on cold paths and in tests).
func callerStackCacheKey(prefixFP uint64, pcs []uintptr, format CallerFormat) string {
	if len(pcs) > maxCallerKeyPC {
		pcs = pcs[:maxCallerKeyPC]
	}
	var kb callerKeyBuf
	return string(kb.appendKey(prefixFP, pcs, format))
}

// appendCallerFormatted appends the caller location to buf according to format:
//
//	CallerShort -> "dir/file.go:line"
//	CallerFull  -> "/full/path/dir/file.go:line"
//	CallerFunc  -> "import/path.Func:line"
func appendCallerFormatted(buf *bytes.Buffer, file string, line int, fnName string, format CallerFormat) {
	switch format {
	case CallerFull:
		buf.WriteString(file)
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(line))
	case CallerFunc:
		buf.WriteString(fnName)
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(line))
	default:
		// CallerShort: keep the last two path elements, cached in callerCache.
		appendCallerShortFile(buf, file, line)
	}
}

// appendCallerShortFile appends the shortened "dir/file.go:line" form for a known
// file path.
func appendCallerShortFile(buf *bytes.Buffer, file string, line int) {
	shortFile, ok := callerCache.Load(file)
	if !ok {
		// Keep the last two path elements: "package/file.go"
		idx := strings.LastIndexByte(file, '/')
		if idx != -1 {
			secondIdx := strings.LastIndexByte(file[:idx], '/')
			if secondIdx != -1 {
				shortFile = file[secondIdx+1:]
			} else {
				shortFile = file[idx+1:]
			}
		} else {
			shortFile = file
		}
		callerCache.Store(file, shortFile)
	}

	buf.WriteString(shortFile.(string))
	buf.WriteByte(':')
	buf.WriteString(strconv.Itoa(line))
}

// appendCaller appends the caller source location to the buffer.
//
// skip is the number of stack frames to skip, with the same meaning as in
// runtime.Caller(skip) (0 refers to the runtime.Caller call inside appendCaller
// itself). skipItems is the caller skip rule list, where each item carries its
// own Mode (nil skips nothing and is used only in tests). When skipItems is
// non-nil, prefixFP must equal fingerprintCallerPrefixes(skipItems). format
// controls how the caller string is rendered (CallerShort, CallerFull, or
// CallerFunc).
//
// The hot path performs no heap allocations: both pcs and the cache key buffer
// live on the stack.
func appendCaller(buf *bytes.Buffer, skip int, skipItems []CallerSkipConfigItem, prefixFP uint64, format CallerFormat) {
	var pcs [maxCallerFrames]uintptr
	n := runtime.Callers(skip+1, pcs[:])
	if n == 0 {
		buf.WriteString(callerUnknown)
		return
	}
	// Only the first maxCallerKeyPC frames take part in the cache key; captured and
	// the key are built from the same slice.
	captured := pcs[:n]
	if len(captured) > maxCallerKeyPC {
		captured = captured[:maxCallerKeyPC]
	}

	var kb callerKeyBuf
	cacheKey := string(kb.appendKey(prefixFP, captured, format))

	if v, ok := callerResolveCache.Load(cacheKey); ok {
		buf.WriteString(v.(string))
		return
	}

	frames := runtime.CallersFrames(captured)
	for {
		frame, more := frames.Next()
		if frame.Function == "" {
			if !more {
				break
			}
			continue
		}
		if len(skipItems) > 0 && callerSkipPrefixesMatch(frame.Function, skipItems) {
			if !more {
				break
			}
			continue
		}
		var tmp bytes.Buffer
		appendCallerFormatted(&tmp, frame.File, frame.Line, frame.Function, format)
		resolved := tmp.String()
		callerResolveCache.Store(cacheKey, resolved)
		buf.WriteString(resolved)
		return
	}
	callerResolveCache.Store(cacheKey, callerUnknown)
	buf.WriteString(callerUnknown)
}
