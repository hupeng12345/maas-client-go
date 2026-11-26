// Command tts-stream streams synthesised audio to stdout.
//
// Pipe it straight into a player to hear the result while it is still being
// generated:
//
//	go run ./examples/tts-stream -api-key sk_xxx -model m -voice v -text "hello" | \
//	    ffplay -f s16le -ar 24000 -ac 1 -
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/tts"
)

func main() {
	apiKey := flag.String("api-key", "", "MaaS API key (required)")
	baseURL := flag.String("base-url", "", "service domain (required)")
	model := flag.String("model", "", "TTS model assigned to your account (required)")
	voice := flag.String("voice", "", "voice_id (required)")
	text := flag.String("text", "", "text to synthesise (required)")
	sample := flag.Int("sample", 24000, "sample rate in Hz")
	flag.Parse()

	if *apiKey == "" || *model == "" || *voice == "" || *text == "" {
		log.Fatal("-api-key, -model, -voice and -text are required")
	}

	client, err := maas.New(maas.WithAPIKey(*apiKey), maas.WithBaseURL(*baseURL))
	if err != nil {
		log.Fatal(err)
	}

	audio, err := tts.New(client).Say(context.Background(), *text,
		tts.WithModel(*model),
		tts.WithVoice(*voice),
		tts.WithSample(*sample),
	)
	if err != nil {
		log.Fatalf("synthesise failed: %v", err)
	}
	defer audio.Close()

	// Raw PCM goes to stdout; the sample rate is on stderr so piping stays clean.
	fmt.Fprintf(os.Stderr, "streaming %d Hz, %d channel(s), request_id=%s\n",
		audio.SampleRate, audio.Channels, audio.Meta.RequestID)
	if _, err := io.Copy(os.Stdout, audio); err != nil {
		log.Fatalf("stream failed: %v", err)
	}
}
