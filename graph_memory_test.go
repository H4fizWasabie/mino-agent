package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestRememberValidationCases is MEM-05: prove "right fact, right time, right
// reason" against a synthetic store — topical query returns the right fact
// with a correct rationale, irrelevant query is rejected, the co-authored
// use_when path fires, and the active turn rescues a thin query. (Expired-why
// rejection arrives with MEM-06 — not landed yet.)
func TestRememberValidationCases(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	facts := []Fact{
		{ID: "planet", Type: "semantic", Subject: "My planet is Mars", Body: "Red planet, fourth from the sun.",
			Why: "because I live there", UseWhen: []string{"when user asks about where I live"}},
		{ID: "coffee", Type: "semantic", Subject: "Coffee preference is dark roast", Body: "Strong coffee, no sugar.",
			Why: "I like strong coffee", UseWhen: []string{"when deciding what to order"}},
		{ID: "server", Type: "semantic", Subject: "VPS runs mino as systemd service", Body: "User mino, /home/mino/.mino store.",
			Why: "the real memory store lives there", UseWhen: []string{"when the vps misbehaves", "when debugging deployments"}},
	}
	for _, f := range facts {
		if err := gm.RecordFact(f); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name    string
		query   string
		turn    string
		want    string   // fact ID expected first; "" = rejection expected
		wantIn  []string // substrings the output must contain
		wantOut []string // substrings the output must not contain
	}{
		{"topical query returns right fact", "My planet is Mars", "", "planet",
			[]string{"exact subject", "why: because I live there"}, nil},
		{"co-authored intent recall", "where do I live", "", "planet",
			[]string{"use_when: live, where"}, nil},
		{"irrelevant query rejected", "zebra migration patterns", "", "", nil, nil},
		{"turn rescues thin query", "remind me about the thing", "the vps is down", "server",
			[]string{"your words: vps"}, nil},
		{"same query without turn rejected", "remind me about the thing", "", "", nil, nil},
	}

	for _, c := range cases {
		got := gm.Remember(c.query, c.turn)
		if c.want == "" {
			if !strings.Contains(got, "No memories found") {
				t.Errorf("%s: expected rejection, got:\n%s", c.name, got)
			}
			continue
		}
		fact, ok := gm.facts[c.want]
		if !ok {
			t.Fatalf("%s: test fact %s missing", c.name, c.want)
		}
		if !strings.HasPrefix(got, fact.Subject) {
			t.Errorf("%s: %s not ranked first, got:\n%s", c.name, c.want, got)
		}
		for _, want := range c.wantIn {
			if !strings.Contains(got, want) {
				t.Errorf("%s: missing %q in:\n%s", c.name, want, got)
			}
		}
		for _, out := range c.wantOut {
			if strings.Contains(got, out) {
				t.Errorf("%s: unexpected %q in:\n%s", c.name, out, got)
			}
		}
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

// CTX-022 B: the archive tier must exist from first boot — a missing dir made
// the archive path dead code in practice (Agent-Reach facts were never
// retired because nothing ever created it).
func TestGraphMemoryInitCreatesArchiveDir(t *testing.T) {
	dir := t.TempDir()
	NewGraphMemory(dir, nil)
	if _, err := os.Stat(filepath.Join(dir, "archive")); err != nil {
		t.Fatalf("archive dir not created on init: %v", err)
	}
}

// MEM-08 archive lifecycle: ArchiveFact moves a fact out of the live graph into
// memories/archive/, cleans inbound edges, queues the digest line, and the
// archived fact stays readable. Digest entries round-trip through failure.
func TestGraphMemoryArchiveLifecycle(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "meeting", Type: "semantic", Subject: "Arachem meeting is Friday 7 Aug",
		Body: "Team sync.", Why: "so I attend on time", Edges: []Edge{{Target: "person", Rel: "with"}}}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "person", Type: "semantic", Subject: "Arachem contact is Sam",
		Edges: []Edge{{Target: "meeting", Rel: "attends"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := gm.ArchiveFact(*gm.facts["meeting"], "judgment: why no longer holds"); err != nil {
		t.Fatal(err)
	}
	if _, ok := gm.FindFact("meeting"); ok {
		t.Fatal("archived fact still live")
	}
	if len(gm.facts["person"].Edges) != 0 {
		t.Fatalf("inbound edge not cleaned: %#v", gm.facts["person"].Edges)
	}
	arch := gm.archiveFactsLocked()
	fact, ok := arch["meeting"]
	if !ok {
		t.Fatal("archived fact not readable")
	}
	if fact.Subject != "Arachem meeting is Friday 7 Aug" || fact.Why == "" {
		t.Fatalf("archived fact lost content: %+v", fact)
	}
	// digest queued once, cleared by take, restorable after a failed send
	if pending := gm.TakePendingDigest(); len(pending) != 1 || !strings.Contains(pending[0], "meeting|") {
		t.Fatalf("digest pending = %v", pending)
	}
	if gm.TakePendingDigest() != nil {
		t.Fatal("digest not cleared")
	}
	gm.AppendPendingDigest([]string{"meeting|Arachem meeting|user rejection"})
	if pending := gm.TakePendingDigest(); len(pending) != 1 || !strings.Contains(pending[0], "user rejection") {
		t.Fatalf("digest restore = %v", pending)
	}
}

// MEM-08 archive fallback: empty or thin live recall falls back to the archive
// with an [archived] tag; strong live recall never leaks archived facts.
func TestGraphMemoryRememberArchivedFallback(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "coffee", Type: "semantic", Subject: "Coffee preference is dark roast",
		Body: "Strong coffee, no sugar."}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "meeting", Type: "semantic", Subject: "Arachem meeting is Friday 7 Aug",
		Body: "Team sync.", Why: "so I attend on time"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gm.ArchiveFact(*gm.facts["meeting"], "judgment: why no longer holds"); err != nil {
		t.Fatal(err)
	}

	// Empty live → archive fallback, tagged, why still shown.
	got := gm.Remember("when is the meeting", "")
	if !strings.Contains(got, "[archived]") || !strings.Contains(got, "Arachem meeting") || !strings.Contains(got, "so I attend on time") {
		t.Fatalf("archive fallback missing:\n%s", got)
	}
	// Thin live (one body word, score 3 < 10) → archive still wins.
	if err := gm.RecordFact(Fact{ID: "note", Type: "semantic", Subject: "Random notes",
		Body: "meeting minutes folder"}); err != nil {
		t.Fatal(err)
	}
	got = gm.Remember("meeting", "")
	if !strings.Contains(got, "[archived]") {
		t.Fatalf("thin live recall did not fall back:\n%s", got)
	}
	// Strong live match → no archive fallback.
	got = gm.Remember("coffee", "")
	if strings.Contains(got, "[archived]") {
		t.Fatalf("archive leaked into strong live recall:\n%s", got)
	}
}

// MEM-08 active expiry: a negative feedback signal archives immediately.
func TestGraphMemoryFeedbackArchivesOnReject(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "pref", Type: "semantic", Subject: "Preference"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gm.Feedback("pref", 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := gm.FindFact("pref"); !ok {
		t.Fatal("positive feedback archived the fact")
	}
	if _, err := gm.Feedback("pref", -2); err != nil {
		t.Fatal(err)
	}
	if _, ok := gm.FindFact("pref"); ok {
		t.Fatal("reject feedback did not archive")
	}
	if _, ok := gm.archiveFactsLocked()["pref"]; !ok {
		t.Fatal("rejected fact not in archive")
	}
	if pending := gm.TakePendingDigest(); len(pending) != 1 || !strings.Contains(pending[0], "user rejection") {
		t.Fatalf("digest = %v", pending)
	}
}

func TestGraphMemoryLenientTimestampSelfHeals(t *testing.T) {
	// A malformed at: must not drop the whole fact (issue #65): it loads with
	// zero time, stays recallable, and the rebuild pass stamps a valid one.
	dir := t.TempDir()
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	path := filepath.Join(gm.dir, "bad_clock.md")
	os.WriteFile(path, []byte("---\nid: bad_clock\ntype: semantic\nsubject: Bad clock fact\nat: 2026-08-08T045300Z\n---\n\nBody survives.\n"), 0644)
	gm.Refresh()

	f, ok := gm.FindFact("bad_clock")
	if !ok {
		t.Fatalf("fact dropped by malformed timestamp")
	}
	if !f.At.IsZero() {
		t.Fatalf("expected zero At, got %v", f.At)
	}
	if f.Body != "Body survives." {
		t.Fatalf("body lost: %q", f.Body)
	}
	got := gm.Remember("bad clock", "")
	if !strings.Contains(got, "Bad clock fact") {
		t.Fatalf("fact not recallable:\n%s", got)
	}
	// Self-heal: ReplaceFact (the rebuild write path) stamps now on zero At.
	if err := gm.ReplaceFact(*f); err != nil {
		t.Fatal(err)
	}
	f2, _ := gm.FindFact("bad_clock")
	if f2.At.IsZero() {
		t.Fatalf("rebuild did not self-heal the timestamp")
	}
	// Second parse of the healed file succeeds without warnings.
	if _, err := gm.readFile(path); err != nil {
		t.Fatalf("healed file still fails to parse: %v", err)
	}
}

// issue #180: a user correction outranks a model re-entry of the same
// knowledge, even when the re-entry is newer.
func TestEntryRankingPrefersUserProvenance(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "hosting_reentry", Type: "semantic", Subject: "Image hosting runs on the legacy box", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "hosting_correction", Type: "semantic", Subject: "Image hosting setup corrected to HTTPS", Source: "user-correction-20260812", At: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	starts := gm.entryRanking("image hosting", "", gm.facts, true)
	if len(starts) == 0 || starts[0].id != "hosting_correction" {
		t.Fatalf("top start = %+v, want the user-corrected fact", starts)
	}
}

// issue #180: two recalled facts carrying different URL domains surface a
// conflict marker on both instead of silently trusting rank.
func TestRememberMarksConflictingURLs(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "hosting_a", Type: "semantic", Subject: "Image hosting", Body: "Runs at http://149.28.146.30"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "hosting_b", Type: "semantic", Subject: "Image hosting setup", Body: "Serves at https://images.example.com"}); err != nil {
		t.Fatal(err)
	}
	got := gm.Remember("image hosting", "")
	if !strings.Contains(got, "⚠ conflicts with hosting_b") || !strings.Contains(got, "⚠ conflicts with hosting_a") {
		t.Fatalf("conflict markers missing:\n%s", got)
	}
}

// issue #180: same-domain facts are not flagged as conflicting.
func TestRememberSameDomainNotConflicting(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Docs", Body: "See https://docs.example.com/one"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "More docs", Body: "See https://docs.example.com/two"}); err != nil {
		t.Fatal(err)
	}
	if got := gm.Remember("docs", ""); strings.Contains(got, "⚠ conflicts") {
		t.Fatalf("false conflict on same domain:\n%s", got)
	}
}

