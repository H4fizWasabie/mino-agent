package main

// Cost policy (REL-01, issue #126): the one price table and the two review
// triggers. The weekly-cost report, the $2/run trigger, and the $25/month
// trigger all price from policyPrices — a price change happens in exactly one
// place. Triggers are review triggers, not hard caps: they page the owner via
// the health-alert channel and re-open the policy question; they never kill a
// run.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// modelPrice is USD per million tokens. Cache is the cache_read rate.
type modelPrice struct {
	In    float64 `json:"in"`
	Out   float64 `json:"out"`
	Cache float64 `json:"cache"`
}

// seedPrices covers the policy models (main+small deepseek; hy3 row kept as a
// fallback+vision qwen) at the prices locked in REL-01. Unlisted models price
// at $0 and count as "unpriced" so a report never silently omits spend.
var seedPrices = map[string]modelPrice{
	"tencent/hy3:tencent":                       {In: 0.132, Out: 0.528, Cache: 0.033},
	"deepseek/deepseek-v4-flash-0731:deepinfra": {In: 0.08, Out: 0.18, Cache: 0.016},
	"qwen/qwen3.7-flash":                        {In: 0.03, Out: 0.13, Cache: 0.006},
}

const (
	runCostCeiling   = 2.0  // $ per scheduled run → review trigger
	monthCostCeiling = 25.0 // $ per calendar month → review trigger
)

// usageCost prices one usage.jsonl record. Real provider-reported USD wins
// (issue #76): usage.jsonl is the source of truth for spend, the price table
// is only a fallback for providers that omit cost. The second return
// is false when the record carries neither.
func usageCost(r map[string]any) (float64, bool) {
	return usageCostWith(r, seedPrices)
}

// usageCostWith is usageCost against an explicit price map (CTX-020: the
// config-driven table from priceMapFor).
func usageCostWith(r map[string]any, prices map[string]modelPrice) (float64, bool) {
	if c, ok := r["cost_usd"].(float64); ok && c > 0 {
		return c, true
	}
	model, _ := r["model"].(string)
	p, ok := prices[model]
	if !ok {
		return 0, false
	}
	f := func(k string) float64 {
		if v, ok := r[k].(float64); ok {
			return v
		}
		return 0
	}
	return (f("in")*p.In + f("cache_read")*p.Cache + f("out")*p.Out) / 1e6, true
}

// priceMapFor merges the built-in seed with the user's prices.json (if any) so
// fallback pricing is config-driven — a user's models price from THEIR table,
// never from ours (CTX-020). prices.json shape: {"model/slug": {"in":..,"out":..,"cache":..}}.
func priceMapFor(home string) map[string]modelPrice {
	m := map[string]modelPrice{}
	for k, v := range seedPrices {
		m[k] = v
	}
	data, err := os.ReadFile(filepath.Join(home, "prices.json"))
	if err != nil {
		return m
	}
	var custom map[string]modelPrice
	if json.Unmarshal(data, &custom) == nil {
		for k, v := range custom {
			m[k] = v
		}
	}
	return m
}

// daySpendUSD sums priced usage records for the current day (in loc).
func daySpendUSD(home string, loc *time.Location, now time.Time) (float64, int) {
	now = now.In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	cost, unpriced := 0.0, 0
	prices := priceMapFor(home)
	for _, r := range usageRecords(home) {
		ts, _ := r["ts"].(string)
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil || t.Before(start) || !t.Before(end) {
			continue
		}
		if c, ok := usageCostWith(r, prices); ok {
			cost += c
		} else {
			unpriced++
		}
	}
	return cost, unpriced
}

// costSince sums priced usage records at/after a time (all sessions) — the
// per-run cost estimate shown in run_playbook results (CTX-020).
func costSince(home string, since time.Time) (float64, bool) {
	cutoff := since.Format(time.RFC3339)
	cost := 0.0
	found := false
	prices := priceMapFor(home)
	for _, r := range usageRecords(home) {
		ts, _ := r["ts"].(string)
		if ts < cutoff {
			continue
		}
		if c, ok := usageCostWith(r, prices); ok {
			cost += c
			found = true
		}
	}
	return cost, found
}

// costCatalogueSummary returns a brief snapshot of cost-catalogue.json (written
// by the cost-watch extension) or "" when absent. Schema (CTX-020):
// {"scraped_at": RFC3339, "entries": [{"model", "provider", "in", "out", "discount", "data_handling"}]}
// with in/out in USD per 1M tokens and data_handling in zdr|cache_only|trains|unknown.
func costCatalogueSummary(home string) string {
	data, err := os.ReadFile(filepath.Join(home, "cost-catalogue.json"))
	if err != nil {
		return ""
	}
	var cat struct {
		ScrapedAt string `json:"scraped_at"`
		Entries   []struct {
			Model       string  `json:"model"`
			Provider    string  `json:"provider"`
			In          float64 `json:"in"`
			Out         float64 `json:"out"`
			DataHandling string `json:"data_handling"`
		} `json:"entries"`
	}
	if json.Unmarshal(data, &cat) != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d providers @ %s", len(cat.Entries), cat.ScrapedAt)
	for i, e := range cat.Entries {
		if i >= 6 {
			break
		}
		fmt.Fprintf(&b, "; %s $%.3f/$%.3f per 1M [%s]", e.Provider, e.In, e.Out, e.DataHandling)
	}
	return b.String()
}

