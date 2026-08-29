# Mino Galaxy UI/UX — Wayfinder Map

## Destination

Galaxy is Mino's read-only exploration surface: a light, no-glow orbital atlas
for durable entities, communities, relationships, search, inspection, and
history. Today, Work, Memory, Routines, System, and Conversations retain
operational ownership.

Measurable: Galaxy startup transfers and renders a bounded overview, explicit
selection loads a bounded neighborhood, authoritative search still comes from
`/api/memory/remember`, `/api/universe` remains backward-compatible, and the
data/projection seam supports a 100k-node / roughly 1M-edge source graph without
eagerly sending or drawing the full graph.

## Decisions so far

- [GitHub #246 — Galaxy visual language](https://github.com/H4fizWasabie/mino-agent/issues/246) — **resolved**: depth comes from scale, perspective, and occlusion; community uses restrained hue and orbital region; no glow or decorative activity.
- [GitHub #247 — Product ownership](https://github.com/H4fizWasabie/mino-agent/issues/247) — **resolved**: Galaxy explores and routes; canonical operational surfaces own mutation, schedules, runtime controls, and conversation execution.
- [GitHub #248 — Scalable graph delivery](https://github.com/H4fizWasabie/mino-agent/issues/248) — **resolved**: keep `/api/universe`; add read-only overview, community, entity, search, and refresh projections. Every returned edge carries both endpoints; initial refresh may refetch.
- [GitHub #249 — Native rendering contract](https://github.com/H4fizWasabie/mino-agent/issues/249) — **resolved**: dependency-free WebGL2 with truthful Canvas2D fallback, bounded LOD budgets, supplementary keyboard/search/index access, and rendering suspended at rest.
- [GitHub #250 — Search-to-orbit](https://github.com/H4fizWasabie/mino-agent/issues/250) — **resolved**: debounce exact queries to `/api/memory/remember`, preserve server rank and camera state while typing, load a bounded projection only on selection, persist selection in a deep link, and ignore stale requests.

## Frontier

- [GitHub #245 — Galaxy UI/UX Wayfinder](https://github.com/H4fizWasabie/mino-agent/issues/245) — open umbrella for later measured iteration.
- [GitHub #254 — First production slice](https://github.com/H4fizWasabie/mino-agent/issues/254) — implementation and release gate.

## Out of scope

- Operational mutation from Galaxy
- Dark mode, glow, perpetual force physics, or invented topology
- Mobile-first Galaxy navigation
- Replacing the existing graph query authorities

---

# Mino Verification Gap — Wayfinder Map

## Destination

Mino verifies task completion against actual state, not just tool call success. When Mino says "done," the change is actually in place — validated by evidence, not assumption.

Measurable: the next time Mino mutates runtime state (schedule, cron, config, playbook), it reads back or lists the result before declaring done.

## Notes

- **Language:** Go (harness) + English (SOUL.md, skills)
- **This is a reasoning gap, not a tool gap.** Mino has bash, read_file, write_file — all the capabilities needed to verify. The model just doesn't think to verify.
- **Real examples from today:**
  1. Surgery schedule: rm -rf playbook dir ✅, forgot schedules.json ❌
  2. Cron safety net: wrote script ✅, ran once ✅, forgot crontab ❌
  3. Playbook delivery: patched 7 files ✅, created safety net ✅, but delivery only happened because safety net caught it after

## Decisions so far

- [VFY-001 — Root cause](tickets/vfy-001-root-cause.md) — SOUL.md says "silently verify" and trusts "tool evidence" (which is just "ok"). Mino thinks, doesn't check.
- [VFY-002 — Harness fix](tickets/vfy-002-harness.md) — Enrich tool responses: cancel_schedule returns what was removed, bash cron includes verification hint, new system_check tool for state summary. Fix SOUL.md: "silently" → "with tools."
- [VFY-003 — Model fix](tickets/vfy-003-model.md) — Not the primary approach. Single SOUL.md word fix is the only model-side change.
- [VFY-004 — Validation](tickets/vfy-004-validation.md) — Regression test with a test playbook: remove it, check traces show verification calls before reply.

## Frontier (open tickets)

- [VFY-001 — Root cause analysis](tickets/vfy-001-root-cause.md)
- [VFY-002 — Harness-level interventions](tickets/vfy-002-harness.md)
- [VFY-003 — Model-level interventions](tickets/vfy-003-model.md)
- [VFY-004 — Validation approach](tickets/vfy-004-validation.md)

## Out of scope

- Fixing the specific incidents (surgery schedule, cron, playbook delivery) — Mino already resolved those
- Changing the iteration cap
- Provider/model changes

---

# Mino Schedule Reliability — Wayfinder Map

## Destination

Every scheduled playbook run either happens (on time, or caught up late) or leaves a visible record — never silent. One long routine must not starve sibling schedules; a run missed during downtime must be fired late or reported.

Measurable: a schedule whose window passes without a run appears in `schedules.json` as `missed: true` and produces a Telegram notice; a sibling schedule always fires even when another routine overruns its window.

## Decisions so far

- [SCH-001 — Root cause](tickets/sch-001-root-cause.md) — **confirmed** against playbook.go: serial dispatcher, 1-min window, no catch-up, `LastError` only on fire-and-fail. "Due but never fired" has no representation.
- [SCH-002 — Harness fix](tickets/sch-002-harness-fix.md) — **resolved**: per-schedule goroutine + synchronous slot claim (starvation), boot catch-up same-day-only (downtime), `missed_at` + one outbox notice + trace + audit (silence). Never-run schedules are not flagged.
- [SCH-003 — Validation](tickets/sch-003-validation.md) — **resolved**: 8 tests / 15-case classify table at the `dispatchDueSchedulesAt(core, now, run)` seam; 355 total pass, `-race` clean.

## Out of scope

- Ticker cadence / timezone semantics
- Full cron-style job framework (roadmap: playbooks, not a job framework)
- Missed-run alerting policies beyond always-notify

## Not yet specified

- Production observation (sch-003): confirm missed runs surface in dashboard traces/audit after deploy.

---

# Mino Quality Frontiers — Wayfinder Map

## Destination

Memory growth is bounded at the source (distill acceptance), recall carries no dead machinery, and drift churn (parse failures) is measurably below its 08-08→08-13 baseline. Telegram delivery carries crow-grade formatting (sections, threading, keepalive).

## Frontier (open tickets)

- [GIG-001 — GIGO at the distill source](tickets/gig-001-gigo-distill.md) (GitHub #178, open) — **principle decided 2026-08-13**: procedural/long-term split via existing `type` field (episodic = traversal-only + 30d expiry; semantic = protected); per-playbook distill gate (semantic ON: daily-ai-concept only); `save_note` stamps `Source: "user"`; provenance + regex gate skipped. Implementation-ready.
- [EMB-001 — Remove embeddings entirely](tickets/emb-001-embedding-recall.md) (GitHub #179, open) — **principle decided 2026-08-13**: full removal; floor = essentials + FTS5 on user-vocabulary descriptions + skill triggers. Recall gap-fill dead (0/80), dedup broken since RekeyFacts, tool gating a convenience layer. Tool descriptions strengthened as data change. Implementation-ready.
- [DRF-002 — Staleness enforcement](tickets/drf-002-stale-enforcement.md) (**resolved** 2026-08-14) — the enforcement half of drift prevention: 30d backstop archives model-authored semantic facts (reason "stale", archive fallback answers), optional `stale_after` front-matter for volatile facts, subject-based conflict markers, and correction demotion (only user/agent corrections demote model facts — asymmetry kept). save_note stamps `model-distill` inside playbook runs.
- [DRF-001 — Memory & context drift prevention](tickets/drf-001-parse-drift.md) (GitHub #180, open) — **principle decided 2026-08-13**: self-awareness layer (v2.8.11) verified by before/after VPS data (churn collapse, FB failed→complete); remaining gaps = provenance weighting, recall contradiction marker, rebuild provenance guard. Implementation-ready.
- [TGM-001 — Telegram enrichment](tickets/tgm-001-telegram-enrichment.md) (GitHub #181, **resolved** — shipped v2.9.0) — `---` section split with reply threading, italic/underline/spoiler/blockquote, 4s typing keepalive; crow-specific pieces skipped.

## Out of scope

- Embedding consumers other than fact recall (tool gating, skill selection)
- Loop-to-cap drift (governed by CTX-006)

---

# Mino Daily-Job Reliability — Wayfinder Map

Canonical tracker: GitHub issue #115 (map + child tickets REL-01..REL-06). This local file is not the source of truth for the REL series.

---

# Mino Context Truth — Wayfinder Map

## Destination

A turn's established knowledge survives into the next turn; a user-named number is verified against ground truth, never confirmed by proximity; a run that degrades stops cheaply instead of burning to the iteration cap.

Measurable: (1) a session whose previous replies exceed `inputPreviewLimit` still has the method (paths, commands) in context; (2) a user-named value that differs from a computed one produces a reply stating the discrepancy; (3) a turn with repeated `tool_call_parse_failed` iterations stops before the iteration cap.

## Notes

- **Incident 2026-08-10 (telegram session):** the user challenged an item's inclusion in an app's analytics chart. Mino burned 30 iterations, hit the cap, replied "(stopped after 30 iterations)". The user gave up and fetched the data themselves.
- **Ground truth:** the app's analytics module (its source, `internal/analytics/analytics.go:178`) computes the chart as `out+adj_out × cost` over items whose behaviour is "in-house use". The chart's July value matched the user's recollection exactly; Mino's report was built from invented SQL (net depletion × cost over all items of the type, no behaviour filter) and differed from the chart by ~4% — and included the challenged item, whose behaviour value correctly excludes it. The deployed binary was verified against the app's source: identical analytics queries, same frozen baselines, July not frozen (computed live).
- **Why Mino failed:** the three previous replies (20-25k chars each) exceeded `inputPreviewLimit` (8000) and were wholesale-replaced with a bare placeholder — the method-bearing `[tools used:]` trails never reached the model. It started the next turn at a different project's development database, re-derived everything from scratch, and confirmed by proximity ("essentially correct") instead of exact match.
- **The model self-diagnosed mid-session:** wrote a `remember` note "directly query the database without searching around first" — compensating for harness-level rot with a pull-memory note that lacked the path.
- **Skills rejected as a fix:** an existing skill carries the correct database path and was listed earlier that session — walked past anyway. A static skill rots the same way the model does.

## Decisions so far

- [CTX-001 — Root cause](tickets/ctx-001-root-cause.md) — **confirmed** against session.go + VPS state.db: the 8000-char wholesale replacement is the primary rot source; proximity confirmation (VFY class) the secondary; no stop signal the multiplier.
- [CTX-002 — Head/tail large-message preview](tickets/ctx-002-head-tail-preview.md) — **resolved** (closes #145, commit 4ffae81): messages over the limit keep first 4000 + last 4000 chars with HEAD/TAIL markers; the tail carries the trails. Test: `TestContextMessagesKeepsMethodTailOfLargeMessages`.
- [CTX-003 — Verification discipline](tickets/ctx-003-verification-discipline.md) — **resolved** (closes #149): system prompt rule — user-named ≠ computed must state both numbers and the gap; "verified" only from source of truth.
- [CTX-004 — Working-state persistence](tickets/ctx-004-working-state.md) — **resolved** (closes #146): per-session `session_notes` row, appended by the harness (bash commands) and the model (`note_session` tool), injected at turn start, bounded 1500 chars.
- [CTX-005 — Cancel-intent recognition](tickets/ctx-005-cancel-intent.md) — **resolved** (closes #148): natural cancel phrasings stop; doubt/cancel hybrids proceed as turns.
- [CTX-006 — Degeneration guard](tickets/ctx-006-degeneration-guard.md) — **resolved** (closes #147): parse failures counted per-turn total, abort at 6 — closes the alternation loophole the 2026-08-10 run exploited.

## Frontier (open tickets)

- [CTX-007 — Dashboard client disconnect can wedge the session mutex](tickets/ctx-007-wedge.md) — **resolved** (closes #152): the loop's LLM calls now go through the ctx-aware path; a dead client connection propagates into the provider call and the loop returns instead of wedging. Regression test included.
- [CTX-008 — Provider policy docs lag the main-model change](tickets/ctx-008-policy-docs.md) — **resolved** (closes #151): policy file, cost-watch monitored set, and docs now declare deepseek:deepinfra as main; the swap is permanent.
- [CTX-009 — Native send_document tool](tickets/ctx-009-send-document.md) — **resolved** (closes #153, commit ff4ecec): outbox `doc_*.json` drafts delivered via multipart `/sendDocument`; token never in args. Awaiting release to the VPS.
- [CTX-010 — Log provider failure reasons](tickets/ctx-010-provider-failure-logging.md) — **resolved** (closes #154): every failed provider call logs provider/role/model/error; circuit-breaker trips log the cooldown.
- [CTX-011 — Stop-word anywhere stops](tickets/ctx-011-stop-anywhere.md) — **resolved** (closes #157): "its fine, stop" now cancels; questions about stopping still proceed.
- [CTX-012 — Interrupt replies dropped on tool-call answers](tickets/ctx-012-interrupt-empty.md) — **resolved** (closes #156): no schemas in the interrupt call, plain-text instruction, snapshot-status fallback.
- [CTX-013 — Stale workaround memory overrides the native tool](tickets/ctx-013-send-document-unpinned.md) (resolved, #155) — deleting the four stale notes was necessary but insufficient: `send_document` was non-essential, so it wasn't in the schema at a send turn and the model curl'd the bot token in bash args (observed 2026-08-11). Fix: promote `send_document` to essentials (reverses the original "no pinning" call; production showed availability, not preference, was the failure). Regression: `TestSendDocumentIsEssential`.
- [CTX-014 — Memory facts surface age at recall](tickets/ctx-014-memory-freshness.md) (resolved, #172) — live recall now appends `age: Nd` (and `(possibly stale)` past 30d) to the match rationale via the existing `At` field; ranking score untouched. Code-checked first: `At` existed but was unwired; `Feedback` only did rejection-expiry (MEM-08). First witnessed case for the OKF `stale_after` idea we skipped — and the field was already there.
- [CTX-015 — Consolidation selects by recency](tickets/ctx-015-consolidation-recency.md) (resolved, #173) — `ConsolidateDue` gated eligibility by a per-session row-count floor (~12 rows), so short chat sessions never consolidated (78 rows stuck) and a 0-result pass let the model fabricate "consolidated 8." Now gated by recency (oldest unconsolidated row ≥1h), so chat drains while active conversations stay protected; the tool reports "nothing eligible" truthfully when 0 qualify.
- **Harness framing (open): the LLM is a component; Mino-the-harness owns the conditions (context, tools, signals, grounding). Every session failure was a harness gap, not a model fault.** The three-level ladder is harness engineering, not "teach the LLM to fix itself." Prerequisite for all three: the brain knows it is the brain — an explicit identity block telling the LLM it is Mino's mind and that its tools/memory/loop/traces are its own body (self-repair is incoherent without it; the harness supplies the identity too).
  - [CTX-017 — Post-mortem: Mino diagnoses its own failures from its own traces](tickets/ctx-017-postmortem.md) (open) — trace-cited diagnosis, or labeled hypothesis (CTX-016 applies to self-narrative). Lowest risk; the natural next build.
  - [CTX-018 — Design-time: Mino audits its own playbook contracts](tickets/ctx-018-design-time-review.md) (open) — risk-flags, not assertions; the FB churn fix made preemptive.
  - [CTX-019 — Mid-flight: Mino provides stop/redirect signals](tickets/ctx-019-mid-flight.md) (open, hard frontier) — act on the verified signal; self-explanation is provisional. #171 is the seed.
- [CTX-020 — Cost awareness & privacy: cost-watch feeds the brain](tickets/ctx-020-cost-awareness-privacy.md) (implemented, #176; released v2.8.13) — Mino is blind to its own spend (usage.jsonl only pages the owner). Scope: privacy swap (OpenRouter/DeepInfra main, drop direct DeepSeek — config-only); system_check cost block; per-run cost; daily cost observation; and a model-agnostic cost catalogue (targets derived from the user's providers.json, cost-watch reads an editable hot-reloaded config Mino maintains itself, hardcoded policyPrices moves to config). Privacy is a hard constraint, cost a soft preference — never route to a `trains` provider.
- [CTX-021 — Updater: 30s client timeout too tight for the 22MB binary download](tickets/ctx-021-updater-download-timeout.md) (**resolved**, #177) — the download now uses its own 5-minute client; the releases-API checks keep the 30s client (v2.9.0, locked by `TestDownloadTimeoutExceedsCheckTimeout`).
- [UPD-001 — Same-version rebuilds skip the update (stale binary deployed silently)](tickets/upd-001-same-version-rebuild.md) (**resolved**, #231) — `isNewer` compared only semver numerics, so a stale same-version build made `mino update` skip the fresh release (live 2026-08-16: the v2.11.0 RUN map needed a manual swap). `DoUpdate` now compares the running binary's sha256 against the release checksum at the same version: positive identity match skips, anything else proceeds through the health-checked `applyUpdate` path.
- [CTX-022 — Memory retrieval is loop-private; retirement semantics + provenance-gated verification](tickets/ctx-022-memory-external-surface.md) (**resolved** 2026-08-15, GitHub #194) — Part A: `remember` (entryRanking + BFS + provenance + conflict flags) exists only as an in-loop tool; an external agent (same model, 2026-08-15 session) had to scrape `memories/*.md` over SSH. Fix: `mino remember` CLI + MCP server + localhost/auth-gated REST, read-only, reusing `GraphMemory.Remember`. Part B: owner "keep as history"/"forget" maps to archiving the target facts — the Agent-Reach case left `agent_reach_tool_description` live and rankable (stale why/use_when fed a wrong "still actively maintained" answer) because `memories/archive/` is never created and the prompt has no retirement guidance. Fix: prompt guidance (archive tier, not live) + archive-dir init + regression test. Part C: the demo's wrong answer was harness-side — the verify rule (session.go:110 "live state is truth") biased the model toward web over user-provenanced memory; fix is a provenance gate in the prompt + a mid-flight warning when web search follows a user-provenanced recall (nerves.go pattern).

## Out of scope

- New skills as the fix (proven to rot: walked past during the incident session)
- Changes to the 30-iteration cap itself (the guard stops early; the cap stays)
- Reversing or duplicating history ordering (chronology is required; tail is already the newest)

# Mino Context-Awareness & Tool-Loading — Wayfinder Map

## Destination

Mino's context is lean at the eager-injection layer (skills loaded by section, not full body) and the model can self-regulate (see iterations/retries, diverge or stop before the cap) instead of burning to a silent harness cap.

## Decisions so far

- **Frontier (not yet a ticket): the working-set *choice* layer (#4).** Mechanism side (lazy fetch: `remember` pointers, artifact catalog, 8-tool playbook scoping, bounded history) is ~shipped; choice side (model can perceive/prune/compress its own window mid-turn) is ~0 — every model context tool is a *writer* (`note_session`, `save_note`, `add_working_memory`), none reads or shrinks the current window. Deferred by design: choice needs awareness (#171) first; revisit with a real case after #170+#171 land. (Measured 2026-08-12: playbook `one_turn` 18–28k chars ≈ 2–3× the whole ~2.4k static prompt.)
- [GitHub #170 — Eager skill bodies injected en bloc (no section routing)](https://github.com/H4fizWasabie/mino-agent/issues/170) — measured token waste on automation; only `image-generation` needed by playbooks.
- [GitHub #171 — Iteration/retry awareness + containment](https://github.com/H4fizWasabie/mino-agent/issues/171) — expose live `i/maxIter` + repeated-tool signal; model-visible rule to diverge or give up before the cap. Driver: 2026-08-12 FB `01-post` 50-iteration research churn (pre-contract-fix).
- [CTX-024 — Mino doesn't know its own max-token budget](tickets/ctx-024-budget-awareness.md) (**resolved**, #240) — per-turn budget block (chars used / ceiling / headroom) in the turn tail (clock pattern, prefix-cache-safe) with a locked-template threshold warning at ≥70% (also covering the 90% level): "compact or consolidate, or wrap up with a status report" — never skip/rush; informational only, verification discipline stays absolute.
- [TOOL-006 — Native screenshot capability](tickets/tool-006-native-screenshot.md) (**resolved**, #239) — the harness had `view_image` (read) but no `screenshot` (capture); auth'd JS dashboards cost ~15 bootstrap iterations of wkhtmltoimage/curl/playwright/proxy work (live 2026-08-16). New `screenshot` tool: URL or local file → wkhtmltoimage (host tool) → PNG in the RUN-007 spill store → path for `view_image`; every failure names the requirement (headless browser via `install_package`) — the #235 no-phantom-success contract.
- **Static system prompt is thin (~2.4k tok), not fat** — measured; trimming it is a low-value/high-regression lever. The real token cost is the [eager skill injection](#170), not the static prompt.
- **Tool-schema union is correct as-is** — chat=20 wide (legit), automation=8 tight (legit).

## Out of scope
- Context-budget telemetry (#2) — rejected pre-#240; the #171 stop-on-spin guard stops repetition, but live evidence (2026-08-16: 30-iteration turns at 30k+ context) showed the model still needed its own budget numbers — now shipped as per-turn budget awareness (CTX-024).
- Multi-owner trust/provenance (single-owner Mino)
- The 30-iteration cap itself

# Mino Provider Coupling — Wayfinder Map

## Destination

A provider or model swap is a config edit, not a code change: no `name ==` branches in the client/dashboard, no model lists in Go, one generic transport seam per family.

## Frontier (open tickets)

- [PRV-001 — Provider/model coupling](tickets/prv-001-provider-coupling.md) (**resolved**, #190) — explicit `transport` field (openai/anthropic/codex) declared in providers.json; URL sniffing, hardcoded codex model lists, and dashboard name literals removed. Model-list change = oauth.d edit.

## Out of scope

- Provider stack rewrite (routing/circuit-breaker behavior is settled, #159 stays)
- Multi-owner anything

# Mino Observability & Reliability — Wayfinder Map

## Destination

A stuck or failing Mino pages the owner within minutes, not hours; a crash leaves reconcilable state, not orphans; and every background path either completes, logs, or pages — diagnosis comes from the journal, never from accident.

## Frontier (open tickets)

- [OBS-003 — Universe overview node density](tickets/obs-003-universe-overview-density.md) (**resolved** 2026-08-14) — overview shows the top 15-25% of nodes by degree at zoom < 1.5 instead of a ~30-node hub skeleton.
- [OBS-001 — Observability & log coverage](tickets/obs-001-observability-and-log-coverage.md) (**resolved** 2026-08-15, GitHub #191) — trace-freshness heartbeat (page on 15 min of trace silence; the 5-min edge-judgment ticker is the free signal — would have caught the 08-14 wedge in minutes), boot reconciliation of `running`-stuck runs, and a log-coverage inventory (every path completes/logs/pages). No new metrics infra — the trace journal is the log.
- [OBS-002 — Playbook-stage audit coverage](tickets/obs-002-playbook-stage-audit-coverage.md) (**resolved** 2026-08-15, GitHub #193) — tool calls inside scheduled playbook runs never reach `audit.jsonl`: 61 images generated since 08-08, zero `generate_image` audit entries (evidence only in traces + run logs). Playbook-stage dispatch should emit to the shared audit with playbook/run/stage attribution so `query_audit` answers "what did the run do" alone.

## Out of scope

- New observability sinks/dashboards
- The 6h dead-man's switch semantics (slow backstop stays)
- The DRF-001 judgment gap (verification ≠ visibility)

---

# Mino Playbooks — Navigable Workspaces

## Destination

Make each playbook a navigable filesystem workspace that one Mino agent can
enter, understand, execute, recover, and maintain. The public vocabulary stays
"playbook"; the workspace supplies routing, selective context, stage contracts,
personas, references, tools, artifacts, run state, and verification evidence.
Unlike the source ICM framework, Mino playbooks are autonomous and do not
require human checkpoints between ordinary stages.

Confirmed direction: stage tool declarations become capability guidance rather
than rigid allowlists; runtime policy remains authoritative for tool access,
risk, retry, audit, and approval.

Confirmed direction: every run starts with workspace orientation, then narrows
to stage execution. Outputs remain durable run artifacts and pass through
Mino's existing handoff, audit, and selective memory-distillation paths; the
workspace does not bypass output filtering or promote every artifact to memory.

Confirmed direction: Mino adopts ICM's five-layer loading protocol as the
playbook context contract—workspace map, root routing, stage contract,
selective references, and selective working artifacts—while Mino runtime owns
run state, filtering, audit, memory distillation, and autonomous recovery.

Confirmed direction: Layer 0 is a root `AGENTS.md` per workspace, containing
the map, triggers, hard rules, routing, source-of-truth boundaries, and load
exclusions. Persona remains a distinct workspace voice/role layer and uses
Mino's existing binding and validation path.

## Frontier

- [PB-001 — Playbooks as navigable workspaces](tickets/pb-001-navigable-workspaces.md) (**open**, GitHub #380) — define the workspace model, failure-recovery boundaries, and the narrow implementation tickets that follow.

## Out of scope

- An agent for every task, stage, persona, or workspace.
- A second workflow engine or agent loop.
- Blind retries after uncertain external mutations.
- Mandatory human checkpoints between ordinary stages.
- Relying on stage tool lists as the primary safety boundary.

---

# Mino Playbook Personas — Wayfinder Map

## Destination

Every playbook run wears a lean, purpose-built prompt profile instead of Mino's full chat
personality: harness rails + one identity anchor + a per-playbook agent persona (a "hat" the same
brain wears — single-agent architecture, no new runtime). Each playbook binds deterministically to
one hat from a shared roster, so refining a hat improves every playbook wearing it.

Measurable: playbook runs carry no chat-voice sections (~70% system-prompt cut per call), all 15
playbooks bound to 6 roster hats, and one week of live runs at same-or-better
cost-per-verified-story vs the pre-persona week.

## Decisions so far

- Personas proven in the zero-code interim (CONTEXT.md blocks, this week): more verified stories
  at same-or-lower cost, visible discipline. 15 playbooks / 16 stage contracts wear 6 hats today.
- Persona grammar is "operating as", never "you are" — stance/mission/lens/voice only, no
  identity claims; rails (harness-owned) override any persona instruction.
- Rails extraction must include the `notify: true` → Telegram rule — model-delivered, the runner
  does not enforce it (only missed-schedule notification is enforced in code).
- The roster binds deterministically from `config.md` (`agent: <name>`), not fuzzy-matched like
  skills — byte-stable per run, warm prefix cache across same-hat runs.
- Interim hats stay in CONTEXT.md until the runner change lands; backups exist
  (`CONTEXT.md.bak-persona`).

## Frontier (open tickets)

- [PSN-001 — Playbook persona profile swap](tickets/psn-001-playbook-persona-profile.md) — the
  runner-level change: `BuildPlaybookSystem` branches to rails + anchor + persona, `agents/`
  roster, `config.md` validation, seam tests, release lane.

## Out of scope

- Multi-agent execution / new runtimes (canonical loop stays the sole agent loop)
- Per-stage personas (YAGNI — 12 of 15 playbooks are single-stage)
- Rewriting the chat system prompt (chat profile stays as-is)

---

# Mino Data Residency (JSONL/MD → SQLite) — Wayfinder Map

## Destination

Every `~/.mino` store lives in the format its access pattern justifies. Append-only telemetry that code reads wholesale moves into `state.db`; stores that are files by standing decision stay files. Measurable: no reader in the core binary parses an unbounded file to answer a bounded query.

## Evidence inventory (VPS /home/mino/.mino, 2026-08-22)

| Store | Size | Readers / writers | Verdict |
|---|---|---|---|
| `state.db` | 44M | chat_log, tool_calls, audit_events, responsibilities, ops_journal, session_artifacts/notes | already the DB (§4) |
| `usage.jsonl` | 3.5M, 18.6k lines, unbounded | provider_manager writes; dashboard + cost.go read the WHOLE file per stats render (`usageRecords`) | **migrate — DATA-001** |
| `traces/*.jsonl` | 12M, 23 daily files, no pruning | loop writes; dashboard tail + post_mortem read by date | retention, not migration — DATA-002 |
| `audit.jsonl` | ~5k lines | §8 immutable audit log; mirrored into `audit_events` | stays |
| `memories/*.md` | 8.2M | §23 authoritative semantic graph facts | stays |
| `results/` + playbook run dirs | 30M | §6/§20 filesystem is the durable record | stays |
| `schedules.json` | tiny | SCH series source of truth, human-editable | stays |

## Decisions so far

- [DATA-001 — usage.jsonl → SQLite](tickets/data-001-usage-jsonl.md) (GitHub #344, resolved) — unbounded append, wholesale reads.
- [DATA-002 — trace retention policy](tickets/data-002-trace-retention.md) (GitHub #345, resolved) — wire traces/ into the existing sweep; SQL adds nothing for by-date access.

## Out of scope

- memories/*.md → SQL (reverses §23 for zero access-pattern gain)
- Playbook run state/results → SQL (reverses §6)
- schedules.json, audit.jsonl, auth/config JSONs