// issue #180: the repair drops inferred supersedes edges pointing at
// user-provenanced facts and leaves other edges alone.
func TestRemoveSupersedesIntoUserFacts(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	// Record the target first: RecordFact drops edges to unknown facts.
	if err := gm.RecordFact(Fact{ID: "right", Type: "semantic", Subject: "Corrected claim", Source: "user-correction-20260812"}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "wrong", Type: "semantic", Subject: "Wrong claim", Edges: []Edge{
		{Target: "right", Rel: "supersedes", Kind: "inferred", Confidence: 0.92},
		{Target: "right", Rel: "depends_on", Kind: "inferred", Confidence: 0.9},
		{Target: "right", Rel: "supersedes", Kind: "explicit"},
	}}); err != nil {
		t.Fatal(err)
	}
	if n := gm.RemoveSupersedesIntoUserFacts(); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	wrong, _ := gm.FindFact("wrong")
	if len(wrong.Edges) != 2 {
		t.Fatalf("wrong edges = %+v, want depends_on + explicit supersedes kept", wrong.Edges)
	}
}

// issue #178: episodic facts never start a recall — they stay reachable as
// BFS neighborhood context from semantic starts.
// #320 Fix A: a thin free ranking routes through the computed community index
// (labels + god nodes) before falling to archive — the graphify
// query → community → nodes shape, wired into recall.
func TestRememberCommunityRoutingRescuesThinQuery(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	// Two communities: "VPS Ops" (god: vps) and "Cooking" (god: curry).
	gm.RecordFact(Fact{ID: "vps", Type: "semantic", Subject: "VPS runs mino", Why: "hosts mino", UseWhen: []string{"deployment", "vps"}})
	gm.RecordFact(Fact{ID: "curry", Type: "semantic", Subject: "Curry recipe", Why: "abah loves curry", UseWhen: []string{"dinner", "recipe"}})
	gm.SetCommunities(map[string]int{"vps": 0, "curry": 1}, []string{"vps", "curry"}, map[string]string{"0": "VPS Ops", "1": "Cooking"})

	// The query "ops" matches no fact text and no use_when (subject "VPS runs
	// mino" has no "ops") — free ranking is thin. The community label "VPS
	// Ops" routes to the vps god node.
	got := gm.Remember("ops", "")
	if !strings.Contains(got, "vps") {
		t.Fatalf("community routing did not surface vps:\n%s", got)
	}
	if !strings.Contains(got, "community-routed") {
		t.Fatalf("community-routed signal missing:\n%s", got)
	}
	if strings.Contains(got, "curry") {
		t.Fatalf("unmatched community leaked into recall:\n%s", got)
	}
}

