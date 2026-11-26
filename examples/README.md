# Examples

Runnable programs for each capability. Every example takes the API key and the
endpoint as explicit flags: the SDK reads no environment variables.

| Example | Shows |
|---|---|
| [ocr-document](ocr-document) | async document parsing: submit, poll, read Markdown and pages |
| [ocr-idcard](ocr-idcard) | sync card recognition (no polling), choosing a card kind |
| [tts-wav](tts-wav) | synthesising speech into a playable WAV file |
| [tts-stream](tts-stream) | streaming raw PCM to stdout, piped into a player |
| [asr-file](asr-file) | transcribing an audio file, WAV or raw PCM |
| [asr-realtime](asr-realtime) | a live session fed from stdin, printing partial results |

Run one from the repository root:

```bash
go run ./examples/ocr-document -api-key "$KEY" -file ./contract.pdf
```

Notes:

- Passing secrets on the command line is fine for a local experiment, but a
  production service should read the key from a secret manager and never log it.
- The SDK ships a default endpoint; every example accepts `-base-url` to change
  it. ASR derives its WebSocket path from the host (`-host` swaps the domain,
  `-url` overrides the whole endpoint).
- The examples are illustrative, not production code: they log to stderr and exit
  on the first error.
