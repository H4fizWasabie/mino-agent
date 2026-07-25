# Cost per session tracking

Status: resolved
Type: grilling
Blocked by: —

## Question

Total cost is tracked via `usage.jsonl`. Add per-conversation and per-day cost breakdown, surfaced in the dashboard.

## Answer

1. **Add `session_id` to usage.jsonl.** No new SQLite table. The existing `logUsage` call site has the session ID in context — pass it through and log it. Dashboard computes per-session/per-day aggregates from the file.

2. **No cost estimation.** Show token counts only (in/out per session, per day). No hardcoded model price map. Prices change, providers differ — the user knows their own rates. Avoids a maintenance burden for something the user can do in their head.

3. **Dashboard panel:** New "Usage" section on the existing stats page — table with session ID, date, model, tokens in, tokens out. Sortable. Not a separate tab. Reuses the existing dashboard telemetry patterns (`/api/data` can serve it).

4. **Scope:** LLM API calls only. Embedding calls and extension costs are out of scope for v1 — embeddings are cheap and cached, extensions are user-managed.

File list: `provider.go` (add session_id to logUsage record), `loop.go` (pass session ID to provider client context), `dashboard.go` (add usage panel to stats page).
