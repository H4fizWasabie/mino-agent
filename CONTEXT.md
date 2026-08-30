# Mino Repository — Context Map (Layer 1)

Task-to-area routing for a fresh coding session on this repository. Read
[AGENTS.md](AGENTS.md) first — mandatory rules live there, not here. This
file only routes; it never restates a rule or a design decision.

This is the source-repository map (ICM Layer 1). [`MAP.md`](MAP.md) is a
different thing: the *runtime* map for a deployed Mino's `~/.mino` filesystem
— read that only when working on runtime behavior a live agent sees, not when
navigating this repo's source.

## How to use this file

1. Find your task below. Open the listed entry file(s), then the listed
   test(s) — at the line ranges `graphify`/`codegraph` surface, not the whole
   file (AGENTS.md's First Steps).
2. Load the design reference only if the entry file doesn't answer your
   question — it is stable background, not required reading for every task.
3. Layer 3 (design docs, ADRs, indexes below) is configured once and does not
   change per task. Layer 4 — your issue, branch, new tests, and diff — is
   scoped to this task only and is never promoted into Layer 3.

## Task routing

| Working on | Entry file(s) | Tests | Design reference |
|---|---|---|---|
| Agent loop / tool execution | `loop.go` | `loop_regression_test.go`, `context_test.go` | [docs/rules.md#architecture](docs/rules.md#architecture) |
| Prompt / context assembly | `session.go` | `context_test.go`, `seams_test.go` | seam list in `seams_test.go`'s `promptAssemblySeams` |
| Memory (semantic graph) | `memory.go`, `graph_memory.go` | `memory_test.go`, `graph_memory_test.go`, `memory_eval_test.go`, `memory_audit_test.go`, `memory_migration_test.go` | [docs/memory-graph-design.md](docs/memory-graph-design.md) |
| Playbooks (workspace runtime) | `playbook.go`, `audit_playbook.go` | `playbook_test.go`, `playbook_workspace_test.go`, `playbook_script_test.go`, `playbook_defaults_test.go` | [docs/playbooks-design.md](docs/playbooks-design.md), [MAP.md](MAP.md#playbook-routing) |
| Tools & extensions | `coding_tools.go`, `host_tools.go`, `extensions/` | `tools_test.go`, `tools_essential_test.go`, `host_tools_test.go`, `ext_supervisor_test.go` | `extensions/threads/README.md`, `extensions/cost-watch/README.md` (minowrap and mino-memory have no README yet — read their source directly) |
| Dashboard | `dashboard.go`, `dashboard_universe.go` | `dashboard_test.go`, `dashboard_eval_test.go`, `dashboard_universe_test.go` | [docs/dashboard-design.md](docs/dashboard-design.md) |
| Release & deployment | `build-release.sh`, `scripts/release.sh`, `scripts/stage-smoke.sh` | `scripts/stage-smoke.sh` run against staged state is the verification step | [AGENTS.md#building-and-shipping-mino-mandatory-before-you-build](AGENTS.md#building-and-shipping-mino-mandatory-before-you-build) |
| Open decisions / architecture proposals | — | — | [wayfinder/MAP.md](wayfinder/MAP.md), `wayfinder/tickets/` |
| New feature or non-trivial bug fix (resumable pipeline) | [dev-pipeline/feature-dev/CONTEXT.md](dev-pipeline/feature-dev/CONTEXT.md) | stage 04's own verification report | intake→design→implement→verify→ship; each stage's `output/` lets a fresh session resume exactly where the last one stopped |

## The layers below this one

- **Layer 2 (per-task contract)** — the table row above for your area *is*
  the contract: entry file, test file, doc. There is no separate per-subsystem
  contract file, because this codebase is flat Go packages, not
  folder-per-subsystem; a fifth file would just restate the row.
- **Layer 3 (stable reference)** — `docs/*.md`, `docs/adr/`, `graphify-out/`,
  the `codegraph` index. Configured once; a single task does not change these.
- **Layer 4 (working artifacts)** — the GitHub issue, its branch, any new or
  changed tests, and the diff. Scoped to this task; never becomes Layer 3.
