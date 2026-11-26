package tts_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"
	"io"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/tts"
)

// decodeJSON reads a JSON request body.
func decodeJSON(r *http.Request, dst any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func newClient(t *testing.T, baseURL string) *maas.Client {
	t.Helper()
	c, err := maas.New(maas.WithAPIKey("sk_test"), maas.WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// audioServer returns audio with configurable headers.
func audioServer(t *testing.T, headers map[string]string, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type mismatch: %q", got)
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSpeechAudioMetadata(t *testing.T) {
	cases := []struct {
		name       string
		headers    map[string]string
		wantRate   int
		wantChan   int
		wantFormat string
	}{
		{
			name: "explicit metadata",
			headers: map[string]string{
				"Content-Type": "audio/pcm", "X-Sample-Rate": "24000",
				"X-Audio-Channels": "1", "X-Audio-Format": "pcm_s16le",
			},
			wantRate: 24000, wantChan: 1, wantFormat: "pcm_s16le",
		},
		{
			name:     "missing headers fall back",
			headers:  map[string]string{"Content-Type": "audio/pcm"},
			wantRate: 8000, wantChan: 1, wantFormat: "pcm_s16le",
		},
		{
			name: "garbage headers fall back",
			headers: map[string]string{
				"Content-Type": "audio/pcm", "X-Sample-Rate": "abc", "X-Audio-Channels": "-2",
			},
			wantRate: 8000, wantChan: 1, wantFormat: "pcm_s16le",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := audioServer(t, tc.headers, []byte{1, 2, 3, 4})
			audio, err := tts.New(newClient(t, srv.URL)).Say(context.Background(), "hi",
				tts.WithModel("m"), tts.WithVoice("v"))
			if err != nil {
				t.Fatalf("speak: %v", err)
			}
			defer audio.Close()

			if audio.SampleRate != tc.wantRate || audio.Channels != tc.wantChan || audio.Format != tc.wantFormat {
				t.Fatalf("metadata mismatch: %+v", audio)
			}
		})
	}
}

func TestSpeechValidation(t *testing.T) {
	cases := []struct {
		name string
		req  tts.SpeechRequest
	}{
		{"missing model", tts.SpeechRequest{Input: "x", Voice: "v"}},
		{"blank model", tts.SpeechRequest{Model: "  ", Input: "x", Voice: "v"}},
		{"missing input", tts.SpeechRequest{Model: "m", Voice: "v"}},
		{"missing voice", tts.SpeechRequest{Model: "m", Input: "x"}},
		{"bad format", tts.SpeechRequest{Model: "m", Input: "x", Voice: "v", ResponseFormat: "mp3"}},
	}

	c, err := maas.New(maas.WithAPIKey("sk_test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tts.New(c).Speech(context.Background(), tc.req)
			if err == nil {
				t.Fatal("want validation error")
			}
			var e *maas.Error
			if !errors.As(err, &e) || e.Kind != maas.KindLocal {
				t.Fatalf("want local error, got %v", err)
			}
		})
	}
}

func TestSpeechFailureResponses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   func(error) bool
	}{
		{"tts unavailable", http.StatusUnprocessableEntity,
			`{"code":"700000","message":"unsupported voice","data":null}`, func(err error) bool {
				var e *maas.Error
				return errors.As(err, &e) && e.Code == maas.CodeTTSUnavailable
			}},
		{"insufficient balance", http.StatusPaymentRequired,
			`{"code":"402001","message":"insufficient balance","data":null}`, maas.IsInsufficient},
		{"non-standard", http.StatusOK, `{"code":200}`, maas.IsNonStandardResponse},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := tts.New(newClient(t, srv.URL)).Say(context.Background(), "hi",
				tts.WithModel("m"), tts.WithVoice("v"))
			if err == nil {
				t.Fatal("want error")
			}
			if !tc.want(err) {
				t.Fatalf("error predicate failed: %v", err)
			}
		})
	}
}

