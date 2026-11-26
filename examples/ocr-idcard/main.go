// Command ocr-idcard recognises an ID card image.
//
// Card recognition uses the synchronous endpoint when the platform provides one,
// so no polling happens here.
//
// Usage:
//
//	go run ./examples/ocr-idcard -api-key sk_xxx -file ./idcard.png
//	go run ./examples/ocr-idcard -api-key sk_xxx -file ./idcard.png -kind passport
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/ocr"
)

func main() {
	apiKey := flag.String("api-key", "", "MaaS API key (required)")
	baseURL := flag.String("base-url", "", "service domain (required)")
	path := flag.String("file", "", "card image (required)")
	kind := flag.String("kind", string(ocr.CardIDCard), "card kind, see maas.CardKinds()")
	async := flag.Bool("async", false, "force the asynchronous endpoint")
	flag.Parse()

	if *apiKey == "" || *path == "" {
		log.Fatal("-api-key, -base-url and -file are required")
	}

	client, err := maas.New(maas.WithAPIKey(*apiKey), maas.WithBaseURL(*baseURL))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	opts := []ocr.RecognizeOption{}
	if *async {
		opts = append(opts, ocr.WithAsyncSubmit())
	}

	res, err := ocr.New(client).Recognize(ctx, ocr.CardKind(*kind), maas.File(*path), opts...)
	if err != nil {
		log.Fatalf("recognise failed: %v", err)
	}

	for _, page := range res.Pages {
		for _, entity := range page.IEResult {
			fmt.Printf("category=%s recognized=%v\n", entity.Category, entity.Recognized)
			for k, v := range entity.Fields {
				fmt.Printf("  %s: %v\n", k, v)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "request_id=%s\n", res.Meta.RequestID)
}
