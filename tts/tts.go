// Package tts provides text-to-speech synthesis.
//
// The platform returns raw 16-bit little-endian PCM without a container. Audio
// is streamed: you can copy it straight to a player or use WriteWAV to produce a
// playable file.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	maas "github.com/hupeng12345/maas-client-go"
)

// pathSpeech is the synthesis endpoint.
const pathSpeech = "/tts/v1/speech"

// Service synthesises speech.
type Service struct {
	c *maas.Client
}

// New builds a Service on top of a client.
func New(c *maas.Client) *Service { return &Service{c: c} }

// SpeechRequest is the text-to-speech request body.
//
// Pointer fields distinguish "not set" from an explicit false or zero, which the
// server treats differently.
type SpeechRequest struct {
	// Model is the logical model name assigned to your account (required).
	Model string `json:"model"`
	// Input is the text or supported SSML (required).
	Input string `json:"input"`
	// Voice is a voice_id supported by the model (required).
	Voice string `json:"voice"`

	// Language is a BCP 47 tag such as "zh-CN" or "ja-JP".
	Language string `json:"language,omitempty"`
	// ResponseFormat is only "pcm" today.
	ResponseFormat string `json:"response_format,omitempty"`
	// Stream asks for chunked delivery (default true server-side).
	Stream *bool `json:"stream,omitempty"`
	// Sample is the requested sample rate in Hz.
	Sample int `json:"sample,omitempty"`
	// Speed is 0.5-2.0.
	Speed float64 `json:"speed,omitempty"`
	// LoudRatio is 0.0-2.0.
	LoudRatio float64 `json:"loud_ratio,omitempty"`
	// Pitch is -12-12 semitones.
	Pitch int `json:"pitch,omitempty"`

	// Endpoint overrides the request path for this call. It is never serialised.
	Endpoint string `json:"-"`

	Seed          *int64   `json:"seed,omitempty"`
	Instructions  *string  `json:"instructions,omitempty"`
	BGMID         *string  `json:"bgm_id,omitempty"`
	BGMVol        *float64 `json:"bgm_vol,omitempty"`
	Use8kEnhancer *bool    `json:"use_8k_enhancer,omitempty"`
}

// speechOptions backs the SpeechOption builders.
type speechOptions struct {
	req SpeechRequest
}

// SpeechOption customises a Say call.
type SpeechOption func(*speechOptions)

// WithModel sets the TTS model name.
func WithModel(model string) SpeechOption {
	return func(o *speechOptions) { o.req.Model = model }
}

// WithVoice sets the voice ID.
func WithVoice(voice string) SpeechOption {
	return func(o *speechOptions) { o.req.Voice = voice }
}

// WithLanguage sets the language tag.
func WithLanguage(lang string) SpeechOption {
	return func(o *speechOptions) { o.req.Language = lang }
}

// WithSample sets the requested sample rate.
func WithSample(rate int) SpeechOption {
	return func(o *speechOptions) { o.req.Sample = rate }
}

// WithSpeed sets the speaking rate (0.5-2.0).
func WithSpeed(speed float64) SpeechOption {
	return func(o *speechOptions) { o.req.Speed = speed }
}

// WithPitch sets the pitch offset in semitones (-12-12).
func WithPitch(pitch int) SpeechOption {
	return func(o *speechOptions) { o.req.Pitch = pitch }
}

// WithStream controls chunked delivery.
func WithStream(on bool) SpeechOption {
	return func(o *speechOptions) { o.req.Stream = &on }
}

// WithBGM mixes in a background track.
func WithBGM(id string) SpeechOption {
	return func(o *speechOptions) { o.req.BGMID = &id }
}

// WithEndpoint overrides the request path for this call.
//
// Use it when a deployment exposes a different path than the default one.
func WithEndpoint(path string) SpeechOption {
	return func(o *speechOptions) { o.req.Endpoint = path }
}

// WithInstructions sets style instructions, where the model supports them.
func WithInstructions(text string) SpeechOption {
	return func(o *speechOptions) { o.req.Instructions = &text }
}

// Speech synthesises the request and returns a streaming audio handle.
//
// The caller must close the returned Audio. Success is decided by the response
// Content-Type, not by the status code alone: the platform answers JSON on
// failure even when the HTTP status looks successful.
func (s *Service) Speech(ctx context.Context, req SpeechRequest) (*Audio, error) {
	const op = "tts.Speech"

	if err := s.c.RequireBaseURL(op); err != nil {
		return nil, err
	}
	if err := validate(req); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, &maas.Error{Kind: maas.KindLocal, Message: "cannot encode request", Hint: "check the request fields", Op: op, Err: err}
	}

	path := pathSpeech
	if strings.TrimSpace(req.Endpoint) != "" {
		path = req.Endpoint
	}
	httpReq, err := s.c.NewRequest(ctx, http.MethodPost, path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "audio/pcm, application/json")

	resp, meta, err := s.c.Do(httpReq, op, maas.Streaming())
	if err != nil {
		return nil, err
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(mediaType, "audio/") {
		return nil, audioFailure(op, resp, meta)
	}

	return &Audio{
		rc:          resp.Body,
		SampleRate:  parseHeaderInt(resp.Header.Get("X-Sample-Rate"), 8000),
		Channels:    parseHeaderInt(resp.Header.Get("X-Audio-Channels"), 1),
		Format:      headerOr(resp.Header.Get("X-Audio-Format"), "pcm_s16le"),
		ContentType: mediaType,
		Meta:        meta,
	}, nil
}

// validate checks the required fields and immutable options.
func validate(req SpeechRequest) error {
	const op = "tts.Speech"
	switch {
	case strings.TrimSpace(req.Model) == "":
		return maas.NewLocalError(op, "model is required", "pass the TTS model name assigned to your account")
	case strings.TrimSpace(req.Input) == "":
		return maas.NewLocalError(op, "input is required", "pass the text to synthesise")
	case strings.TrimSpace(req.Voice) == "":
		return maas.NewLocalError(op, "voice is required", "pass a voice_id supported by the model")
	case req.ResponseFormat != "" && req.ResponseFormat != "pcm":
		return maas.NewLocalError(op, `response_format must be "pcm"`, "the platform only returns raw PCM today")
	}
	return nil
}

// audioFailure converts a non-audio response into an error and closes the body.
func audioFailure(op string, resp *http.Response, meta maas.ResponseMeta) error {
	defer drainAndClose(resp.Body)

	env, err := maas.DecodeEnvelope(op, resp)
	if err != nil {
		var e *maas.Error
		if errors.As(err, &e) {
			e.RequestID = meta.RequestID
		}
		return err
	}
	if !env.IsSuccess() {
		return env.Error(op, resp)
	}

	// A success envelope without audio means the server produced nothing usable.
	e := maas.NewProtocolError(op, "expected audio, got "+resp.Header.Get("Content-Type"), resp.StatusCode, env.Data)
	e.RequestID = meta.RequestID
	e.Hint = "the endpoint returned a JSON envelope instead of audio; check model and voice"
	return e
}

// Say is a shortcut for a single-shot synthesis with the common options.
func (s *Service) Say(ctx context.Context, text string, opts ...SpeechOption) (*Audio, error) {
	o := &speechOptions{req: SpeechRequest{Input: text, ResponseFormat: "pcm"}}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return s.Speech(ctx, o.req)
}

// parseHeaderInt parses an integer response header, falling back on failure.
func parseHeaderInt(v string, fallback int) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// headerOr returns v, or fallback when v is blank.
func headerOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

// drainAndClose reads and closes a body so the connection can be reused.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}
