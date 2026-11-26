# Contributing

Thanks for taking the time to contribute.

## Ground rules

1. **English only in Go sources.** Comments, log messages, package docs and
   error text must be English. `make check-english` enforces this.
2. **No implicit configuration.** Library code must not read environment
   variables or config files; everything is passed explicitly. Enforced by
   `make check-env`.
3. **Do not weaken the contract.** Responses that do not match the documented
   agreement return `ErrNonStandardResponse`; do not add compatibility shims
   for malformed payloads.
4. **Keep dependencies minimal.** The only allowed direct dependency is
   `github.com/bytedance/sonic`; adding another requires review.
5. **No secrets, ever.** No internal hosts, credentials or tenant data. CI runs
   gitleaks and `scripts/check-sensitive.sh` on every push.
6. **No deployment specifics.** Do not embed endpoints, product or model
   identifiers, vendor-specific resource IDs, or internal design notes. Use
   `example.com` / `your-endpoint.example.com` placeholders. This SDK ships no
   default endpoint; callers pass one explicitly.

## Source layout

The module is split by role, not by file type:

```
/                      # package maas: shared core
  client.go config.go options.go     construction and configuration
  errors.go envelope.go meta.go      error model and response envelope
  transport.go requestid.go hooks.go HTTP execution, tracing, callbacks
  file.go multipart.go               upload plumbing
  ocr/                               package ocr: documents, cards, receipts
  tts/                               package tts: synthesis and audio
  asr/                               package asr: frames and sessions
  logit/ tripper/                    reusable logging and HTTP middleware
```

Dependency direction is one-way: the root package never imports a capability
package, and capability packages depend only on the root. That is what allows
`ocr.New(client)` without an import cycle.

Rules of thumb:

- one concern per file; if a file grows past ~400 lines, split it by prefix;
- capability packages must not import each other;
- `internal/` is only for generated or non-public helpers.

## Before opening a pull request

```bash
make verify
```

This runs build, vet, gofmt check, the race-enabled test suite, the
English-only check and the implicit-configuration check.

## Commit messages

Use the imperative mood and a scoped prefix where it helps:

```
ocr: return task status on submit failures
core: reject non-standard response envelopes
docs: add ASR session lifecycle notes
```

## Reporting issues

Include the `request_id` from the error when reporting a platform-side problem;
it maps directly to gateway and upstream logs.
