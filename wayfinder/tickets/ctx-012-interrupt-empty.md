# Context Truth — Interrupt replies dropped when the model answers with a tool call

Status: **RESOLVED** (closes GitHub issue #156, commit pending)

## Question

Why did "btw status" return "(no response)" even though the model answered?

## Evidence (2026-08-11 full-suite live test, phase 6)

- Interrupt path fired (audit: `interrupt query="status" reply="(no response)"`)
- usage.jsonl shows the interrupt LLM call returned 203 output tokens — the model DID respond
- `handleInterrupt` (nerves.go) calls the main model with the ObserveOnly tool schemas; `extractText` returns empty when the response is a native tool_use block — the model answered by calling a tool instead of writing text, and the harness dropped the reply
- The snapshot already carries the state summary — tools are unnecessary for a status reply

## Resolution

- The interrupt call now passes **nil schemas** — the model physically cannot emit a native tool call
- The interrupt system prompt says "Reply in plain text only — do NOT call any tools"
- If the response is still tool-only, the reply falls back to a snapshot status line (`(status: iteration N, running_tool, on bash)`) instead of "(no response)"

## Validation

- `TestInterruptFallsBackWhenModelAnswersWithToolCall` — tool-call-only response yields the snapshot status line
- `TestInterruptTextReplyPassesThrough` — text responses unchanged
- `go test ./...` — 511 pass
