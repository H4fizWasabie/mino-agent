# fan_out tool

Status: resolved
Type: grilling
Blocked by: —

## Question

Add a `fan_out` tool: one LLM call dispatches N delegate sub-agents concurrently, collects results, aggregates. The loop stays simple — parallelism is opt-in through the tool registry.

## Answer

1. **API shape:** `{prompts: ["...", "..."], context: "optional shared context"}`. No per-prompt timeout in v1 — delegates use the existing session timeout. No max concurrency cap — `len(prompts)` goroutines fire simultaneously.

2. **Concurrency:** `sync.WaitGroup`. Each goroutine calls the same `RunLoop` path as `delegate.go`. Results collected into a slice with index matching prompt order.

3. **Error handling:** Wait for all delegates. Collect partial results — if one fails, include the error string in its result slot. The aggregating LLM sees both successes and failures.

4. **Streaming:** Deferred to revisit clause (DECISIONS.md §15). v1 returns all results at once after all delegates finish.

5. **Integration:** New tool `fan_out` in `tools.go` (alongside other tool factories). Reuses the delegate `run` helper from `delegate.go` — extract the run logic into a shared function. Same delegate cache TTL (1hr) applies per prompt.

6. **Core tool:** Always available — add to the `coreTools` list in `app.go`. Fan-out is a meta-tool the agent should always have access to.

7. **Output format:** Ordered results with prompt index:
   ```
   [1/3] <prompt>: <result>
   [2/3] <prompt>: <result>
   [3/3] <prompt>: ERROR: <reason>
   ```

File list: `tools.go` (add `makeFanOutTool`), `delegate.go` (extract shared `runDelegate` function), `app.go` (add `fan_out` to coreTools).
