// Command asr-realtime streams microphone audio from stdin and prints partial
// results as they arrive.
//
// Reading a capture device needs platform-specific code, so this example reads
// raw 16 kHz / 16-bit / mono PCM from stdin. On macOS or Linux you can feed it
// with ffmpeg:
//
//	ffmpeg -f avfoundation -i ":0" -ar 16000 -ac 1 -f s16le - 2>/dev/null | \
//	    go run ./examples/asr-realtime -api-key sk_xxx -url wss://your-endpoint.example.com/path
//
// The session ends when stdin closes (Ctrl-D), which sends the finish frame and
// waits for the authoritative terminal result.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/asr"
)

func main() {
	apiKey := flag.String("api-key", "", "MaaS API key (required)")
	baseURL := flag.String("base-url", "", "service domain (required)")
	host := flag.String("host", "", "ASR host override (defaults to -base-url)")
	rawURL := flag.String("url", "", "full WebSocket URL override")
	language := flag.String("language", "", "language tag, e.g. ja-JP")
	flag.Parse()

	if *apiKey == "" || *baseURL == "" {
		log.Fatal("-api-key and -base-url are required")
	}

	client, err := maas.New(maas.WithAPIKey(*apiKey), maas.WithBaseURL(*baseURL))
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	sess, err := asr.New(client).Start(ctx, asr.Config{
		URL:   *rawURL,
		Host:  *host,
		Audio: asr.Audio{Language: *language},
		Request: asr.Request{
			ShowUtterances: maas.Ptr(true),
			EnablePunc:     maas.Ptr(true),
		},
	})
	if err != nil {
		log.Fatalf("open session: %v", err)
	}
	defer sess.Close()

	// Events must be consumed; otherwise writes eventually block.
	terminal := make(chan asr.Event, 1)
	go func() {
		for ev := range sess.Events() {
			switch ev.Kind {
			case asr.EventSnapshot:
				fmt.Printf("\r%s", ev.Text)
			case asr.EventUtteranceSettled:
				fmt.Printf("\n%s\n", ev.Text)
			case asr.EventTerminal:
				terminal <- ev
			case asr.EventError:
				log.Printf("session error: %v", ev.Err)
			}
		}
		close(terminal)
	}()

	chunk := make([]byte, sess.ChunkSize()*2) // 16-bit samples
	for {
		n, readErr := io.ReadFull(os.Stdin, chunk)
		if n > 0 {
			if _, err := sess.Write(chunk[:n]); err != nil {
				log.Fatalf("write audio: %v", err)
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			log.Fatalf("read stdin: %v", readErr)
		}
	}

	res, err := sess.Finish(ctx)
	if err != nil {
		log.Printf("finish: %v", err)
	}
	if res != nil {
		fmt.Fprintf(os.Stderr, "\nstatus=%s request_id=%s\n", res.Status, res.Meta.RequestID)
	}
}
