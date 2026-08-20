# Architecture — Playbooks as Files & Folders; LLM as Judgment Layer (ARCH-001)

Status: **ACCEPTED** (owner decision 2026-08-20, from the agent-philosophy research docs)

## The principle (three ideas combined)

1. **Code mode (PwC/Cloud-Codes paper)** — the model composes multi-tool work into ONE script per turn (batched, few turns). Its failure mode is turn-by-turn churn (many tiny subprocess calls = JSON behavior paying code-mode's tax). Token crossover ≈26 tools; whitelisted stubs below that are the cheap zone.

2. **Agents = naming convention (Jake Van Clief #1)** — an "agent" is just UX/UI: the same model organized with different tools + data in different contexts. The product is the structure — folders, files, contracts — not the model.

3. **LLMs = layer on top of code (Jake Van Clief #2)** — traditional code owns the deterministic machine; the LLM is the judgment layer inside it. Grace Hopper's compiler framing: language over machine code.

## The architecture

- **Playbook = filesystem state machine.** Each stage = a folder with: CONTEXT.md (contract), a tool whitelist, an output slot (payload.json), optionally script.sh. The run state, exclusions, ledgers, payloads are ALL files the model touches. Verification = file existence + attribution.
- **The LLM's only product per stage = the declared output file(s).** It never manages the run, never calls meta-tools, never orchestrates the framework.
- **The framework's job = boundaries + verification.** Stage whitelist must be a REAL boundary: the stub module shows ONLY the stage's whitelisted tools, and `mino exec` inside a stage executes ONLY whitelisted tools. The model cannot see or call what the stage doesn't declare.
- **The stub module is the tool list.** Filter the stub to the whitelist (not just the registry) — closes the knowledge leak that lets a model meta-loop on tools it should not know exist (observed: 6 manage_playbook calls + 41 tiny exec calls in one compose stage).

## Non-negotiable

- Scripts = committed owner artifacts; the LLM never generates them at runtime.
- Scripts = the mechanical half; LLM = the judgment half (creative compose, critique, reply nuance).
- Secrets never reach scripts; minimal env.
- One binary / one sqlite; bash is the script runtime.
- Never-silent failures; stage failure = visible + paged.


## Acceptance criteria (the direction of implementation)

1. **Stub module = stage whitelist**: in a playbook stage, the stub module text contains ONLY the stage's declared tools. Nothing else is seeable.
2. **mino exec = stage whitelist enforcement**: inside a stage, `mino exec <tool>` refuses tools outside the whitelist with a clear error (never silent). Chat turns keep the full registry.
3. **Batch-first contract**: stage CONTEXTs instruct "perform the work in ONE script (or few), at most N `mino exec` calls" so the model realizes code-mode's one-turn-chaining benefit instead of turn-by-turn churn.
4. **Meta-tools out of stage reach**: manage_playbook / run_playbook / approve / schedule etc. are framework-owned; the model inside a stage cannot see or call them.
5. Verified by tests: stage whitelist [read_file, write_file, bash] → stub shows only those 3; `mino exec manage_playbook` inside the stage fails.
