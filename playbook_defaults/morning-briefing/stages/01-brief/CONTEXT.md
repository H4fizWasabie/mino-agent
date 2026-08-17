# Morning Brief

## Process

1. Gather the facts with tools — never guess:
   - `list_reminders`: all pending reminders (note the nearest ones, especially any due today or overdue).
   - `manage_memory` with action `status`: current memory graph state (facts/edges/communities).
   - One `bash` call: `sqlite3 ~/.mino/state.db "SELECT id, status, next_action FROM responsibilities WHERE status IN ('blocked','needs_you','working') ORDER BY updated_at DESC LIMIT 8;"` — anything needing the owner or stuck.
   - One `bash` call: read today's schedule — `cat ~/.mino/schedules.json | jq -r '.[] | [.name, .time] | @tsv'` (local times) — and yesterday's published posts: glob `~/.mino/playbooks/*/runs/*/stages/*/output/*.md` filtered to yesterday's run dirs, extract just the topics/titles (first heading line of each).
2. Compose the brief — one Telegram message, tight sections: 🌅 **Overnight** (posts published yesterday + anything notable), ⏰ **Reminders** (nearest 3), 🧠 **Memory** (one line), ⚠️ **Needs attention** (blocked/needs_you, or 'none'), 📅 **Today** (the day's scheduled playbooks with times).
3. Send via `send_message` with to=the owner, EXACTLY ONCE.
4. Write the DECLARED output `output/morning-brief.md` with the same content (the message text verbatim).

## Tools

- `list_reminders`
- `manage_memory`
- `bash`
- `read_file`
- `write_file`
- `send_message`

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Brief | `output/morning-brief.md` | Markdown |