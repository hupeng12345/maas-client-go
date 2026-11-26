package asr

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
)

// DefaultPath is the WebSocket path used for streaming recognition.
//
// Callers configure a host only; this path is part of the SDK so that every
// deployment of the platform is reached the same way.
const DefaultPath = "/-/volcengine/api/v3/sauc/bigmodel_async"

// Service provides real-time streaming speech recognition.
type Service struct {
	c *maas.Client
}

// New builds a Service on top of a client.
func New(c *maas.Client) *Service { return &Service{c: c} }

// Auth selects the authentication headers used during the handshake.
//
// Three forms are supported; the first one that applies wins:
//
//	ResourceID set        → X-Api-Key + X-Api-Resource-Id (direct endpoints)
//	AppKey/AccessKey set  → X-Api-App-Key + X-Api-Access-Key (legacy console)
//	otherwise             → Authorization: Bearer <client API key> (gateway)
//
// X-Api-Request-Id is always sent as a UUID; see design notes on request IDs.
type Auth struct {
	// BearerKey overrides the client API key for the bearer form.
	BearerKey string
	// APIKey is sent as X-Api-Key in the direct form; empty falls back to the
	// client API key.
	APIKey string
	// ResourceID enables the direct form when non-empty, for example
	// as assigned by the endpoint provider.
	ResourceID string
	// AppKey and AccessKey enable the legacy console form when both are set.
	AppKey    string
	AccessKey string
}

// Audio declares the audio format.
//
// The platform accepts 16 kHz, 16-bit, mono audio only.
type Audio struct {
	// Format is "pcm" for raw samples or "wav" for a RIFF container.
	Format string `json:"format,omitempty"`
	// Codec must be "raw".
	Codec string `json:"codec,omitempty"`
	// Rate is the sample rate in Hz; only 16000 is accepted.
	Rate int `json:"rate,omitempty"`
	// Bits is the sample width; only 16 is accepted.
	Bits int `json:"bits,omitempty"`
	// Channel is the channel count; only 1 is accepted.
	Channel int `json:"channel,omitempty"`
	// Language is the language tag, for example "ja-JP".
	Language string `json:"language,omitempty"`
}

// Corpus configures hotwords and context.
type Corpus struct {
	// BoostingTableIDs references platform hotword tables.
	BoostingTableIDs []string `json:"boosting_table_id,omitempty"`
	// CorrectTableID references a replacement table.
	CorrectTableID string `json:"correct_table_id,omitempty"`
	// Context is a JSON string carrying extra context; the protocol requires a
	// string, not an object.
	Context string `json:"context,omitempty"`
}

// Request mirrors the protocol's request block.
//
// Pointer fields distinguish "not set" from an explicit false, which matters:
// some flags are rejected when omitted.
type Request struct {
	ModelName string `json:"model_name,omitempty"`

	EnableNonstream        *bool `json:"enable_nonstream,omitempty"`
	EnableITN              *bool `json:"enable_itn,omitempty"`
	EnablePunc             *bool `json:"enable_punc,omitempty"`
	EnableDDC              *bool `json:"enable_ddc,omitempty"`
	EnableSpeakerInfo      *bool `json:"enable_speaker_info,omitempty"`
	ShowUtterances         *bool `json:"show_utterances,omitempty"`
	ShowSpeechRate         *bool `json:"show_speech_rate,omitempty"`
	ShowVolume             *bool `json:"show_volume,omitempty"`
	EnableEmotionDetection *bool `json:"enable_emotion_detection,omitempty"`
	EnableGenderDetection  *bool `json:"enable_gender_detection,omitempty"`
	EnableAgeDetection     *bool `json:"enable_age_detection,omitempty"`
	EnableAccelerateText   *bool `json:"enable_accelerate_text,omitempty"`
	EnablePOIFC            *bool `json:"enable_poi_fc,omitempty"`
	EnableMusicFC          *bool `json:"enable_music_fc,omitempty"`

	ResultType         string `json:"result_type,omitempty"`
	OutputZhVariant    string `json:"output_zh_variant,omitempty"`
	AccelerateScore    *int   `json:"accelerate_score,omitempty"`
	VadSegmentDuration *int   `json:"vad_segment_duration,omitempty"`
	EndWindowSize      *int   `json:"end_window_size,omitempty"`
	ForceToSpeechTime  *int   `json:"force_to_speech_time,omitempty"`

	SensitiveWordsFilter string `json:"sensitive_words_filter,omitempty"`

	Corpus *Corpus `json:"corpus,omitempty"`
	// Parret carries extra protocol options, for example
	// {"transcript_representations":["ja_normal"]}.
	Parret map[string]any `json:"parret,omitempty"`
}

