# Mino — Rules

> Process and behavioral rules for every AI coding agent and human contributor.
> Violations = rejected PR. No exceptions.

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

## Public-facing discipline

- **Mino is a public project.** Commits, PRs, CHANGELOG entries, and release notes are read by strangers — never reference the owner's personal use case: no names (Abah/Hafiz), no private incidents (specific meetings, personal reminders, private playbook runs), no owner-only environment details (personal VPS hostnames, personal data paths beyond the documented `~/.mino` layout).
- **CHANGELOG why-notes describe the failure class and the mechanism, not the incident.** Write "a user's reminder" not the owner's specific reminder; "a scheduled playbook run" not the owner's 13:00 run. The generic form is also the more useful form — it states what the fix protects for everyone.
- Personal context belongs in issues when it explains a bug — but the commit and changelog translate it to the general case.
- **Enforced mechanically:** `TestChangelogPublicDiscipline` fails `go test ./...` when CHANGELOG.md or wayfinder tickets contain banned patterns (owner names, business/product specifics, personal data paths, session ids, amounts). Genericize the wording when it fires; the mechanism is the message. Same pattern as REL-04a's seam presence check.

## Version control

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

## Issue-first (tickets)

- **No code change without a GitHub issue.** Create the issue with `gh issue create` BEFORE touching code — context, evidence, expected behavior, acceptance criteria. A ticket is the contract the work is held to.
- Reference the issue number in EVERY step: branch `fix/issue-<N>-short-name`, commit `fix: ... (closes #<N>)`, PR body `Closes #<N>`.
- One issue per branch. One task per PR.
- An agent that changes code without an issue has violated the process — stop and create the ticket, even if the work is already done.

## Release gating

- **Releases are manual and deliberate — never automatic.** Committing/pushing code does NOT ship it. A release is: tag `vX.Y.Z` → `./build-release.sh vX.Y.Z` → `gh release create` + upload assets. The VPS self-update only moves when a release exists.
- **The `[Unreleased]` CHANGELOG section is the release queue.** Release when it is significant enough:
  - 🔴 **Urgent** — a bug actively breaking something (e.g. schedules dying) → release immediately, alone.
  - 🟡 **Batched** — 3+ accumulated fixes, or any feature, or a week has passed → cut a release.
  - 🟢 **Trivial** — docs, comments, typos → commit only; they ride along with the next batch.
- Versioning: patch `v2.3.x` = bug fixes; minor `v2.4.x` = features; major `v3.0.0` = breaking.
- Do not propose a release for a single trivial fix; accumulate instead.
- **Ponytail audit before each release:** dead-code/clutter sweep (deadcode, repo clutter, stale tickets). The 2026-08-14 sweep found ~1.4k lines of dead code and 14 stale tickets in one pass — pruning is a release-cycle step, not a when-you-feel-like-it one.

## Documentation hygiene

- **Tickets close when the work ships.** `TestWayfinderTicketsCloseOnShip` fails the suite if an OPEN wayfinder ticket references an issue the CHANGELOG records as shipped — the changelog is the shipped-work source of truth. New tickets need a Status line (`OPEN` / `CONFIRMED` / `RESOLVED` / `CLOSED` / `IMPLEMENTED`) from birth.
- **Old changelog eras are one-line index entries.** `TestChangelogOldEraSectionsAreOneLiners` fails the suite if any pre-v2.8.0 section carries prose — full text lives in git history. When the current era gets unwieldy, move the boundary (edit the test constant) and compress.

## Testing

- **Tests pass before push.** `go test ./...` must succeed.
- **If you fix a bug, add a test for it.** No exceptions.
- **Table-driven tests** — Go convention, follows stdlib patterns.

## Scope discipline

- **No feature creep.** Check DECISIONS.md §9 (What NOT to build) before proposing anything new.
- **Phase-gated.** We're building in 5 phases. Don't build Phase 4 features in Phase 2.
- **One task per PR.** If it takes more than an afternoon, split it.

## Architecture

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
