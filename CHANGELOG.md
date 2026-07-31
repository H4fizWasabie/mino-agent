# Changelog

## [Unreleased]

### Added
- Channel-neutral explicit capability selection and trace-visible tool schema names for registered extension tools
- Compact Option B desktop top navigation with the chat surface kept secondary
- Responsibility-aware Overview command center with current attention, verified outcomes, workspace shortcuts, and a separate runtime inspection surface
- One-off playbook Responsibility lifecycle: explicit runs now record acceptance, truthful completion or blocked outcomes, and read-back Evidence
- Truthful Routine journal slice connecting scheduled playbook results to Today, Work, dedicated history, and read-back Evidence
- Authoritative SQLite Responsibility projections, append-only history, and idempotent schedule/reminder baseline migration
- Dashboard redesign decision record covering the authoritative responsibility journal, editorial frame, truthful health and empty states, adaptive density, dated navigation, portfolio and detail views, channel conversations, mobile behavior, canonical product language, and state-first delivery sequence
- Memory graph index cache (`index.json`): O(1) startup, skips re-parsing all .md files
- Edge validation: dangling edge targets filtered on write
- Lossless SQLite fact archive and collision-safe graph migration manifest
- Graph-backed memory management, bounded consolidation edge inference, and graph edge rebuild maintenance commands

### Changed
- README now documents Markdown-authoritative graph memory, OpenRouter Exacto routing, and provider configuration without credentials
- Markdown graph claims are now authoritative for semantic memory, dashboard views, correction, forgetting, confirmation, and search; SQLite facts remain read-only diagnostics
- Interrupt status checks may use read-only tool schemas and dashboard SSE keeps the response handler alive until the interrupt reply is delivered
- State-changing tools expose verification-oriented summaries; `system_check` reports schedules, reminders, playbooks, and crontab state
- Memory graph keeps the full knowledge map visible while queries highlight matching neighborhoods, relationship labels appear on demand, and nodes use a richer deterministic color palette.
- Graph edges carry provenance and confidence; legacy generic overlap edges are rejected
- History split from chat_log: LLM sees clean reply, consolidation sees full tool trail
- `remember` tool description tuned: single call sufficient, reduces redundant queries ~60%
- **Tool schema selection tightened:** essentials shrunk 15→8 tools (+3 family), semantic threshold 0.25→0.40, cap 12→8, MCP tools keyword-gated, embedding input narrowed to last turn. Schemas per turn: 30→21 (-30%), trivial-turn tokens: ~14.5k→~12k (-17%).

### Fixed
- One-off Responsibility titles now stay concise while preserving the full accepted request as the outcome
- Scheduled Routine completion history records the actual finish time instead of copying its start timestamp
- Graph recall traverses reverse relationships and excludes ambiguous or low-confidence inferred edges from normal context
- Default session guidance and compaction markers use the graph-backed `remember` tool
- MCP tool selection excludes semantic embedding and applies a stable deterministic cap
- Consolidation now requests JSON mode and tolerates wrapped model output; graph memory also reads legacy front matter with unquoted scalar colons.
- Graph edge rebuild now uses JSON mode and never erases existing edges on empty or invalid inference output.
- Current-time grounding now repeats the configured local clock on the fresh user turn so stale history cannot override it.

### Removed
- FTS5 supplement path in graph traversal (dead code since migration to .md files)

## [v1.4.0] — Cross-Platform Scheduler & Dashboard Polish

### Changed
- Playbook scheduler: replaced systemd dispatcher with in-process Go ticker.
  `schedule_playbook` now writes `~/.mino/schedules.json` instead of systemd
  unit files. Scheduler runs cross-platform (Linux, macOS, Windows) with zero
  external dependencies. Added `list_schedules` and `cancel_schedule` tools.
  Existing systemd dispatcher is disabled on deploy.

### Fixed
- Dashboard reply cards now strip `[tools used: ...]` annotation from displayed
  replies (turnCard, executionTurn, chatTurnCard were missing `stripTools`).

## [v1.3.0] — Playbook Architecture & Production Hardening

### Added
- Playbook system — numbered markdown stages with `## Read` / `## Do` / `## Write`.
  Filesystem is the executor. Mini-loop with retries per stage. Memory routes
  vague prompts to the right playbook via embeddings + FTS5.
- Built-in `playbook-authoring` skill so Mino can create, improve, validate,
  and resume filesystem playbooks.
- Show parsed playbooks beside skills in the dashboard Memory view.\- `schedule_playbook` tool — schedule existing playbooks through the external
  systemd dispatcher without hand-editing shell or unit files.