// #320 Fix A: with no communities computed, routing is a no-op and the recall
// falls through to the archive path unchanged.
func TestRememberCommunityRoutingNoopWithoutCommunities(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	gm.RecordFact(Fact{ID: "vps", Type: "semantic", Subject: "VPS runs mino", Why: "hosts mino", UseWhen: []string{"deployment", "vps"}})
	got := gm.Remember("ops", "")
	if !strings.Contains(got, "No memories found") {
		t.Fatalf("no-communities recall changed behavior:\n%s", got)
	}
}

// #320 Fix B: the BFS neighborhood gets a hard line budget so a dense graph
// cannot inject unbounded text into context on every iteration.
func TestRememberNeighborhoodBudgetCapsOutput(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	gm.RecordFact(Fact{ID: "hub", Type: "semantic", Subject: "Central hub fact", Why: "hub", UseWhen: []string{"hub"}})
	// 60 neighbors, each with its own 3 outgoing edges — a dense neighborhood
	// that would previously emit hundreds of lines.
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("n%d", i)
		edges := []Edge{{Target: fmt.Sprintf("m%d", i*3), Rel: "links"}, {Target: fmt.Sprintf("m%d", i*3+1), Rel: "links"}, {Target: fmt.Sprintf("m%d", i*3+2), Rel: "links"}}
		gm.RecordFact(Fact{ID: id, Type: "semantic", Subject: fmt.Sprintf("Neighbor %d", i), Edges: edges})
		gm.RecordFact(Fact{ID: fmt.Sprintf("m%d", i*3), Type: "semantic", Subject: fmt.Sprintf("M%d", i*3), Edges: []Edge{{Target: id, Rel: "linked_to"}}})
		gm.RecordFact(Fact{ID: fmt.Sprintf("m%d", i*3+1), Type: "semantic", Subject: fmt.Sprintf("M%d", i*3+1), Edges: []Edge{{Target: id, Rel: "linked_to"}}})
		gm.RecordFact(Fact{ID: fmt.Sprintf("m%d", i*3+2), Type: "semantic", Subject: fmt.Sprintf("M%d", i*3+2), Edges: []Edge{{Target: id, Rel: "linked_to"}}})
	}
	gm.RecordFact(Fact{ID: "hub", Type: "semantic", Subject: "Central hub fact", Why: "hub", UseWhen: []string{"hub"}, Edges: []Edge{{Target: "n0", Rel: "connects"}}})

	got := gm.Remember("hub", "")
	lines := strings.Split(strings.TrimSpace(got), "\n")
	// Starts (subject+why+body+matched ≈ 4 lines) plus a bounded neighborhood.
	if len(lines) > recallNeighborhoodBudget+8 {
		t.Fatalf("recall output exceeded budget: %d lines (budget %d):\n%s", len(lines), recallNeighborhoodBudget, got)
	}
}

