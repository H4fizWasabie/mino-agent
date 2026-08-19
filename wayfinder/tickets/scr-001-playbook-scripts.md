# Mino Playbook Execution — Script Runs (SCR-001)

Status: **RESOLVED** — implementation landed (2026-08-19, #272 Phase 1: `mino exec`
stub layer, script-backed stages, validation). Pilot conversions are follow-up ops.

## Before/After protocol (owner request, 2026-08-19)

**Pilot set (owner-selected):** `weekly-audit` (read-only) → `gmail-daily-cleanup`
(write-path, internal) → `instagram-daily-capability` (complex: image
generation, public posting — owner accepts extra posts). One conversion at a
time, each with an owner-reviewed committed script.

**BEFORE baseline — already captured from traces (08-12 → 08-19):**

| Playbook | LLM calls | in tok | out tok | turns | ≈tok/turn | parse fails |
|---|---|---|---|---|---|---|
| weekly-audit | 98 | 2.24M | 43k | 6 | 381k | 0 |
| gmail-daily-cleanup | 99 | 887k | 49k | 9 | 104k | 1 |
| instagram-daily-capability | 347 | 6.15M | 190k | 15 | 423k | 3 |
| **Total** | **544** | **9.28M** | **283k** | **30** | | **4** |

Metrics come from trace JSONL (`llm` in/out per call with run/stage attribution,
`turn_end` iterations, `tool_call_parse_failed`) + `runs/` timestamps + `runs/`
outputs.

**AFTER (one week post-conversion per playbook):** same metrics from the same
sources + `mino exec` calls in `tool_calls` + journal records + exit codes.

**Pass criteria:**
1. Same deliverables (audit reports produced; cleanup ran; posts+images posted
   — count from `runs/` outputs).
2. Zero silent failures — every non-zero exit → one Telegram notice; no missed
   runs (never-silent standard, SCH-002).
3. LLM tokens for the converted playbook ≈ 0 for the week (target: >90%
   reduction vs baseline).

**Instagram creative-payload decision (RESOLVED 2026-08-19):** hybrid — the
LLM keeps the creative steps, the script owns the pipeline. Instagram's stage
01 has 8 steps; steps 1–2 (topic choice + generate/view/critique/regenerate,
the existing material-flaw discipline in CONTEXT.md) stay LLM; steps 3–8
(sync, curl-verify, MCP account/post/publish, log, Telegram report) become
`script.sh`. Seam: the LLM writes its chosen image path + caption to a payload
file; the script reads it and executes the pipeline. The exclusion scan (step
1) becomes a script-produced one-line digest of recent topics instead of the
LLM reading full logs. Expected: ~6.34M → ~300–600k tokens/wk (~90% cut)
while keeping the LLM's creative role and critique discipline.

## Fleet classification (2026-08-19, all 15 playbooks)

| Class | Count | Playbooks |
|---|---|---|
| M (full script) | 7 | gmail-daily-cleanup, weekly-cost, ai-news-daily,
  malaysian-news-daily, morning-briefing, daily-ai-concept,
  threads-replies* |
| H (hybrid: script pipeline + LLM creative) | 7 | weekly-audit*,
  reddit-karma-builder, threads-community, threads-tribal-battle,
  threads-workplace-drama, facebook-daily-ai-post, instagram-daily-capability |
| J (judgment, stays LLM) | 1 | post-mortem |

*threads-replies listed M here but is H on reflection (reply-writing is
judgment; the read/filter half is script) — final call at detailed audit.

**Sequencing decision:** coarse classification now (this table — sufficient
for runner design); detailed per-playbook audit (seams, payload files, digest
scripts, token estimates) deferred until the instagram hybrid passes its
before/after week — the pilot validates the hybrid template before we apply it
14×. The detailed audit is itself an LLM task (judgment work), run as a
one-shot after the pilot.

**Runner implication:** hybrid is the majority (7/15) — script-backed stages
are a first-class stage kind (a stage is either LLM-steps as today, or
`script.sh`), not a special case.

## Decisions (2026-08-19 discussion)

- **The script is a committed artifact embedded in the playbook** — a reviewed,
  deterministic definition (cron-job style), authored by the owner (LLM assist
  only under review). The LLM never generates it at runtime, never improvises
  scheduled execution — it manages and observes. (Owner correction: earlier
  draft assumed LLM-authored scripts via manage_playbook; rejected.)
