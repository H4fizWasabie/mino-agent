package main

// Real-cost capture (issue #76): usage.jsonl carries provider-reported USD
// when available; the price table is only the fallback. These tests lock
// that preference end to end (cost.go consumers + logUsage record shape).

import (
	"context"
	"testing"
	"time"
)

func TestUsageCostPrefersRealCost(t *testing.T) {
	cases := []struct {
		name   string
		record map[string]any
		want   float64
		ok     bool
	}{
		{"real cost wins over table", map[string]any{"model": "tencent/hy3:tencent", "cost_usd": 0.0042}, 0.0042, true},
		{"no cost falls back to table", map[string]any{"model": "tencent/hy3:tencent", "in": float64(1000), "cache_read": float64(500), "out": float64(200)},
			1000*0.132/1e6 + 500*0.033/1e6 + 200*0.528/1e6, true},
		{"zero cost treated as absent", map[string]any{"model": "tencent/hy3:tencent", "cost_usd": 0.0, "in": float64(1000)}, 1000 * 0.132 / 1e6, true},
		{"unknown model without cost is unpriced", map[string]any{"model": "some/unknown-model"}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := usageCost(tc.record)
			if ok != tc.ok {
				t.Fatalf("usageCost(%v) ok = %v, want %v", tc.record, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("usageCost(%v) = %v, want %v", tc.record, got, tc.want)
			}
		})
	}
}

func writeUsageRecords(t *testing.T, home string, records []map[string]any) {
	t.Helper()
	db := Connect(home)
	defer db.Close()
	for _, r := range records {
		var cost any
		if v, ok := r["cost_usd"]; ok {
			cost = v
		}
		in, _ := r["in"].(float64)
		out, _ := r["out"].(float64)
		ts, _ := r["ts"].(string)
		sid, _ := r["session_id"].(string)
		model, _ := r["model"].(string)
		if _, err := db.Exec(`INSERT INTO usage_log
			(ts, provider, model, session_id, in_tokens, out_tokens, cache_read, cache_write, latency_ms, cost_usd)
			VALUES (?, '', ?, ?, ?, ?, 0, 0, 0, ?)`, ts, model, sid, int64(in), int64(out), cost); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunCostUSDWithRealCost(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	writeUsageRecords(t, home, []map[string]any{
		{"ts": "2026-08-10T08:00:01Z", "session_id": "scheduled-tribal", "model": "tencent/hy3:tencent", "cost_usd": 0.0010},
		{"ts": "2026-08-10T08:00:02Z", "session_id": "scheduled-tribal", "model": "deepseek/deepseek-v4-flash-0731:deepinfra", "cost_usd": 0.0025},
		{"ts": "2026-08-10T07:59:59Z", "session_id": "scheduled-tribal", "model": "tencent/hy3:tencent", "cost_usd": 9.99}, // before cutoff
		{"ts": "2026-08-10T08:00:03Z", "session_id": "other", "model": "tencent/hy3:tencent", "cost_usd": 9.99},            // other session
	})
	cost, found := runCostUSD(home, "scheduled-tribal", now)
	if !found || cost != 0.0035 {
		t.Fatalf("runCostUSD = %v (found %v), want 0.0035", cost, found)
	}
}

func TestRunCostUSDUnpricedModelWithoutRealCost(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	writeUsageRecords(t, home, []map[string]any{
		{"ts": "2026-08-10T08:00:01Z", "session_id": "scheduled-x", "model": "unknown/model", "in": float64(1000)},
	})
	// No priced record: the $2/run trigger must not fire on unknowns.
	cost, found := runCostUSD(home, "scheduled-x", now)
	if found || cost != 0 {
		t.Fatalf("runCostUSD = %v (found %v), want 0/false", cost, found)
	}
}

func TestMonthSpendUSDCountsUnpriced(t *testing.T) {
	home := t.TempDir()
	loc := time.FixedZone("MYT", 8*3600)
	writeUsageRecords(t, home, []map[string]any{
		{"ts": "2026-08-01T00:00:01Z", "model": "tencent/hy3:tencent", "cost_usd": 10.0},
		{"ts": "2026-08-15T00:00:01Z", "model": "tencent/hy3:tencent", "cost_usd": 12.0},
		{"ts": "2026-08-20T00:00:01Z", "model": "unknown/model", "in": float64(500)},     // unpriced
		{"ts": "2026-07-25T00:00:00Z", "model": "tencent/hy3:tencent", "cost_usd": 99.0}, // previous month (July, in MYT too)
	})
	total, unpriced := monthSpendUSD(home, loc, time.Date(2026, 8, 25, 0, 0, 0, 0, loc))
	if total != 22.0 || unpriced != 1 {
		t.Fatalf("monthSpendUSD = (%v, %v), want (22.0, 1)", total, unpriced)
	}
}

func TestLogUsageRecordsRealCost(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	c := &Client{usageDB: db}
	resp := &LLMResponse{Usage: UsageInfo{InputTokens: 100, OutputTokens: 10, CostUSD: 0.0042}}
	c.logUsage(context.Background(), "tencent/hy3:tencent", resp, time.Now())

	var cost float64
	if err := db.QueryRow(`SELECT cost_usd FROM usage_log WHERE model = ?`, "tencent/hy3:tencent").Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != 0.0042 {
		t.Fatalf("cost_usd = %v, want 0.0042", cost)
	}

	// Zero cost stays NULL (fallback shape preserved for consumers).
	resp2 := &LLMResponse{Usage: UsageInfo{InputTokens: 5}}
	c.logUsage(context.Background(), "m", resp2, time.Now())
	var zeroCost any
	if err := db.QueryRow(`SELECT cost_usd FROM usage_log WHERE model = ?`, "m").Scan(&zeroCost); err != nil {
		t.Fatal(err)
	}
	if zeroCost != nil {
		t.Fatalf("zero-cost record should store NULL, got %v", zeroCost)
	}

	// Nil DB (client built without a manager) logs nothing and does not panic.
	naked := &Client{}
	naked.logUsage(context.Background(), "m", resp2, time.Now())
}
