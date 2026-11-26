package logit

import "io"

// SimpleLogger is the concrete Logger implementation. It writes human-readable
// structured log lines to the underlying io.Writer. It embeds baseLogger, which
// provides all the core level checking and formatting logic.
type SimpleLogger struct {
	baseLogger
}

// Write implements io.Writer and forwards raw bytes to the underlying writer. It
// lets the logger itself be used as an io.Writer for raw output on the same
// destination. If the underlying writer is nil, it does nothing and reports
// success.
func (s *SimpleLogger) Write(p []byte) (n int, err error) {
	if s.w == nil {
		return 0, nil
	}
	return s.w.Write(p)
}

// NewSimple creates a new SimpleLogger with the default configuration:
//   - minimum level: Info
//
// If w is nil, output is discarded to io.Discard.
func NewSimple(w io.Writer) Logger {
	return NewSimpleWithConfig(w, nil)
}

// NewSimpleWithConfig creates a new SimpleLogger with the provided configuration.
// The user configuration is merged with the defaults: non-empty or non-zero
// fields override them. CallerSkip.List is merged with the built-in defaults,
// which always include this package and the SDK root package. If w is nil,
// output is discarded to io.Discard.
func NewSimpleWithConfig(w io.Writer, config *Config) Logger {
	if w == nil {
		w = io.Discard
	}
	s := &SimpleLogger{}
	s.w = w
	s.config = mergeConfig(config)
	return s
}

func (s *SimpleLogger) withLevel(level Level) Logger {
	cp := *s
	cfg := *s.config
	cfg.Level = level
	cp.config = &cfg
	return &cp
}

// Discard returns a Logger that drops all output.
//
// It is meant for explicitly turning logging off, for example in tests or on
// latency-critical paths:
//
//	client, _ := maas.New(maas.WithAPIKey(key), maas.WithLogger(logit.Discard()))
func Discard() Logger {
	return NewSimple(nil)
}
