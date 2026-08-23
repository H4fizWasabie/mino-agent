# DATA-002 — Trace retention policy

Status: **RESOLVED** (wayfinder ticket DATA-002 — GitHub issue #345, shipped with schema v8)

## Question

`traces/YYYY-MM-DD.jsonl` accumulates with no pruning (23 files / 12M as of 08-22). Migrate to SQLite, or just delete old files?

## Leaning

Retention, not migration. OBS-001 already decided "the trace journal is the log" — no new infra. Every trace consumer is by-date (`post_mortem.go:112` opens `traces/<date>.jsonl`; dashboard tails recent dates). SQL adds nothing for grep-by-date access; deleting files past N days is the lazy fix.

## Open questions

- N days? post_mortem looks back days, not weeks — 30d matches `auditRetentionDays`.
- Prune in-process (daily ticker already exists via edge-judgment) vs cron/systemd timer.
- Archive before delete or hard delete (backup strategy §14 says one dir = everything; traces are reproducible debug output).
