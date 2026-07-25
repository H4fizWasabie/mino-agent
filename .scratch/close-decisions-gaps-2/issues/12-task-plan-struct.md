# task plan struct in checkpoint

Status: resolved
Type: grilling
Blocked by: —

## Question

Replace the flat `ToolsUsed []string` in `TaskSnapshot` with a structured plan the LLM declares and the harness persists:

```go
type TaskStep struct {
    Description string `json:"description"`
    Tool        string `json:"tool"`
    Status      string `json:"status"`  // "done" | "active" | "pending"
    Output      string `json:"output"`  // inline (≤8KB) or artifact ref (>8KB)
}
```

## Answer

1. **TaskStep struct:** New struct in `checkpoint.go` alongside `TaskSnapshot`. Three status values: `done`, `active`, `pending`.

2. **TaskSnapshot.Plan:** Add `Plan []TaskStep` field to `TaskSnapshot`. Keep `ToolsUsed` as a backward-compat flat list — it's still useful for quick checks.

3. **populatePlan helper:** When `complete_task` is called with optional `plan` field in args, parse the plan and populate `TaskSnapshot.Plan`. The LLM declares steps as: `{"step": "read the config", "tool": "read_file", "status": "done"}`.

4. **ResumePrompt update:** Include the plan in the resume prompt when present — format as a checklist the LLM can continue from.

5. **No dependency enforcement.** Harness persists and feeds back — the LLM is the planner. This is explicitly NOT a DAG.

6. **Checkpoint.Save signature:** Add optional `plan []TaskStep` parameter. Backward-compatible — callers that pass `nil` keep the flat ToolsUsed-only checkpoint.

File list: `checkpoint.go` (TaskStep, Plan field, Save signature), `loop.go` (call Save with plan from complete_task args).