- External systemd timer and Telegram delivery runner for scheduled playbooks.
- Persistent one-time reminders with Telegram delivery, listing, and cancellation.
- Context-aware tool selection — essential tools on every turn, specialist tools
  retrieved via FTS5 and optional embeddings.
- OpenRouter provider routing support with configurable provider preference.
- Multi-provider LLM support with priority, fallback, and circuit breaking.

### Changed
- Pass the original user request into every playbook stage so routed workflows
  retain task-specific context.
- Run playbook stages through the canonical Mino runtime instead of a second
  playbook-specific LLM/tool loop.
- Rewrite architecture and handoff documentation around playbook selection,
  the canonical runtime, filesystem state, and human checkpoints.
- Integrated Mino logo into dashboard sidebar, favicon, and onboarding screen.
- Keep dashboard tool activity rows compact; full arguments collapsed until opened.
- Increased playbook stage iterations from 8 to 10 for complex workflows.
- Default reasoning effort set to medium for Xiaomi MiMo providers.

### Fixed
- Always include `read_file` in playbook stage tools so `## Read` sections work.
- Allow playbook stages to write derived summaries of untrusted web content while
  blocking execution of instructions found in that content.
- Inject Mino's configured local date and time into playbook stages.
- Let Mino choose whether to use a matched playbook instead of bypassing reasoning.
- Keep the canonical loop as reason → tool execution → observation without a
  second deduplication cache or repeated-call state machine.
- Accept an output created or updated by the current stage attempt even when
  the model reaches its iteration limit, while rejecting unchanged old output.
- Make deploy builds reproducible and fail deployment if the VPS binary hash
  differs from the just-built local binary.
- Make scheduled runners encode API JSON safely, select only output created by
  the current run, and avoid overlapping executions per playbook.
- Remove obsolete programmatic approval tools; use explicit human checkpoints.
- Remove unused completion tool/protocol remnants.
- Retire stale checkpoints on explicit stop and consume restart recovery only once.
- Reject empty provider responses so fallback/error handling runs correctly.
- Remove dead rollback wiring and stale comments from config and tools.
- Remove dead `sessions` table query from dashboard metrics.

## [v1.2.0] — Agent Intelligence Upgrade

