# Code Conventions

How code in this repository is written. Stage 03 follows these. Full detail lives in
[`docs/coding-conventions.md`](../../../../../docs/coding-conventions.md) — read it, this
file only lists what's easy to miss.

## Match the Surroundings

Read the existing code in a file before adding to it. Match its naming, comment density, and
idiom. Code that reads like the code around it is code a reviewer can trust quickly.

## Build to the Design

The design note is a contract. Build what it says. If the design turns out to be wrong,
stop and return to stage 02. Improvising a different design during implementation means the
design note no longer describes the system, and every later stage reads a lie.

## Tests

Every acceptance criterion from intake gets a test that would fail without the change. Every
failure behaviour from the design gets a test that forces it.

A test that passes before the change was written is not testing the change.

## Provider Boundaries

Provider-specific code lives behind the adapter (`provider.go`, `provider_manager.go`). If a
change needs a provider-specific branch outside the adapter, that is a design problem.
Return to stage 02.

## Config

New config keys get their default and their absent-behaviour implemented exactly as the
design note states. If the implementation would differ, change the note first.

## Errors

Errors surface. They do not get logged and swallowed, and they do not get retried silently.
A caller that cannot see a failure cannot handle it.

## Scope

Change the files the design note lists. Touching a file outside that list is allowed when
necessary, and it gets a line in the manifest saying why. Unexplained scope is the thing
reviewers cannot review.

## Mino-Specific Rules Easy to Miss

- Flat package structure — no `cmd/`, `internal/`, or `pkg/`. Every `.go` file lives at the
  repository root.
- Go stdlib preferred over a new dependency (`docs/coding-conventions.md`'s "Simplicity: the
  prime directive").
- A prompt-assembly change (anything touching what goes into a model call) needs a named
  seam test in `seams_test.go`'s `promptAssemblySeams` list (REL-04) — `go test ./...` fails
  without one.
- `CONTEXT.md` (repository root) routes by subsystem (loop, memory, playbooks, tools,
  dashboard) to the entry files and tests for that area — check it before searching.
- Before reading unfamiliar code, prefer `codegraph explore "<question>"` /
  `graphify query "<question>"` over grep when `.codegraph/` or `graphify-out/` exist.
