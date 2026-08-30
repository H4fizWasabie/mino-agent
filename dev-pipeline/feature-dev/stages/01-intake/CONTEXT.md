# Stage 01: Intake

Turn a rough feature idea into a scoped problem statement, or reject it early.

## Inputs

| Source | File/Location | Section/Scope | Why |
|--------|--------------|---------------|-----|
| User | (conversation) | The idea, in whatever form | The starting point |
| Shared | `../../shared/project-identity.md` | Full file | Know what Mino is and is not |
| Shared | `../../shared/decision-log.md` | "Do Not Build" section | Catch ideas already rejected |
| Reference | `references/scoping-questions.md` | Full file | The questions that expose hidden scope |

## Process

1. Restate the idea in one sentence as a problem, not a solution. "Users cannot X" rather than "add a Y."
2. Check the idea against the "Do Not Build" list. If it matches, say so and stop. Record the match in the output.
3. Identify who hits this problem and how often. An idea nobody hits regularly is a later idea, not a now idea.
4. Name the smallest change that solves the problem. Then name what the idea would grow into if unchecked, and draw the line between them.
5. List what this change would touch: loops, context management, guardrails, interface surfaces, provider adapters.
6. **[Checkpoint 1]** Present the problem statement, the smallest version, and the touched surfaces. The human confirms scope or cuts it.
7. Write the acceptance criteria as observable behaviour. Each one must be checkable by someone who did not write the code.
8. Run the audit checks. If any fail, revise before saving.
9. Save to `output/`.

## Checkpoints

| After Step | Agent Presents | Human Decides |
|------------|---------------|---------------|
| 5 | Problem statement, smallest solving change, the growth risk, and the surfaces touched | Whether to proceed, cut scope, or drop the idea |

## Audit

| Check | Pass Condition |
|-------|---------------|
| Problem not solution | The one-sentence statement describes a problem and names no implementation |
| Rejection check | The idea was compared against the "Do Not Build" list and the result is recorded |
| Observable criteria | Every acceptance criterion can be checked by running or using the harness, not by reading code |
| Scope line | The output names at least one thing this change explicitly will not do |
| Surface list | Every affected surface is named, or the output states that none are affected |

## Outputs

| Artifact | Location | Format |
|----------|----------|--------|
| Problem statement | `output/[feature-slug]-intake.md` | Problem, who hits it, smallest change, non-goals, surfaces touched, acceptance criteria |

The intake file is the human edit surface. Cut scope here, where it costs nothing. Stage 02
reads whatever is in that file.