- **Runtime is bash, not Python.** Mino's motto is 1 binary / 1 sqlite — no
  external runtime dependency. Bash is the platform. The model already works
  this way (1,301 bash calls/week vs 205 threads calls).
- **Stub-call method**: the binary is the stub layer — `mino exec <tool>
  <args-json>` subcommand (main.go already dispatches subcommands; `exec` slots
  in as a sibling). Scripts call tools through it; every exec lands in
  `tool_calls` + audit like any tool call. Threads/composio extension tools are
  reachable through the same front door.
- **Tokens never appear in scripts** — secrets stay in mino.env; `mino exec`
  resolves them internally.
- **JSON tool calling is removed from the loop** (owner decision) — see CDE-001.
  Playbook scripts use `mino exec`; there is no JSON tool-calling path for
  scheduled runs either.
- **Regression gates (3 vectors)**:
  1. JSON removal affects the interactive loop only; playbook conversion is
     one-playbook-at-a-time, week-over-week output comparison vs the LLM
     baseline, LLM path as automatic fallback on non-zero exit.
  2. Scripts are reviewed before scheduling — the readable-before-it-runs
     property is the safety floor for scheduled work.
  3. Non-zero exit → journal record + one Telegram notice (same never-silent
     standard as SCH-002), then the LLM investigates as exception handler.

## Question

Scheduled playbooks are Mino's largest tool-call consumer, yet their work is
highly procedural (3–8 distinct tools each). Can a scheduled playbook run as a
**deterministic script** — the LLM managing and observing, not executing — with
no loss of failure visibility?

## Evidence (VPS state.db, 2026-08-12 → 08-19)

- 3,258 tool calls in 7 days. `scheduled-*` sessions: **1,490 calls (46%)**;
  main tg session: 958.
- Per scheduled session (calls / distinct tools):
  `threads-replies` 474/6 · `ai-news-daily` 162/5 · `reddit-karma-builder` 130/6 ·
  `facebook-daily-ai-post` 122/8 · `instagram-daily-capability` 108/7 ·
  `malaysian-news-daily` 105/5 · `weekly-audit` 86/3 · `gmail-daily-cleanup` 61/4.
- Every run pays per-stage LLM turns (one inference per tool link) for work a
  script would do in one pass.
- `run_playbook` failures this week are config-shape errors (missing
  CONTEXT.md; stage declares tools without `write_file`) — nothing that a
  reviewed script couldn't catch *before* running.
- Model deepseek-v4-flash-0731 is cheap (~$0.0765/M in via Decart), so the win
  is not spend — it is reliability, latency, and freeing the loop for judgment.

## Resolution direction

A playbook gains an optional executable: `script.sh` / `script.py` in the
playbook dir, alongside `CONTEXT.md` / `config.md` (python3.12 and bash exist on
the VPS). When present, the scheduler (SCH-002 machinery) runs the script
directly; the run is recorded in `runs/` + journal; exit code and output are
observable. LLM involvement shrinks to: authoring/updating the script via
`manage_playbook`, reviewing run output when asked, and exception handling —
non-zero exit → one Telegram notice via the existing outbox pattern (same as
missed-schedule).

Hybrid stages allowed: script stage + LLM observation stage + human checkpoint
stage (existing machinery untouched).

## Measurable

- `scheduled-*` session tool_calls drop to ~0/week for script-backed playbooks
  (from 1,490 today).
- Same outputs still delivered (posts, replies, cleanup) — compared against the
  week before conversion.
- Every script run leaves a journal record; every non-zero exit produces exactly
  one notice (never silent — same standard as SCH-002).

## Options considered

- **A. Keep LLM stages, trim per-stage context** — cheapest, but keeps the
  per-turn tax; PSN-001 personas already captured most of that win.
- **B. Pilot read-only playbooks first** (`weekly-audit`, `weekly-cost`) — the
  natural first slice; write-path playbooks (posts) convert after the runner is
  proven. Recommended as the landing slice.
- **C. Full cron-style job framework** — rejected; roadmap says playbooks, not a
  job framework (same line as SCH-002's out-of-scope).

## Out of scope

- Judgment playbooks (analysis, owner interaction) — stay LLM-driven.
- Cron/ticker semantics — SCH epic owns scheduling.
- Sandbox hardening beyond existing timeouts (codingToolTimeout).
