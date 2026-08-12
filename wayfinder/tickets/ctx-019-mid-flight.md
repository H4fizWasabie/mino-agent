# Harness — Mid-flight: Mino provides stop/redirect signals, the LLM acts on them

Status: **IMPLEMENTED** (code + tests; rides the held v2.8.11 release)

## Prerequisite (shared with CTX-017, CTX-018): the brain knows it is the brain

See CTX-017 — the LLM must be explicitly aware it is Mino's mind driving Mino-the-harness. Mid-flight self-repair is the strongest test of this: the brain must know that "my loop is spinning" means *its own* body is malfunctioning, so it redirects on its own verified signal rather than narrating a guess. #171 gives the signal; the identity block makes the signal *self*-relevant. The identity block must land before this.

## Framing (harness, not LLM)

The LLM is the component that *decides* the next token; Mino (the harness) owns **giving it the signals and the tools to stop/redirect** — iteration count, repetition detection, context state, and the ability to change course. When the LLM spirals, it is because the harness did not supply the signal or the exit route. #171 (iteration/retry awareness) is the seed.

## Why

Level 3 of the ladder. The highest value and the highest risk: mid-flight correction happens *live*, so a wrong course-correction compounds the error instead of fixing it.

## The strictest discipline (the sharpest point, applied live)

Mid-flight, the LLM should **change behavior on a verified signal** (stop, change approach, read session_notes) **more readily than it explains itself**. A self-explanation mid-flight is provisional at best — the harness must steer on the verified signal, and treat the model's self-narrative as a guess until the trace confirms it. Wrong self-diagnosis mid-flight is worse than no diagnosis (the anti-confabulation rule applies to self-narrative with full force here).

## Scope

- Extend #171: harness-side signals (iteration, repetition, context size) already exist; add the *exit tools/routes* and the prompt-level "change behavior > narrate cause" rule.
- Verify each mid-flight redirect against the outcome (did the change actually help?) — the redirect is a hypothesis until the run evidence confirms it.

## Difficulty note

Not a model capability problem — a harness complexity + discipline problem. The risk is confabulated self-diagnosis compounding live; the mitigation is "verified signal → act; self-explanation → provisional."

## Implementation (2026-08-12)

- **Mid-flight discipline rule** (session.go, static prompt): when a system observation says change approach or abandon (repeated tool, near-cap, lost context), CHANGE BEHAVIOR immediately — take a different action, read session_notes, or state the blocker and stop. Do NOT re-narrate why you're stuck; a self-explanation mid-flight is provisional. Acting on the verified signal beats explaining it.
- **Redirect observability** (loop.go): `midflight_signal` trace events fire on repetition (with tool sig + streak) and near-cap — so the post-mortem (CTX-017) can verify whether a redirect was followed and whether it helped (redirects are checked against outcomes, not assumed).
- Test: `TestLoopLogsMidflightRedirectSignal` (mirrors the #171 test; asserts the repetition signal is logged to the trace). Full suite 533 pass.

## Acceptance criteria

- [x] The LLM can stop/redirect mid-turn on a verified harness signal (repetition, near-cap) — signals exist (#171) + the mid-flight rule + midflight_signal trace.
- [x] Redirects are behavior-first, explanation-second (provisional) — the mid-flight rule enforces this.
- [x] Post-run, redirects are observable (midflight_signal trace) so CTX-017 can verify against the outcome and feed back.
- [x] Live (2026-08-12, v2.8.11-rc4): a stuck task (3 nonexistent files, "don't give up easily") ended in 4 iterations — changed approach (bash verification instead of repeated blind reads), concluded honestly, refused to fabricate. No spin, no cap. Unit test covers the midflight_signal trace on true identical repetition.