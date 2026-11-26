// Package logit provides structured text logging: SimpleLogger (the default
// implementation), the Logger interface, lazily evaluated Field and FieldFunc
// constructors, and key-value pairs attached to a context.Context through
// logit/lctx so that they appear automatically on every log line.
package logit

import (
	"bytes"
	"context"
	"io"

	"github.com/hupeng12345/maas-client-go/logit/lctx"
)

// Level is a log level, used for filtering and for inline rendering.
type Level string

// CallerFormat controls how the caller location is rendered in the log line.
type CallerFormat uint8

const (
	// CallerShort renders "dir/file.go:line" (the last two path elements) and is the
	// default.
	CallerShort CallerFormat = iota
	// CallerFull renders the full absolute path "/full/path/dir/file.go:line".
	CallerFull
	// CallerFunc renders the fully qualified function name "import/path.Func:line".
	CallerFunc
)

// CallerSkipMode controls how a single CallerSkipConfigItem matches a function name.
type CallerSkipMode uint8

const (
	// CallerSkipPrefix uses boundary-safe prefix matching: "a/b" matches "a/b.Foo"
	// and "a/b/sub.Pkg", but not "a/bextra.Foo".
	CallerSkipPrefix CallerSkipMode = iota
	// CallerSkipContains uses substring matching: "logit" matches any frame whose
	// fully qualified function name contains "logit".
	CallerSkipContains
)

const (
	Trace Level = "trace"
	Debug Level = "DEBUG"
	Info  Level = "INFO"
	Warn  Level = "WARN"
	Error Level = "ERROR"
)

const (
	// Separator separates the major segments of a log line.
	Separator = " "
	// NewLine is appended to the end of every log line.
	NewLine = "\n"
)

// levelOrder maps every log level to its numeric severity rank. A higher value
// means a more severe level. It is used for filtering: only entries with
// rank >= minLevel are emitted.
var levelOrder = map[Level]int{
	Trace: 0,
	Debug: 10,
	Info:  20,
	Warn:  50,
	Error: 100,
}

// Logger is implemented by SimpleLogger and may also be implemented by other
// backends.
//
// Info, Debug, and Warn record a message with optional structured fields.
// Error records an entry when err is non-nil; see SimpleLogger.Error for how
// the message is handled.
//
// Embedding io.Writer allows raw writes to the same output as the structured
// log entries.
type Logger interface {
	io.Writer
	Trace(ctx context.Context, msg string, fields ...FieldFunc)
	Info(ctx context.Context, msg string, fields ...FieldFunc)
	Debug(ctx context.Context, msg string, fields ...FieldFunc)
	Warn(ctx context.Context, msg string, fields ...FieldFunc)
	Error(ctx context.Context, err error, fields ...FieldFunc)
}

type levelScopedLogger interface {
	withLevel(level Level) Logger
}

// PackageImportPath is the import path of this package. It is always merged into
// the caller skip configuration so that frames inside the logit package (and its
// subpackages, such as logit/lctx) are skipped.
const PackageImportPath = "/maas-client-go/logit"

// SDKImportPath is the import path of the SDK root package. It is skipped by
// default as well, so the caller reported in the log points at application code
// rather than at SDK internals.
const SDKImportPath = "/maas-client-go"

// WithLevel returns a Logger scoped to the given minimum level without modifying the
// original logger.
func WithLevel(logger Logger, level Level) Logger {
	if logger == nil {
		return nil
	}
	if scoped, ok := logger.(levelScopedLogger); ok {
		return scoped.withLevel(level)
	}
	return logger
}

// Config controls the minimum level and the caller annotation behavior of a
// SimpleLogger.
type Config struct {
	PodName string
	// Level is the minimum level that is emitted; entries below it are dropped.
	Level Level
	// CallerSkip configures the skip rules for caller frames and the display format.
	// The built-in defaults (this package and the SDK root package, using
	// CallerSkipContains mode) are always prepended to List. The zero value means
	// built-in defaults only, rendered with CallerShort.
	CallerSkip CallerSkipConfig

	// Internal field: fingerprint of CallerSkip.List, set by mergeConfig.
	callerSkipFP uint64

	TraceID TraceID // trace ID provider
}

