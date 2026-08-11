package main

// Cost-policy tests (issue #126): the one price table, the $2/run trigger,
// the $25/month trigger, and their dedup.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeUsage appends one usage.jsonl record.
func writeUsage(t *testing.T, home, model, session, ts string, in, cacheRead, out int) {
	t.Helper()
	rec := fmt.Sprintf(`{"ts":%q,"model":%q,"session_id":%q,"in":%d,"cache_read":%d,"out":%d}`+"\n", ts, model, session, in, cacheRead, out)
	path := filepath.Join(home, "usage.jsonl")
	os.MkdirAll(home, 0700)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(rec); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyPricesCoverPolicyModels(t *testing.T) {
	// The REL-01 policy models must all be priced from the one table.
	cases := []struct {
		model string
		in    float64
		out   float64
	}{
		{"tencent/hy3:tencent", 0.132, 0.528},
		{"deepseek/deepseek-v4-flash-0731:deepinfra", 0.08, 0.18},
		{"qwen/qwen3.7-flash", 0.03, 0.13},
	}
	for _, tc := range cases {
		p, ok := policyPrices[tc.model]
		if !ok {
			t.Fatalf("policy model %q missing from price table", tc.model)
		}
		if p.In != tc.in || p.Out != tc.out {
			t.Fatalf("price for %q = %+v, want in $%g out $%g", tc.model, p, tc.in, tc.out)
		}
	}
	if _, ok := usageCost(map[string]any{"model": "gpt-5.6-luna"}); ok {
		t.Fatal("non-policy model must be unpriced")
	}
}

func TestRunCostUSD(t *testing.T) {
	loc := mustKL(t)
	fire := time.Date(2026, 8, 10, 9, 30, 0, 0, loc).UTC()
	cases := []struct {
		name    string
		records [][6]any // model, session, ts, in, cache, out
		want    float64
		found   bool
	}{
		{"no records", nil, 0, false},
		{"other session ignored",
			[][6]any{{"qwen/qwen3.7-flash", "scheduled-other", fire.Add(time.Minute).Format(time.RFC3339), 1_000_000, 0, 0}},
			0, false},
		{"pre-fire record excluded",
			[][6]any{{"qwen/qwen3.7-flash", "scheduled-tribal", fire.Add(-time.Minute).Format(time.RFC3339), 1_000_000, 0, 0}},
			0, false},
		{"hy3 in + cache + out",
			[][6]any{{"tencent/hy3:tencent", "scheduled-tribal", fire.Add(time.Minute).Format(time.RFC3339), 1_000_000, 1_000_000, 100_000}},
			0.132 + 0.033 + 0.0528, true},
		{"two calls summed",
			[][6]any{
				{"qwen/qwen3.7-flash", "scheduled-tribal", fire.Add(time.Minute).Format(time.RFC3339), 1_000_000, 0, 0},
				{"deepseek/deepseek-v4-flash-0731:deepinfra", "scheduled-tribal", fire.Add(2 * time.Minute).Format(time.RFC3339), 1_000_000, 0, 0},
			},
			0.03 + 0.08, true},
		{"unpriced model ignored",
			[][6]any{{"z-ai/glm-5.2", "scheduled-tribal", fire.Add(time.Minute).Format(time.RFC3339), 1_000_000, 0, 0}},
			0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for _, r := range tc.records {
				writeUsage(t, home, r[0].(string), r[1].(string), r[2].(string), r[3].(int), r[4].(int), r[5].(int))
			}
			got, found := runCostUSD(home, "scheduled-tribal", fire)
			if found != tc.found || (found && got != tc.want) {
				t.Fatalf("runCostUSD = %v (found %v), want %v (found %v)", got, found, tc.want, tc.found)
			}
		})
	}
}

func TestMonthSpendUSD(t *testing.T) {
	loc := mustKL(t)
	home := t.TempDir()
	// 2026-08-31 23:30 KL = 2026-08-31 15:30 UTC (in August), 2026-09-01 00:30 KL (out).
	writeUsage(t, home, "qwen/qwen3.7-flash", "x", "2026-08-31T15:30:00Z", 1_000_000, 0, 0)
	writeUsage(t, home, "qwen/qwen3.7-flash", "x", "2026-08-31T15:31:00Z", 1_000_000, 0, 0)
	writeUsage(t, home, "z-ai/glm-5.2", "x", "2026-08-31T15:32:00Z", 1_000_000, 0, 0)
	now := time.Date(2026, 9, 1, 1, 0, 0, 0, loc)
	spend, unpriced := monthSpendUSD(home, loc, now)
	if spend != 0 || unpriced != 0 {
		t.Fatalf("September spend = %v (unpriced %d), want 0 (all records are August in KL)", spend, unpriced)
	}
	nowAug := time.Date(2026, 8, 31, 23, 59, 0, 0, loc)
	spend, unpriced = monthSpendUSD(home, loc, nowAug)
	if spend != 0.06 {
		t.Fatalf("August spend = %v, want 0.06", spend)
	}
	if unpriced != 1 {
		t.Fatalf("unpriced = %d, want 1 (glm record)", unpriced)
	}
}

