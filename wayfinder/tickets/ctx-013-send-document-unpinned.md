# Context Truth — Stale workaround memory overrides the native tool

Status: **RESOLVED** (GitHub issue #155)

## Question

Why did the model use the insecure bash+curl sendDocument flow again, even though the native send_document tool exists?

## Root cause (corrected diagnosis)

**Not a schema-selection failure.** The FTS index surfaces `send_document` at the top for "send OR telegram" — verified against the live catalog. The model had the tool available and still followed the workaround, because **four memory notes from the pre-tool era actively taught the curl path** ("Verified method... bypassing the send_message tool", "fallback to multipart upload via curl", "fell back to curl with the bot token; sent successfully"). The note's authoritative framing beat the system prompt's tool-preference rule.

## Principle (owner decision — reversed by production evidence)

**Original call: no pinning.** Mino's essential tool set stays minimal — a tool is essential when every user needs it every turn, not when one user's stale memory misbehaves.

**Reversed (2026-08-12).** Production disproved the assumption behind that call: the failure was not "the model prefers curl because of stale notes" — it was that `send_document` was *not in the schema at all* at a normal send turn, so the model genuinely believed no native tool existed and hand-rolled `curl ... bot<token>/sendDocument`. Deleting the notes removed the teaching but left the tool unreachable. A recurring owner need (files sent to Telegram regularly) plus a credential-leak risk justify the one-slot schema cost. `send_document` is now essential.

## Resolution applied

- Deleted the four stale instructional notes on the VPS (the curl workaround, connection-issue workaround, delivery-workaround, and the original note) — they taught the token-exposing path and predated the native tool
- Episodic records referencing them were left as history (harmless dangling edges)
- The existing system-prompt rule ("prefer the purpose-built tool... dedicated tools before generic workarounds") now prevails with the stale teaching removed

## Acceptance criteria (met by the essentials fix)

- [x] `send_document` verified reachable by keyword selection (FTS rank #1 for send/telegram)
- [x] Stale workaround notes removed from the VPS memory store
- [x] `send_document` added to the essential set (reverses the original "no pinning" call — production showed the tool was otherwise unreachable, driving the token-leaking curl fallback)
- [x] Next "send a file" turn now has the native tool in-schema, so it uses `send_document` with no token in bash args (the curl path is no longer the only option). Regression: `TestSendDocumentIsEssential`.