// A success envelope without audio is a protocol error, not empty audio.
func TestSpeechSuccessEnvelopeWithoutAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"000000","message":"ok","data":{}}`))
	}))
	defer srv.Close()

	_, err := tts.New(newClient(t, srv.URL)).Say(context.Background(), "hi",
		tts.WithModel("m"), tts.WithVoice("v"))
	if err == nil || !maas.IsNonStandardResponse(err) {
		t.Fatalf("want protocol error, got %v", err)
	}
}

func TestWriteWAVHeader(t *testing.T) {
	cases := []struct {
		name     string
		rate     string
		wantRate uint32
	}{
		{"default 8k", "", 8000},
		{"16k", "16000", 16000},
		{"24k", "24000", 24000},
		{"48k", "48000", 48000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{"Content-Type": "audio/pcm"}
			if tc.rate != "" {
				headers["X-Sample-Rate"] = tc.rate
			}
			srv := audioServer(t, headers, []byte{0x00, 0x01})

			audio, err := tts.New(newClient(t, srv.URL)).Say(context.Background(), "hi",
				tts.WithModel("m"), tts.WithVoice("v"))
			if err != nil {
				t.Fatal(err)
			}
			defer audio.Close()

			var buf bytes.Buffer
			n, err := audio.WriteWAV(&buf)
			if err != nil {
				t.Fatal(err)
			}
			out := buf.Bytes()
			if n != int64(len(out)) {
				t.Fatalf("reported %d bytes, wrote %d", n, len(out))
			}
			if string(out[0:4]) != "RIFF" || string(out[8:12]) != "WAVE" || string(out[36:40]) != "data" {
				t.Fatalf("bad WAV header: % x", out[:44])
			}
			if got := binary.LittleEndian.Uint32(out[24:28]); got != tc.wantRate {
				t.Fatalf("sample rate mismatch: got %d want %d", got, tc.wantRate)
			}
			if got := binary.LittleEndian.Uint16(out[22:24]); got != 1 {
				t.Fatalf("channel mismatch: %d", got)
			}
			if got := binary.LittleEndian.Uint16(out[34:36]); got != 16 {
				t.Fatalf("bits per sample mismatch: %d", got)
			}
		})
	}
}

func TestSayOptions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write([]byte{0})
	}))
	defer srv.Close()

	audio, err := tts.New(newClient(t, srv.URL)).Say(context.Background(), "hello",
		tts.WithModel("m1"), tts.WithVoice("v1"), tts.WithSample(16000),
		tts.WithSpeed(1.5), tts.WithLanguage("zh-CN"), tts.WithBGM("rain"))
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()

	for k, want := range map[string]any{
		"model": "m1", "voice": "v1", "input": "hello", "language": "zh-CN",
		"bgm_id": "rain", "response_format": "pcm",
	} {
		if gotBody[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, gotBody[k], want)
		}
	}
	if gotBody["sample"] != float64(16000) {
		t.Errorf("sample = %v", gotBody["sample"])
	}
	if gotBody["speed"] != 1.5 {
		t.Errorf("speed = %v", gotBody["speed"])
	}
}

// WithEndpoint must let callers target a different path without an SDK release.
func TestEndpointOverride(t *testing.T) {
	cases := []struct {
		name string
		opt  []tts.SpeechOption
		want string
	}{
		{"default", nil, "/tts/v1/speech"},
		{"override", []tts.SpeechOption{tts.WithEndpoint("/custom/v2/speech")}, "/custom/v2/speech"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			mux := http.NewServeMux()
			handler := func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "audio/pcm")
				_, _ = w.Write([]byte{0})
			}
			mux.HandleFunc("/tts/v1/speech", handler)
			mux.HandleFunc("/custom/v2/speech", handler)
			srv := httptest.NewServer(mux)
			defer srv.Close()

			opts := append([]tts.SpeechOption{tts.WithModel("m"), tts.WithVoice("v")}, tc.opt...)
			audio, err := tts.New(newClient(t, srv.URL)).Say(context.Background(), "hi", opts...)
			if err != nil {
				t.Fatalf("speak: %v", err)
			}
			_ = audio.Close()

			if gotPath != tc.want {
				t.Fatalf("path: got %q want %q", gotPath, tc.want)
			}
		})
	}
}