func TestAlertRunCost(t *testing.T) {
	loc := mustKL(t)
	home := t.TempDir()
	writeHealthSchedule(t, home, "tribal", 0, "", "")
	core := &Core{Settings: &Settings{Home: home}}
	fire := time.Date(2026, 8, 10, 9, 30, 0, 0, loc)

	// A cheap run (below the ceiling) must not alert.
	writeUsage(t, home, "tencent/hy3:tencent", "scheduled-tribal", fire.Add(time.Minute).Format(time.RFC3339), 1_000_000, 0, 0)
	alertRunCost(core, scheduleHealthEntry(t, home, "tribal"), fire)
	if _, err := os.Stat(filepath.Join(home, "outbox", "msg_owner.txt")); err == nil {
		t.Fatal("cheap run must not alert")
	}

	// A $3.30 run (25M hy3 input) triggers once, deduped for the day.
	writeUsage(t, home, "tencent/hy3:tencent", "scheduled-tribal", fire.Add(2*time.Minute).Format(time.RFC3339), 25_000_000, 0, 0)
	alertRunCost(core, scheduleHealthEntry(t, home, "tribal"), fire)
	msg, err := os.ReadFile(filepath.Join(home, "outbox", "msg_owner.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) == "" || !strings.Contains(string(msg), "tribal") || !strings.Contains(string(msg), "$3.43") {
		t.Fatalf("run alert = %q, want tribal + cost over $2", msg)
	}
	alertRunCost(core, scheduleHealthEntry(t, home, "tribal"), fire)
	data, _ := os.ReadFile(filepath.Join(home, "outbox", "msg_owner.txt"))
	if string(data) != string(msg) {
		t.Fatal("second overrun the same day must not produce a second alert")
	}
}

func TestCheckMonthlyCostOnce(t *testing.T) {
	loc := mustKL(t)
	home := t.TempDir()
	core := &Core{Settings: &Settings{Home: home, Timezone: "Asia/Kuala_Lumpur"}}
	// One expensive month: 200M hy3 input ≈ $26.40.
	for i := 0; i < 8; i++ {
		writeUsage(t, home, "tencent/hy3:tencent", "x", "2026-08-10T01:00:0"+fmt.Sprint(i%10)+"Z", 25_000_000, 0, 0)
	}
	now := time.Date(2026, 8, 31, 23, 59, 0, 0, loc)
	checkMonthlyCostOnce(core, now)
	msg, err := os.ReadFile(filepath.Join(home, "outbox", "msg_owner.txt"))
	if err != nil {
		t.Fatalf("crossing $25 must alert: %v", err)
	}
	if !strings.Contains(string(msg), "Month spend") || !strings.Contains(string(msg), "$25") {
		t.Fatalf("month alert = %q", msg)
	}
	// Same-day re-check and a later check in the same month: no second alert.
	checkMonthlyCostOnce(core, now.Add(time.Hour))
	data, _ := os.ReadFile(filepath.Join(home, "outbox", "msg_owner.txt"))
	if string(data) != string(msg) {
		t.Fatal("monthly alert must fire once per month")
	}

	// A new month under the ceiling: no alert, state advances.
	home2 := t.TempDir()
	core2 := &Core{Settings: &Settings{Home: home2, Timezone: "Asia/Kuala_Lumpur"}}
	checkMonthlyCostOnce(core2, now.Add(24*time.Hour))
	if _, err := os.Stat(filepath.Join(home2, "outbox", "msg_owner.txt")); err == nil {
		t.Fatal("cheap month must not alert")
	}
}

func TestPolicyProvidersFile(t *testing.T) {
	// providers.policy.json is the canonical REL-01 config: main/stages =
	// deepseek:deepinfra pinned (since 2026-08-11), small = same model pinned,
	// fallback = qwen unpinned, main provider text-only so vision turns route to qwen.
	t.Setenv("MINO_OPENROUTER_KEY", "test-key")
	home := t.TempDir()
	data, err := os.ReadFile("providers.policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "providers.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewProviderManager(home, &Settings{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(m.providers))
	}
	main, fallback := m.providers[0], m.providers[1]
	if main.Model != "deepseek/deepseek-v4-flash-0731:deepinfra" || main.Priority != 1 {
		t.Fatalf("main = %+v, want deepseek:deepinfra priority 1", main)
	}
	if len(main.ProviderRouting) != 1 || main.ProviderRouting[0] != "DeepInfra" {
		t.Fatalf("main routing = %v, want [DeepInfra]", main.ProviderRouting)
	}
	if !main.TextOnly {
		t.Fatal("main provider must be text_only so vision turns skip it")
	}
	if main.Small != "deepseek/deepseek-v4-flash-0731:deepinfra" ||
		len(main.SmallRouting) != 1 || main.SmallRouting[0] != "DeepInfra" {
		t.Fatalf("small = %+v routing %v, want deepseek:deepinfra routed to DeepInfra", main.Small, main.SmallRouting)
	}
	if fallback.Model != "qwen/qwen3.7-flash" || fallback.Priority != 2 {
		t.Fatalf("fallback = %+v, want qwen3.7-flash priority 2", fallback)
	}

	// Vision routing: an image-bearing turn flips to VisionModel, whose
	// candidates exclude the text-only main provider and land on qwen.
	vision := routeRole(MainModel, []Message{{Role: "user", Content: "x", Images: []string{"data:image/png;base64,abc"}}})
	if vision != VisionModel {
		t.Fatalf("image-bearing turn should flip to VisionModel, got %s", vision)
	}
	cands := m.candidates("t", VisionModel)
	if len(cands) != 1 || cands[0].Name != "qwen-fallback" {
		t.Fatalf("vision candidates = %v, want only qwen-fallback", cands)
	}
}
