package ocr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	maas "github.com/hupeng12345/maas-client-go"
)

// submitVision uploads a file to an asynchronous endpoint and returns the task.
func (s *Service) submitVision(ctx context.Context, op, path string, file maas.FileSource, fields map[string]any) (*Task, error) {
	env, meta, err := s.callVision(ctx, op, path, file, fields)
	if err != nil {
		return nil, err
	}

	var data wireTaskSubmit
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			e := maas.NewProtocolError(op, "task submission payload is not an object", meta.HTTPStatus, env.Data)
			e.RequestID = meta.RequestID
			return nil, e
		}
	}
	if data.TaskUUID == "" {
		e := maas.NewProtocolError(op, "task submission did not return task_uuid", meta.HTTPStatus, env.Data)
		e.RequestID = meta.RequestID
		return nil, e
	}

	return &Task{
		UUID:   data.TaskUUID,
		Status: TaskStatus(data.TaskStatus),
		Meta:   meta,
		svc:    s,
		op:     op,
	}, nil
}

// invokeVision calls a synchronous endpoint and returns the result directly.
func (s *Service) invokeVision(ctx context.Context, op, path string, file maas.FileSource, fields map[string]any) (*VisionResult, error) {
	env, meta, err := s.callVision(ctx, op, path, file, fields)
	if err != nil {
		return nil, err
	}
	return s.decodeVisionResult(op, env, meta)
}

// callVision performs the multipart request and validates the envelope.
func (s *Service) callVision(ctx context.Context, op, path string, file maas.FileSource, fields map[string]any) (*maas.Envelope, maas.ResponseMeta, error) {
	if err := s.c.RequireBaseURL(op); err != nil {
		return nil, maas.ResponseMeta{}, err
	}
	if file == nil {
		return nil, maas.ResponseMeta{}, maas.NewLocalError(op, "no file provided", "pass maas.File(...) to specify the upload")
	}
	if max := s.c.MaxUploadSize(); max > 0 {
		if size := file.Size(); size > max {
			return nil, maas.ResponseMeta{}, &maas.Error{
				Kind:    maas.KindLocal,
				Message: "file exceeds the local size limit",
				Hint:    "raise WithMaxUploadSize or split the input",
				Op:      op,
			}
		}
	}

	body, err := maas.NewMultipart(file, fields)
	if err != nil {
		return nil, maas.ResponseMeta{}, err
	}
	defer func() { _ = body.Reader.Close() }()

	req, err := s.c.NewRequest(ctx, http.MethodPost, path, body.Reader)
	if err != nil {
		return nil, maas.ResponseMeta{}, err
	}
	req.Header.Set("Content-Type", body.ContentType)
	if body.ContentLength > 0 {
		req.ContentLength = body.ContentLength
	}

	resp, meta, err := s.c.Do(req, op)
	if err != nil {
		return nil, meta, err
	}
	defer drainAndClose(resp.Body)

	env, err := maas.DecodeEnvelope(op, resp)
	if err != nil {
		var e *maas.Error
		if errors.As(err, &e) {
			e.RequestID = meta.RequestID
		}
		return nil, meta, err
	}
	if !env.IsSuccess() {
		return nil, meta, env.Error(op, resp)
	}
	return env, meta, nil
}

// queryVision fetches the current state of a task.
func (s *Service) queryVision(ctx context.Context, op, taskUUID string) (*VisionResult, error) {
	if err := s.c.RequireBaseURL(op); err != nil {
		return nil, err
	}
	req, err := s.c.NewRequest(ctx, http.MethodGet, queryPath(taskUUID), nil)
	if err != nil {
		return nil, err
	}

	resp, meta, err := s.c.Do(req, op)
	if err != nil {
		return nil, err
	}
	defer drainAndClose(resp.Body)

	env, err := maas.DecodeEnvelope(op, resp)
	if err != nil {
		var e *maas.Error
		if errors.As(err, &e) {
			e.RequestID = meta.RequestID
			e.TaskUUID = taskUUID
		}
		return nil, err
	}
	if !env.IsSuccess() {
		e := env.Error(op, resp)
		e.TaskUUID = taskUUID
		return nil, e
	}
	return s.decodeVisionResult(op, env, meta)
}

// decodeVisionResult turns envelope data into a VisionResult.
func (s *Service) decodeVisionResult(op string, env *maas.Envelope, meta maas.ResponseMeta) (*VisionResult, error) {
	var wire wireVision
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &wire); err != nil {
			e := maas.NewProtocolError(op, "result payload is not an object", meta.HTTPStatus, env.Data)
			e.RequestID = meta.RequestID
			return nil, e
		}
	}
	return wire.toVisionResult(meta, cloneRaw(env.Data), s.c.KeepRaw()), nil
}

// cloneRaw copies raw JSON so it can outlive the response buffer.
func cloneRaw(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(b))
	copy(out, b)
	return out
}

// drainAndClose reads and closes a body so the connection can be reused.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}
