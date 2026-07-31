# Make index reconciliation live and recoverable

Status: resolved
Type: task
Blocked by: 02

## Question

How should Mino detect changed, new, deleted, malformed, or partially written Markdown files and safely rebuild the derived `index.json` without an LLM or external filesystem dependency?

## Answer

Use a deterministic reconciliation loop inside `GraphMemory`:

- Run an initial full scan when the graph starts.
- Run a lightweight background scan every few seconds for the 24/7 process.
- Perform a cheap freshness check before graph reads as a safety net.
- Track each Markdown file's modification time and size in the derived index state; reparse only changed or new files.
- Remove in-memory facts whose Markdown files were deleted.
- Keep the last valid in-memory fact when a changed file is malformed or partially written, log the parse error, and retry on the next scan.
- Do not infer edges during reconciliation.
- Rebuild `index.json` from the valid in-memory facts and write it through a temporary file followed by an atomic rename.
- Use lowercase JSON tags for all index fields, matching the Markdown schema.

The Markdown files remain authoritative. The index is a recoverable cache: deleting it causes a full Markdown rebuild, while deleting or editing a Markdown file is detected without restarting Mino.

## Context pointer

This decision is recorded in the Wayfinder map under `Decisions so far`.
