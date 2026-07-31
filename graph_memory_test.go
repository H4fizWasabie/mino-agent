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
		ID: "abah", Type: "semantic", Subject: "Abah maintains Mino",
		Edges: []Edge{{Target: "mino", Rel: "maintains"}},
	}); err != nil {
		t.Fatal(err)
	}

	got := gm.Remember("Abah")
	if !strings.Contains(got, "Abah maintains Mino") || !strings.Contains(got, "[maintains]") {
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
	got := gm.Remember("Apex")
	if !strings.Contains(got, "Source claim") || !strings.Contains(got, "[depends_on]") {
		t.Fatalf("reverse relationship missing: %q", got)
	}
	if strings.Contains(got, "Uncertain claim") {
		t.Fatalf("ambiguous relationship leaked into recall: %q", got)
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
		ID: "source", Type: "semantic", Subject: "Source", Why: "A useful reason", Source: "session:test",
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
	if idx["version"] != float64(2) {
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
	if !strings.Contains(string(md), "why: A useful reason") || !strings.Contains(string(md), "kind: explicit") {
		t.Fatalf("fact schema missing metadata: %s", md)
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
	if got := gm.Remember("new body"); !strings.Contains(got, "Source") {
		t.Fatalf("external update was not visible: %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("---\nid: new\ntype: semantic\nsubject: Brand new node\nat: 2026-07-29T00:00:00Z\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := gm.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := gm.Remember("Brand new node"); !strings.Contains(got, "Brand new node") {
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
	if got := gm.Remember("Source"); !strings.Contains(got, "Source") {
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
