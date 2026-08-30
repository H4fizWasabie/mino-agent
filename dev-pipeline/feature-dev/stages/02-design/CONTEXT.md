# Stage 02: Design

Turn a scoped problem into a design note that names interfaces, data flow, and failure
behaviour. No implementation code.

## Inputs

| Source | File/Location | Section/Scope | Why |
|--------|--------------|---------------|-----|
| Previous stage | `../01-intake/output/` | Full file | The problem and its acceptance criteria |
| Shared | `../../shared/harness-invariants.md` | Full file | Rules the design may not break |
| Reference | `references/design-note-format.md` | Full file | The shape of the output |
| Reference | `references/interface-checklist.md` | Full file | What every new surface must answer |
| Source | (repository) | Only files named as affected in intake | Understand what exists before changing it |

## Process

1. Read the intake output and the invariants.
2. Read the existing code at each affected surface. Note how it works today before proposing a change.
3. Propose two or three approaches. For each, state the interface change, the failure mode, and what it costs later.
4. **[Checkpoint 1]** Present the approaches side by side with a recommendation. The human picks one.
5. For the chosen approach, define the interfaces: function or type signatures, config keys, and defaults.
6. Define behaviour under failure: what happens on provider timeout, malformed response, cancelled loop, exhausted context.
7. Check the design against every invariant. Any conflict is resolved here, not in implementation.
8. **[Checkpoint 2]** Present the interfaces and failure behaviour. The human confirms before implementation is planned.
9. Run the audit checks. If any fail, revise before saving.
10. Save to `output/`.

## Checkpoints

| After Step | Agent Presents | Human Decides |
|------------|---------------|---------------|
| 3 | Two or three approaches with interface change, failure mode, and long-term cost | Which approach to take |
| 7 | Interfaces, config keys, defaults, and failure behaviour | Confirm before the design is locked |

## Audit

| Check | Pass Condition |
|-------|---------------|
| Invariants held | Every invariant is listed with an explicit "held" or "resolved by" note |
| Model agnostic | No interface names a specific provider or model, and provider-specific behaviour sits behind an adapter |
| Failure defined | Every external call and every loop boundary has a stated failure behaviour |
| No implementation | The note defines what and when, never how. It contains no function bodies |
| Config surface | Every new config key has a name, a type, a default, and a note on what happens when it is absent |

## Outputs

| Artifact | Location | Format |
|----------|----------|--------|
| Design note | `output/[feature-slug]-design.md` | Chosen approach, interfaces, config surface, failure behaviour, invariant check, files to touch |

The design note is a contract. Stage 03 builds to it and has freedom in how, not in what.
