package logit_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hupeng12345/maas-client-go/logit"
)

// newLogger returns a logger writing into buf.
func newLogger(t *testing.T, level logit.Level) (*bytes.Buffer, logit.Logger) {
	t.Helper()
	buf := &bytes.Buffer{}
	return buf, logit.NewSimpleWithConfig(buf, &logit.Config{PodName: "test", Level: level})
}

func TestLevelFiltering(t *testing.T) {
	cases := []struct {
		configured logit.Level
		emit       func(logit.Logger)
		wantOutput bool
	}{
		{logit.Info, func(l logit.Logger) { l.Debug(context.Background(), "dbg") }, false},
		{logit.Info, func(l logit.Logger) { l.Info(context.Background(), "inf") }, true},
		{logit.Info, func(l logit.Logger) { l.Warn(context.Background(), "wrn") }, true},
		{logit.Info, func(l logit.Logger) { l.Error(context.Background(), errors.New("e")) }, true},
		{logit.Debug, func(l logit.Logger) { l.Debug(context.Background(), "dbg") }, true},
		{logit.Warn, func(l logit.Logger) { l.Info(context.Background(), "inf") }, false},
		{logit.Error, func(l logit.Logger) { l.Warn(context.Background(), "wrn") }, false},
	}

	for _, tc := range cases {
		t.Run(string(tc.configured), func(t *testing.T) {
			buf, logger := newLogger(t, tc.configured)
			tc.emit(logger)
			if got := buf.Len() > 0; got != tc.wantOutput {
				t.Fatalf("output present = %v, want %v (log: %q)", got, tc.wantOutput, buf.String())
			}
		})
	}
}

func TestErrorIgnoresNil(t *testing.T) {
	buf, logger := newLogger(t, logit.Info)
	logger.Error(context.Background(), nil)
	if buf.Len() != 0 {
		t.Fatalf("nil error must not log: %q", buf.String())
	}
}

func TestFieldRendering(t *testing.T) {
	cases := []struct {
		name  string
		field logit.FieldFunc
		want  string
	}{
		{"string", logit.StringField("k", "v"), "k[v]"},
		{"int", logit.IntField("k", -3), "k[-3]"},
		{"int64", logit.Int64Field("k", 12), "k[12]"},
		{"bool", logit.BoolField("k", true), "k[true]"},
		{"float", logit.Float64Field("k", 1.5), "k[1.5]"},
		{"duration", logit.TimeDurationField("k", time.Second, logit.WithTimeDurationFormatMethod(logit.TimeFormatMSUnit)), "k[1000ms]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, logger := newLogger(t, logit.Info)
			logger.Info(context.Background(), "msg", tc.field)
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("log %q does not contain %q", buf.String(), tc.want)
			}
		})
	}
}

func TestErrorField(t *testing.T) {
	buf, logger := newLogger(t, logit.Info)
	logger.Error(context.Background(), errors.New("boom"))
	if got := buf.String(); !strings.Contains(got, "boom") || !strings.Contains(got, "error[") {
		t.Fatalf("error field missing: %q", got)
	}
}

// Fields are evaluated lazily: a filtered-out call must not build them.
func TestFieldsAreLazy(t *testing.T) {
	built := 0
	field := func() logit.Field {
		built++
		return logit.StringField("k", "v")()
	}

	buf, logger := newLogger(t, logit.Error)
	logger.Info(context.Background(), "filtered", field)
	if built != 0 {
		t.Fatalf("field built for a filtered call (%d)", built)
	}
	if buf.Len() != 0 {
		t.Fatalf("filtered call produced output: %q", buf.String())
	}

	logger.Error(context.Background(), errors.New("x"), field)
	if built == 0 {
		t.Fatal("field was not built for an emitted call")
	}
}

func TestWithLevel(t *testing.T) {
	buf, logger := newLogger(t, logit.Info)
	verbose := logit.WithLevel(logger, logit.Debug)
	verbose.Debug(context.Background(), "dbg")
	if !strings.Contains(buf.String(), "dbg") {
		t.Fatalf("WithLevel did not lower the level: %q", buf.String())
	}

	// The derived logger must not affect the original.
	buf.Reset()
	logger.Debug(context.Background(), "dbg2")
	if buf.Len() != 0 {
		t.Fatalf("original logger changed: %q", buf.String())
	}
}

func TestDiscard(t *testing.T) {
	logger := logit.Discard()
	if logger == nil {
		t.Fatal("Discard must return a logger")
	}
	logger.Info(context.Background(), "nothing")
}

func TestWritePassesThrough(t *testing.T) {
	buf, logger := newLogger(t, logit.Info)
	if _, err := logger.Write([]byte("raw")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "raw" {
		t.Fatalf("write passthrough failed: %q", buf.String())
	}
}

func TestConcurrentWrites(t *testing.T) {
	buf := &syncBuffer{}
	logger := logit.NewSimpleWithConfig(buf, &logit.Config{Level: logit.Info})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				logger.Info(context.Background(), "line", logit.IntField("j", j))
			}
		}()
	}
	wg.Wait()

	if got := strings.Count(buf.String(), "\n"); got != 16*50 {
		t.Fatalf("expected %d lines, got %d", 16*50, got)
	}
}

// syncBuffer is a concurrency-safe buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