### Added — New capabilities
- **Safety guards** (§8) — four-layer protection: workspace boundary check, SQL gate, git auto-commit before destructive bash, immutable audit log (`~/.mino/audit.jsonl`).
- **Fan-out** (§15) — `fan_out` tool spawns N parallel delegates with WaitGroup, aggregates results.
- **Onboarding flow** (§16) — provider button grid (ChatGPT OAuth, Claude PKCE, manual key), auto-open browser, Telegram optional. No config needed to start.
- **`mino eval` CLI** (§17) — reads `eval/cases.json`, runs against real LLM, judges behavior (not output), writes `eval_report.json`, exits 0/1 for CI.
- **Observability** (§18) — alerting (error rate + dead man's switch), `/health` + `/metrics` (Prometheus), cost-per-session tracking, Telegram notifications.
- **Context-aware memory ranking** (§19) — `contextBoost` signal in `scoreFact`. Formula: `0.45×sim + 0.20×imp + 0.15×rec + 0.10×fdbk + 0.10×ctx`.
- **Structured task plans** (§20) — `TaskStep` struct + `Plan` field in `TaskSnapshot`. LLM declares plan via `complete_task`, harness persists and feeds back on restart.
- **Extension retry detection** (§21) — trigram similarity on consecutive same-tool outputs, alert when stuck ≥3 calls.
- **Eval auto-generation** (§22) — two-tier confidence (`manual`/`auto`), dashboard thumbs-up endpoint, auto-harvest from completed tasks.
- **File rollback system** — `write_file`/`edit_file` snapshot originals, `restore_files` tool for session-level recovery.
- **Text-embedded tool calls** — models without native function calling can write `[tool_call: name({...})]` in text.
- **Startup message** — `Mino is ready! Open: http://localhost:7779` printed on launch.
- **Cross-platform builds** — `build-release.sh` produces binaries for linux, darwin, windows (amd64/arm64).

### Changed
- Delegate worker upgraded with more tools, context injection, and response caching.
- Batch skill embeddings in one API call instead of N.
- Time string moved from system prompt to user message for cache stability.
- `maxReadOnlyStreak` reads `MINO_MAX_READ_ONLY_STREAK` env var (default 5).
- Dashboard and Telegram run together when port is configured.
- Telegram reply-to messages and scheduler notifications linked into session context.

### Fixed
- LLM call hangs no longer block the loop (90s per-call deadline).
- Approval keywords expanded for Telegram friendliness ("yes", "go ahead").
- `reasoning_effort` passthrough for OpenAI-compatible providers.
- Delegate registered before tool filter so it's always available.
- Alert datetime format mismatch (SQLite vs RFC3339) — false silence alerts stopped.
- `gitCommitBeforeBash` no longer pollutes project repo during tests.
- `eval/cases.json` uses `MINO_HOME` path instead of CWD-relative.
- Missing `--ink-dim` CSS variable — onboarding buttons now visible.
- Default port unified to 7779 across all components.

## [v1.1.0] — Core Upgrade

### Added
- RTK integration: automatic Bash command rewriting for compact test, Git, search, and log output (RTK installs separately — falls back to plain Bash if not present)
- Optional SQLite-backed project state tools (`project_get` and `project_update`)
- `MINO_WORKSPACE` — universal local editing boundary
- `MINO_TIMEZONE` for authoritative local time in prompts, schedules, and calendar
- `MINO_MAX_HISTORY_TURNS` — cap chat history to last N exchanges (default 5, 0 = unlimited)
- New config knobs: `MINO_BASH_TIMEOUT`, `MINO_CODING_TIMEOUT`, `MINO_SYNC_TIMEOUT`, `MINO_CONSOLIDATE_LIMIT`, `MINO_TELEGRAM_CHAT_ID`
- Scheduler: input validation, atomic file writes, one-shot jobs, duplicate prevention
- Keyword-based tool filter fallback when no embedding store is available
- Deploy script (`deploy.sh`) with configurable `VPS_HOST`
- Coding skill: absolute paths and workspace-aware staging guidance

### Changed
- Richer loop: completion protocol enforcement, no-progress detection, improved tool hygiene
- Raised default model output ceiling to 16K
- SQLite driver: mattn/go-sqlite3 → modernc.org/sqlite (pure Go, no CGo, FTS5 built-in)
- Dashboard: responsive Runtime Spine replacing static Overview
- Tool budgets per-turn reduced to six core + eight relevant, lowering prompt overhead

### Fixed
- Stop repeated-tool loops: observations preserve tool call, status, and cache state; exact duplicate actions never re-execute
- Recover from output-truncated tool calls without malformed argument execution
- Keep paired project read/write tools available together during dynamic filtering
- Action receipts recorded for every tool result (identity, status, proof, cache state)

## [v1.0.0] — Initial OSS Release

### Added
- Runtime-enforced `complete_task` protocol
- VPS-safe ChatGPT/Codex OAuth login with automatic token refresh
- Dashboard: add/remove API-key providers, OAuth login controls
- Keyless providers (`api_key_env` can be empty for Ollama, LM Studio)
- Native coding agent: 10 discovery tools + phased coding skill
- Multi-edit support: `edit_file` accepts `edits` array
- Context7 MCP default (no API key required)
- `read_file` 16KB limit (up from 4KB)
- `minowrap`: universal tool adapter for self-extending tools
- `reload_plugins` tool for hot-reloading extensions and MCP configs
- Graphify architecture index with semantic community labels
- Vision-aware provider routing (`text_only` providers skip image turns)
- Telegram rich formatting: bold, code, fences, links, headings, tables
- Tool filter: embedding-based top-K selection per turn
- `EmbedBatch`: batch embeddings (86 tools in <2s)
- Approval system: `request_approval` + `resolve_approval` for destructive ops
- `LLMClient` interface — test seam for deterministic evals
- Eval test suite (9 tests): scripted fake LLM, zero API cost
- `view_image` and `generate_image` tools (Pollinations.ai, no key needed)

### Changed
- Agent loop continues past 3 tool calls; drives recovery until completion or blocker
- Telegram auto-continues until no-tool final reply
- Codex Responses omit `max_output_tokens` for ChatGPT backend compatibility
- Dashboard provider switching per session
- `deploy.sh`: builds + ships minowrap, seeds tools.json
- Telegram: 4000-char HTML-safe chunking + plain-text fallback
- `RunLoop` accepts `LLMClient` interface instead of concrete `*ProviderManager`
- Default soul includes SELF-VERIFY and tool truth rules

## [0.1.0] — Initial release

- Agent loop with tool discipline (reason → act → observe)
- SQLite + FTS5 memory with semantic search and consolidation
- Built-in tools: read/write/edit file, bash, calendar, notes, web search, image gen
- OpenRouter embeddings for semantic recall
- Telegram bot gateway
- Web dashboard with chat, memory, tools, files, database, ops tabs
- MCP bridge for stdio-based servers
- Extension protocol for HTTP-based external tools
- Skill loader (SKILL.md) with keyword and semantic matching
- Provider manager with priority, retry, fallback, circuit breaking
- Checkpoint/resume for task survival
- Scheduler for proactive prompts
- Pollinations.ai free image generation