// runCostUSD prices one scheduled run's usage: every record tagged with the
// run's session (scheduled-<name>) written at or after the fire time. The
// second return is false when no priced record was found.
func runCostUSD(home, session string, since time.Time) (float64, bool) {
	cutoff := since.Format(time.RFC3339)
	cost := 0.0
	found := false
	prices := priceMapFor(home)
	for _, r := range usageRecords(home) {
		sid, _ := r["session_id"].(string)
		ts, _ := r["ts"].(string)
		if sid != session || ts < cutoff {
			continue
		}
		if c, ok := usageCostWith(r, prices); ok {
			cost += c
			found = true
		}
	}
	return cost, found
}

// monthSpendUSD sums every priced record in the calendar month containing now
// (in loc) and returns the total plus the count of unpriced records.
func monthSpendUSD(home string, loc *time.Location, now time.Time) (float64, int) {
	now = now.In(loc)
	cost := 0.0
	unpriced := 0
	prices := priceMapFor(home)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 1, 0)
	for _, r := range usageRecords(home) {
		ts, _ := r["ts"].(string)
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil || t.Before(start) || !t.Before(end) {
			continue
		}
		if c, ok := usageCostWith(r, prices); ok {
			cost += c
		} else {
			unpriced++
		}
	}
	return cost, unpriced
}

// costTriggerState records when the monthly check last ran and which month
// already raised its alert, so the $25 trigger fires once per month.
type costTriggerState struct {
	LastCheck    string `json:"last_check_day"`
	AlertedMonth string `json:"alerted_month"`
}

func costTriggerPath(home string) string { return filepath.Join(home, "cost-trigger.json") }

func loadCostTrigger(home string) costTriggerState {
	var st costTriggerState
	if data, err := os.ReadFile(costTriggerPath(home)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func saveCostTrigger(home string, st costTriggerState) {
	if data, err := json.Marshal(st); err == nil {
		_ = os.WriteFile(costTriggerPath(home), data, 0600)
	}
}

// checkMonthlyCostOnce runs daily (guarded by state): when the month's spend
// crosses the $25 review trigger and no alert was raised for this month yet,
// page the owner once.
func checkMonthlyCostOnce(core *Core, now time.Time) {
	home := core.Settings.Home
	st := loadCostTrigger(home)
	today := now.Format("2006-01-02")
	if st.LastCheck == today {
		return
	}
	st.LastCheck = today
	month := now.Format("2006-01")
	if st.AlertedMonth != month {
		loc := time.Local
		if core.Settings != nil && core.Settings.Location() != nil {
			loc = core.Settings.Location()
		}
		spend, unpriced := monthSpendUSD(home, loc, now)
		// CTX-020: daily LLM-visible cost state — the brain wakes cost-aware
		// even below the page threshold (system_check reads the same numbers).
		today, _ := daySpendUSD(home, loc, now)
		logTrace(home, "cost_state", map[string]any{"month": spend, "today": today, "unpriced": unpriced})
		if spend > monthCostCeiling {
			st.AlertedMonth = month
			msg := fmt.Sprintf("💰 Month spend $%.2f — over the $%g review trigger. Re-review the Brain Policy (REL-01).", spend, monthCostCeiling)
			if unpriced > 0 {
				msg += fmt.Sprintf(" (%d calls from unpriced models)", unpriced)
			}
			queueOutbox(home, "owner", msg)
			logTrace(home, "cost_alert", map[string]any{"kind": "month", "month": month, "spend": spend, "unpriced": unpriced})
		}
	}
	saveCostTrigger(home, st)
}

// alertRunCost enforces the $2/run review trigger on a scheduled run: cost is
// summed from usage.jsonl for the run's session since its fire time, and the
// alert shares the one-per-playbook-per-day dedup slot with failure alerts.
func alertRunCost(core *Core, s PlaybookSchedule, at time.Time) {
	cost, found := runCostUSD(core.Settings.Home, "scheduled-"+s.Name, at)
	if !found || cost <= runCostCeiling {
		return
	}
	today := at.In(scheduleLoc(s)).Format("2006-01-02")
	alerted := ""
	updateScheduleHealth(core.Settings.Home, s.Name, func(p *PlaybookSchedule) {
		if p.AlertedDay == today {
			alerted = today
			return
		}
		p.AlertedDay = today
	})
	if alerted == today {
		return
	}
	msg := fmt.Sprintf("💰 Run cost $%.2f for *%s* — over the $%g/run trigger. Check for a runaway loop or mispricing.", cost, s.Name, runCostCeiling)
	queueOutbox(core.Settings.Home, "owner", msg)
	logTrace(core.Settings.Home, "cost_alert", map[string]any{"kind": "run", "playbook": s.Name, "cost": cost})
}
