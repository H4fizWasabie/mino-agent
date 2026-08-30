# Harness Invariants

Rules no change may break. Checked at design (stage 02) and proven at verify (stage 04).

An invariant is not a preference. It is a property that, if broken, makes Mino a different
and worse thing. Keep this list short. A list of thirty invariants is a list of zero.

## Model Agnosticism

No interface, config key, or data structure names a specific provider or model. Provider
differences live behind an adapter and nowhere else. Any behaviour that works on one
provider works on all of them, or degrades in a stated and documented way.

Verification: run the changed path against at least two providers and compare behaviour.

## Loop Termination

Every loop has a bound. A loop that can run forever under any input, including a hostile or
malformed model response, is a defect regardless of how unlikely the input seems.

Verification: identify the bound and force the path that reaches it.

## Context Is Managed, Never Assumed

No code path assumes the context window is large enough. Growth in conversation length,
tool output size, or memory recall must degrade predictably rather than fail at a threshold.

Verification: exercise the path with oversized input and observe the degradation.

## Guardrails Are Not Optional

A guardrail cannot be bypassed by a code path that forgot to call it. Enforcement belongs at
the boundary the guardrail protects, not at each call site that remembers to ask.

Verification: find a path to the protected resource that skips the check. Failing to find
one is the pass condition.

## Failure Is Explicit

Every external call and every loop boundary has a defined behaviour for timeout, malformed
response, and cancellation. Silent failure and silent retry are both defects.

Verification: force each failure and observe that it surfaces.

## State Stays Local and Inspectable

Conversation, memory, and audit state remain under the owner's control in an inspectable
format. A feature that moves state somewhere the owner cannot read breaks the contract.

Verification: confirm any new state is readable with ordinary tools.

## Single Binary, No Framework

Go stdlib only. No npm, pip, or Docker. External dependencies stay limited to what mino-oss
already accepted (Telegram bot API, MCP protocol, YAML parsing). A capability some owners
want but not all belongs in an extension process, not the core binary.

Verification: confirm no new dependency was added for a capability that could be an
extension, and that the core binary still builds with `go build ./...` using only accepted
dependencies.
