# Feature Development Workspace

Take one Mino harness change from idea to shipped code, with a human review point between
every stage. Read `CLAUDE.md` first — it has the resume instructions and folder map.

## Task Routing

| Task Type | Go To | Description |
|-----------|-------|-------------|
| Scope an idea | `stages/01-intake/CONTEXT.md` | Turns a rough idea into a scoped problem statement with a rejection check |
| Design the change | `stages/02-design/CONTEXT.md` | Produces interfaces, data flow, and config surface before any code |
| Implement | `stages/03-implement/CONTEXT.md` | Writes code and tests into the repository, records a change manifest |
| Verify | `stages/04-verify/CONTEXT.md` | Runs tests and checks every harness invariant, produces a report |
| Ship (docs only) | `stages/05-ship/CONTEXT.md` | Writes the changelog entry and updates user-facing docs |

## Shared Resources

| Resource | Location | Contains |
|----------|----------|----------|
| Project identity | `shared/project-identity.md` | What Mino is, the stack, build and test commands |
| Harness invariants | `shared/harness-invariants.md` | Rules no change may break, checked at design and verify |
| Decision log | `shared/decision-log.md` | Accepted decisions and the explicit "do not build" list |
| Stage entry guide | `references/stage-entry-guide.md` | Which stage to enter for which kind of work |

## Entry Points

The pipeline usually runs 01 through 05. Two shortcuts are legitimate:

- A bug with a known cause enters at stage 03 and still runs 04 and 05.
- A pure architecture decision runs 01 and 02, then stops. The design note is the
  deliverable. Implementation happens in a later run.

See `references/stage-entry-guide.md` before skipping a stage.
