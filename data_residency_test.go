package main

// DATA-001 (#344) + DATA-002 (#345): usage history lives in state.db
// (backfilled once from the legacy usage.jsonl), traces/ is pruned on the
// 30-day horizon.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageJSONLBackfillMigration(t *testing.T) {
	home := t.TempDir()
	jsonl := `{"ts":"2026-08-01T00:00:01Z","provider":"openai","model":"m/a","session_id":"s1","in":10,"out":5,"cache_read":2,"cache_write":0,"latency_ms":120,"cost_usd":0.001}
not json at all
{"ts":"2026-08-02T00:00:02Z","provider":"openai","model":"m/b","session_id":"s2","in":7,"out":3,"latency_ms":80}
`
	if err := os.WriteFile(filepath.Join(home, "usage.jsonl"), []byte(jsonl), 0644); err != nil {
		t.Fatal(err)
	}
	db := Connect(home)
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 { // malformed line skipped, same rule as the old reader
		t.Fatalf("usage_log rows = %d, want 2", n)
	}
	var cost float64
	var sid string
	if err := db.QueryRow(`SELECT session_id, cost_usd FROM usage_log WHERE model='m/a'`).Scan(&sid, &cost); err != nil {
		t.Fatal(err)
	}
	if sid != "s1" || cost != 0.001 {
		t.Fatalf("row = (%q, %v), want (s1, 0.001)", sid, cost)
	}
	if _, err := os.Stat(filepath.Join(home, "usage.jsonl.imported")); err != nil {
		t.Fatalf("legacy file not renamed aside: %v", err)
	}

	// Second boot: marker prevents duplicate import.
	db2 := Connect(home)
	defer db2.Close()
	if err := db2.QueryRow(`SELECT COUNT(*) FROM usage_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("re-boot duplicated rows: %d, want 2", n)
	}
}

func TestUsageBackfillFreshInstallNoOp(t *testing.T) {
	home := t.TempDir() // no usage.jsonl at all
	db := Connect(home)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_log`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("fresh install rows = %d (err %v), want 0", n, err)
	}
}

func TestUsageRecordsReadsSQLite(t *testing.T) {
	home := t.TempDir()
	writeUsageRecords(t, home, []map[string]any{
		{"ts": "2026-08-10T08:00:01Z", "model": "m/a", "in": float64(100), "cost_usd": 0.5},
		{"ts": "2026-08-10T09:00:01Z", "model": "m/b"},
	})
	recs := usageRecords(home)
	if len(recs) != 2 {
		t.Fatalf("usageRecords = %d records, want 2", len(recs))
	}
	if r := recs[0]; r["model"] != "m/a" || r["in"] != float64(100) || r["cost_usd"] != 0.5 {
		t.Fatalf("record[0] = %v", r)
	}
	// Zero-cost record omits cost_usd — fallback path stays exercisable.
	if _, ok := recs[1]["cost_usd"]; ok {
		t.Fatalf("zero-cost record should omit cost_usd: %v", recs[1])
	}
}

func TestPruneTracesDeletesOldKeepsRecent(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "traces")
	os.MkdirAll(dir, 0755)

	oldDate := time.Now().UTC().AddDate(0, 0, -40).Format("2006-01-02")
	freshDate := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	files := map[string]string{
		oldDate + ".jsonl":   "old by date",
		freshDate + ".jsonl": "recent",
		"garbage.jsonl":      "unparseable name, fresh mtime",
		"notes.txt":          "wrong extension",
	}
	for name, body := range files {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0644)
	}
	// Unparseable name with old mtime → removed via mtime fallback.
	stale := filepath.Join(dir, "weird-name.jsonl")
	os.WriteFile(stale, []byte("old by mtime"), 0644)
	staleTime := time.Now().AddDate(0, 0, -40)
	os.Chtimes(stale, staleTime, staleTime)

	pruneTraces(home)

	for name, want := range map[string]bool{
		oldDate + ".jsonl":   false,
		freshDate + ".jsonl": true,
		"garbage.jsonl":      true, // fresh mtime survives
		"weird-name.jsonl":   false,
		"notes.txt":          true,
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
}
