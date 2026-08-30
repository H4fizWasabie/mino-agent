# Design: Progress-based iteration extension

## Problem

The loop currently stops at a flat iteration limit even when the model is making
useful progress, while a stalled model can consume the same budget repeating
actions that produce no new information.

## Approach

Extend the existing repetition signal into one progress signal based on both
tool-call identity and a coarse result identity. A changed call or changed
result counts as progress; an unchanged pair advances the no-progress streak.
The normal budget remains the base budget, useful progress may continue beyond
it, and every turn has a hard ceiling of 60 iterations. Three consecutive
no-progress iterations inject the existing nudge; six consecutive iterations
stop the turn only when the nudge has already fired and the same stall persists.

Rejected: a separate progress evaluator, because it duplicates the existing
`repStreak` mechanism and would give #448 a second stuck detector to reconcile.

Rejected: an unbounded progress-based loop, because Loop Termination requires a
hard bound and plain turns have no declared output against which to measure.

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| `RunLoop` | `func RunLoop(client LLMClient, sessionID, system string, messages []Message, tools *Registry, maxIter, maxTokens int, obs Observer, stream bool, traceHome string) *LoopResult` | Preserve the public loop entry point; `maxIter` remains the base budget. |
| `RunLoopContext` | `func RunLoopContext(ctx context.Context, client LLMClient, sessionID, system string, messages []Message, tools *Registry, maxIter, maxTokens int, obs Observer, stream bool, traceHome string) *LoopResult` | Apply progress-aware continuation and the hard 60-iteration ceiling to all callers. |
| `iterationCapReply` | `func iterationCapReply(maxIter int, toolCalls []ToolCall, checkpoint, reason string) string` | Explain whether the turn stalled after the nudge or reached the hard ceiling while still progressing, while retaining existing checkpoint/resume instructions. |

No provider, model, endpoint, or user-facing configuration key is introduced.

## Config Surface

| Key | Type | Default | When absent |
|-----|------|---------|-------------|
| None | — | — | Existing `maxIter` caller/default remains the base budget; the harness applies the fixed hard ceiling of 60. |

## Data Flow

1. Each completed tool iteration produces the existing deterministic tool-call
   signature and a bounded/coarse identity for its result.
2. The loop compares the combined identity with the preceding stalled run of
   iterations. A changed identity resets the no-progress distance; an unchanged
   identity increments it.
3. At distance 3, append the existing cache-safe mid-flight nudge and emit the
   existing `midflight_signal` trace family with the progress/streak evidence.
4. At distance 6, if the nudge has fired and the same no-progress identity
   remains, stop with `iteration_limit` and checkpoint the partial tool trail.
5. If progress continues beyond the base budget, continue until progress stops
   or iteration 60 is reached.
6. At iteration 60, stop with the existing checkpoint machinery and a reply
   that identifies the hard ceiling rather than a confirmed stall.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| Repeated identical tool call | Counts toward the shared no-progress streak; nudges at 3 and stops at 6 after the nudge if repetition persists. |
| Distinct tool call with no new result | Counts as no progress through the combined call/result identity; it is not treated as progress merely because arguments differ. |
| Changed call or result | Resets the no-progress distance and permits continuation up to the hard ceiling. |
| Malformed model response | Existing parse-failure handling remains authoritative; malformed responses cannot bypass the outer 60-iteration bound. |
| Provider timeout/error | Existing explicit loop error handling remains unchanged; no silent retry is added by this design. |
| Context cancellation | Existing cancellation path returns `cancelled`; it does not checkpoint as an iteration-limit stop. |
| Stall after nudge | Returns `iteration_limit`, checkpoints partial progress, and tells the resume turn that the stop was caused by confirmed no progress. |
| Hard ceiling while progressing | Returns `iteration_limit`, checkpoints partial progress, and tells the resume turn that the hard ceiling was reached while progress was still detected. |
| Checkpoint write failure | Preserve the existing reply/error behavior for checkpoint failure; the loop remains bounded and must not retry indefinitely. |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model Agnosticism | Held | Detection uses provider-neutral tool calls and results; no model-specific behavior is named. |
| Loop Termination | Held | Every turn is bounded by the fixed 60-iteration ceiling; stall detection may stop earlier. |
| Context Is Managed, Never Assumed | Held | Nudges remain message-stream additions and checkpoint replies retain bounded existing behavior. |
| Guardrails Are Not Optional | Held | This design does not move or bypass any guardrail boundary. |
| Failure Is Explicit | Held | Timeout, malformed response, cancellation, stall, and hard-ceiling outcomes remain distinguishable. |
| State Stays Local and Inspectable | Held | Progress evidence is transient/trace-visible and partial state continues through the existing local checkpoint. |
| Single Binary, No Framework | Held | Uses existing Go loop machinery and no dependency or service. |

## Files to Touch

- `loop.go`: progress identity, shared streak behavior, effective iteration
  bound, and cap-reply reason.
- `loop_regression_test.go`: focused tests for result repetition, reset on
  changed progress, nudge-then-stop at six, extension past the base budget,
  hard stop at 60, and distinct cap-reply reasons.
- `context_test.go`: preserve legacy loop detection as an advisory signal
  without allowing it to pre-empt the progress-based termination rule.
- `seams_test.go`: add a named test for any newly identified prompt-assembly
  seam only if the implementation changes prompt assembly; otherwise unchanged.
- `CHANGELOG.md`: stage-05 user-facing entry after verification.

## Out of Scope

- No implementation of #448 self-repair.
- No mechanical guardrail-deviation detector from #447.
- No shared declared-step/checkpoint model for #449/#450/#451.
- No provider-specific thresholds or model-specific adapters.
- No new configuration keys or runtime tuning surface.
- No change to script-backed playbook stages' zero-inference behavior.
- No production build, release, deployment, or scheduler mutation.
