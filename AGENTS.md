# Mino — Agent Rules (Index)

> Every AI coding agent working on this project MUST follow these rules.
> Violations = rejected PR. No exceptions.
>
> This file is the index. **Read the relevant section below, then the linked
> file, before working.** The links are mechanically verified — a dangling
> link or missing section header fails `go test ./...` (`TestAgentsIndexLinksResolve`).

## First Steps (mandatory)

1. **Check `CHANGELOG.md`** to understand recent changes and patterns.
2. **Understand the philosophy**: Mino is a Go rewrite synthesizing Mino (architecture), Mino (capabilities), and Mino (context). Keep it simple. If Mino doesn't have it, question why Mino needs it.
3. **Navigate with the graphs, not grep**: before reading any code, run `graphify query "<question>"` (when `graphify-out/graph.json` exists), `graphify path "<A>" "<B>"` for relationships, and `graphify explain "<concept>"` for concepts. For symbols and call paths, use `codegraph query` and `codegraph explore`. Read files only at the line ranges the graphs surface. After modifying code, run `graphify update .` and `codegraph sync` to keep both indexes current. (Dirty graph files are expected after hooks/incremental updates — not a reason to skip the graphs; only skip if the task is about the graphs themselves.)
4. **Prompt-assembly seams carry tests (REL-04)**: the named seams in `seams_test.go` (`promptAssemblySeams`) must each have a Test function naming them before any feature touching them ships — the presence check fails `go test ./...` otherwise. New seams join the list when they are born.

## Rules index

| Need | Read |
|---|---|
| How to behave, think, and execute (Karpathy discipline) | [rules.md#engineering-discipline-karpathy-rules](rules.md#engineering-discipline-karpathy-rules) |
| Public-facing discipline (what never ships in public docs) | [rules.md#public-facing-discipline](rules.md#public-facing-discipline) |
| Commits, branches, changelog format | [rules.md#version-control](rules.md#version-control) |
| Issue-first workflow, release gating, testing, scope | [rules.md#issue-first-tickets](rules.md#issue-first-tickets), [rules.md#release-gating](rules.md#release-gating), [rules.md#testing](rules.md#testing), [rules.md#scope-discipline](rules.md#scope-discipline) |
| Architecture rules (loop, playbooks, memory, context) | [rules.md#architecture](rules.md#architecture) |
| How code is written (simplicity, structure, quality, patterns) | [coding-conventions.md#simplicity-the-prime-directive](coding-conventions.md#simplicity-the-prime-directive) |
| Human contributor workflow (PRs, setup, layout) | [CONTRIBUTING.md](CONTRIBUTING.md) |

## Design reference

| Need | Read |
|---|---|
| What NOT to build, database access, secrets, downtime | [DECISIONS.md](DECISIONS.md) |
| Overall design | [DESIGN.md](DESIGN.md) |
| Playbook stage contracts and scheduling | [PLAYBOOKS_DESIGN.md](PLAYBOOKS_DESIGN.md) |
| Dashboard / Living Field design | [DASHBOARD_DESIGN.md](DASHBOARD_DESIGN.md) |
| Roadmap and phases | [ROADMAP.md](ROADMAP.md) |
| Open engineering maps and tickets | [wayfinder/MAP.md](wayfinder/MAP.md) |
