# Documentation

## Layout

```
docs/
└── README.md   # this file
```

## What lives here

Public documentation for this SDK is kept in two places:

| Document | Audience | Content |
|---|---|---|
| [`../README.md`](../README.md) | Users | Install, quick start, capability examples, error handling, tracing |
| [`../CONTRIBUTING.md`](../CONTRIBUTING.md) | Contributors | Ground rules, source layout, review checklist |

Internal design notes, contract surveys and rollout plans are deliberately **not**
stored in this repository. They describe deployment topology and platform
internals that do not belong in a public project.

## Adding documentation

- User-facing API documentation belongs in godoc comments, not here.
- Behavioural guarantees (timeouts, retries, streaming) belong in the README and
  in package documentation.
- If a document needs to mention a specific deployment, endpoint or vendor, it
  should not be committed here.
