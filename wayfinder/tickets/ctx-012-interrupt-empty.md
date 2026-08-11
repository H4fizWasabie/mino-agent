# Context Truth — Interrupt replies dropped when the model answers with a tool call

Status: **OPEN** (GitHub issue #156)

## Question

Why did "btw status" return "(no response)" even though the model answered?

## Evidence (2026-08-11 full-suite live test, phase 6)

- Interrupt path fired (audit: `interrupt query="status" reply="(no response)"`)
- usage.jsonl shows the interrupt LLM call returned 203 output tokens — the model DID respond
- `handleInterrupt` (nerves.go) calls the main model with the ObserveOnly tool schemas; `extractText` returns empty when the response is a native tool_use block — the model answered by calling a tool instead of writing text, and the harness dropped the reply
- The snapshot already carries the state summary — tools are unnecessary for a status reply

## Design sketch

- Pass nil schemas to the interrupt call, with an explicit "reply in plain text, do not call tools" instruction
- If the response is still tool-only, fall back to the snapshot's status text
- Acceptance: interrupt reply contains the snapshot's iteration/status; regression test with a tool-call-only response
