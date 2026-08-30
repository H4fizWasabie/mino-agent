# Design: Mid-flight self-repair

## Problem

When a model repeats the same no-progress action, the existing advisory signal
waits too long before asking it to change its next action, allowing a weak model
to spend additional iterations on a dead approach.

## Approach

Reuse #443's shared call/result no-progress streak. At streak 2, inject one
stronger mid-flight repair instruction telling the model to choose a genuinely
different next action or state the blocker. Continue using #443's streak-3
nudge and streak-6 stop; self-repair never blocks the turn, judges contract
compliance, or edits a playbook.

Rejected: a second stuck detector, because #443 already owns the shared
no-progress signal.

Rejected: an LLM judge or automatic playbook rewrite, because #447 is
mechanical detection and the map explicitly excludes autonomous contract edits.

## Interfaces

| Name | Signature | Purpose |
|------|-----------|---------|
| `RunLoopContext` | Existing signature unchanged | Reuse the #443 progress streak and inject one repair instruction at streak 2. |
| `loopProgressSignature` | Existing internal signature unchanged | Provide the shared no-progress identity for self-repair and iteration control. |

No new exported function, endpoint, provider adapter, or configuration key is
introduced.

## Config Surface

| Key | Type | Default | When absent |
|-----|------|---------|-------------|
| None | — | — | The fixed two-streak trigger applies; users see the repair prompt automatically. |

## Data Flow

1. #443 computes the call/result progress identity for the latest completed
   tool iteration.
2. A second consecutive identical identity emits one `midflight_signal` trace
   with signal `self_repair` and appends the repair instruction to the next
   model request.
3. Any changed identity resets the repair latch and the shared streak.
4. Streak 3 still emits #443's ordinary nudge; streak 6 still stops only after
   the nudge has had an opportunity to work.

## Failure Behaviour

| Failure | Behaviour |
|---------|-----------|
| Two repeated call/result outcomes | Inject one repair instruction; do not stop or retry outside the normal loop. |
| Repair ignored | The existing #443 streak-3 nudge and streak-6 stop remain authoritative. |
| Progress resumes | Reset the streak and permit a future repair on a new stall. |
| Malformed model response | Existing parse-failure handling remains authoritative. |
| Provider timeout/error | Existing explicit error behavior remains unchanged; no repair retry is added. |
| Context cancellation | Existing `cancelled` result remains unchanged. |
| Trace failure | The model-facing repair still follows the existing loop path; trace failure does not create an unbounded retry. |

## Invariant Check

| Invariant | Verdict | Note |
|-----------|---------|------|
| Model Agnosticism | Held | The signal uses provider-neutral calls and results. |
| Loop Termination | Held | #443's fixed 60-iteration ceiling and six-streak stop remain in force. |
| Context Is Managed, Never Assumed | Held | One bounded message is appended only when the signal fires. |
| Guardrails Are Not Optional | Held | Self-repair is not a guardrail bypass and does not alter enforcement boundaries. |
| Failure Is Explicit | Held | Repair, stall, malformed response, timeout, and cancellation remain observable. |
| State Stays Local and Inspectable | Held | Only existing in-memory streak state and trace events are used. |
| Single Binary, No Framework | Held | No dependency or service is added. |

## Files to Touch

- `loop.go`: add the one-shot streak-2 repair prompt and trace signal.
- `loop_regression_test.go`: test repair injection, one-shot behavior, reset
  after progress, and coexistence with the #443 nudge/stop.
- `README.md`, `docs/architecture-series.md`, and `CHANGELOG.md`: document
  the earlier repair signal and unchanged hard bound.
- `dev-pipeline/feature-dev/stages/03-implement/output/issue-448-manifest.md`:
  implementation handoff.

## Out of Scope

- #447 contract-deviation detection.
- LLM-based judging of whether a turn is compliant.
- Automatic stopping at the repair threshold.
- Autonomous edits to playbook contract files.
- Provider-specific behavior, configuration, release, deployment, or live-state mutation.