// #321: the synthesis prompt marks user-provenanced facts as PROTECTED and
// never invites rewriting them — the additive invariant lives in the contract.
func TestBuildCommunitySynthesisPromptProtectsUserFacts(t *testing.T) {
	members := []Fact{
		{ID: "owner_thing", Source: "user", Subject: "Abah loves ayam gepuk", Why: "his favorite"},
		{ID: "run_node", Type: "episodic", Source: "model-distill", Subject: "Posted IG 2026-08-21", Body: "posted OK"},
	}
	prompt := buildCommunitySynthesisPrompt(0, members, false)
	if !strings.Contains(prompt, "PROTECTED USER FACT owner_thing") {
		t.Fatalf("user fact not marked protected:\n%s", prompt)
	}
	if !strings.Contains(prompt, "never rewrite, merge, or claim this fact") {
		t.Fatalf("protection rule missing:\n%s", prompt)
	}
	if strings.Contains(prompt, "PROTECTED USER FACT run_node") {
		t.Fatalf("non-user fact wrongly protected:\n%s", prompt)
	}
}

func TestRememberExcludesEpisodicStarts(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "ep_run", Type: "episodic", Subject: "Ran ai-news digest 2026-08-13", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "news_playbook", Type: "semantic", Subject: "News playbook publishes headlines", Edges: []Edge{{Target: "ep_run", Rel: "ran"}}}); err != nil {
		t.Fatal(err)
	}
	// A query that only matches the episode must come up empty, not start there.
	if got := gm.Remember("digest 2026", ""); !strings.Contains(got, "No memories found") {
		t.Fatalf("episodic fact became a recall start:\n%s", got)
	}
	// From a semantic start the episode is still visible as neighborhood.
	got := gm.Remember("News playbook", "")
	if !strings.Contains(got, "ep_run") {
		t.Fatalf("episodic neighbor missing from tree:\n%s", got)
	}
}

