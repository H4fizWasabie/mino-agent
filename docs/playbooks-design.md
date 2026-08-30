# Mino Playbook Architecture

## Purpose

A playbook is an autonomous, repeatable contract between Mino and its owner. It is a filesystem workspace with ordered stage contracts and durable run state. It does not replace Mino's normal reasoning loop.

Every message still enters the canonical runtime. Mino may choose a relevant playbook and call `run_playbook`. Each call creates or resumes a playbook run, verifies whatever stage is currently in progress, drives any zero-inference script stages straight through, and hands back the next stage's contract for Mino to act on with its own tool calls — call `run_playbook` again to advance once that stage's declared outputs exist. Scheduled invocations (`schedule_playbook`) still run every stage of a playbook through a dedicated loop in one call; the two entry points share the same run state and resume rules, described below.

## Definition layout

```text
~/.mino/
├── MAP.md                         # Mino's global runtime orientation
└── playbooks/<name>/
    ├── AGENTS.md                  # optional workspace map and boundaries
    ├── CONTEXT.md                 # workspace purpose and routing
    ├── config.md                  # description, status, and shared values
    ├── stages/
    │   ├── 01-collect/
    │   │   ├── CONTEXT.md         # stage contract
    │   │   └── references/        # stable stage rules
    │   └── 02-report/
    │       ├── CONTEXT.md
    │       └── references/
    └── runs/
        └── <run-id>/
            ├── state.json         # durable stage status and evidence
            └── stages/
                └── 01-collect/output/
```

Definitions are editable by Mino. A run is separate from its definition, so a new run cannot overwrite prior evidence and a failed run can resume without redoing completed work.

`MAP.md` is Mino's global runtime map and is loaded for normal chat and
playbook runs. Within a playbook, `CONTEXT.md` is the root workflow route.
`AGENTS.md` is an optional workspace-local map override; live legacy
definitions without it remain compatible.

## Stage contract

Each `stages/NN-name/CONTEXT.md` defines:

```markdown
# Collect source

## Inputs

| Source | File/Location | Section/Scope | Why |
| --- | --- | --- | --- |
| Shared rule | `shared/policy.md` | Full file | Selection rule |

## Process

1. Collect the source data.
2. Apply the rule.

## Tools

- read_file
- write_file

## Audit

| Check | Pass condition |
| --- | --- |
| Coverage | Every requested source was checked |

## Outputs

| Artifact | Location | Format |
| --- | --- | --- |
| Candidates | `output/candidates.md` | Markdown table with IDs |
```

The runner loads only the declared stage contract and inputs. A stage output from the current run is available to the next stage through its declared relative input, for example `../01-collect/output/candidates.md`.

## Execution and recovery

```text
run or scheduler fire
  → find latest failed/running run, or create a new one
  → find first non-complete stage
  → load its declared inputs and references
  → execute through the canonical Mino loop
  → verify declared outputs
  → persist stage result
  → continue to the next stage
```

`state.json` records each stage's status, attempts, timestamps, outputs, and error. A failed stage stops the run. The next invocation resumes that stage; completed stages are never re-executed merely because a later stage failed. A stage whose tools are all read-only or `write_file` is retry-safe and resumes automatically; a stage with any other tool is never auto-resumed — the run is left untouched on disk and the next invocation starts fresh, naming the abandoned run so nothing is silently repeated (the guard against a duplicate side effect on resume, such as a duplicate social post).

The playbook is an agreed autonomous contract. There is no approval protocol or human checkpoint state. Mino stops only when the contract cannot be fulfilled or verified truthfully.

## Scheduling

Schedules decide when to invoke a playbook. A scheduled invocation uses the same run discovery and resume rules as a manual invocation, but drives every stage through a dedicated loop in one call rather than navigating one stage per call the way chat's `run_playbook` does — the two entry points share `state.json` and are interchangeable across invocations of the same run. `last_run` is timing metadata; the run state and its outputs are the outcome evidence.

## Management

Mino may create, edit, validate, schedule, disable, or delete playbooks. Before activation it must validate the folder contract, declared tools, output paths, and absence of secrets. Definition changes apply to future runs; an active run keeps its recorded stage state.

## Boundaries

- The canonical Mino loop remains the sole agent loop.
- Filesystem run state is authoritative for playbook progress.
- External mutations require declared verification; an output report alone is not proof of an external outcome.
- Tool adapters hide provider-specific protocols from playbook contracts.
