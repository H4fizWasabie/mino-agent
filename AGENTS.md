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
5. **Playbook live tests go through the scheduler path, never `mino exec run_playbook` (#287)**: an exec subprocess owns the run it starts — a caller-side timeout kills it and marks the run `interrupted` (and can wedge the DB for later schedule claims). To test a playbook live: arm a `schedules.json` entry due the next minute, let the scheduler fire it, then read `runs/<id>/state.json` + traces. `mino exec` is for single tools (send_message, compose_message, write_file).

## Building and shipping Mino (mandatory before you build)

Any agent that builds the mino binary follows the release lane; a locally built binary is never pushed straight to the VPS (live lesson 2026-08-15: an scp'd local build bypassed the lane and had to be walked back through it).

1. **The only production path** (REL-05): issue → branch → commit + CHANGELOG → PR → owner merge → tag `vX.Y.Z` → `./scripts/release.sh vX.Y.Z` (builds from the tag, runs the `stage-smoke.sh` gate against a copy of live state) → `gh release create` + assets → `MINO_HOME=/home/mino/.mino mino update` on the VPS (the self-updater verifies the SHA256 against `SHA256SUMS.txt` before the atomic swap).
2. **Staging is a real step, not an afterthought**: `scripts/stage-smoke.sh /path/to/candidate [port]`, run ON the VPS — it boots the candidate against a copy of live state (schedules and Telegram disabled), checks boot/schema and one real chat turn. Run it on any candidate before proposing a release; the release lane runs it again as a hard gate.
3. **Releases are owner-only and manual.** An agent prepares: issue, code, tests, changelog, PR, staging pass. The owner merges, tags, and publishes.
4. **Emergency lane** — only when GitHub is unreachable or the latest release is broken: [docs/emergency-deploy.md](docs/emergency-deploy.md#emergency-deploy-procedure-rel-05) (SHA256 verification, atomic `.new` swap, `deployments.log` ledger line, follow-up issue). Taking it is a recorded exception, never a shortcut — say so in the ledger and file the follow-up.
5. **Config is not code**: editing `providers.json` / `mino.env` on the VPS (backup + `kill -HUP`) needs no release; a code change that fixes what the config exposed does.

## Rules index

| Need | Read |
|---|---|
| How to behave, think, and execute (Karpathy discipline) | [docs/rules.md#engineering-discipline-karpathy-rules](docs/rules.md#engineering-discipline-karpathy-rules) |
| Public-facing discipline (what never ships in public docs) | [docs/rules.md#public-facing-discipline](docs/rules.md#public-facing-discipline) |
| Commits, branches, changelog format | [docs/rules.md#version-control](docs/rules.md#version-control) |
| Issue-first workflow, release gating, testing, scope | [docs/rules.md#issue-first-tickets](docs/rules.md#issue-first-tickets), [docs/rules.md#release-gating](docs/rules.md#release-gating), [docs/rules.md#testing](docs/rules.md#testing), [docs/rules.md#scope-discipline](docs/rules.md#scope-discipline) |
| Building, staging, and shipping mino (read before you build) | [docs/rules.md#release-gating](docs/rules.md#release-gating), [docs/emergency-deploy.md](docs/emergency-deploy.md#emergency-deploy-procedure-rel-05) |
| Architecture rules (loop, playbooks, memory, context) | [docs/rules.md#architecture](docs/rules.md#architecture) |
| How code is written (simplicity, structure, quality, patterns) | [docs/coding-conventions.md#simplicity-the-prime-directive](docs/coding-conventions.md#simplicity-the-prime-directive) |
| Human contributor workflow (PRs, setup, layout) | [CONTRIBUTING.md](CONTRIBUTING.md) |

## Design reference

| Need | Read |
|---|---|
| What NOT to build, database access, secrets, downtime | [docs/decisions.md](docs/decisions.md) |
| Overall design | [docs/design.md](docs/design.md) |
| Playbook stage contracts and scheduling | [docs/playbooks-design.md](docs/playbooks-design.md) |
| Dashboard / Living Field design | [docs/dashboard-design.md](docs/dashboard-design.md) |
| Roadmap and phases | [docs/roadmap.md](docs/roadmap.md) |
| Open engineering maps and tickets | [wayfinder/MAP.md](wayfinder/MAP.md) |
