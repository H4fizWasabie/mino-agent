# Health and metrics endpoints

Status: resolved
Type: grilling
Blocked by: —

## Question

Add `/health` (HTTP 200 + uptime) and `/metrics` (Prometheus format) endpoints to the dashboard HTTP server for external monitoring via Grafana, UptimeRobot, etc.

## Answer

1. **`/health`:** `200 OK` + JSON: `{"status":"ok","version":"<version>","uptime_seconds":12345}`. Version + uptime are useful for monitoring dashboards and practically free. No DB status check (keeps it fast and simple — if DB is down, tool errors will fire alerts from ticket 05).

2. **`/metrics`:** Prometheus text format. Counters sourced from SQLite & usage.jsonl:
   - `mino_tool_calls_total{name="...",status="ok|error"}`
   - `mino_llm_calls_total{model="..."}`
   - `mino_tokens_used_total{model="..."}`
   - `mino_active_sessions`
   - `mino_uptime_seconds`

3. **Same port (7779).** No separate port. Users who need isolation put nginx/Caddy in front.

4. **No auth.** Open to localhost. If the dashboard is exposed externally, that's the user's reverse proxy concern.

File list: `dashboard.go` (add `/health` and `/metrics` handlers, wire in `RunDashboard`).
