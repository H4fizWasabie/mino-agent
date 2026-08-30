# Feature Development Pipeline

Take one Mino harness change from raw idea to shipped, documented, verified code, with a
human review point between every stage. Run `setup` once before the first feature (already
done — see `setup/questionnaire.md`).

## When to Use This Workspace

Use it for any change that adds or alters harness behaviour: a new loop mode, a context
management strategy, a guardrail, an interface surface, a provider adapter, a playbook
runtime change. Use it for non-trivial bug fixes too, entering at stage 02 when the cause
needs design work or stage 03 when the fix is already understood.

Do not use it for typo fixes, dependency bumps, or formatting. The pipeline costs more than
those changes are worth. See `references/stage-entry-guide.md` for the full entry-point
rules.

## Resuming Unfinished Work

Type `status` first. It shows which stage's `output/` folder holds the latest artifact —
that stage's `CONTEXT.md` and its `output/` file are where the last session left off. Read
that file before doing anything else; it is the actual state of the work, not this file and
not the conversation history.

## Folder Map

```
feature-dev/
├── CLAUDE.md              (auto-loaded pointer: "Read AGENTS.md")
├── AGENTS.md              (you are here)
├── CONTEXT.md             (start here for task routing)
├── setup/
│   └── questionnaire.md   (one-time onboarding record, already answered)
├── shared/
│   ├── project-identity.md      (what Mino is, stack, commands)
│   ├── harness-invariants.md    (rules a change may never break)
│   └── decision-log.md          (accepted decisions and what NOT to build)
├── references/
│   └── stage-entry-guide.md     (which stage to enter for which kind of work)
├── skills/                (bundled domain skills, empty until needed)
└── stages/
    ├── 01-intake/         (idea -> scoped problem statement)
    ├── 02-design/         (problem -> design note with interfaces)
    ├── 03-implement/      (design -> code and tests in the repo)
    ├── 04-verify/         (code -> verification report)
    └── 05-ship/           (verified change -> changelog and docs)
```

## Triggers

| Keyword | Action |
|---------|--------|
| `setup` | Reconfigure this pipeline itself (stack, commands, invariants). Not for a single feature — see `stages/01-intake/CONTEXT.md` for that. |
| `status` | Scan `stages/*/output/`. A stage is COMPLETE if its output folder holds files other than `.gitkeep`, otherwise PENDING. |

### How `status` works

```
Pipeline Status: feature-dev

  [01-intake] --> [02-design] --> [03-implement] --> [04-verify] --> [05-ship]
     STATUS          STATUS           STATUS            STATUS         STATUS
```

## Routing

| Task | Go To |
|------|-------|
| Scope a new feature idea | `stages/01-intake/CONTEXT.md` |
| Design interfaces and data flow | `stages/02-design/CONTEXT.md` |
| Write the code and tests | `stages/03-implement/CONTEXT.md` |
| Verify behaviour and invariants | `stages/04-verify/CONTEXT.md` |
| Write changelog and docs | `stages/05-ship/CONTEXT.md` |
| Reconfigure this pipeline | `setup/questionnaire.md` |

## What to Load

| Task | Load These | Do NOT Load |
|------|-----------|-------------|
| Intake | `shared/project-identity.md`, `shared/decision-log.md`, `stages/01-intake/references/*` | invariants, later stages, source code |
| Design | `stages/01-intake/output/`, `shared/harness-invariants.md`, `stages/02-design/references/*` | decision log rationale, implementation references |
| Implement | `stages/02-design/output/`, `shared/harness-invariants.md`, `stages/03-implement/references/*`, the source files named in the design | intake output, `stages/01-intake/`, other features' outputs |
| Verify | `stages/03-implement/output/`, `shared/harness-invariants.md`, `stages/04-verify/references/*` | intake, design rationale |
| Ship | `stages/04-verify/output/`, `stages/02-design/output/`, `stages/05-ship/references/*` | source code, intake |

## Stage Handoffs

Each stage writes to its own `output/`. The next stage reads from there. Edit any output
file between stages and the next stage picks up your edit. That is the control surface, and
it is also the resume mechanism: a fresh session with no memory of this conversation can
read the latest stage's output and continue exactly where the last session stopped.

Stage 03 is the exception worth understanding: it writes code to the repository root, not
to `output/`. Its `output/` holds a change manifest listing what was touched and why, so
stages 04 and 05 know where to look without re-reading the design.

## After Stage 05

Stage 05 writes the changelog entry and touches user-facing docs. It does not commit, open a
PR, tag, or deploy — that's the repository's existing production path, unrelated to this
pipeline: [`AGENTS.md`](../../AGENTS.md#building-and-shipping-mino-mandatory-before-you-build)
(issue → branch → PR → owner approval → release lane → deploy, each boundary needing its own
explicit approval). This pipeline's job ends when the change and its documentation are
correct and verified; shipping it to production is a separate, already-governed process.

## Starting a New Feature

A feature's `output/` files are its resume state — do not clear them while work is still in
flight, even across sessions. Clear or archive the previous feature's `output/` folders only
after stage 05 has shipped it and a new, unrelated feature is starting.
