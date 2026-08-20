# Hybrid Stage Runner — script + LLM stages in one playbook (SCR-003)

Status: **RESOLVED** (2026-08-20 — runner shipped as v2.19.0, closes #280, commits 84f3966/e825f34). Revisit direction (script-first lens) recorded below as the owner's standing instruction; #283 review-gated stages (script.sh + CONTEXT.md in one stage) extend this ticket's shape — the script does the work, the LLM only gates (observe/reason/act → VERDICT), so the old "mixed stages — YAGNI" out-of-scope line is superseded by the gate design.

## Question

A playbook needs BOTH deterministic stages and judgment stages in sequence —
the instagram shape: LLM composes the creative payload, a committed script
publishes it. Can one playbook mix script-backed and LLM stages?

## Design (owner decisions 2026-08-20)

- **(a) Stage marking**: `stages/<NN-name>/script.sh` marks a script-backed
  stage. A stage is either CONTEXT.md (LLM) or script.sh — never both.
- **(b) One stage = one kind**: a stage needing both splits into two stages
  (LLM compose → script post). YAGNI on mixed stages.
- **(c) Fail-fast script stages**: deterministic scripts get no retry — a
  non-zero exit fails the stage, fails the run, one Telegram page (never
  silent, SCH-002 standard).

## Changes

1. **Runner**: one dispatch branch in `runWorkspacePlaybook`'s stage loop —
   script stage → `runScriptStage` (runs the stage's script, cwd = the stage
   dir, session env via MINO_EXEC_SESSION, output →
   `runs/<id>/stages/<NN-name>/script-output.txt` + journal + trace; exit 0 →
   stage complete); LLM stage → the existing loop unchanged (code mode).
2. **Validation**: the shared validator (`validatePlaybookScript`) extends to
   scan every stage script (`bash -n` + `mino exec` tool-name scan). The
   schedule gate and boot re-check inherit the coverage via the same seam.
3. **`run_playbook`**: hybrid playbooks become runnable interactively (both
   kinds dispatch). The refusal stays for fully-scripted playbooks (no LLM
   stages — nothing for the interactive loop to do).
4. **State**: `PlaybookRunStage` records script stages with the script marker
   (additive, schema-light — RUN-004 rollback stays valid).

## Instagram conversion (the pilot)

Current single stage `01-compose-and-post` splits in two:

- **01-compose (LLM)** — topic choice + generate/view/critique/regenerate
  (the existing material-flaw discipline); writes the payload as its declared
  output: `output/payload.json` = `{"image": "<path>", "caption": "<text>"}`
  (no new seam — the output pattern already exists).
- **02-post (script)** — reads `../01-compose/output/payload.json`, syncs the
  image, curl-verifies the public URL, composio publish (exact slug
  FACEBOOK-style discipline from the existing contract), writes the log,
  Telegram once. Committed + reviewed — the deterministic half.

## Measurable

- instagram-daily-capability: 6.34M → ~300–600k tokens/wk (creative stays
  LLM; pipeline at ~0).
- Posts still delivered (same count/week as the LLM baseline).
- Script stages fail loudly — no silent misses.

## Owner revisit (2026-08-20, paused)

Paused after the runner shipped. Owner's direction for the revisit: most
hybrid playbooks' parts can likely be FULL scripts — the LLM stage's creative
surface is smaller than assumed (the instagram compose stage is the only
candidate so far). Revisit the fleet with "script-first, LLM-only-where-
unavoidable" as the lens.

## Out of scope

- Mixed stages (CONTEXT.md + script.sh in one stage) — YAGNI.
- Playbook-level script.sh behavior — unchanged (SCR-001 fast path).
- Fix-B harness recovery guards (CTX-025) — separate ticket.
