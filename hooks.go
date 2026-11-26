package maas

import (
	"context"
	"time"
)

// Hooks are optional lifecycle callbacks for plugging the SDK into your own
// instrumentation and troubleshooting tooling.
//
// Constraints:
//   - callbacks run synchronously on the request path; keep them cheap;
//   - callbacks must be safe for concurrent use (a single Client may be used
//     from many goroutines);
//   - a full API key is never exposed, only a masked form.
type Hooks struct {
	// OnRequest runs before the request is sent.
	OnRequest func(ctx context.Context, e RequestEvent)
	// OnResponse runs after the response (or the failure) is received.
	OnResponse func(ctx context.Context, e ResponseEvent)
	// OnRetry runs before a retry attempt.
	OnRetry func(ctx context.Context, e RetryEvent)
	// OnPoll runs after each asynchronous task poll.
	OnPoll func(ctx context.Context, e PollEvent)
}

// RequestEvent describes an outgoing request.
type RequestEvent struct {
	Op        string
	Method    string
	URL       string
	RequestID string
	// APIKeyMasked looks like "sk_abc1****9f2e"; use it to confirm which key
	// was used without ever handling the secret.
	APIKeyMasked string
}

// ResponseEvent describes the end of a request.
type ResponseEvent struct {
	Op        string
	Method    string
	URL       string
	Status    int
	RequestID string
	Duration  time.Duration
	Err       error
}

// RetryEvent describes a retry attempt.
type RetryEvent struct {
	Op      string
	Attempt int
	Delay   time.Duration
	Reason  string
}

// PollEvent describes one asynchronous task poll.
type PollEvent struct {
	Op       string
	TaskUUID string
	Status   string
	Attempt  int
}

// maskKey renders a key as "prefix****suffix", keeping just enough to identify it.
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	r := []rune(key)
	switch {
	case len(r) <= 8:
		return "****"
	case len(r) <= 16:
		return string(r[:3]) + "****" + string(r[len(r)-2:])
	default:
		return string(r[:6]) + "****" + string(r[len(r)-4:])
	}
}

// fireRequest invokes OnRequest when set.
func (h Hooks) fireRequest(ctx context.Context, e RequestEvent) {
	if h.OnRequest != nil {
		h.OnRequest(ctx, e)
	}
}

// fireResponse invokes OnResponse when set.
func (h Hooks) fireResponse(ctx context.Context, e ResponseEvent) {
	if h.OnResponse != nil {
		h.OnResponse(ctx, e)
	}
}

// fireRetry invokes OnRetry when set.
func (h Hooks) fireRetry(ctx context.Context, e RetryEvent) {
	if h.OnRetry != nil {
		h.OnRetry(ctx, e)
	}
}

// firePoll invokes OnPoll when set.
func (h Hooks) firePoll(ctx context.Context, e PollEvent) {
	if h.OnPoll != nil {
		h.OnPoll(ctx, e)
	}
}
