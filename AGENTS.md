# Mino — Agent Rules

> Every AI coding agent working on this project MUST follow these rules.
> Violations = rejected PR. No exceptions.

## First Steps (mandatory)

1. **Check `CHANGELOG.md`** to understand recent changes and patterns.
2. **Understand the philosophy**: Mino is a Go rewrite synthesizing Mino (architecture), Mino (capabilities), and Mino (context). Keep it simple. If Mino doesn't have it, question why Mino needs it.

## Engineering discipline (Karpathy rules)

Behavioral guidelines that bias toward caution over speed. For trivial tasks, use judgment.

### Think before coding
- Don't assume. Don't hide confusion. Surface tradeoffs.
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them — don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### Surgical changes
- Touch only what you must. Clean up only your own mess.
- Don't "improve" adjacent code, comments, or formatting. Don't refactor what isn't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it — don't delete it.
- Remove imports/variables/functions YOUR changes made unused; don't remove pre-existing dead code unless asked.
- The test: every changed line should trace directly to the request.

### Goal-driven execution
- Define success criteria before coding. "Add validation" → "write tests for invalid inputs, then make them pass".
- "Fix the bug" → "write a test that reproduces it, then make it pass".
- "Refactor X" → "ensure tests pass before and after".
- For multi-step tasks, state a brief plan with a verify check per step.

## Rules

### Simplicity (the prime directive)
- **Less code = less bugs.** Prefer one-liners. If you can solve it in 10 lines, don't write 50.
- **Fewest files possible.** New files are a last resort. Group related logic.
- **Flat over nested.** Minop directory trees hide bugs. Keep it visible.
- **Read Mino's source first.** If Mino did it in 50 lines, Mino should too.

### Project structure (core first, extensions last)
```
mino/
  main.go          # entry point, wire everything
  go.mod           # module: github.com/H4fizWasabie/mino-agent
  config.go         # env vars → Settings
  db.go             # SQLite schema and connection
  loop.go           # canonical reasoning and tool runtime
  memory.go         # SQLite operational state + semantic graph bridge
  tools.go          # tool registry + built-in tools
  session.go        # session, history, context assembly
  provider.go       # LLM protocol adapters
  provider_manager.go # provider priority, fallback, and reasoning settings
  playbook.go       # optional filesystem state machines
  telegram.go       # primary gateway
  dashboard.go      # secondary web UI + SSE
  extensions.go     # external HTTP tools
```
**Core path:** `app.go` assembles context, `loop.go` runs reasoning and tools,
and `session.go` records the result. Playbooks may be selected by that runtime;
they never replace it with a second agent loop. Extensions remain separate
services managed by systemd.

### Code quality
- **Go stdlib first.** No external dependency without explicit discussion.
- **Readable in an afternoon.** The entire codebase should be understandable in one sitting.
- **~100 lines per file target** for core modules (loop, tools, memory). Split new growth where practical; the current GraphMemory migration is a deliberate transitional exception and must remain covered by behavior tests.
- **No frameworks.** Stdlib HTTP, stdlib SQL, stdlib templates. No gin, no echo, no gorm.
- **Single binary.** Everything embedded via `embed.FS`. One `go build`, one deploy.

### Version control
- **Commit at every working milestone.** Subject says what, body says WHY.
- **Update `CHANGELOG.md` with every commit.** No changelog = no merge. Format:
  ```
  ## [Unreleased]
  ### Added
  - Feature X (reason)
  ### Changed
  - Refactored Y (why)
  ```
- **Push after commit.** Don't let commits accumulate locally.
- **Branch naming:** `feat/short-description`, `fix/short-description`, `refactor/short-description`

### Testing
- **Tests pass before push.** `go test ./...` must succeed.
- **If you fix a bug, add a test for it.** No exceptions.
- **Table-driven tests** — Go convention, follows stdlib patterns.

### Scope discipline
- **No feature creep.** Check DECISIONS.md §9 (What NOT to build) before proposing anything new.
- **Phase-gated.** We're building in 5 phases. Don't build Phase 4 features in Phase 2.
- **One task per PR.** If it takes more than an afternoon, split it.

### Architecture
- **SQLite for operational state.** Single file, never shared across processes (Mino corruption lesson). Semantic graph claims are the deliberate production exception: Markdown files under the configured memories directory are authoritative, while SQLite facts remain a read-only diagnostic archive.
- **No Apple-specific code.** Mino runs on Linux VPS.
- **Telegram is the primary interface.** Dashboard is secondary.
- **Extensions are separate processes** (HTTP, not embedded). Systemd manages lifecycle.
- **Playbooks are optional state machines.** Matching suggests; Mino decides
  whether to call `run_playbook`.
- **Human checkpoints stay in the procedure.** Use `Stop here. Ask the owner.` in a
  stage instead of adding an approval tool or approval state machine.
- **Keep the loop canonical and mechanical.** Call the model, execute requested
  tools, return observations, and repeat. Bounded snapshot, interrupt, and loop
  detection hooks may observe or correct runtime behavior, but they must not
  create a second agent loop or a tool-result deduplication cache.
- **Tool results compacted inline:** `[tools used: name(args) -> summary]`.
- **Context is bounded without deleting history.** Recent turns, artifact
  catalogs, compaction, consolidation, and pull-based `remember` work together.

### Code patterns
- Flat project structure (see above). No `cmd/`, `internal/`, `pkg/` — that's premature layering.
- Error handling: explicit, never panic in library code
- Logging: `log/slog` (structured, levels)
- Config: environment variables + `~/.mino/config.json`
- SQLite: `modernc.org/sqlite` with WAL mode
