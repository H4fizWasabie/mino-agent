# Define graph front-matter and edge schema

Status: resolved
Type: grilling

## Question

What exact front-matter and edge schema represents claim identity, rationale, explicit versus inferred provenance, confidence, source, and directed typed relationships while remaining simple to edit and index?

## Answer

Use Markdown front matter as the authoritative structured graph record:

```yaml
---
id: abah_prefers_go
type: semantic
subject: Abah prefers Go for backend development
at: 2026-07-29T10:30:00+08:00
why: Used when choosing backend implementation technologies
source: session:default
edge:
  - target: mino_backend
    rel: used_in
    kind: explicit
    confidence: 1.0
    source: session:default
---

Additional context, examples, or longer explanation go here.
```

The stable `id` identifies the claim, not its wording. `type`, `subject`, and `at` are required. `why` and `source` are optional node metadata. Edges are directed and carry `target`, `rel`, `kind`, `confidence`, and `source`; `kind` is `explicit`, `inferred`, or `ambiguous`. The Markdown body carries longer context. JSON serialization must use lowercase field names consistently.

## Context pointer

This decision is recorded in the Wayfinder map under `Decisions so far`.
