# Context Truth — Stale workaround memory overrides the native tool

Status: **OPEN** (GitHub issue #155, re-scoped)

## Question

Why did the model use the insecure bash+curl sendDocument flow again, even though the native send_document tool exists?

## Root cause (corrected diagnosis)

**Not a schema-selection failure.** The FTS index surfaces `send_document` at the top for "send OR telegram" — verified against the live catalog. The model had the tool available and still followed the workaround, because **four memory notes from the pre-tool era actively taught the curl path** ("Verified method... bypassing the send_message tool", "fallback to multipart upload via curl", "fell back to curl with the bot token; sent successfully"). The note's authoritative framing beat the system prompt's tool-preference rule.

## Principle (owner decision)

**No pinning.** Mino's essential tool set stays minimal and universal — a tool is essential when every user needs it every turn, not when one user's stale memory misbehaves. Pinning `send_document` would tax every session's schema payload to band-aid a local incident.

## Resolution applied

- Deleted the four stale instructional notes on the VPS (the curl workaround, connection-issue workaround, delivery-workaround, and the original note) — they taught the token-exposing path and predated the native tool
- Episodic records referencing them were left as history (harmless dangling edges)
- The existing system-prompt rule ("prefer the purpose-built tool... dedicated tools before generic workarounds") now prevails with the stale teaching removed

## Acceptance criteria

- [x] `send_document` verified reachable by keyword selection (FTS rank #1 for send/telegram)
- [x] Stale workaround notes removed from the VPS memory store
- [x] No tool added to the essential set
- [ ] Production observation: the next "send a file" turn uses send_document, no token in bash args
