// Command ocr-document parses a document and prints the extracted Markdown.
//
// The SDK submits an asynchronous task and polls until it finishes, so this
// program never deals with task IDs.
//
// Usage:
//
//	go run ./examples/ocr-document -api-key sk_xxx -file ./contract.pdf
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
	path := flag.String("file", "", "document to parse (pdf, docx, png, ...)")
	toc := flag.Bool("toc", false, "also extract the table of contents")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall wait for the task")
	flag.Parse()

	if *apiKey == "" || *baseURL == "" {
		log.Fatal("-api-key and -base-url are required")
	}
	if *path == "" {
		log.Fatal("-file is required")
	}

	client, err := maas.New(
		maas.WithAPIKey(*apiKey),
		maas.WithBaseURL(*baseURL),
	)
	if err != nil {
		log.Fatalf("build client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := ocr.New(client).ParseDocument(ctx, maas.File(*path),
		ocr.EnableTOC(*toc),
	)
	if err != nil {
		// Every error is a *maas.Error with a request ID worth reporting.
		var apiErr *maas.Error
		if errAs(err, &apiErr) {
			log.Fatalf("parse failed: code=%s request_id=%s err=%v", apiErr.Code, apiErr.RequestID, err)
		}
		log.Fatalf("parse failed: %v", err)
	}

	fmt.Println(res.MarkdownContent)
	fmt.Fprintf(os.Stderr, "\n--- pages=%d request_id=%s took=%s\n",
		len(res.Pages), res.Meta.RequestID, res.Meta.Duration)
}

// errAs is a tiny helper so the example does not import errors just for one call.
func errAs(err error, target **maas.Error) bool {
	for err != nil {
		if e, ok := err.(*maas.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
