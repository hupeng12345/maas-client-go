package asr

import (
	"encoding/json"
	"reflect"
	"testing"

	maas "github.com/hupeng12345/maas-client-go"
)

func TestInitPayloadDefaults(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		check map[string]any // dotted path -> expected value
	}{
		{
			name: "defaults",
			cfg:  Config{},
			check: map[string]any{
				"audio.format": "pcm", "audio.codec": "raw", "audio.rate": float64(16000),
				"audio.bits": float64(16), "audio.channel": float64(1),
				"request.model_name": "bigmodel", "request.enable_nonstream": true,
			},
		},
		{
			name: "overrides",
			cfg: Config{
				Audio:   Audio{Format: "wav", Rate: 8000, Channel: 2, Language: "ja-JP"},
				Request: Request{ModelName: "custom", EndWindowSize: maas.Ptr(500)},
			},
			check: map[string]any{
				"audio.format": "wav", "audio.rate": float64(8000),
				"audio.channel": float64(2), "audio.language": "ja-JP",
				"request.model_name": "custom", "request.end_window_size": float64(500),
				"request.enable_nonstream": true,
			},
		},
		{
			name: "user block and corpus",
			cfg: Config{
				User:    map[string]any{"uid": "u1"},
				Request: Request{Corpus: &Corpus{BoostingTableIDs: []string{"t1"}}},
			},
			check: map[string]any{
				"user.uid":                         "u1",
				"request.corpus.boosting_table_id": []any{"t1"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.cfg.initPayload()
			if err != nil {
				t.Fatalf("payload: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for path, want := range tc.check {
				if gotVal := lookup(got, path); !equal(gotVal, want) {
					t.Errorf("%s = %#v, want %#v", path, gotVal, want)
				}
			}
		})
	}
}

func TestChunkSamples(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, defaultChunkSamples},
		{-1, defaultChunkSamples},
		{16000, 16000},
		{16001, defaultChunkSamples},
		{800, 800},
	}
	for _, tc := range cases {
		if got := (Config{Seg: Segmentation{ChunkSamples: tc.in}}).chunkSamples(); got != tc.want {
			t.Errorf("ChunkSamples(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEndpointURL(t *testing.T) {
	cases := []struct {
		name      string
		clientURL string
		host      string
		url       string
		want      string
		wantErr   bool
	}{
		{
			name:      "client endpoint is used by default",
			clientURL: "https://example.com",
			want:      "wss://example.com" + DefaultPath,
		},
		{
			name:      "host overrides the client endpoint",
			clientURL: "https://example.com",
			host:      "https://other.test",
			want:      "wss://other.test" + DefaultPath,
		},
		{
			name: "http host is upgraded",
			host: "http://other.test",
			want: "ws://other.test" + DefaultPath,
		},
		{
			name: "base path is preserved as a prefix",
			host: "https://other.test/gateway",
			want: "wss://other.test/gateway" + DefaultPath,
		},
		{
			name: "full URL override wins",
			host: "https://other.test",
			url:  "wss://direct.test/api/v3/sauc/bigmodel_async",
			want: "wss://direct.test/api/v3/sauc/bigmodel_async",
		},
		{
			name: "full URL override upgrades https",
			url:  "https://direct.test/x",
			want: "wss://direct.test/x",
		},
		{
			name:    "unsupported scheme",
			host:    "ftp://other.test",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []maas.Option{maas.WithAPIKey("k")}
			if tc.clientURL != "" {
				opts = append(opts, maas.WithBaseURL(tc.clientURL))
			}
			c, err := maas.New(opts...)
			if err != nil {
				t.Fatal(err)
			}
			got, err := New(c).endpointURL(Config{Host: tc.host, URL: tc.url})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("endpointURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// lookup resolves a dotted path inside nested maps.
func lookup(m map[string]any, path string) any {
	var cur any = m
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '.' {
			key := path[start:i]
			start = i + 1
			mm, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = mm[key]
		}
	}
	return cur
}

// equal compares values after normalising numeric types.
func equal(a, b any) bool {
	switch bv := b.(type) {
	case float64:
		av, ok := a.(float64)
		return ok && av == bv
	case int:
		av, ok := a.(float64)
		return ok && av == float64(bv)
	}
	return reflect.DeepEqual(a, b)
}
