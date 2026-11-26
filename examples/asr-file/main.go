// Command asr-file transcribes an audio file.
//
// The file is streamed in 200 ms frames; WAV input is detected by its RIFF
// header and sent as raw PCM automatically.
//
// Usage:
//
//	go run ./examples/asr-file -api-key sk_xxx \
//	    -url wss://your-endpoint.example.com/path \
//	    -file ./sample.wav -language ja-JP
package main

import (
	"context"
	"flag"
	"log"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/asr"
)

func main() {
	apiKey := flag.String("api-key", "", "MaaS API key (required)")
	baseURL := flag.String("base-url", "", "service domain (required)")
	host := flag.String("host", "", "ASR host override (defaults to -base-url)")
	rawURL := flag.String("url", "", "full WebSocket URL override")
	path := flag.String("file", "", "audio file, WAV or raw PCM (required)")
	language := flag.String("language", "", "language tag, e.g. ja-JP")
	utterances := flag.Bool("utterances", true, "return per-sentence results")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall timeout")
	flag.Parse()

	if *apiKey == "" || *path == "" {
		log.Fatal("-api-key, -base-url and -file are required")
	}

	client, err := maas.New(maas.WithAPIKey(*apiKey), maas.WithBaseURL(*baseURL))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := asr.New(client).Transcribe(ctx, maas.File(*path), asr.Config{
		URL:   *rawURL,
		Host:  *host,
		Audio: asr.Audio{Language: *language},
		Request: asr.Request{
			ShowUtterances: maas.Ptr(*utterances),
		},
	})
	if err != nil {
		log.Fatalf("transcribe failed: %v", err)
	}

	log.Printf("status=%s duration=%s request_id=%s", res.Status, res.Duration, res.Meta.RequestID)
	for _, u := range res.Utterances {
		log.Printf("  [%d-%dms] %s", u.StartMS, u.EndMS, u.Text)
	}
	log.Printf("final transcript: %s", res.Text)
}
