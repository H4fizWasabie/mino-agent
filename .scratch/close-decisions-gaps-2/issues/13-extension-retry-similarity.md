# extension retry + similarity alert

Status: resolved
Type: grilling
Blocked by: —

## Question

Detect when extension tools are stuck in retry loops returning similar useless results. Two combined signals from `tool_calls` table:

1. **Consecutive same-tool calls:** ≥3 calls to same extension tool within 5 minutes with no other extension call between.
2. **Output similarity:** Last 3 outputs within 10 minutes are >90% similar (Trigram similarity on first 500 chars).

When both fire concurrently, trigger: `[MINO ALERT] Extension <name> appears stuck — 3+ similar consecutive calls in last 5 min.`

## Answer

1. **Query approach:** Single SQL query against `tool_calls` table, filtered to extension tools (tools whose name doesn't match any built-in). Group by `tool_name`, order by `created_at`.

2. **Trigram similarity** (`extensions.go`): new `trigramSimilarity(a, b string) float64` — split into character trigrams, compute Jaccard `intersection / union`. Cap input at 500 chars. No external deps — pure stdlib.

3. **Checker function:** `checkExtensionRetryLoops(db *sql.DB, notifyFn func(string))` — queries tool_calls for last 10 min, groups by tool_name, checks consecutive same-tool pattern + trigram similarity, fires alert if both conditions met. Added to the existing alert goroutine in `alert.go`.

4. **Integration:** Called alongside `checkErrorRate` and `checkSilence` in the same `checkAlerts` ticker loop. Uses the same `notifyFn` for Telegram/slog delivery.

5. **No per-extension tuning:** Single 90% threshold. Tune later if needed (revisit clause §21).

File list: `extensions.go` (trigramSimilarity, checkExtensionRetryLoops), `alert.go` (wire into checkAlerts goroutine).
