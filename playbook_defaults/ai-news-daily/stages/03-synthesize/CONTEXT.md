# Synthesize — compose the Threads post and the Telegram report (final stage)

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Previous stage | `../02-fetch/output/facts.md` | Full file | The verified facts to synthesize |
| Shared rules | `~/.mino/playbooks/shared/platform-rules.md` | Full | Platform boilerplate (clock, anti-skip, Telegram report rules) |
| Threads gate | `~/.mino/playbooks/shared/threads-gate.md` | Full | The composed Threads post must pass this gate |
| Runtime | Authoritative local date | Full | Date the report |

## Process

1. Read facts.md from the fetch stage. A story marked `Status: fetch failed` is reported as unverified — never invent details for it.
2. Compose ONE concise combined Threads post (≤ ~500 characters) covering the top stories. Run the Threads judgment gate (threads-gate.md) on it; rewrite ONCE if it fails; if it still fails, skip the post and write the reason to the report.
3. Compose the Telegram report for the owner: the date, one line of significance per story, source URLs, and the knowledge file path. Terse, structured, skimmable — the trend-researcher deliverable voice.
4. Write the knowledge file `~/.mino/knowledge/ai-daily/<YYYY-MM-DD>-ai-news.md` via write_file — exact dated filename from the run date (this is the playbook's primary output).
5. Send the owner the Telegram report EXACTLY ONCE via send_message with to=the owner (never re-send on retry or failure).
6. Write the complete report to the declared output `output/03-report.md` via write_file — exact path, including the composed Threads post text, links, and the knowledge file path.
7. Verify the declared output exists before finishing.

## Tools

- write_file
- send_message

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Final report | `output/03-report.md` | Markdown: composed Threads post, report, knowledge file path |
