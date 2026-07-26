# Morning briefing

## Read

- `config.md`
- `output/01-briefing.md` (previous day's briefing, if it exists)

## Do

1. Call `recall` with query "commitments, follow-ups, deadlines, things Abah asked me to track, pending decisions, people waiting for replies."
2. List calendar events for the next 7 days using available calendar tools.
3. Triage each item discovered:
   - AUTO-HANDLE: if it's purely informational (check status, report progress), handle it now.
   - CREATE NOTE: if it's a new commitment, save with `save_note` and subject describing the commitment.
   - ESCALATE: if Abah's decision is required, deadline is within 48h with no progress, or blocker is external — flag it.

4. Deliver a Telegram briefing using the format below. Use curl: bot `8905695639:AAGw4w08yz_AWMUXGoEb7A90-f3o00sh5yk`, chat `1794722543`.

```
🌅 Good morning, Abah.

📋 HANDLED TODAY:
- [what Mino did automatically]

⚠️ NEEDS YOU:
- [items requiring your decision, with context and deadline]

📅 UPCOMING (7 days):
- [calendar events, deadlines]

🧠 MEMORY NOTES:
- [count of active memory notes]
```

5. Rules:
   - Auto-handle means take the action silently: send a reminder, check a file, query a database.
   - Never auto-send to other people, never auto-delete, never auto-publish. Those always escalate.
   - Be concise. Abah reads this on Telegram during breakfast.

## Tools

- recall
- save_note
- list_events
- bash
- search_web

## Write

`output/01-briefing.md`