// CallerSkipConfig groups the caller skip settings. Built-in defaults are merged
// into List by mergeConfig.
type CallerSkipConfig struct {
	// Format controls how the caller location is rendered in the log output.
	// CallerShort (default) renders "dir/file.go:123", CallerFull the absolute
	// path, and CallerFunc the fully qualified function name.
	Format CallerFormat `json:"format"`
	// List holds the caller skip rules. Each entry specifies a Value and how it is
	// matched (Mode). Built-in defaults are prepended during merging; duplicate
	// values are removed, keeping the first occurrence.
	List []CallerSkipConfigItem `json:"list"`
}

// CallerSkipConfigItem is a single caller skip rule.
type CallerSkipConfigItem struct {
	// Value is the import path prefix or package name string to skip.
	Value string `json:"value"`
	// Mode controls how Value is matched: CallerSkipPrefix (boundary-safe) or
	// CallerSkipContains (substring).
	Mode CallerSkipMode `json:"mode"`
}

func defaultConfig() *Config {
	return &Config{
		Level:   Info,
		PodName: "AppName",
		TraceID: &DefaultTraceID{},
	}
}

// builtinCallerSkipItems returns the built-in skip items, which always use
// CallerSkipContains mode so that all frames of this package and of the SDK
// root package are skipped.
func builtinCallerSkipItems() []CallerSkipConfigItem {
	return []CallerSkipConfigItem{
		{Value: PackageImportPath, Mode: CallerSkipContains},
		{Value: SDKImportPath, Mode: CallerSkipContains},
	}
}

// mergeCallerSkipItems merges the built-in items with the user-provided items,
// de-duplicating by Value. Built-in items come first, followed by the user items
// in their original order.
func mergeCallerSkipItems(user []CallerSkipConfigItem) []CallerSkipConfigItem {
	builtins := builtinCallerSkipItems()
	seen := make(map[string]struct{}, len(builtins)+len(user))
	out := make([]CallerSkipConfigItem, 0, len(builtins)+len(user))
	for _, item := range builtins {
		if item.Value == "" {
			continue
		}
		if _, ok := seen[item.Value]; ok {
			continue
		}
		seen[item.Value] = struct{}{}
		out = append(out, item)
	}
	for _, item := range user {
		if item.Value == "" {
			continue
		}
		if _, ok := seen[item.Value]; ok {
			continue
		}
		seen[item.Value] = struct{}{}
		out = append(out, item)
	}
	return out
}

// mergeConfig returns a new Config with the defaults merged with the user
// overrides. If c is nil, the default configuration is returned. Non-zero fields
// in c override the defaults. CallerSkip.List is merged with the built-in
// defaults (this package and the SDK root package).
func mergeConfig(c *Config) *Config {
	out := *defaultConfig()
	if c != nil {
		if c.Level != "" {
			out.Level = c.Level
		}
		if c.PodName != "" {
			out.PodName = c.PodName
		}
		if c.TraceID != nil {
			out.TraceID = c.TraceID
		}
		out.CallerSkip = c.CallerSkip
	}
	out.CallerSkip.List = mergeCallerSkipItems(out.CallerSkip.List)
	out.callerSkipFP = fingerprintCallerPrefixes(out.CallerSkip.List)
	return &out
}

// levelRank returns the numeric severity rank for a log level. The boolean reports
// whether the level is recognized.
func levelRank(l Level) (int, bool) {
	v, ok := levelOrder[l]
	return v, ok
}

// baseLogger provides the core logging behavior shared by concrete logger
// implementations. It handles level checks, output formatting, and writing to the
// underlying io.Writer.
type baseLogger struct {
	w      io.Writer // destination writer
	config *Config   // logger configuration (level, caller settings)
}

// shouldLog reports whether a message at the given level should be emitted. It
// compares the message level with the configured minimum level. If either level
// is unrecognized, it falls back to a safe default.
func (s *baseLogger) shouldLog(level Level) bool {
	lv, lok := levelRank(level)
	if !lok {
		lv = levelOrder[Info]
	}
	minLevel, mok := levelRank(s.config.Level)
	if !mok {
		minLevel = levelOrder[Debug]
	}
	return lv >= minLevel
}

// writeLine formats and writes a single log line when the level passes the
// filter. It builds the line in a buffer and writes it to the underlying writer.
// Errors returned by Write are silently ignored, since logging must not be
// interrupted by a write failure.
func (s *baseLogger) writeLine(level Level, msg string, ctx context.Context, data []FieldFunc) {
	if !s.shouldLog(level) {
		return
	}
	var sb bytes.Buffer
	s.output(&sb, level, msg, ctx, data...)
	_, _ = s.w.Write(sb.Bytes())
}

