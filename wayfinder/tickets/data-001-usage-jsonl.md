# DATA-001 — usage.jsonl → SQLite

Status: **RESOLVED** (wayfinder ticket DATA-001 — GitHub issue #344, shipped with schema v8)

## Question

`~/.mino/usage.jsonl` is appended forever (18.6k lines / 3.5M since 07-31) and every consumer parses the whole file to answer a bounded question ("spend today", "tokens this week"). At what point does it move into `state.db`, and what does the migration look like?

## Evidence (2026-08-22)

- Writer: `provider_manager.go:110,568` — one JSON line per LLM call.
- Readers, all in-repo, all wholesale:
  - `dashboard.go:1930` `usageRecords()` — full `os.ReadFile`, split, unmarshal every line; called from two dashboard endpoints.
  - `cost.go:96,118` — same `usageRecords()` for spend aggregation.
- cost-watch does **not** read it (reads providers.json + mino.env) — no extension contract break.
- VPS size: 3.5M and growing linearly with usage.

## Options

1. **Migrate**: new `usage_log` table (mirror the record fields), writer swap in provider_manager, readers become SQL aggregates. One-time backfill script over the existing jsonl, then retire the file.
2. **Keep file, add rotation**: archive `usage.jsonl` monthly like traces-by-date already does. Cheapest, but readers still parse a month of lines per request.

## Open questions (for discussion)

- What's the actual pain driving this — dashboard latency, disk, or queryability? At 18k lines nothing hurts yet; if the driver is only tidiness, option 2 or "do nothing" wins on YAGNI.
- Schema: exact columns vs one `record TEXT` blob with indexes on timestamp/provider/model only.
- Backfill: import history or start clean at migration day (history stays readable in the archived jsonl)?
- Anything outside this repo reading the file directly (scripts, cron)?
