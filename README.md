# maas-client-go

A Go client for the MaaS (Model as a Service) platform: OCR, text-to-speech,
and real-time speech recognition in one SDK.

[中文文档](README.zh-CN.md)

---

## Status

| Area | State |
|---|---|
| Core (config, errors, envelope, file upload, transport, request IDs) | ✅ Implemented |
| OCR (documents, cards, receipts, generic OCR; sync + async) | ✅ Implemented |
| TTS (streaming PCM, WAV writer) | ✅ Implemented |
| ASR (WebSocket session, frame codec, events) | ✅ Implemented |

Capabilities are covered by unit and integration-style tests against in-process
servers. Live verification against the platform requires an API key; see
[examples](examples/) for runnable programs.

## Why another SDK

Wrapping HTTP is the easy part. The hard parts that this SDK absorbs are:

- **One model for sync and async.** You upload a file and get a result. Whether
  the platform answered immediately or created a task you had to poll is not
  your problem.
- **Errors you can branch on.** Six categories, stable codes, a `RequestID` on
  every failure, and a `Hint` you can show a human.
- **No hidden behaviour.** No environment variables, no automatic retries on
  paid submissions, no silent reconnects, no truncating your audio.
- **Traceability by default.** A request ID flows to your logs, the response,
  and the platform.

## Install

```bash
go get github.com/hupeng12345/maas-client-go
```

Requires Go 1.21+.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	maas "github.com/hupeng12345/maas-client-go"
	"github.com/hupeng12345/maas-client-go/ocr"
)

