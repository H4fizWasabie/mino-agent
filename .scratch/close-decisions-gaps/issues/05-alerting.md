# Alerting and dead man's switch

Status: resolved
Type: grilling
Blocked by: 04

## Question

Mino should alert the user when things go wrong: tool error rate spikes, or Mino produces no output for N hours (dead man's switch). How are thresholds defined, and how are alerts delivered?

## Answer

1. **Error rate:** Query `tool_calls` table every 5 minutes for errors in the last hour. If error ratio > `MINO_ALERT_ERROR_RATE` (default 0.10 = 10%), fire alert. Runs as a check added to the existing `Scheduler` goroutine.

2. **Dead man's switch:** If no tool call logged in `MINO_ALERT_SILENCE_HOURS` (default 6), the loop is likely stuck. Alert via goroutine. Covers "stuck loop" — not "process dead." Process-dead detection belongs to systemd (`WatchdogSec=`), not Mino. Mino alerts on stuck; systemd handles dead.

3. **Delivery:** Telegram DM to owner if `TELEGRAM_BOT_TOKEN` + `MINO_TELEGRAM_CHAT_ID` are set. Fallback: `slog.Error` (systemd journal).

4. **Rate limiting:** Once per alert condition per hour. Track last-alert timestamp in memory (no persistence needed — if Mino restarts, resetting the cooldown is fine).

5. **Alert message format:** `[MINO ALERT] <condition>: <detail>.` Example: `[MINO ALERT] High error rate: 5/20 tool calls failed in the last hour (25%).`

File list: `scheduler.go` (add alert check goroutine or integrate into existing tick), `alert.go` (new file for alert logic: query, threshold, rate-limit, send).
