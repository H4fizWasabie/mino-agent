package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphMemoryRecordAndRemember(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), &Settings{TopK: 2})
	if err := gm.RecordFact(Fact{ID: "mino", Type: "semantic", Subject: "Mino is an operator"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{
		ID: "owner", Type: "semantic", Subject: "Owner maintains Mino",
		Edges: []Edge{{Target: "mino", Rel: "maintains"}},
	}); err != nil {
		t.Fatal(err)
	}

	got := gm.Remember("Owner", "")
	if !strings.Contains(got, "Owner maintains Mino") || !strings.Contains(got, "[maintains]") {
		t.Fatalf("remember() = %q", got)
	}
}

func TestGraphMemoryRememberTraversesReverseAndSkipsAmbiguous(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "target", Type: "semantic", Subject: "Apex"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "source", Type: "semantic", Subject: "Source claim", Edges: []Edge{{Target: "target", Rel: "depends_on"}}}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "ambiguous", Type: "semantic", Subject: "Uncertain claim", Edges: []Edge{{Target: "target", Rel: "related_to", Kind: "ambiguous", Confidence: 0.4}}}); err != nil {
		t.Fatal(err)
	}
	got := gm.Remember("Apex", "")
	if !strings.Contains(got, "Source claim") || !strings.Contains(got, "[depends_on]") {
		t.Fatalf("reverse relationship missing: %q", got)
	}
	if strings.Contains(got, "Uncertain claim") {
		t.Fatalf("ambiguous relationship leaked into recall: %q", got)
	}
}

func TestGraphMemoryRememberRanksUseWhenOverSubjectNoise(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	// Fact A: no subject overlap, but use_when is written for this exact query.
	if err := gm.RecordFact(Fact{ID: "style", Type: "semantic", Subject: "Style guide",
		UseWhen: []string{"when user asks about programming philosophy"}}); err != nil {
		t.Fatal(err)
	}
	// Fact B: one subject word overlaps, no intent signal.
	if err := gm.RecordFact(Fact{ID: "go", Type: "semantic", Subject: "Programming language origins"}); err != nil {
		t.Fatal(err)
	}
	got := gm.Remember("programming philosophy", "")
	if strings.Index(got, "Style guide") > strings.Index(got, "Programming language origins") {
		t.Fatalf("use_when fact should rank first:\n%s", got)
	}
}

func TestGraphMemoryRememberUsesActiveTurnForIntent(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "style", Type: "semantic", Subject: "Style guide",
		UseWhen: []string{"when user asks about programming philosophy"}}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "camp", Type: "semantic", Subject: "Camping guide"}); err != nil {
		t.Fatal(err)
	}
	// Query "guide" matches both subjects equally; the turn's words (programming
	// philosophy) only overlap the style fact's use_when.
	got := gm.Remember("guide", "tell me about programming philosophy")
	if strings.Index(got, "Style guide") > strings.Index(got, "Camping guide") {
		t.Fatalf("turn-aware intent should rank style first:\n%s", got)
	}
}

func TestGraphMemoryRememberNoMatch(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "planet", Type: "semantic", Subject: "My planet is Mars"}); err != nil {
		t.Fatal(err)
	}
	if got := gm.Remember("zebra migration", ""); !strings.Contains(got, "No memories found") {
		t.Fatalf("expected no-match message, got %q", got)
	}
}

