# eval auto-generation

Status: resolved
Type: grilling
Blocked by: —

## Question

Auto-generate eval cases from real interactions. Two paths:

1. **User-approved (thumbs-up):** Dashboard button on completed tasks → auto-generates an eval case `{name, prompt, expected_tool, confidence: "manual"}`. These gate deploys.

2. **Auto-harvest (background):** When `complete_task` fires with status=complete, auto-generate from all tools called. Marked `confidence: "auto"` — run silently, report only.

## Answer

1. **EvalCase.Confidence field:** Add `Confidence string` to `EvalCase` struct. Values: `"manual"` (blocks deploys), `"auto"` (reports only). Default existing cases to `"manual"`.

2. **RunEval update:** Skip `confidence: "auto"` cases in pass/fail count for exit code. Report them separately in `eval_report.json` as `"auto_results"`.

3. **GenerateEvalCase helper** (`eval.go`): `func GenerateEvalCase(prompt string, toolsUsed []string, confidence string) EvalCase` — builds a case from the prompt + first mutation tool (or first tool overall). Name is auto-generated from the prompt (first 50 chars, sanitized).

4. **AppendEvalCase** (`eval.go`): `func AppendEvalCase(home string, c EvalCase)` — reads `eval/cases.json`, appends, deduplicates by name, writes back atomically (tmp + rename).

5. **Dashboard thumbs-up endpoint** (`dashboard.go`): POST handler that takes a completed task's prompt + tools, calls `GenerateEvalCase` + `AppendEvalCase` with `confidence: "manual"`. Returns success/error.

6. **Auto-harvest hook** (`loop.go`): In the defer block where `complete_task` is detected, if status=complete and >0 tool calls, append an auto-harvested case with `confidence: "auto"`. Only if at least one mutation tool was called (observations-only tasks aren't worth eval'ing).

7. **Deduplication:** Before appending, check if a case with the same name already exists. If so, skip (auto-generated) or update (manual overrides auto).

File list: `eval.go` (EvalCase.Confidence, GenerateEvalCase, AppendEvalCase, RunEval update), `dashboard.go` (thumbs-up endpoint), `loop.go` (auto-harvest hook).
