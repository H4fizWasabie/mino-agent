# Context Truth — Native send_document tool for Telegram file delivery

Status: **CLOSED** (GitHub issue #153) — shipped v2.8.4: native send_document tool via the outbox; token never in args.

## Question

Why did sending the June Excel to Telegram require bash+curl with the bot token, and how do we make file delivery a first-class capability?

## Evidence (2026-08-11 live test, final step)

- Model tried composio MCP first → "No active connection found for toolkit(s) telegram" — failed
- Fell back to bash: read `TELEGRAM_BOT_TOKEN` + `MINO_TELEGRAM_CHAT_ID` from env, hand-rolled curl `sendDocument` — worked, but **the bot token landed in bash args → `tool_calls` table, `audit.jsonl`, session trails**
- One python one-liner failed with a syntax error before the successful curl

## Design sketch

Ride the existing outbox pattern (`send_message` → `queueOutbox` → `deliverOutboxOnce`):

1. **`send_document(path, caption)` tool** — validates the file, drafts `outbox/doc_<ts>.json` (`{"path":..., "caption":...}`). No token, no chat id in args — they come from Settings at delivery time.
2. **`deliverOutboxOnce`** — handles `doc_*.json` drafts: multipart POST to `/bot<token>/sendDocument`, removes the draft on success, retries next tick on failure (same semantics as text).
3. **`sendTelegramDocument`** — stdlib multipart, mirrors `postTelegram`'s return contract.

## Acceptance criteria

- [ ] send_document delivers a real file to the configured chat (end-to-end verified)
- [ ] Bot token never appears in tool args, trails, tool_calls, or audit log
- [ ] Delivery verified from the API response (`ok`), not assumed
- [ ] send_message (text) behavior unchanged — existing outbox tests keep passing
- [ ] Missing file → tool error, no draft written
