// Package maas is a Go client for the MaaS (Model as a Service) platform.
//
// The root package owns the parts every capability shares: configuration, the
// HTTP transport, the strict response envelope, the unified error model, file
// sources and request IDs.
//
// Capabilities live in their own packages and are built on top of a Client:
//
//	client, err := maas.New(maas.WithAPIKey(apiKey), maas.WithBaseURL(baseURL))
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	res, err := ocr.New(client).ParseDocument(ctx, maas.File("contract.pdf"))
//	audio, err := tts.New(client).Say(ctx, "hello", tts.WithVoice(voiceID))
//	sess, err := asr.New(client).Start(ctx, asr.Config{})
//
// Every capability package follows the same rules: explicit configuration, no
// environment variables, one error type, and a request ID on every response.
package maas
