package ocr

import (
	"context"
	"errors"
	"math/rand"
	"net/url"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
)

// Task is a handle to an asynchronous OCR job.
type Task struct {
	// UUID is the task identifier returned by the submission.
	UUID string
	// Status is the status reported at submission time.
	Status TaskStatus
	// Meta carries the request ID of the submission call.
	Meta maas.ResponseMeta

	svc *Service
	op  string
}

// Well-known actions for turning a task outcome into an error hint.
const (
	actionRetryInput = "submit a new task after fixing the input"
	actionKeepTask   = "keep the task_uuid and query it later"
)

// Get performs a single status query.
//
// A successful query does not imply a successful task: inspect TaskStatus.
func (t *Task) Get(ctx context.Context) (*VisionResult, error) {
	return t.svc.queryVision(ctx, "ocr.Task.Get", t.UUID)
}

// Wait polls until the task reaches a terminal state.
//
// It returns the final result when the task succeeds. FAILED and CANCELED
// produce a *maas.Error with Kind KindTask that still carries the task UUID.
func (t *Task) Wait(ctx context.Context, opts ...WaitOption) (*VisionResult, error) {
	if t.UUID == "" {
		return nil, maas.NewLocalError("ocr.Task.Wait", "task uuid is empty", "pass the task returned by the submission")
	}

	cfg := waitConfig{policy: t.svc.c.PollPolicy()}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	deadline := time.Now().Add(cfg.policy.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	delay := cfg.policy.InitialDelay
	attempt := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, &maas.Error{
				Kind:     maas.KindTask,
				Message:  "stopped waiting for the task",
				Hint:     "the context was canceled; the platform keeps processing the task",
				TaskUUID: t.UUID,
				Op:       "ocr.Task.Wait",
				Err:      err,
			}
		}

		attempt++
		queryCtx, cancel := context.WithTimeout(ctx, cfg.policy.QueryTimeout)
		res, err := t.svc.queryVision(queryCtx, "ocr.Task.Wait", t.UUID)
		cancel()
		if err != nil {
			var e *maas.Error
			if errors.As(err, &e) {
				e.TaskUUID = t.UUID
			}
			return nil, err
		}

		if cfg.onStatus != nil {
			cfg.onStatus(ctx, res.TaskStatus, attempt)
		}
		if h := t.svc.c.Hooks(); h.OnPoll != nil {
			h.OnPoll(ctx, maas.PollEvent{
				Op: "ocr.Task.Wait", TaskUUID: t.UUID, Status: string(res.TaskStatus), Attempt: attempt,
			})
		}

		switch res.TaskStatus {
		case TaskSuccess:
			return res, nil
		case TaskFailed, TaskCanceled:
			return res, &maas.Error{
				Kind:       maas.KindTask,
				Message:    "task did not succeed",
				Hint:       "inspect the result for details; " + actionRetryInput,
				HTTPStatus: res.Meta.HTTPStatus,
				RequestID:  res.Meta.RequestID,
				TaskUUID:   t.UUID,
				Op:         "ocr.Task.Wait",
			}
		}

		if time.Now().Add(delay).After(deadline) {
			return res, &maas.Error{
				Kind:       maas.KindTask,
				Message:    "timed out waiting for the task",
				Hint:       "raise WithWaitTimeout, or " + actionKeepTask,
				HTTPStatus: res.Meta.HTTPStatus,
				RequestID:  res.Meta.RequestID,
				TaskUUID:   t.UUID,
				Op:         "ocr.Task.Wait",
				Retryable:  true,
			}
		}

		select {
		case <-ctx.Done():
			return nil, &maas.Error{
				Kind:     maas.KindTask,
				Message:  "stopped waiting for the task",
				Hint:     "the context was canceled; the platform keeps processing the task",
				TaskUUID: t.UUID,
				Op:       "ocr.Task.Wait",
				Err:      ctx.Err(),
			}
		case <-time.After(jitter(delay, cfg.policy.Jitter)):
		}

		delay = nextDelay(delay, &cfg.policy)
	}
}

// waitConfig is the resolved waiting configuration.
type waitConfig struct {
	policy   maas.PollPolicy
	onStatus func(ctx context.Context, status TaskStatus, attempt int)
}

// WaitOption customises a single Wait call.
type WaitOption func(*waitConfig)

// WithPollInterval sets the first interval between polls.
func WithPollInterval(d time.Duration) WaitOption {
	return func(c *waitConfig) {
		if d > 0 {
			c.policy.InitialDelay = d
		}
	}
}

// WithMaxPollInterval caps the interval between polls.
func WithMaxPollInterval(d time.Duration) WaitOption {
	return func(c *waitConfig) {
		if d > 0 {
			c.policy.MaxDelay = d
		}
	}
}

// WithWaitTimeout sets the total time to wait for the task.
func WithWaitTimeout(d time.Duration) WaitOption {
	return func(c *waitConfig) {
		if d > 0 {
			c.policy.Timeout = d
		}
	}
}

// WithPollCallback installs a per-poll callback, useful for progress reporting.
func WithPollCallback(fn func(ctx context.Context, status TaskStatus, attempt int)) WaitOption {
	return func(c *waitConfig) { c.onStatus = fn }
}

// nextDelay computes the next backoff interval.
func nextDelay(d time.Duration, p *maas.PollPolicy) time.Duration {
	next := time.Duration(float64(d) * p.Multiplier)
	if next > p.MaxDelay {
		next = p.MaxDelay
	}
	return next
}

// jitter applies a ratio to d.
func jitter(d time.Duration, ratio float64) time.Duration {
	if ratio <= 0 {
		return d
	}
	delta := float64(d) * ratio
	offset := (rand.Float64()*2 - 1) * delta
	out := time.Duration(float64(d) + offset)
	if out < 0 {
		out = 0
	}
	return out
}

// queryPath builds the result query URL.
func queryPath(taskUUID string) string {
	return pathResult + "?task_uuid=" + url.QueryEscape(taskUUID)
}
