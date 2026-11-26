// Command tts-wav synthesises speech into a playable WAV file.
//
// The platform returns raw PCM without a container, so this example uses the
// SDK's WAV writer, which takes the sample rate from the response header.
//
// Usage:
//
//	go run ./examples/tts-wav -api-key sk_xxx -model <model-id> -voice <voice-id> \
//	    -text "hello" -out hello.wav
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/tts"
)

func main() {
	apiKey := flag.String("api-key", "", "MaaS API key (required)")
	baseURL := flag.String("base-url", "", "service domain (required)")
	model := flag.String("model", "", "TTS model assigned to your account (required)")
	voice := flag.String("voice", "", "voice_id (required)")
	text := flag.String("text", "", "text to synthesise (required)")
	language := flag.String("language", "", "BCP 47 language tag, e.g. zh-CN")
	sample := flag.Int("sample", 24000, "sample rate in Hz")
	out := flag.String("out", "speech.wav", "output WAV path")
	flag.Parse()

	if *apiKey == "" || *model == "" || *voice == "" || *text == "" {
		log.Fatal("-api-key, -model, -voice and -text are required")
	}

	client, err := maas.New(maas.WithAPIKey(*apiKey), maas.WithBaseURL(*baseURL))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	audio, err := tts.New(client).Say(ctx, *text,
		tts.WithModel(*model),
		tts.WithVoice(*voice),
		tts.WithLanguage(*language),
		tts.WithSample(*sample),
	)
	if err != nil {
		log.Fatalf("synthesise failed: %v", err)
	}
	defer audio.Close()

	if err := audio.WriteWAVFile(*out); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}

	fmt.Printf("wrote %s (rate=%d channels=%d format=%s) request_id=%s\n",
		*out, audio.SampleRate, audio.Channels, audio.Format, audio.Meta.RequestID)
}
