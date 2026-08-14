# Quality Frontier — Enrich Telegram delivery with crow-agent borrowings (not a port)

Status: **CLOSED** (GitHub issue #181) — shipped in v2.9.0 (commit 4bd8774): `---` section split with reply threading, italic/underline/spoiler/blockquote in `formatTelegramHTML`, 4s typing keepalive. Live-verified 2026-08-14 on the VPS: a 5-section reply delivered as a threaded chain with zero send errors.

## Question

Crow's Telegram delivery (`crow_agent/telegram_bot.py`, `crow_agent/telegram_rich.py`) does several things Mino's (`telegram.go`, `telegram_format.go`) doesn't. Which borrowings earn their place in Mino?

## Decided scope (from session discussion)

**Take:**
1. **Section split on `---`** — crow splits final text into multiple DMs (telegram_bot.py:327-341); Mino's `chunkHTML` splits on byte cap only. Split on the separator first, chunk after, in `sendTelegramReply`.
2. **Reply threading** — crow uses `reply_parameters`; Mino's `sendTelegramReply` never threads. One field (`ReplyToMessageID`) on the outgoing message.
3. **Formatting gaps in `formatTelegramHTML`** — mino handles bold/strike/code/links/tables but is missing italic, underline, `tg-spoiler`, and blockquote (incl. `>!` expandable). Each is a 1-2 line regex following the existing stash pattern.
4. **Typing keepalive** — crow re-sends typing every 4s (telegram_bot.py:256-266); Mino sends `ChatTyping` once and the indicator dies after ~5s on long turns. A 4s ticker in `handleTelegramMessage`.

**Skip (crow-specific or cosmetic):**
- raw-HTML→markdown conversion (crow receives MiMo's HTML output; Mino generates its own text — dead code for Mino).
- per-tool icon status messages (Mino's single edit-in-place status message is better UX).
- Crow Log activity summary (Mino has traces/audit already).
- `message_effect_id`, skip-on-`send_telegram` double-send check (Mino's post-`send_message` confirmation reply is desirable).

## VPS context

Telegram volume is modest but real: `send_message` 4×, `send_document` 1×, 1186 chat rows. No VPS data changes the scope — this ticket is pure code comparison.

## Acceptance criteria

- [ ] Final replies containing `\n---\n` arrive as separate threaded messages.
- [ ] `formatTelegramHTML` renders italic, underline, spoiler, and blockquote; regression tests in `telegram_format_test.go`.
- [ ] Typing indicator persists across long turns (4s keepalive).
- [ ] Outbox dispatch path unaffected (plain text there, not reply threads).
