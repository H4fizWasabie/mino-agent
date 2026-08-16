# Runtime Self-Management — Durable spill artifacts

Status: **RESOLVED** (wayfinder ticket, RUN-007 — GitHub issue #221)

Resolved 2026-08-16: the spill store moves from `/tmp/mino/results/` (RAM-backed,
died on reboot) to `~/.mino/results/`, with a 30-day max-age prune as the
retention bound. `spillDir(home)` is the single routing helper — every spill
writer goes through it, and it runs the throttled sweep (boot + once an hour
on writes). Dashboard artifact-browser roots follow. Tests:
`TestPruneSpillsRemovesOldArtifacts`,
`TestCompactToolOutputWritesArtifact` (moved-path assertion).

## Question

Full tool outputs must survive reboot: `compactToolOutput` (loop.go, issue #99)
spills oversized results to `/tmp/mino/results/`, which is ephemeral — the
durable `tool_calls` table keeps only the 120-char `output_summary`. Needed by
the on-demand external-agent eval workflow (ex-#214). The move to `~/.mino/`
must come with a retention bound — unbounded growth there is the failure mode
the ticket exists to prevent.

## Decisions so far

- **Location: `~/.mino/results/`** (`filepath.Join(home, "results")` — `home`
  is already the MINO_HOME root). All writers routed: `compactToolOutput`,
  `toolTrailForHistory`, `compactUserInput`, Telegram downloads, and the three
  image generators (Cloudflare/OpenRouter/Pollinations). Dashboard artifact
  browser default + roots list updated (dashboard.go:1779/1866).
- **Retention: max-age pruning, 30 days** (same horizon as audit events),
  run at boot and throttled to once an hour on writes — a full directory walk
  on every spill write would tax write-heavy turns. Empty dirs collapse with
  the pruned files (deepest-first removal; `os.Remove` on non-empty is a
  harmless no-op under write races).
- **dsh spillStore verification (per ticket): no retention pattern exists to
  copy.** Files persist until external cleanup; per-session dirs only group
  files for that future cleanup (`spill-local/README.md`: "Local spill files
  persist until external cleanup — the backend has no session-lifecycle
  deletion or age-based retention policy"). Mino's bound is therefore our own.
- Deliberately not borrowed (as specced): dsh's read exemption (Mino's
  evidence-based opposite choice, loop.go) and a configurable cap (constants
  live-tuned; YAGNI).

## Out of scope

- Configurable retention cap
- Per-session deletion on session end (trails outlive sessions via chat_log
  references; max-age covers it)
