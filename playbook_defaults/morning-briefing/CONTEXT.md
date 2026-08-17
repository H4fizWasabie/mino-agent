Purpose: Give the owner a one-message morning brief every day at 07:30: what happened overnight, what Mino owns today, and what needs attention.

Routing: One stage.

Inputs: authoritative local clock, list_reminders, manage_memory status, sqlite3 via bash, yesterday's post logs, today's schedule.

Outputs: a Telegram brief to the owner and output/morning-brief.md.

Safety: facts only — read from tools and files, never guess. If a source is unavailable, say so in the brief.