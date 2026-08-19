# Audit Pass 1 — Mechanical Sweep (2026-08-19)

Scope: dead code, code smell, mechanical bugs. Pass 2 (semantic LLM review for
conflicting logic / races) runs separately.

## Baseline

| Check | Result |
|---|---|
| `go vet -tags sqlite_fts5 ./...` | CLEAN — 0 findings |
| `staticcheck -tags sqlite_fts5 ./...` | 22 findings |
| `deadcode -tags sqlite_fts5 ./...` | 13 unreachable funcs |
| `go test -tags sqlite_fts5 -count=1 ./...` | 736 passed — green baseline |

Environment: go1.25.5, staticcheck via `~/go/bin/staticcheck`.

## Dead code — 19 candidates (union, ALL grep-verified 2026-08-19)

### Deletion-verified (14) — zero callers in prod AND tests

> ✅ SHIPPED in PR for #264 (2026-08-19) — 15 deleted incl. `contextLLMClient`
> (loop.go:48, surfaced in the final staticcheck pass; same class — redundant twin
> of `LLMClient`). Test consumers refactored: `loopStalled` → test-local
> `stallPredicate`, `LoadWorkingMemory` → direct file read. The 3 test-only items stay.

| Symbol | File |
|---|---|
| `LoadWorkingMemory` | adapters.go:19 |
| `LoadPatterns` | adapters.go:44 |
| `sqliteNow` | alert.go:48 |
| `loopStalled` | alert.go:172 |
| `Core.sendNotification` | app.go:281 |
| `runCoding` | coding_tools.go:17 |
| `runCodingFallback` | coding_tools.go:31 |
| `sortedFiles` | dashboard.go:843 |
| `CheckExtensions` | extensions.go:157 |
| `appendDeploymentLog` | update.go:258 |
| `approval.nextID` field | approval.go:96 |
| `streamingFake` | eval_helpers_test.go:20 |
| `makeTestHome` | eval_helpers_test.go:66 |
| `makeEvalTools` | eval_helpers_test.go:74 |

### Test-only (3) — NOT clean deletions; delete only with test refactor

| Symbol | Used by |
|---|---|
| `GraphMemory.Stat` | memory_test.go:507-540 (dedup assertions) |
| `usageCost` | cost_test.go, cost_capture_test.go |
| `OpJournal.Get` | approval_test.go:98+, ops_journal_test.go:37 |

## Code smell — 10 (all style, none logic)

### ST1013 — numeric 405 → `http.StatusMethodNotAllowed` (8)
- dashboard.go:152, 209, 1083, 1334, 1382, 1467, 1607, 1613

### S1008 — simplify boolean return (1)
- privilege.go:269 — `if err != nil { return false }; return true` → `return err == nil`

### S1031 — unnecessary nil check around range (1)
- provider.go:465

## Mechanical bugs: 0 (vet clean, no SA-class findings)

## Races: not in Pass 1 scope; CI-enforced (`go test -race`) green.

## Skipped (noted): govulncheck — dependency vuln audit, out of scope for this pass.

---

# Audit Pass 2 — Verification + Semantic Review (2026-08-19)

## 1. Dead-code verification — COMPLETE

All 19 candidates grep-verified (see tables above):
- **14 deletable** (zero callers in prod AND tests)
- **3 test-only** (`GraphMemory.Stat`, `usageCost`, `OpJournal.Get`) — delete only with test refactor

## 2. Race review — daemon paths (strict scope: untested goroutine loops)

Reviewed: schedule-dispatcher, outbox-dispatcher, approval-sweeper, reminder-dispatcher,
archive-digest, alert-checker, audit-prune, telegram 20s/4s tickers, dashboard-in-process.

**No races found.** Evidence:
- All shared in-memory state is mutexed: `traceFiles` (global mutex, loop.go), `spillPruneMu`,
  `ApprovalGate.mu`, `Core.notifyMu`, `GraphMemory.mu`
- SQLite `SetMaxOpenConns(1)` (db.go:134) with the rows-open footgun already documented and
  respected in both call sites (reminder.go:164, dashboard.go:1213)

**Low-severity note (not a race, a hardening):** outbox drafts use fixed filename
`msg_<to>.txt` (telegram.go:36) — concurrent writers could interleave with dispatcher
read; `doc_*` drafts already use unique UnixNano names. Fix: same for `msg_*`.

## 3. Conflicting logic — targeted scan

Checked: conn limits (1 site), retention constants (30d audit/spill/stale — consistent),
timezone default (1 site, config.go:74), alert windows (checkErrorRate 1h vs parameterized
hours — distinct functions, not a conflict). **No conflicts found.**

Remaining optional stretch: full deep-read of dashboard.go (70KB) / graph_memory.go (52KB) /
loop.go for subtle conflicting logic — not done, say the word.

## Pass 2 verdict

Only actionable items: 14 dead-code deletions (one PR, issue-first) + 10 style fixes + outbox
filename hardening. No bugs, no races, no conflicts found mechanically or in review.

---

# Pass 2 addendum — git-history classification (2026-08-19)

**Policy change (owner): NO deletion without explicit owner sign-off per candidate.**
Findings below classify each candidate by evidence (git history + live equivalents),
they do not authorize deletion.

## ⚠️ Real finding — working memory is WRITE-ONLY (not dead code, missing read half)

- `add_working_memory` / `save_pattern` tools (tools.go:1921, 1959) WRITE
  working_memory.md / patterns.md; session.go:194 tells Mino those files are its
  working memory; but `LoadWorkingMemory` / `LoadPatterns` (the readers) have zero
  callers — and nothing else reads the files back into context.
- Either (a) bug: the read half was never wired (loaders exist, unused), or (b)
  vestige: graph memory superseded them and the write path is a write-only sink.
- **Decision needed: wire the loaders into context assembly, or remove the write
  tools.** Neither is deletion of "dead code" — deleting Loaders permanently
  orphans the live write feature.

## Classification of all 14 deletable candidates

### A. Superseded duplicates — live equivalent exists (delete only on sign-off)

| Symbol | Evidence |
|---|---|
| `appendDeploymentLog` (update.go:258) | duplicate of LIVE `recordDeployment` (rollback.go:413) — same intent, dead copy |
| `runCoding` / `runCodingFallback` | wrappers superseded by LIVE `runCodingContext` / `runCodingFallbackContext` (coding_tools.go:36) |
| `CheckExtensions` | extension supervision landed via ExtensionSupervisor; poll-alerts variant never wired |

### B. Leftover from cut feature — caller stripped in refactor

| Symbol | Evidence |
|---|---|
| `Core.sendNotification` | added ~07-24, caller stripped 07-26 (4546e13 "strip scheduler, checkpoints, artifacts") |
| `sortedFiles` | added 07-27 with playbook-authoring catalog; catalog landed without it |

### C. Recent scaffolding — possible intended feature (KEEP unless decided)

| Symbol | Evidence |
|---|---|
| `loopStalled` | added 08-14 with OBS-001 heartbeat feature — stall detector never wired; feature landed without it. Might be planned. |
| `approval.nextID` | added 08-16 with approval tier; DB-id path won. Fresh, harmless; decide when touching approval.go |
| `sqliteNow` | added 07-25 with context_boost; feature landed with inline formatting |

### D. Test scaffolding (harmless, no prod impact)

`streamingFake`, `makeTestHome`, `makeEvalTools` — eval-suite helpers, zero users in tests.

## Standing instruction

No code deleted until owner reviews this classification and signs off per candidate.