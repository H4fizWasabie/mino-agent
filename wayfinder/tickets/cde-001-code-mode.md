# Mino Tool Execution — Code Mode (CDE-001)

Status: **RESOLVED** — implemented in v2.18.0/v2.18.1 (2026-08-20, closes #271): stub module, script markers (+fenced bash), denylist gate, script runner with minimal env, synthetic ToolCalls keep the guard machinery, JSON path removed (schema selection, tool-call parsing, JSON repair family deleted), vision conversion moved to the exec seam, observational probe via `tool_calls` rows named `script`. Live-verified: chat turn (2 iterations), playbook stage execution, stage-smoke gate. The mid-flight provenance warning awaits a script-aware form (structural CTX-022 C injection remains).

## Decisions (2026-08-19 discussion)

- **JSON tool calling is REMOVED.** Code mode is the only path — owner decision.
  The provider `tools` array goes away; the model's only interface to act is
  writing and running bash scripts. The registry stays as the `mino exec`
  backend (shared with SCR-001); every exec lands in `tool_calls` + audit.
- **Code mode = write one bash script, execute once** — same exec path, same
  timeout, same output capture as the existing bash tool. No Python, no new
  runtime (1 binary / 1 sqlite).
- **Tool exposure cap is moot** — there are no JSON schemas to cap. The stub
  module (`mino exec` usage) is compact code in the prompt; the sliding-union
  machinery (schemaUnionCap=20) retires with the JSON path.
- **Trust stance — "good code generator, but mistakes happen":** code mode
  turns model errors into *visible, debuggable failures* (stderr → fix → retry)
  instead of JSON parse failures. Guardrails are the existing debug loop:
  iteration cap, repetition guard, midflight signals. The paper's cliff models
  died on invisible serialization bugs; here the failure is visible by
  construction.
- **Remaining open: the trust probe.** With JSON gone there is no fallback path
  — the probe becomes a *diagnostic* (per-model: does it self-correct from
  stderr within the iteration cap?) instead of a gate. Models that can't
  self-correct stay on a restricted tool set rather than getting a JSON path
  back.
- **One tool, no escape hatch** (option a, owner decision): the `bash` tool is
  absorbed into a single write-and-run-script tool. There is no
  single-command JSON tool to fall back into — the model's only action is
  writing a script.
- **Measured token case (traces 08-19, 21 turns)**: the schema block is
  **51.3% of the per-turn input window** — avg 4,894 chars schema vs 9,538
  chars window (max schema block 12,106). Stub module ≈1.5–2k chars;
  round-trip churn (7–12 iterations/turn measured) collapses on top.
- **Cap-exceeded behavior: abandon with visible report** (owner decision,
  2026-08-19). The existing iteration cap (30) + repetition guard end the
  turn with a report of what failed and why. No template mode — YAGNI until
  measured; owner's read: a current-gen LLM self-corrects its own bash within
  the cap, and a failed turn is visible/recoverable, not catastrophic.
- **Trust probe is observational, not a gate** (owner decision) — with JSON
  gone there is no fallback path to gate. Measure self-correction rate on the
  first N code-mode turns (stderr → fix within cap) from existing traces;
  only if the rate is poor does the restricted-tool-set conversation return.
- **Vision survives code mode unchanged** (verified 2026-08-19): `view_image`
  is text-based by design (loop.go T8) — the harness sends the image to the
  VisionModel (`xiaomi/mimo-v2.5` via OpenRouter on this VPS) and returns a
  TEXT description; the main brain never carries image bytes. In code mode the
  script calls `mino exec view_image` and critiques the description in stdout.
  The vision model never writes scripts (bash skill irrelevant to it); the
  main model (deepseek-v4-flash-0731) is the script author — the probe target.
- **Stub module is generated from the live registry, user-agnostic** (owner
  requirement): walks BuildRegistry + MCP discovery at boot (the same dynamic
  registry that yields 72 tools here); no hardcoded list — other users with
  different MCP surfaces get their own module. Per-tool line stays compact
  (name + one-line desc + args) so large MCP surfaces stay small (Cloudflare
  format lesson).
- **Interactive-loop rollback = the existing release lane** (verified
  2026-08-19): RUN-004 self-rollback (`exe.prev`, staged health-check,
  `mino rollback`, `binary.swap` ledger — 3 swaps logged this week) + RUN-005
  config self-heal. No dual path / no A/B flag needed. Constraint: the new
  binary must not ship data migrations the previous binary can't read
  (rollback restores the old binary against the same state.db) — this design
  is schema-light by construction.
- **Script classification gate** (owner concern 2026-08-19: "what if the LLM
  produces rm -rf"): static denylist scan of script text BEFORE exec — `rm
  -r/-rf`, `: >`, redirect-to-root, `dd of=`, `mkfs`, `shutdown/reboot`,
  `curl|sh`, `chmod/chown -R` on homes, `mv` of config dirs. Flagged → script
  does not run; model gets the reason and rewrites (interactive) / owner
  reviews (scheduled). The existing per-command `classifyBash`
  (tools.go:1220) returns Unknown for any multi-line script, so the guard
  moves to script granularity — same philosophy, new footprint. Approval tier
  (RUN-006 `request_approval`) stays for legitimately-risky scripts. Blast
  radius contained: scripts run as the `mino` user; recovery = existing
  backups (`backups/`, `state.db.bak`) + RUN-004 rollback. Code mode adds no
  capability (the model can already attempt rm -rf via the bash tool today)
  — it adds footprint, and the gate scales with it.

## Question

Mino's loop is JSON tool calling: model emits structured calls, harness parses,
executes, feeds results back — one round trip per link. Tool loading already
caps per-turn schemas at 20 (13 essentials + 7 sliding, TOOL-001), *below* the
PwC paper's 26-tool token crossover — so token savings per turn is not the win.
Can the loop let the model write **one script against tool stubs**, run it once,
and observe? And is deepseek-v4-flash-0731 safe for it?

## Evidence

- Main tg session: **958 tool calls / 27 distinct tools in 7 days** — the
  chaining workload that pays a round trip per link.
- `tool_calls.iteration` is **0 on every row** — the column exists in the schema
  but the INSERT (tools.go) never passes it. However, **iterations ARE recorded**
  in the trace JSONL (`traces/YYYY-MM-DD.jsonl`, `turn_end` events):
  - 08-17: 47 turns, mean **9.5** iterations/turn, max 30; 45% of turns ≥8
  - 08-18: 61 turns, mean **6.7**, max 24; 33% ≥8
  - 08-19 (partial): 20 turns, mean **11.6**, max 27; 60% ≥8
  Chaining depth is real and heavy — the mean turn costs 7–12 round trips.
- The loop's fragility machinery exists precisely because multi-link chaining is
  flaky: repetition guards + midflight signals (#171), parse-failure counters
  (issue #24 / CTX-006), `maxIter` caps.
- PwC benchmark (Berkeley FC v4, 309 tasks, 14 models, Nov 2024→Jul 2026):
  code mode matches/beats JSON in **11/14**, ~half wall time on chaining, stable
  under schema flood — **but 3 models fell off a cliff** on a literal-`\n`
  serialization bug. Model-specific trust is the deciding factor, not the
  format. Recomputing the headline gain without the 3 buggy models: +0.6.
  *Stability is the finding.*
- deepseek-v4-flash-0731 is current-gen open-weight — must pass a write-code
  probe (multi-line Python with real newlines, temperature 0) before the path is
  enabled for it.

## Resolution direction

1. **Populate `tool_calls.iteration`** (one-line INSERT change — the column
   exists, the call never passes it) so per-task iteration data lands in the DB
   where the dashboard can read it. Baseline already exists in traces (above).
2. **Add a code-mode branch to the loop** (opt-in per task, JSON stays default):
   the registry generates a Python stub module (python3.12 on VPS) for the ~20
   selected schemas; the model writes one script importing the stubs; the
   harness runs it in a subprocess with the existing `codingToolTimeout`,
   captures stdout to the journal, one observation turn.
3. **Model trust gate**: the newline probe above runs before code mode is
   offered to any model; failures pin the model to JSON (the paper's cliff is
   per-model, not per-format).

## Measurable

- `tool_calls.iteration` populated; average turns-per-task drops for tasks that
  used code mode vs the JSON baseline.
- Parse-failure counters / midflight repetition signals drop — the fragility
  machinery goes dormant (proven by traces, not deleted).
- One runnable check per trust gate: the newline probe passes on
  deepseek-v4-flash-0731.

## Options considered

- **A. Full replacement of JSON calling** — rejected; the paper's own math says
  the aggregate gain is mostly three broken models recovering. Keep both paths.
- **B. Native provider feature** (OpenAI Responses programmatic tool calling) —
  rejected; Mino is provider-coupling-averse (Provider Coupling map). The
  paper's own harness is a Python subprocess — a harness-owned seam matches it
  and keeps the provider swappable.
- **C. Do nothing, keep sliding tools** — per-turn tokens are fine, but
  round-trip chaining and the registry-breadth problem (MCP composio ≈ 142
  calls/week) stay as-is.

## Out of scope

- Tool registry redesign / sliding selection — TOOL-001..006 own that.
- Scheduled playbook execution — SCR-001 owns scripts for scheduled runs; code
  mode here is the interactive loop.
- Model writing Go — the model writes Python; the harness stays Go.
