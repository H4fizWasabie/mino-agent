# Context Truth — Head/tail large-message preview

Status: **RESOLVED** (closes GitHub issue #145, commit 4ffae81)

## Question

How do we stop the large-message cap from deleting method knowledge that cost nothing to keep?

## Resolution

`ContextMessages` now renders messages over `inputPreviewLimit` (8000) as head+tail preview instead of a bare placeholder:

```
[Large previous assistant message (25222 chars); full text is in the session log.
HEAD:
<first 4000 chars — the reply text>
...
TAIL:
<last 4000 chars — the [tools used:] trails with paths and commands>]
```

Same budget (8000 total), same cap — only the middle (large result tables, already artifacted) is dropped. Assistant messages are built reply-then-trail, so the tail is exactly the method the next turn needs. Mirrors the existing `compactUserInput` pattern.

## Acceptance criteria (all met)

- [x] Head+tail preview with HEAD/TAIL markers for messages > inputPreviewLimit
- [x] Trailing tool trails (paths, commands) present in the preview tail
- [x] Preview length bounded by inputPreviewLimit + small overhead
- [x] Test covering the incident shape: `TestContextMessagesKeepsMethodTailOfLargeMessages` (12k reply + method-bearing trail)

## Validation

- `go test ./...` — 500 pass
- Changelog updated; pushed to master
