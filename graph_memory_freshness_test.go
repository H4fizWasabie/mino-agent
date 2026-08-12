package main

import (
	"testing"
	"time"
)

// CTX-014: live recall must surface a fact's age so a stale-but-unrejected
// fact isn't trusted blindly (the FB photo-post incident rode a week-old URL).
// Score is intentionally untouched — only the match rationale gains an age
// signal. The At field already existed on every Fact; this wires it in.
func TestEntryRankingSurfacesStaleness(t *testing.T) {
	now := time.Now().UTC()
	facts := map[string]*Fact{
		"fresh": {ID: "fresh", Subject: "public image hosting", At: now.Add(-2 * time.Hour)},
		"week":  {ID: "week", Subject: "public image hosting", At: now.Add(-6 * 24 * time.Hour)},
		"old":   {ID: "old", Subject: "public image hosting", At: now.Add(-40 * 24 * time.Hour)},
	}
	ranked := (&GraphMemory{}).entryRanking("public image hosting", "post image to facebook", facts, true)

	// Fresh (< freshGrace): no age signal.
	if hasSignal(ranked, "fresh", "age:") {
		t.Errorf("fresh fact must not show age signal; ranked=%+v", ranked)
	}
	// Week-old: age shown, not flagged stale.
	if !hasSignal(ranked, "week", "age: 6d") {
		t.Errorf("week fact should show \"age: 6d\"; ranked=%+v", ranked)
	}
	if hasSignal(ranked, "week", "possibly stale") {
		t.Errorf("week fact must not be flagged stale; ranked=%+v", ranked)
	}
	// Old (> staleAgeThreshold): age shown AND flagged stale.
	if !hasSignal(ranked, "old", "age: 40d") {
		t.Errorf("old fact should show \"age: 40d\"; ranked=%+v", ranked)
	}
	if !hasSignal(ranked, "old", "possibly stale") {
		t.Errorf("old fact should be flagged possibly stale; ranked=%+v", ranked)
	}
	// Archive path (useEmbedder=false): no age signal — stale facts are already
	// out of the live rotation.
	archived := (&GraphMemory{}).entryRanking("public image hosting", "post image to facebook", facts, false)
	if hasSignal(archived, "old", "age:") {
		t.Errorf("archive path must not surface age; archived=%+v", archived)
	}
}