// Segmentation controls how the SDK splits audio into frames.
type Segmentation struct {
	// ChunkSamples is the number of samples per frame. The protocol caps a frame
	// at 16000 samples (one second); the default is 3200 (200 ms).
	ChunkSamples int
}

// Config configures one ASR session.
type Config struct {
	// URL is the full WebSocket endpoint. When set it wins over Host and the
	// client endpoint, which lets callers target a deployment whose path differs
	// from DefaultPath.
	URL string
	// Host overrides the endpoint host for this session, for example
	// "https://asr.example.com". The WebSocket path still comes from DefaultPath.
	//
	// When empty, the client endpoint is used.
	Host string
	// Headers adds extra handshake headers; they never override SDK headers.
	Headers map[string]string
	// Auth selects the authentication form.
	Auth Auth
	// Audio declares the audio format; sensible defaults are applied.
	Audio Audio
	// Request configures recognition.
	Request Request
	// User is the protocol's user block, for example {"uid": "..."}.
	User map[string]any
	// Seg controls frame splitting.
	Seg Segmentation
	// ReadyTimeout bounds how long Start waits for the ready frame.
	ReadyTimeout time.Duration
}

// sessionWidth is the default frame size: 200 ms at 16 kHz.
const defaultChunkSamples = 3200

// endpointURL resolves the WebSocket URL for the session.
//
// Precedence: Config.URL (full override), then Config.Host plus DefaultPath, then
// the client endpoint plus DefaultPath.
func (s *Service) endpointURL(cfg Config) (string, error) {
	if raw := strings.TrimSpace(cfg.URL); raw != "" {
		u, err := url.Parse(raw)
		if err != nil {
			return "", &maas.Error{Kind: maas.KindLocal, Message: "invalid ASR URL", Hint: "check Config.URL", Op: "asr.Start", Err: err}
		}
		switch strings.ToLower(u.Scheme) {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		case "ws", "wss":
		default:
			return "", &maas.Error{Kind: maas.KindLocal, Message: "unsupported ASR URL scheme", Hint: "use http, https, ws or wss", Op: "asr.Start"}
		}
		return u.String(), nil
	}

	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		host = s.c.BaseURL()
	}
	if host == "" {
		return "", &maas.Error{
			Kind:    maas.KindLocal,
			Message: "no ASR host configured",
			Hint:    "pass maas.WithBaseURL(domain) or set Config.Host",
			Op:      "asr.Start",
		}
	}

	u, err := url.Parse(host)
	if err != nil {
		return "", &maas.Error{Kind: maas.KindLocal, Message: "invalid ASR host", Hint: "check Config.Host or WithBaseURL", Op: "asr.Start", Err: err}
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", &maas.Error{Kind: maas.KindLocal, Message: "unsupported ASR host scheme", Hint: "use http, https, ws or wss", Op: "asr.Start"}
	}
	u.Path = strings.TrimRight(u.Path, "/") + DefaultPath
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// initPayload renders the session configuration as JSON.
//
// The protocol rejects unknown fields, so defaults are applied explicitly and
// Extra-style merging is not offered at this level.
func (cfg Config) initPayload() ([]byte, error) {
	audio := map[string]any{
		"format":  "pcm",
		"codec":   "raw",
		"rate":    16000,
		"bits":    16,
		"channel": 1,
	}
	if v := strings.TrimSpace(cfg.Audio.Format); v != "" {
		audio["format"] = v
	}
	if v := strings.TrimSpace(cfg.Audio.Codec); v != "" {
		audio["codec"] = v
	}
	if cfg.Audio.Rate > 0 {
		audio["rate"] = cfg.Audio.Rate
	}
	if cfg.Audio.Bits > 0 {
		audio["bits"] = cfg.Audio.Bits
	}
	if cfg.Audio.Channel > 0 {
		audio["channel"] = cfg.Audio.Channel
	}
	if v := strings.TrimSpace(cfg.Audio.Language); v != "" {
		audio["language"] = v
	}

	req := map[string]any{}
	raw, err := json.Marshal(cfg.Request)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if _, ok := req["model_name"]; !ok {
		// The protocol accepts exactly one model name.
		req["model_name"] = "bigmodel"
	}
	if _, ok := req["enable_nonstream"]; !ok {
		// Second-pass recognition is what makes final transcripts accurate; the
		// gateway requires it to be explicitly true.
		req["enable_nonstream"] = true
	}

	payload := map[string]any{
		"audio":   audio,
		"request": req,
	}
	if len(cfg.User) > 0 {
		payload["user"] = cfg.User
	}
	return json.Marshal(payload)
}

// chunkSamples returns the effective frame size.
func (cfg Config) chunkSamples() int {
	if cfg.Seg.ChunkSamples > 0 && cfg.Seg.ChunkSamples <= 16000 {
		return cfg.Seg.ChunkSamples
	}
	return defaultChunkSamples
}
