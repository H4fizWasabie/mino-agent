# Harness — Mid-flight: Mino provides stop/redirect signals, the LLM acts on them

Status: **OPEN** (wayfinder ticket, CTX-019 — the hard frontier)

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

## Acceptance criteria

- [ ] The LLM can stop/redirect mid-turn on a verified harness signal (repetition, near-cap, context loss).
- [ ] Redirects are behavior-first, explanation-second (provisional).
- [ ] Post-run, redirects are checked against the outcome (helped or not) and fed back (to CTX-017 post-mortem + the library refresh).