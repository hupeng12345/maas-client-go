package logit

import "context"

// TraceID defines the interface for extracting a trace ID from a context.
type TraceID interface {
	TraceID(ctx context.Context) string
}

// DefaultTraceID is the default TraceID implementation. It extracts a string
// value from the context using defaultTraceID as the key.
type DefaultTraceID struct{}

var defaultTraceID = DefaultTraceID{}

func (*DefaultTraceID) TraceID(ctx context.Context) string {
	t, ok := ctx.Value(defaultTraceID).(string)
	if ok {
		return t
	}
	return "-"
}

// WithDefaultTraceID stores traceID in the context under the default key used
// by DefaultTraceID.
func WithDefaultTraceID(ctx context.Context, traceID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, defaultTraceID, traceID)
}