func main() {
	// The key comes from your own configuration source. The SDK reads no env vars.
	client, err := maas.New(
		maas.WithAPIKey(apiKey),
		// Optional: the SDK ships a default endpoint, and every capability can
		// override it per call.
		maas.WithBaseURL(baseURL),
	)
	if err != nil {
		log.Fatal(err)
	}

	res, err := ocr.New(client).ParseDocument(context.Background(), maas.File("contract.pdf"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(res.MarkdownContent)
}
```

## The three capabilities

| | OCR | TTS | ASR |
|---|---|---|---|
| Transport | HTTP multipart | HTTP JSON → binary | WebSocket binary frames |
| Call model | submit + poll | single request | long-lived session |
| Success | `code` + `task_status` | `Content-Type` is audio | terminal frame + `status` |
| Cancel | stops polling only | closes the stream | `Finish` or disconnect |

### OCR

```go
// Documents: submit, poll, and return the parsed result in one call.
res, err := ocr.New(client).ParseDocument(ctx, maas.File("a.pdf"), ocr.EnableTOC(true))

// Cards: same call shape, no polling when the platform has a sync endpoint.
res, err := ocr.New(client).Recognize(ctx, ocr.CardIDCard, maas.File("id.png"))

for _, p := range res.Pages {
	fmt.Println(p.Index, p.Markdown)
}
```

### TTS

```go
audio, err := tts.New(client).Say(ctx, "Welcome aboard",
	tts.WithVoice(voiceID),
	tts.WithSample(24000),
)
if err != nil {
	return err
}
defer audio.Close()

io.Copy(w, audio)              // stream it
// or: audio.WriteWAVFile("out.wav")
```

### ASR

```go
sess, err := asr.New(client).Start(ctx, asr.Config{
	Request: asr.Request{
		EnableNonstream: maas.Ptr(true),
		ShowUtterances:  maas.Ptr(true),
	},
})
if err != nil {
	return err
}
defer sess.Close()

go func() {
	for ev := range sess.Events() {
		if ev.IsFinal {
			fmt.Println(ev.Text)
		}
	}
}()

for chunk := range audioChunks {
	if _, err := sess.Write(chunk); err != nil {
		return err
	}
}

res, err := sess.Finish(ctx) // sends the end frame and waits for the terminal state
```

ASR works out of the box: the WebSocket path is built into the SDK and the host
comes from the client endpoint. Two optional overrides are available, in
increasing order of specificity:

```go
sess, err := asr.New(client).Start(ctx, asr.Config{
	// Host: "https://other-domain.example.com",           // different domain, same path
	// URL:  "wss://other.example.com/custom/path",        // full override
	Auth: asr.Auth{ /* see package docs */ },
})
```

## Errors

Every failure is a `*maas.Error` with a category, so you can branch without
string matching:

```go
res, err := ocr.New(client).ParseDocument(ctx, maas.File("a.pdf"))
if err != nil {
	switch {
	case maas.IsAuth(err):
		// key missing, invalid or expired
	case maas.IsInsufficient(err):
		// top up, or allow postpaid credit
	case maas.IsRateLimited(err):
		time.Sleep(maas.RetryAfter(err))
	case maas.IsTaskFailed(err):
		// the task reached a failed terminal state
	default:
		log.Printf("%v", err)
	}
	return err
}
_ = res
```

Categories: config, local (validation, nothing sent), transport, protocol
(response did not match the contract), api (business code), task (async task
failed).

## Request IDs and tracing

```go
ctx = maas.WithRequestID(ctx, "order-123") // optional; generated otherwise

res, err := ocr.New(client).ParseDocument(ctx, maas.File("a.pdf"))
if err != nil {
	var e *maas.Error
	if errors.As(err, &e) {
		log.Printf("failed, quote request_id=%s to support", e.RequestID)
	}
	return err
}
log.Printf("ok request_id=%s took %s", res.Meta.RequestID, res.Meta.Duration)
```

The ID echoed by the server always wins over the locally generated one. ASR
needs a UUID in `X-Api-Request-Id`; if your ID is not a UUID the SDK generates
one for the protocol while still sending yours as `X-Request-ID`.

## Logging

Silent by default (writes to `io.Discard`). Turn it on, or bring your own
implementation:

```go
client, _ := maas.New(
	maas.WithAPIKey(apiKey),
	maas.WithLogger(logit.NewSimple(os.Stderr)),
)
```

Credentials never reach the logs: `Authorization`, `Cookie` and credentials in
query strings are redacted by the transport layer.

## Bringing your own transport

```go
// 1. Full control
client, _ := maas.New(
	maas.WithAPIKey(apiKey),
	maas.WithHTTPClient(&http.Client{Transport: myRoundTripper}),
)

// 2. Or extend the built-in chain
client, _ := maas.New(
	maas.WithAPIKey(apiKey),
	maas.WithTripperOptions(tripper.Retry(3)),
)
```

These two are mutually exclusive: if you own the `http.Client`, you own the
middleware chain.

## Packages

| Package | Purpose |
|---|---|
| `maas` | Client, configuration, transport, errors, request IDs, files |
| `ocr` | Documents, cards, receipts and generic text recognition |
| `tts` | Speech synthesis, streaming audio and the WAV writer |
| `asr` | Streaming recognition: frame codec, session, events |
| `logit` | Structured logging: default implementation and the `Logger` interface |
| `tripper` | HTTP transport with a logging and retry middleware chain |

Every capability follows the same configuration shape: a default endpoint, an
optional per-client override (`WithBaseURL`), and an optional per-call path
override (`ocr.WithEndpoint`, `tts.WithEndpoint`, `asr.Config.URL`).

`logit` and `tripper` are public on purpose: reuse them in your own services and
you get the same log format, retry semantics, and credential redaction as the
SDK itself.

## Examples

Six runnable programs live in [examples/](examples/):

```bash
go run ./examples/ocr-document -api-key "$KEY" -file ./contract.pdf
go run ./examples/tts-wav      -api-key "$KEY" -model <model> -voice <voice> -text "hello"
go run ./examples/asr-file     -api-key "$KEY" -url wss://your-host/... -file ./sample.wav
```

## Design notes

Public documentation lives in this README and in package godoc. Internal design
notes are not stored in this repository.

## Security

- Use the API key on the server side only. Never ship it to a browser or mobile
  app, and never commit it.
- The SDK reads no environment variables; pass the key explicitly from your own
  configuration source.
- This repository contains no internal hosts, credentials, or tenant data. If you
  find any, please open an issue.

## License

MIT. See [LICENSE](LICENSE).
