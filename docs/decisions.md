# Decisions

Key architectural decisions for Mino. Each entry explains what, why, and when to revisit.

> The current `feat/playbooks` branch supersedes the older task-survival,
> scheduler, tool-filter, and completion-protocol sections where
> `PLAYBOOKS_DESIGN.md` describes a simpler filesystem-based replacement.

---

## 1. Single binary, no framework

Go stdlib only. SQLite (modernc.org/sqlite), stdlib HTTP, stdlib templates. One `go build`, one deploy. External deps limited to Telegram bot API, MCP protocol, and YAML parsing.

**Why:** Mino runs on a $6 VPS. Minimal attack surface. One process to monitor. No npm, no pip, no Docker.

**Revisit when:** PostgreSQL becomes necessary, or dashboard complexity demands a frontend framework.

---

## 2. Flat package structure

All `.go` files in root. No `cmd/`, `internal/`, `pkg/`. Extensions are separate processes (HTTP), not embedded packages.

**Why:** Premature layering hides bugs. ~13K lines across 33 files is readable in one sitting.

**Revisit when:** The codebase exceeds ~30K lines or gains a second binary beyond `minowrap`.

---

## 3. Extension protocol (HTTP)

Extensions are separate processes. Mino discovers tools via `GET /tools` and calls them via `POST /execute`. Systemd manages lifecycle.

**Why:** Extensions can be written in any language. Failure isolation — a crashed extension doesn't take down the agent.

