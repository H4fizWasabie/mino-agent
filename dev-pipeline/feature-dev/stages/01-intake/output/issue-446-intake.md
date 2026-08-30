# Intake: replace head/tail output truncation with smarter paging

Issue: #446. Reviewed 2026-08-31 — resolved without implementation.

## Problem (as posed)

Head/tail truncation is today's answer to oversized tool output. `read_file` with
offset/limit already exists as an on-demand paging mechanism, but a live eval (#439) showed
the model using it inefficiently — inconsistent chunk sizes, some fabricated paths. The
ticket asked for "smarter paging": larger default chunks when the caller already knows the
total size, guidance toward efficient paging, and/or validation that a `read_file` path was
actually seen in a prior tool result.

## Finding

All three named ideas are already shipped, tagged `#439` in the code (the sibling bug ticket
this map ticket explicitly said shared "the same underlying gap"):

- **Larger default chunks when the size is known**: `read_file` (`tools.go`) hands back the
  entire remainder in one call whenever it fits under 16KB, regardless of a smaller requested
  limit — "stops a weak model from paging a <16KB file across ten round-trips."
- **Path validation**: the `knownArtifactsKey` fabricated-path guard rejects a `results/`
  path that wasn't handed back verbatim by a prior tool result this turn.
- **Guidance toward efficient paging**: `compactToolOutput` (`loop.go`) never truncates data
  away — it spills the full output to a file, shows a head+tail preview inline, and tells the
  model explicitly `"use read_file with offset and limit"` for the rest. Head/tail is already
  a preview alongside a full-content paging pointer, not lossy truncation; this same
  established pattern is used for oversized conversation history too (`ctx-002-head-tail-preview`,
  `ContextMessages`), so this isn't a one-off fix but a repo-wide convention.

## Decision

No additional code change. The ticket's title describes something that isn't actually true of
the current design — head/tail truncation as a lossy final answer doesn't happen anywhere in
the tool-output or context-message path; both already pair a bounded preview with a pointer
to the full, pageable content.

## Non-goals

- Speculative further tuning of chunk sizes or preview lengths absent a new incident.

## Outcome

No code change. Issue closed with this design note as the record of why.