func TestGraphMemoryRememberOutputsWhyBodyAndRationale(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "planet", Type: "semantic", Subject: "My planet is Mars",
		Body: "The red planet, fourth from the sun.", Why: "because I live there",
		UseWhen: []string{"when user asks about where I live"}}); err != nil {
		t.Fatal(err)
	}
	got := gm.Remember("where I live", "")
	for _, want := range []string{"My planet is Mars", "why: because I live there",
		"body: The red planet, fourth from the sun.", "matched:", "use_when: live, where"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestGraphMemoryRememberBudgetsBodyAndKeepsNeighborsLean(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	long := strings.Repeat("word ", 80) // 400 chars
	if err := gm.RecordFact(Fact{ID: "spoke", Type: "semantic", Subject: "Spoke fact",
		Body: "spoke body", Why: "spoke why"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "hub", Type: "semantic", Subject: "Hub fact",
		Body: long, Why: "hub why", Edges: []Edge{{Target: "spoke", Rel: "links_to"}}}); err != nil {
		t.Fatal(err)
	}
	got := gm.Remember("Hub", "")
	if !strings.Contains(got, "…") {
		t.Fatalf("long body not truncated:\n%s", got)
	}
	// Start fact carries why + body; the neighbor stays lean (budget discipline).
	// Query is exact-subject "Hub" so spoke is reachable only as a neighbor.
	if !strings.Contains(got, "why: hub why") {
		t.Fatalf("start fact why missing:\n%s", got)
	}
	if strings.Contains(got, "spoke why") || strings.Contains(got, "spoke body") {
		t.Fatalf("neighbor fact leaked why/body — budget blowup:\n%s", got)
	}
	if !strings.Contains(got, "→ [links_to] Spoke fact") {
		t.Fatalf("graph structure lost:\n%s", got)
	}
}

func TestGraphMemoryUpdateBodyPersists(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	if err := gm.RecordFact(Fact{ID: "preference", Type: "semantic", Subject: "A preference", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.UpdateBody("preference", "new"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dir + "/preference.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new") || strings.Contains(string(data), "old") {
		t.Fatalf("updated fact file = %q", data)
	}
}

func TestGraphMemoryFiltersDanglingEdges(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{
		ID: "source", Type: "semantic", Subject: "Source",
		Edges: []Edge{{Target: "missing", Rel: "depends_on"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(gm.facts["source"].Edges) != 0 {
		t.Fatalf("dangling edge was retained: %#v", gm.facts["source"].Edges)
	}
}

func TestGraphMemoryManagementRemovesInboundEdgesAndTracksFeedback(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "target", Type: "semantic", Subject: "Target"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "source", Type: "semantic", Subject: "Source", Edges: []Edge{{Target: "target", Rel: "depends_on"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gm.Feedback("target", 1); err != nil {
		t.Fatal(err)
	}
	if fact, ok := gm.FindFact("target"); !ok || fact.Feedback != 1 {
		t.Fatalf("feedback fact = %+v, found=%v", fact, ok)
	}
	if _, err := gm.DeleteFact("target"); err != nil {
		t.Fatal(err)
	}
	if _, ok := gm.FindFact("target"); ok || len(gm.facts["source"].Edges) != 0 {
		t.Fatalf("delete left target or inbound edge: %#v", gm.facts)
	}
}

func TestGraphMemoryReplaceFactOverwritesEdges(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "claim", Type: "semantic", Subject: "Claim", Edges: []Edge{{Target: "a", Rel: "old"}}}); err != nil {
		t.Fatal(err)
	}
	if err := gm.ReplaceFact(Fact{ID: "claim", Type: "semantic", Subject: "Claim corrected", Edges: []Edge{{Target: "b", Rel: "new"}}}); err != nil {
		t.Fatal(err)
	}
	fact, ok := gm.FindFact("claim")
	if !ok || fact.Subject != "Claim corrected" || len(fact.Edges) != 1 || fact.Edges[0].Rel != "new" {
		t.Fatalf("replaced fact = %+v, found=%v", fact, ok)
	}
}

func TestGraphMemoryWritesSchemaAndLowercaseIndex(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	if err := gm.RecordFact(Fact{ID: "target", Type: "semantic", Subject: "Target"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{
		ID: "source", Type: "semantic", Subject: "Source", Why: "A useful reason", UseWhen: []string{"when user asks about Source", "source mentions"}, Source: "session:test",
		Edges: []Edge{{Target: "target", Rel: "depends_on", Kind: "explicit", Confidence: 1, Source: "session:test"}},
	}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]any
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	if idx["version"] != float64(3) {
		t.Fatalf("index version = %v", idx["version"])
	}
	if _, ok := idx["files"]; !ok {
		t.Fatal("index has no file freshness state")
	}
	facts := idx["facts"].(map[string]any)
	source := facts["source"].(map[string]any)
	edges := source["edges"].([]any)
	edge := edges[0].(map[string]any)
	if _, ok := edge["Target"]; ok {
		t.Fatal("index contains legacy Target edge field")
	}
	if _, ok := edge["Rel"]; ok {
		t.Fatal("index contains legacy Rel edge field")
	}
	md, err := os.ReadFile(filepath.Join(dir, "source.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "why: A useful reason") || !strings.Contains(string(md), "kind: explicit") || !strings.Contains(string(md), "use_when:") || !strings.Contains(string(md), "when user asks about Source") {
		t.Fatalf("fact schema missing metadata: %s", md)
	}
	// Reload from disk: use_when must survive the front-matter round trip.
	gm2 := NewGraphMemory(dir, nil)
	srcFact, ok := gm2.FindFact("source")
	if !ok || len(srcFact.UseWhen) != 2 || srcFact.UseWhen[0] != "when user asks about Source" {
		t.Fatalf("use_when lost on reload: %+v", srcFact)
	}
}

func TestGraphMemoryRefreshesExternalChanges(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	if err := gm.RecordFact(Fact{ID: "source", Type: "semantic", Subject: "Source", Body: "old body"}); err != nil {
		t.Fatal(err)
	}
	gm = NewGraphMemory(dir, nil)

	path := filepath.Join(dir, "source.md")
	if err := os.WriteFile(path, []byte("---\nid: source\ntype: semantic\nsubject: Source\nat: 2026-07-29T00:00:00Z\n---\n\nnew body\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := gm.Remember("new body", ""); !strings.Contains(got, "Source") {
		t.Fatalf("external update was not visible: %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("---\nid: new\ntype: semantic\nsubject: Brand new node\nat: 2026-07-29T00:00:00Z\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := gm.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := gm.Remember("Brand new node", ""); !strings.Contains(got, "Brand new node") {
		t.Fatalf("new file was not visible: %q", got)
	}

	if err := os.Remove(filepath.Join(dir, "new.md")); err != nil {
		t.Fatal(err)
	}
	if err := gm.Refresh(); err != nil {
		t.Fatal(err)
	}
	if gm.Stat() != 1 {
		t.Fatalf("deleted file remained in graph: %d facts", gm.Stat())
	}
}

func TestGraphMemoryKeepsLastValidFactOnMalformedEdit(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	if err := gm.RecordFact(Fact{ID: "source", Type: "semantic", Subject: "Source", Body: "valid"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.md"), []byte("not markdown front matter"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := gm.Refresh(); err == nil {
		t.Fatal("malformed edit did not report an error")
	}
	if got := gm.Remember("Source", ""); !strings.Contains(got, "Source") {
		t.Fatalf("last valid fact was lost: %q", got)
	}
}

func TestGraphMemoryReadsLegacyColonScalar(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	fact, err := gm.parseFrontMatter([]byte("---\nid: ai-learning-20260728-a2a-protocol\ntype: semantic\nsubject: AI Learning: Google's Agent2Agent (A2A) protocol for cross-platform AI agent interoperability\nat: 2026-07-28T13:45:22Z\n---\n\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if fact.Subject != "AI Learning: Google's Agent2Agent (A2A) protocol for cross-platform AI agent interoperability" || fact.Body != "body" {
		t.Fatalf("legacy fact = %+v", fact)
	}
}

func TestGraphMemoryIndexV3JudgmentLedger(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	if err := gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Fact A"}); err != nil {
		t.Fatal(err)
	}
	if got := gm.JudgedAt("a"); got != "" {
		t.Fatalf("new fact must be unjudged, got %q", got)
	}
	gm.MarkJudged("a")
	if got := gm.JudgedAt("a"); got == "" {
		t.Fatal("MarkJudged did not persist")
	}
	// Survives restart via index.json
	gm2 := NewGraphMemory(dir, nil)
	if got := gm2.JudgedAt("a"); got == "" {
		t.Fatal("judgment state lost across restart")
	}
	if gm2.Communities() == nil {
		t.Fatal("Communities() must return empty map, not nil")
	}
	// UnjudgedFacts only returns unjudged
	if got := gm2.UnjudgedFacts(); len(got) != 0 {
		t.Fatalf("unjudged facts = %d, want 0", len(got))
	}
	if err := gm2.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "Fact B"}); err != nil {
		t.Fatal(err)
	}
	if got := gm2.UnjudgedFacts(); len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("unjudged facts = %+v, want [b]", got)
	}
	// File change clears judgment
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nid: a\ntype: semantic\nsubject: Fact A changed\nat: 2026-08-02T00:00:00Z\n---\n"), 0644)
	gm2.Refresh()
	if got := gm2.JudgedAt("a"); got != "" {
		t.Fatalf("changed file must clear judgment, got %q", got)
	}
}

func TestGraphMemoryCommunitiesPersist(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Fact A"})
	gm.SetCommunities(map[string]int{"a": 0}, []string{"a"}, map[string]string{"0": "Test Cluster"})
	gm2 := NewGraphMemory(dir, nil)
	if got := gm2.Communities()["a"]; got != 0 {
		t.Fatalf("community lost across restart: %v", got)
	}
}