**Extension vs native — the decision rule:** a capability is NATIVE when every owner needs it (loop, memory, playbooks, providers, the core tools); it is an EXTENSION when some owners want it (threads, fileingest, minowrap, cost-watch, and future capabilities like video generation or browser automation). Costly or niche capabilities never ship in the core binary — the extensions directory is the growth surface, and each extension carries its own cost profile (e.g. cost-watch's scraper, a video tool's per-second billing). Extensions stay compiled binaries with no runtime dependencies (issue #47 — the brand is "no dependencies, just download and run").

**Revisit when:** Extension latency becomes a bottleneck (add Unix socket transport).

---

## 4. SQLite with WAL mode

Single file database at `~/.mino/state.db`. `SetMaxOpenConns(1)` serializes all access. WAL mode for concurrent reads during writes.

**Why:** Zero administration. Backup is `cp state.db`. No separate database server.

**Revisit when:** Concurrent write throughput becomes a bottleneck, or database exceeds ~10GB.

---

## 5. Consolidation (LLM-driven memory compression)

Every N exchanges, a small model distills chat logs into durable facts and episode summaries. Runs on background goroutines (6-hour full pass, 5-minute threshold pass). Only the consolidated rows are marked — raw logs are never deleted.

**Why:** Unlimited chat history without unbounded context growth. Facts survive across sessions.

**Revisit when:** Consolidation quality degrades (tune prompt) or becomes too slow (use faster model).

---

## 6. Playbooks and recovery

Multi-step workflows live in playbook workspaces with root and stage `CONTEXT.md` contracts. Each invocation has a separate filesystem run containing `state.json` and stage outputs. A resumed run starts at its first incomplete stage; completed stages are not replayed.

The runtime uses the canonical loop for each stage. Run state records the durable checkpoint, while declared outputs and external verification provide evidence.

**Why:** The filesystem is simpler to inspect, edit, back up, and hand off than a second task state machine.

---

## 7. Playbook routing

Mino matches playbooks by keywords first. If keyword matching finds nothing and an embedder is available, semantic matching is used as a fallback. The selected playbook is exposed to the LLM, which decides whether to run it.

Playbooks are optional capabilities, like `recall`, rather than a mode that
replaces conversation. Every message enters the normal runtime; matching alone
never executes a procedure. Once Mino calls `run_playbook`, the numbered files
become the state machine for that task and each stage uses the canonical loop.

**Why:** Fast deterministic discovery handles common requests; embeddings improve recall without making every request pay the embedding cost. Keeping the decision inside Mino preserves follow-up context and avoids forcing ordinary questions through a workflow.

**Superseded (EMB-001, v2.9.0):** embeddings are removed entirely — the recall gap-fill contributed zero hits on production, dedup clustering was a silent no-op after fact rekeying, and every other consumer was dead or replaceable. The floor is essentials + skill triggers + keyword-narrowed graph candidates (the legacy SQLite FTS5 facts table was retired in schema v9, #407); tool descriptions were strengthened in the user's vocabulary as the data-side fix.

---

## 8. Safety and recovery

Mino uses lightweight mechanical recovery around an LLM-controlled tool loop:

1. **Explicit tools.** File writes, edits, shell commands, and remote copies use
   visible tool calls rather than hidden execution.
2. **Verified copies.** Raw `cp`, `scp`, and `rsync` are rejected in favor of
   `sync_file`, which records destination proof.
3. **Git rollback.** Before recognized destructive Bash runs, Mino makes a
   best-effort Git snapshot when the target workspace is a repository.
4. **Immutable audit log.** Tool calls and outputs are appended to a separate
   restricted log.

There is no `request_approval` or `resolve_approval` protocol for playbook
execution. A playbook is an agreed autonomous contract: Mino proceeds without
approval and stops only when it cannot fulfill or verify the contract
truthfully. This does not remove conversational approval gates from coding
agents performing consequential repository or production actions.

**Why:** Recovery and evidence remain mechanical, while human decisions stay in
the conversation and procedure instead of creating a second approval state
machine.

**Revisit when:** A concrete destructive-action failure shows that these simple
guards and explicit human checkpoints are insufficient.

---

## 9. What NOT to build

Mino is a personal AI agent, not a platform:

- **No multi-tenancy.** One Mino = one person. Multi-user is an allowlist of Telegram chat IDs, not RBAC.
- **No plugin marketplace.** Extensions are self-hosted HTTP services. No distribution mechanism beyond git clone.
- **No web-based code editor.** Mino edits files, but the dashboard is for chat and monitoring, not an IDE.
- **No agent-to-agent protocol.** Mino talks to humans, not other Minos.
- **No billing/subscription.** Mino is OSS. You pay for your own VPS and LLM API keys.
- **No fine-tuning pipeline.** Prompt engineering and skills are the extension mechanism.

**Revisit when:** A clear, demonstrated need from users outweighs the complexity cost.

## 10. Database access

Mino connects to any database via bash (`psql`, `mysql`, `sqlite3`, etc.). No native driver, no ORM — the CLI is the interface. Results come back as plain text, and the LLM interprets them.

Recognized destructive shell or database commands receive a best-effort Git
snapshot when they run inside a repository. A repeatable procedure that needs
human confirmation must state that checkpoint in its playbook stage.

**Why:** Every database already has a CLI. Wrapping each one would bloat the core. Users own their VPS, their data, and their guard configuration.

**Revisit when:** A minowrap tool for read-only query formatting with connection pooling becomes a common user request.

## 11. Recovery from downtime

Telegram uses polling (`getUpdates`). If Mino is down for more than a few minutes, messages sent during the outage are lost — Telegram only retains updates for 24 hours, and polling resumes from the last processed offset. There is no message queue, no webhook fallback, no retry mechanism.

**Why:** Polling is simpler than running a webhook server with TLS. For a personal agent, occasional message loss is acceptable. For business use, this is a known tradeoff.

**Revisit when:** Business users report message loss as a critical gap. Webhook mode with a lightweight HTTP endpoint is the natural upgrade path.

## 12. Secrets management

API keys and credentials are stored in three locations: environment variables, `~/.mino/auth.json` (OAuth tokens, API keys set via dashboard), and `~/.mino/mino.env` (systemd-style env file). None are encrypted at rest. The `auth.json` file has `0600` permissions.

**Why:** Mino runs on a single-user VPS. Disk encryption and vault integration are the user's responsibility. The agent should never need to manage its own secrets.

**Revisit when:** Teams or shared VPS deployments become common. A `MINO_SECRETS_BACKEND` env var pointing to a vault or password manager would be the integration point.

## 13. Extension sandboxing

Extensions (minowrap, MCP servers, fileingest) run as separate processes with full access to the host. A malicious or misconfigured `tools.json` entry can execute arbitrary shell commands. The trust model: the user wrote the config, the user trusts themselves.

**Why:** Sandboxing (Docker, seccomp, chroot) adds operational complexity for a threat that only exists if the user deliberately configures it. Mino is not a multi-tenant platform.

**Revisit when:** A third-party extension ecosystem emerges, or users begin sharing extension configs.

## 14. Backup strategy

Mino's entire state is one directory: `~/.mino/`. Backup is `cp -r ~/.mino /backup/mino-$(date)`. The deploy script already snapshots `state.db` before restarts. No built-in S3 sync, no incremental backup, no retention policy.

**Why:** Backup strategy depends on the user's infrastructure. Mino provides the simple invariant (one directory = everything). Users choose their own backup tool (rsync, restic, Borg, S3 CLI).

**Revisit when:** Dashboard users request a one-click backup button or automated scheduling.

## 15. Multi-agent fan-out

The `delegate` tool spawns isolated sub-agents. Fan-out extends this: one LLM call dispatches N delegates concurrently, collects results, aggregates.

Implementation: a new `fan_out` tool (`{prompts: ["...", "..."]}`) that spawns delegate goroutines in parallel, waits for all, returns combined results. Complexity lives in the tool, not the loop.

**Why:** The loop stays simple (observe → plan → act → observe). Fan-out is opt-in through the tool registry. Users who don't need parallelism never pay for it.

**Revisit when:** Fan-out latency is dominated by the slowest delegate. Add streaming partial results via observer callback so the user sees progress before all delegates finish.

## 16. Onboarding

Current: user downloads binary, opens dashboard, sees a blank form (base URL, model, API key). Works for developers. Fails for everyone else.

Target flow: double-click Mino → browser opens → "Pick your AI brain" with provider logo buttons (ChatGPT, Claude, or "I have a key") → OAuth popup or paste key → "Hi! I'm Mino" → optional Telegram QR code setup.

Principles:
- **Zero knowledge required.** User doesn't need to know what a base URL or model is. Provider buttons handle the details.
- **OAuth first.** Claude PKCE and Codex device flow are already implemented. Make them the primary path.
- **Auto-open browser.** `xdg-open` / `open` after dashboard starts.
- **Telegram as step 2.** After core setup, offer a flow: create bot with @BotFather, paste token, send test message.
- **First prompt.** Post-setup, suggest "Hi Mino! What can you do?" to give the user an immediate success moment.

**Why:** Mino is OSS. The onboarding is the product's first impression. If it feels like a developer tool, it stays a developer tool. A 10-year-old should be able to set it up.

**Revisit when:** Provider landscape changes (new OAuth flows, new major AI providers). Add a "bring your own key" advanced toggle for developers who want custom endpoints.

## 17. Eval and regression testing

Mino tests use a `fakeClient` that plays back scripted LLM responses. This is fast and deterministic, but doesn't catch regressions caused by real LLM behavior changes, prompt drift, or tool interactions.

A separate `mino eval` command runs a suite of test cases against a real LLM:

1. Reads `eval/cases.json` — array of `{prompt, expected_tool, must_not_loop, must_complete_in_n}`
2. Runs each case through the full agent loop with a real provider
3. Judges behavior, not output — correct tool called? Completed? No stall? No fabricated results?
4. Writes `~/.mino/eval_report.json` — a single flat file, overwritten each run, never grows
5. Non-zero exit on failure → CI-friendly

Run manually before release, or nightly via cron. Not part of `go test` (costs API credits, takes minutes).

**Why:** Fake LLM tests verify code logic. Real LLM eval verifies behavior — prompt changes, tool additions, loop modifications can silently degrade quality. The dashboard already displays `eval_report.json`; this fills the gap of actually generating it.

**Revisit when:** Test suite exceeds ~100 cases (add benchmarking mode), or when eval costs become significant (add sampling — run N random cases per commit, full suite before release).

## 18. Observability

Mino logs extensively: `slog` to systemd, `traces/*.jsonl` per turn, `usage.jsonl` per LLM call, `tool_calls` and `chat_log` in SQLite. The dashboard exposes stats: token usage, latency p95, tool count, gate skip rate, trace tail.

What's missing for production:

1. **Alerting.** Mino has the data but doesn't act on it. "Tool error rate > 10% in the last hour" should trigger a Telegram notification. A dead man's switch: if Mino produces no output for N hours, alert.
2. **Error trending.** Tool errors are logged individually but never aggregated over time. Is `search_web` failing more often this week?
3. **Cost per session.** Total cost is available. Per-conversation or per-day breakdown helps users understand spending.
4. **Health endpoint.** A lightweight `/health` (HTTP 200 + uptime) and `/metrics` (Prometheus format) for external monitoring. Hooks into Grafana, UptimeRobot, etc.

Priority order: alerting > health endpoint > cost breakdown > error trending.

**Why:** A business user pays for API credits and relies on Mino for daily tasks. They need to know things are working without actively checking. Mino should be the first to tell you it's broken.

**Revisit when:** Dashboard gains a dedicated observability tab, or when multi-user deployments need per-user cost attribution.

## 19. Context-aware memory ranking

Memory recall (`recall`) currently scores facts with a static four-signal formula:

```
finalScore = 0.55 × similarity + 0.20 × importance + 0.15 × recency + 0.10 × feedback
```

A fifth signal, `context_boost`, will be added — bumping facts whose subject overlaps with the active conversation turn (keyword or semantic match). The formula becomes:

```
finalScore = 0.45 × similarity + 0.20 × importance + 0.15 × recency + 0.10 × feedback + 0.10 × context_boost
```

The system prompt stays stable (cache-friendly). Facts are still pulled via `recall` on demand, not pre-injected.

**What was skipped:** Dynamic fact injection into the system prompt before each turn (Option B). That approach gives better relevance — the harness knows the conversation topic before the LLM asks — but breaks prompt caching because the system prompt changes every turn.

**Why not a reranker model:** Cross-encoder rerankers cost an API call per recall and add latency. For a single-user agent with a fact pool under ~1K entries, the weighted formula is free math on existing metadata and already gets ~95% of the value.

**Revisit when:**
- Anthropic prompt caching with `cache_control` markers is implemented (makes Option B cache-friendly)
- Fact pool exceeds ~10K entries and `scoreFact` tuning plateaus (revisit reranker model)
- `context_boost` tuning plateaus and relevance is still the bottleneck (revisit Option B)

## 20. Playbook stage plans

The numbered Markdown files inside a playbook are the workflow plan. A stage declares its required reads, action, and output contract; the runtime supplies the original user request and verifies that the expected output exists.

The LLM remains responsible for sequencing within a stage. Stage execution
uses the same `RunLoopContext`, tools, reasoning settings, and context
management as normal Mino work. The filesystem remains the durable record
rather than a separate `TaskSnapshot` or dependency graph.

**Revisit when:** Playbooks need dependencies that cannot be expressed as numbered stages and output files.

## 21. Extension quality feedback loop

Extension tools can return valid-looking but useless results — the LLM retries in a loop without the harness noticing. The existing error-rate alert (§18.1) only catches `status=error`, not silent quality failures.

Two signals combined, both queryable from `tool_calls` + `created_at` timestamps (no turn/goal boundaries needed):

1. **Consecutive same-tool calls.** If the same extension tool is called ≥3 times within a 5-minute window with no other extension call in between, flag as a retry loop.
2. **Output similarity.** If the same tool's last 3 outputs within 10 minutes are >90% similar (Trigram or Jaccard on the first 500 chars), it's likely returning the same broken result.

When both signals fire concurrently, trigger an alert: "[MINO ALERT] Extension `tool_name` appears stuck — 3+ similar consecutive calls in last 5 min."

**What was skipped:** Turn/goal boundaries (not applicable to continuous sessions), user rejection parsing (fragile), tool dominance heuristic (add later if needed).

**Revisit when:** The output similarity threshold needs tuning per extension, or when extension-specific quality baselines ("this tool normally takes 2 calls, 5+ is anomalous") become worth the complexity.

## 22. Eval case generation

`eval/cases.json` is manually written. Auto-generate cases from real interactions to catch regressions without manual curation:

1. **User-approved (primary).** Add a thumbs-up button to completed tasks in the dashboard. User clicks → auto-generate an eval case from that interaction: `{name, prompt, expected_tool, confidence: "manual"}`. These gate deploys — fail the build on regression.

2. **Auto-harvest (background).** When a run completes successfully without further tool calls, auto-generate a case from all tools called. Marked `confidence: "auto"` — run silently, report results, don't fail eval. Seeds the pool and surfaces anomalies without blocking deploys.

Two tiers by confidence:
- `manual`: user-verified, blocks deploys on failure
- `auto`: harness-generated, reports only, doesn't block

**Why not scheduled user review:** Adds friction. Thumbs-up is one click in the existing dashboard workflow. Auto-harvest is zero-touch.

**Revisit when:** Auto-generated cases consistently disagree with manual cases (confidence scoring needs tuning), or the case pool exceeds ~100 entries (add sampling: N random cases per commit, full suite before release).

## 23. Current production branch baseline

As of 2026-07-31, `feat/memory-graph` is the canonical next-generation Mino
branch and the intended production path. It supersedes the earlier flat
SQLite-only semantic-memory implementation while retaining SQLite for
operational state, chat history, episodes, audit records, and
diagnostics. (Embeddings were later removed entirely — EMB-001, v2.9.0; the `memory_embeddings` table was dropped via schema v7.)

Semantic claims are authoritative Markdown graph facts under the configured
memories directory. The canonical runtime remains one loop; bounded snapshots,
read-only interrupts, loop detection, and audit events observe or correct that
loop without creating a second agent runtime.

This branch is not production-certified until the exact binary is deployed,
live behavior is tested in one continuous session, backup/database integrity is
verified, and local/VPS binary identity matches.

## 24. Deploy: one path to production (REL-05)

**Decision (2026-08-10, #120):** The self-updater (`mino update`, release → VPS,
version-checked, SHA256SUMS-verified) is the ONLY path to production.
`deploy.sh` is bootstrap-only: user setup, RTK, oauth.d, extensions.json,
systemd units, state.db backup — it builds and pushes no binaries. The manual
path survives only as a documented emergency procedure (raw scp + SHA-verification
checklist), awkward on purpose, for GitHub-down or broken-release cases.

**Why:** Three agents on three states. 2026-08-10: a manual scp shipped a
pre-landmarks build (caught by hash mismatch, not by design); cost-watch's unit
was invisible to deploy.sh and never updated; a v2.8.0 label ran before the tag
existed; the release was cut from a stale master. The self-updater was the only
path with built-in verification — so it becomes the only path. The failure class
was always "a human ran a build from an unverified local tree"; removing the
local-tree path removes the class.

**Who may deploy:** An agent may perform the release and deployment workflow only
after explicit approval immediately before each consequential boundary. Initiating
the release lane (tag + build + stage-smoke + publish) and running `mino update`
are separate approvals; GitHub and the updater carry who/what/when evidence.
Post-SHA-verification the updater can only install a released, checksum-verified
binary; the updater itself writes the who/what/when line to
`/home/mino/.mino/deployments.log` (code-generated, cannot rot).

**Builds:** Only from tags. build-release.sh refuses to build unless HEAD is an
exact tag match on a clean tree, so a version label can never precede its tag.

**Revisit when:** An extension grows a self-updater of its own, or the release
pipeline automates (CI) — the explicit approval gates and verification chain
must survive any automation.

## 25. Prompt-assembly test surface (REL-04)

**Decision (2026-08-10, #119):** The named seam list is the rule — not "all
prompt functions". The Prompt-Assembly Test Surface is the chain that turns
structured state and CONTEXT.md into model-visible text, or parses
model-facing contracts. Eleven seams: contract parse (`parseStageInputs`,
`parseStageOutputs`, `parseStageSuccess`), prompt render
(`buildWorkspaceStagePrompt`, `renderWorkspaceInput`, `renderWorkspaceInputFiles`,
`truncateWorkspaceInput`, `workspaceInputPath`), request clean
(`cleanPlaybookRequest`), ## Success verification, plus the already-covered
`alertScheduleHealth` and `cost.go`. Playbook contracts count — through their
Go seams; there is no separate contract-testing regime, bad CONTEXT.md is
caught by parse tests with realistic fixtures plus the existing "missing input
is not a skip reason" runtime rule.

**Why:** 2026-08-10's two production failures were 5-line prompt-assembly bugs
(unresolvable stage inputs, routing tail leaked into stage prompts) with zero
tests on the path while 366 tests sat elsewhere — conventional coverage
signals gave false confidence. Both fixes landed with table-driven tests that
caught real edge cases in minutes (triple-newline markers, path-header
attribution, empty-glob semantics).

**Enforcement:** Mechanical, not procedural. A presence check in
`seams_test.go` — the authoritative append-only seam list — fails the suite if
any listed seam lacks a `Test*` function naming it. It rides `go test ./...`,
which CI already runs on every push/PR, so no new workflow. Presence-only:
the check prevents the silent zero (the actual failure class); test quality is
code review's job. AGENTS.md carries the rule: a feature touching a listed
seam ships with a table-driven test on that seam; new seams join the list in
seams_test.go when born.

**Revisit when:** The list outgrows ~20 seams (split by file), or a
non-Go surface (shell scripts, playbook markdown itself) ships prompt text
worth protecting — the same presence-check shape applies.
