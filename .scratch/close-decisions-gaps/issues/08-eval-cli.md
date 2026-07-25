# mino eval CLI command

Status: resolved
Type: grilling
Blocked by: —

## Question

Add a `mino eval` command that runs a test suite against a real LLM: reads `eval/cases.json`, runs each case through the full agent loop, judges behavior (not output), writes `~/.mino/eval_report.json`, exits non-zero on failure.

## Answer

1. **`eval/cases.json` format:** Array of `{name, prompt, expected_tool, must_not_loop (bool), must_complete_in_n (seconds), skip (optional bool)}`. File ships in the repo at `eval/cases.json`.

2. **Judgment:**
   - `expected_tool`: pass if the tool was called at any point during the run (not necessarily first). Exact name match.
   - `must_not_loop`: pass if the agent completed (called `complete_task`) before hitting `MaxIter` (25).
   - `must_complete_in_n`: wall-clock timeout per case. Default 120s if not specified.
   - No output/content comparison — behavior only.

3. **Exit code:** 0 = all pass. 1 = any fail or error. CI-friendly.

4. **Report:** `~/.mino/eval_report.json`, overwritten each run. Format: `{"deterministic": "pass"|"fail:N/M", "judge": "<model> @ <iso-date>", "cases": [{...per-case result...}], "run_at": "<rfc3339>"}`. The `judge` field records which model evaluated (agent-under-test = judge). Dashboard already reads this file — no dashboard changes needed.

5. **CLI wiring:** `case "eval"` in `main.go`, calls `RunEval(home)` which loads cases, runs each through the agent loop with a real provider client, judges, writes report, exits.

File list: `main.go` (add case), `eval.go` (new: RunEval logic), `eval/cases.json` (new: test cases).