// issue #178: episodes older than 30d archive with reason expiry; semantic
// facts and fresh episodes stay live; archived episodes remain answerable via
// the archive fallback.
func TestArchiveExpiredEpisodic(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), nil)
	if err := gm.RecordFact(Fact{ID: "old_ep", Type: "episodic", Subject: "Old run", At: time.Now().Add(-31 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "fresh_ep", Type: "episodic", Subject: "Fresh run", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := gm.RecordFact(Fact{ID: "old_sem", Type: "semantic", Subject: "Old semantic fact", At: time.Now().Add(-31 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n := gm.ArchiveExpiredEpisodic(time.Now().Add(-30 * 24 * time.Hour)); n != 1 {
		t.Fatalf("archived %d, want 1", n)
	}
	if _, ok := gm.FindFact("old_ep"); ok {
		t.Fatal("old episode still live")
	}
	if _, ok := gm.FindFact("fresh_ep"); !ok {
		t.Fatal("fresh episode must stay live")
	}
	if _, ok := gm.FindFact("old_sem"); !ok {
		t.Fatal("semantic facts never expire")
	}
	if _, ok := gm.archiveFactsLocked()["old_ep"]; !ok {
		t.Fatal("expired episode not in archive")
	}
	// Second pass is a no-op.
	if n := gm.ArchiveExpiredEpisodic(time.Now().Add(-30 * 24 * time.Hour)); n != 0 {
		t.Fatalf("second pass archived %d, want 0", n)
	}
}

// The double front-matter class (2026-08-14, reproduced live twice): a caller
// embeds the whole file — front-matter included — in the body. RecordFact must
// strip the leading block so the written file carries exactly one front-matter.
func TestRecordFactStripsEmbeddedFrontMatterFromBody(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), &Settings{TopK: 2})
	body := "---\nid: ai_concept_function_calling\ntype: semantic\nsubject: 'AI concept'\n---\n\nThe real concept prose."
	err := gm.RecordFact(Fact{ID: "ai_concept_function_calling", Type: "semantic", Subject: "AI concept: function calling", At: time.Now(), Source: "user", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(gm.dir, "ai_concept_function_calling.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Count(s, "---\n") != 2 { // exactly one opening + one closing delimiter
		t.Fatalf("double front-matter still possible:\n%s", s)
	}
	if !strings.Contains(s, "The real concept prose") {
		t.Fatalf("prose lost after strip:\n%s", s)
	}
	// round-trip: the file must parse back as one fact
	f, err := gm.parseFrontMatter(got)
	if err != nil || f.ID != "ai_concept_function_calling" {
		t.Fatalf("parse failed: %v %+v", err, f)
	}
}

func TestStripLeadingFrontMatter(t *testing.T) {
	cases := []struct{ in, want string }{
		{"---\na: 1\n---\n\nprose", "prose"},
		{"---\nid: x\n---", ""},
		{"plain prose", "plain prose"},
		{"---\nno closing delimiter", "---\nno closing delimiter"},
	}
	for _, c := range cases {
		if got := stripLeadingFrontMatter(c.in); got != c.want {
			t.Errorf("strip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// DRF-002: the 30d backstop — model-authored semantic facts past the cutoff
// archive with reason "stale"; authoritative facts never auto-stale; a
// declared stale_after wins over At.
func TestArchiveStaleSemantic(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), &Settings{TopK: 2})
	old := time.Now().Add(-40 * 24 * time.Hour)
	fresh := time.Now().Add(-5 * 24 * time.Hour)
	mustRecord(t, gm, Fact{ID: "old_model", Type: "semantic", Subject: "old model fact", At: old, Source: "graph-rebuild"})
	mustRecord(t, gm, Fact{ID: "old_user", Type: "semantic", Subject: "old user fact", At: old, Source: "user"})
	mustRecord(t, gm, Fact{ID: "fresh_model", Type: "semantic", Subject: "fresh model fact", At: fresh, Source: "model-distill"})
	mustRecord(t, gm, Fact{ID: "declared", Type: "semantic", Subject: "declared fact", At: fresh, StaleAfter: old, Source: "graph-rebuild"})
	mustRecord(t, gm, Fact{ID: "future_declared", Type: "semantic", Subject: "future declared fact", At: old, StaleAfter: time.Now().Add(10 * 24 * time.Hour), Source: "graph-rebuild"})

	n := gm.ArchiveStaleSemantic(time.Now().Add(-30 * 24 * time.Hour))
	if n != 2 { // old_model + declared; old_user protected, fresh/future kept
		t.Fatalf("archived %d, want 2", n)
	}
	for _, gone := range []string{"old_model", "declared"} {
		if _, ok := gm.facts[gone]; ok {
			t.Errorf("%s still live, want archived", gone)
		}
	}
	for _, kept := range []string{"old_user", "fresh_model", "future_declared"} {
		if _, ok := gm.facts[kept]; !ok {
			t.Errorf("%s archived, want live", kept)
		}
	}
	// archive fallback still answers (never destroyed)
	if gm.archiveFactsLocked() == nil {
		t.Fatal("archive empty after staleness pass")
	}
}

// DRF-002: an authoritative correction demotes conflicting model facts
// (asymmetry: a model re-entry demotes nothing).
func TestCorrectionDemotesConflictingModelFacts(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), &Settings{TopK: 2})
	mustRecord(t, gm, Fact{ID: "stale_qwen", Type: "semantic", Subject: "Mino runs on qwen flash via OpenRouter with deepseek small model", Source: "graph-rebuild", At: time.Now().Add(-6 * 24 * time.Hour)})
	mustRecord(t, gm, Fact{ID: "unrelated", Type: "semantic", Subject: "Abah supplier meeting for the PCR machine", Source: "graph-rebuild", At: time.Now()})

	// User correction lands on the model-stack subject -> stale_qwen archives.
	mustRecord(t, gm, Fact{ID: "model_stack", Type: "semantic", Subject: "current authoritative Mino model stack", Body: "main and small are deepseek flash pinned to deepinfra, fallback qwen3.7-flash", Source: "user-correction-20260814", At: time.Now()})
	if _, ok := gm.facts["stale_qwen"]; ok {
		t.Fatal("stale_qwen still live after user correction — demotion failed")
	}
	if _, ok := gm.facts["unrelated"]; !ok {
		t.Fatal("unrelated fact archived — subject matching too loose")
	}
	if _, ok := gm.facts["model_stack"]; !ok {
		t.Fatal("correction itself missing")
	}

	// Model re-entry on the same subject must demote NOTHING (asymmetry).
	mustRecord(t, gm, Fact{ID: "model_reen", Type: "semantic", Subject: "current authoritative Mino model stack re-stated", Source: "model-distill", At: time.Now()})
	if _, ok := gm.facts["model_stack"]; !ok {
		t.Fatal("model re-entry demoted the user fact — asymmetry violated")
	}
}

// DRF-002: same-subject contradictions get the conflict marker at recall.
func TestMarkConflictSignalsSubjectBased(t *testing.T) {
	gm := NewGraphMemory(t.TempDir(), &Settings{TopK: 2})
	mustRecord(t, gm, Fact{ID: "a", Type: "semantic", Subject: "Mino runs on qwen flash via OpenRouter", Body: "qwen is the main model", Source: "graph-rebuild", At: time.Now()})
	mustRecord(t, gm, Fact{ID: "b", Type: "semantic", Subject: "Mino model stack uses deepseek flash main", Body: "deepseek flash is the main model", Source: "graph-rebuild", At: time.Now()})
	ranks := gm.entryRanking("mino model", "", gm.facts, true)
	if len(ranks) < 2 {
		t.Fatalf("want 2 top facts, got %d", len(ranks))
	}
	markConflictSignals(ranks, gm.facts)
	conflicts := 0
	for _, r := range ranks {
		for _, sig := range r.signals {
			if strings.Contains(sig, "conflicts with") {
				conflicts++
			}
		}
	}
	if conflicts < 2 {
		t.Fatalf("same-subject contradiction not flagged: %+v", ranks)
	}

	// Unrelated subjects must not conflict.
	gm2 := NewGraphMemory(t.TempDir(), &Settings{TopK: 2})
	mustRecord(t, gm2, Fact{ID: "x", Type: "semantic", Subject: "Abah meeting at Qbistro on Saturday", Body: "friends meetup", Source: "graph-rebuild", At: time.Now()})
	mustRecord(t, gm2, Fact{ID: "y", Type: "semantic", Subject: "Bair Hugger demo with supplier", Body: "medical equipment", Source: "graph-rebuild", At: time.Now()})
	ranks2 := gm2.entryRanking("abah bair", "", gm2.facts, true)
	markConflictSignals(ranks2, gm2.facts)
	for _, r := range ranks2 {
		for _, sig := range r.signals {
			if strings.Contains(sig, "conflicts with") {
				t.Fatalf("unrelated facts flagged as conflict: %+v", ranks2)
			}
		}
	}
}

func mustRecord(t *testing.T, gm *GraphMemory, f Fact) {
	t.Helper()
	if err := gm.RecordFact(f); err != nil {
		t.Fatal(err)
	}
}