func getRequestIDByCtx(ctx context.Context, config *Config) string {
	if ctx == nil || config == nil || config.TraceID == nil {
		return "-"
	}
	return config.TraceID.TraceID(ctx)
}

// output renders a complete log line into the provided buffer.
// The format (inspired by log4j) is: timestamp [app] [Thread] level
// [caller:line] context fields msg[message] key[value]...
// All context fields from lctx are included automatically before the message.
func (s *baseLogger) output(bf *bytes.Buffer, level Level, msg string, ctx context.Context, data ...FieldFunc) {
	formatLogLine(bf, s.config.PodName, string(level), getRequestIDByCtx(ctx, s.config), msg, ctx, data, func() {
		appendCaller(bf, 4, s.config.CallerSkip.List, s.config.callerSkipFP, s.config.CallerSkip.Format)
	})
}

// formatLogLine writes a complete standardized log line into the buffer. It is the
// single source of truth for the log format of every logger.
func formatLogLine(bf *bytes.Buffer, appName, level, traceID, msg string, ctx context.Context, fields []FieldFunc, appendCaller func()) {
	timeCache.appendFormattedTimestamp(bf)
	bf.WriteString(Separator)

	bf.WriteByte('[')
	bf.WriteString(appName)
	bf.WriteByte(']')
	bf.WriteString(Separator)

	bf.WriteString("[Thread]")
	bf.WriteString(Separator)

	bf.WriteString(level)
	bf.WriteString(Separator)

	bf.WriteByte('[')
	appendCaller()
	bf.WriteString("]")
	bf.WriteString(Separator)

	bf.WriteByte('[')
	bf.WriteString(traceID)
	bf.WriteByte(']')
	bf.WriteString(Separator)

	// context fields
	beforeCtxLen := bf.Len()
	outputCtx(bf, ctx)
	if bf.Len() > beforeCtxLen {
		bf.WriteString(Separator)
	}

	bf.WriteString("msg[")
	bf.WriteString(msg)
	bf.WriteByte(']')

	for _, field := range fields {
		if field == nil {
			continue
		}
		k, v := field().Value()
		bf.WriteString(Separator)
		bf.WriteString(k)
		bf.WriteByte('[')
		bf.WriteString(v)
		bf.WriteByte(']')
	}

	bf.WriteString(NewLine)
}

// outputCtx appends all structured fields of the provided context to the buffer.
// The key-value pairs are extracted with lctx.ListPair and formatted with the
// same key[value] form.
func outputCtx(bf *bytes.Buffer, ctx context.Context) {
	if ctx == nil {
		return
	}
	ps := lctx.ListPair(ctx)
	if len(ps) == 0 {
		return
	}
	for _, pair := range ps {
		bf.WriteByte(' ')
		bf.WriteString(pair.Key)
		bf.WriteByte('[')
		bf.WriteString(pair.Value)
		bf.WriteByte(']')
	}
}

// Trace logs a message at Trace level with optional structured fields.
func (s *baseLogger) Trace(ctx context.Context, msg string, data ...FieldFunc) {
	s.writeLine(Trace, msg, ctx, data)
}

// Info logs a message at Info level with optional structured fields.
func (s *baseLogger) Info(ctx context.Context, msg string, data ...FieldFunc) {
	s.writeLine(Info, msg, ctx, data)
}

// Debug logs a message at Debug level with optional structured fields.
func (s *baseLogger) Debug(ctx context.Context, msg string, data ...FieldFunc) {
	s.writeLine(Debug, msg, ctx, data)
}

// Warn logs a message at Warn level with optional structured fields.
func (s *baseLogger) Warn(ctx context.Context, msg string, data ...FieldFunc) {
	s.writeLine(Warn, msg, ctx, data)
}

// Error logs an error at Error level. If err is nil, nothing is logged. The error is
// automatically added to the entry as the structured field "error".
func (s *baseLogger) Error(ctx context.Context, err error, data ...FieldFunc) {
	if err == nil || !s.shouldLog(Error) {
		return
	}
	fields := append([]FieldFunc{ErrorField("error", err)}, data...)
	s.writeLine(Error, "", ctx, fields)
}
