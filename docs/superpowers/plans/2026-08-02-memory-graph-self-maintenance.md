# Memory Graph Self-Maintenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Mino's memory graph self-maintaining: model-judged edges on every memory change, playbook outputs distilled into compact episodic nodes, 6-hourly full edge re-inference, and automatic community detection — with deterministic storage/recall as the resilience floor.

**Architecture:** Keep the .md-authoritative storage + index.json exactly as-is (the agreed "storage was never the issue"). Rebuild the intelligence layer around it: (1) per-fact edge judgment on new/changed memories, (2) playbook-run output distillation into one compact episodic node per run, (3) the existing `RebuildGraphEdges` promoted from manual CLI to a 6-hourly scheduled pass with embedding backfill, (4) Louvain community detection + god nodes stored in index.json. Edge production becomes automatic and sees *all* facts — never gated behind manual invocation or missing embeddings.

**Tech Stack:** Go (stdlib only — no new deps), SQLite (modernc.org/sqlite, existing), existing small-model client (`CreateJSON`), existing EmbeddingStore, YAML front matter .md files.

## Global Constraints

- Storage contract frozen: one `.md` per fact, YAML front matter (`id`, `type`, `subject`, `at`, `edge`, optional body); `index.json` is the cache. Bodies NEVER in index.json.
- No new dependencies. Stdlib only (Louvain implemented in ~100 lines of Go, not a library).
- Edge relations: use existing vocabulary; never `related_to`; confidence floor 0.85 for inferred edges (existing `validInferredEdges`).
- Deterministic floor must survive model failure: substring entry + embedding fallback in `remember` must work with zero LLM calls. LLM enriches, never gates.
- Retry-and-catch-up: any failed LLM pass leaves state unmarked so the next pass retries. Never mark work done that wasn't written.
- The loop stays canonical: these are background maintenance passes (app.go goroutines), never a second agent loop.
- Every task: `go test ./...` must pass, CHANGELOG.md updated, commit per task, branch per task (`feat/...` or `fix/...`).
- Index format version bump 2 → 3 when `index.json` gains new fields (`loadIndex` rejects wrong version, so bump forces one-time rebuild — acceptable).

---

### Task 1: Edge-judgment ledger + index v3

Track per-fact LLM edge judgment state so the pipeline knows which facts need judging, and add community/god-node fields to index.json (populated in later tasks).

**Files:**
- Modify: `graph_memory.go` — `indexEntry`, `index`, `loadIndex`, `saveIndex`, `loadAll`, `refreshLocked`, `NewGraphMemory`

**Interfaces:**
- Produces: `indexEntry.JudgedAt string` (RFC3339, empty = needs judgment); `index.JudgedAt` per entry; `index.Communities map[string]int`; `index.Gods []string`; `index.Labels map[string]string`; `index.Version = 3`; `GraphMemory.JudgedAt(id string) string`; `GraphMemory.MarkJudged(id string)`; `GraphMemory.UnjudgedFacts() []*Fact`; `GraphMemory.Communities() map[string]int`; `GraphMemory.SetCommunities(map[string]int, []string, map[string]string)`

- [ ] **Step 1: Write the failing test** — append to `graph_memory_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestGraphMemoryIndexV3JudgmentLedger -v`
Expected: FAIL — `JudgedAt` undefined.

- [ ] **Step 3: Implement**

In `graph_memory.go`:

```go
type indexEntry struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Subject  string `json:"subject"`
	At       string `json:"at"`
	Why      string `json:"why,omitempty"`
	Source   string `json:"source,omitempty"`
	Feedback int    `json:"feedback,omitempty"`
	Edges    []Edge `json:"edges,omitempty"`
	JudgedAt string `json:"judged_at,omitempty"`
}

type index struct {
	Version      int               `json:"version"`
	Facts        map[string]indexEntry `json:"facts"`
	Files        map[string]fileStamp  `json:"files"`
	Communities  map[string]int    `json:"communities,omitempty"`
	Gods         []string          `json:"gods,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}
```

- Add `judgedAt map[string]string` field to `GraphMemory` struct (id → RFC3339), loaded in `loadIndex` from `entry.JudgedAt`, initialized empty in `loadAll`, cleared in `refreshLocked` whenever a file's stamp changed (the `changed = true` path for that file), and written in `saveIndex` from the map. `Version` constant in `loadIndex` check becomes `!= 3`.
- New methods (all under `gm.mu` where touching `judgedAt`):

```go
func (gm *GraphMemory) JudgedAt(id string) string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.judgedAt[id]
}

func (gm *GraphMemory) MarkJudged(id string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.judgedAt[id] = time.Now().UTC().Format(time.RFC3339)
	gm.saveIndex()
}

func (gm *GraphMemory) UnjudgedFacts() []*Fact {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	var out []*Fact
	for _, f := range gm.facts {
		if gm.judgedAt[f.ID] == "" {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (gm *GraphMemory) Communities() map[string]int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	out := make(map[string]int, len(gm.communities))
	for k, v := range gm.communities {
		out[k] = v
	}
	return out
}

func (gm *GraphMemory) SetCommunities(communities map[string]int, gods []string, labels map[string]string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.communities = communities
	gm.gods = gods
	gm.labels = labels
	gm.saveIndex()
}
```

- Add `communities map[string]int`, `gods []string`, `labels map[string]string` fields to `GraphMemory`; initialize maps in `NewGraphMemory`; load/persist in `loadIndex`/`saveIndex`. In `refreshLocked`, when a changed file is detected: `gm.judgedAt[fact.ID] = ""` (and delete the old ID's entry if `stamp.ID != fact.ID`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestGraphMemory' -v`
Expected: PASS (all existing graph tests + 2 new).

- [ ] **Step 5: Commit**

```bash
git add graph_memory.go graph_memory_test.go CHANGELOG.md
git commit -m "feat: edge-judgment ledger + community fields in index v3"
```
Add CHANGELOG entry: `### Added` — "Edge-judgment ledger tracks which facts still need LLM edge judgment; index.json v3 carries judgment state, communities, god nodes, and labels."

---

### Task 2: Embedding backfill + edge-source labeling + mutual-pair cleanup

Fixes the three verified edge-production bugs: migrated facts invisible to candidate generation (no embeddings), edges mislabeled `consolidation` when written by rebuild, and mirrored inferred pairs surviving cleanup (only `supersedes` was cleaned).

**Files:**
- Modify: `adapters.go` (`EmbeddingStore`), `memory.go` (`validInferredEdges`, `RebuildGraphEdges`), `graph_memory.go` (`RemoveMutualInferredEdges`)
- Test: `memory_test.go`

**Interfaces:**
- Produces: `EmbeddingStore.HasFactEmbedding(id string) bool`; `validInferredEdges(edges []Edge, candidates map[string]bool, source string) []Edge` (signature change — update both callers); `RemoveMutualInferredEdges` now removes ANY mirrored inferred pair (keeps higher confidence), not just `supersedes`.

- [ ] **Step 1: Write the failing tests** — append to `memory_test.go` (add `"io"` to its imports):

```go
func TestGraphRebuildBackfillsMissingEmbeddings(t *testing.T) {
	// RebuildGraphEdges must embed facts lacking vectors BEFORE GraphCandidates,
	// else migrated facts are invisible to edge inference.
	// The embedder calls the package-level httpClient (hardcoded OpenRouter
	// URL), so swap it to route embedding requests to the local test server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "text-embedding") {
			fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"edges\":[]}"}}]}`)
	}))
	defer server.Close()
	old := httpClient
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		// Rewrite the hardcoded OpenRouter host to the local test server.
		r2 := r.Clone(r.Context())
		r2.URL.Scheme = "http"
		r2.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return server.Client().Transport.RoundTrip(r2)
	})}
	defer func() { httpClient = old }()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	dir := t.TempDir()
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	gm.RecordFact(Fact{ID: "orphan", Type: "semantic", Subject: "Orphan fact", Body: "Body"})
	m := &Memory{client: pm, graph: gm, embedder: NewEmbeddingStore(Connect(dir), "test-key", "openai/text-embedding-3-large")}
	if _, err := m.RebuildGraphEdges(); err != nil {
		t.Fatalf("rebuild failed: %v", err)
	}
	if !m.embedder.HasFactEmbedding("orphan") {
		t.Fatal("fact was not embedded during rebuild")
	}
}

func TestRemoveMutualInferredEdgesAnyRelation(t *testing.T) {
	dir := t.TempDir()
	gm := NewGraphMemory(dir, nil)
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "A", Edges: []Edge{{Target: "b", Rel: "used_in", Kind: "inferred", Confidence: 0.9}}})
	gm.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "B", Edges: []Edge{
		{Target: "a", Rel: "contains", Kind: "inferred", Confidence: 0.95},
		{Target: "a", Rel: "maintains", Kind: "explicit"},
	}})
	if n := gm.RemoveMutualInferredEdges(); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	// Mutual rule: when A→B and B→A are BOTH inferred, drop the
	// lower-confidence edge; explicit edges always survive. A→B (0.9)
	// vs B→A (0.95): keep B→A, drop A→B. So after cleanup: a has 0
	// edges, b has contains (0.95) + maintains (explicit) = 2 edges.
	a, _ := gm.FindFact("a")
	if len(a.Edges) != 0 {
		t.Fatalf("a edges = %+v, want none (lower-confidence mirror dropped)", a.Edges)
	}
	b, _ := gm.FindFact("b")
	if len(b.Edges) != 2 {
		t.Fatalf("b edges = %+v, want contains(0.95) + maintains(explicit)", b.Edges)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestGraphRebuildBackfillsMissingEmbeddings|TestRemoveMutualInferredEdgesAnyRelation' -v`
Expected: FAIL — `HasFactEmbedding` undefined; mutual removal only handles `supersedes`.

- [ ] **Step 3: Implement**

In `adapters.go`:

```go
// HasFactEmbedding reports whether a stable fact:<id> vector exists.
func (es *EmbeddingStore) HasFactEmbedding(id string) bool {
	es.mu.RLock()
	defer es.mu.RUnlock()
	for _, d := range es.docs {
		if d.Source == "fact:"+id {
			return true
		}
	}
	return false
}
```

In the test file, add the `roundTripFunc` helper (top of `memory_test.go`):

```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
```

In `memory.go` — backfill before candidates in `RebuildGraphEdges`:

```go
	// Backfill embeddings for facts that never got vectors (migrated facts,
	// installs where the embedder was configured later). Without a vector a
	// fact is invisible to GraphCandidates and can never gain edges.
	for _, fact := range facts {
		if !m.embedder.HasFactEmbedding(fact.ID) {
			m.embedder.IndexFact(fact.ID, fact)
		}
	}
```

Change `validInferredEdges` signature to take a `source string` and use it instead of the hardcoded `"consolidation"`. Update the consolidation caller to pass `"consolidation"` and `RebuildGraphEdges` (which calls it for batch edges) to pass `"graph-rebuild"`.

In `graph_memory.go` — rewrite `RemoveMutualInferredEdges`:

```go
// RemoveMutualInferredEdges resolves mirrored inferred pairs (A→B and B→A,
// any relation): the lower-confidence edge is dropped, explicit edges win.
func (gm *GraphMemory) RemoveMutualInferredEdges() int {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	removed := 0
	for _, fact := range gm.facts {
		filtered := fact.Edges[:0]
		for _, edge := range fact.Edges {
			if edge.Kind == "inferred" {
				target := gm.facts[edge.Target]
				if target != nil {
					for _, reverse := range target.Edges {
						if reverse.Kind == "inferred" && reverse.Target == fact.ID && reverse.Confidence > edge.Confidence {
							removed++
							goto drop
						}
					}
				}
			}
			filtered = append(filtered, edge)
		drop:
		}
		fact.Edges = filtered
		if err := gm.writeFile(*fact); err == nil {
			gm.files[fact.ID+".md"] = gm.fileStamp(fact.ID)
		}
	}
	if removed > 0 {
		gm.saveIndex()
	}
	return removed
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestGraphRebuild|TestRemoveMutual|TestConsolidation' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters.go memory.go graph_memory.go memory_test.go CHANGELOG.md
git commit -m "fix: embed missing facts before edge inference; drop mirrored inferred pairs; correct edge provenance"
```
CHANGELOG: `### Fixed` — "facts without embeddings were invisible to graph edge inference (migrated facts could never gain edges); mirrored inferred pairs in any relation are now resolved (previously only supersedes); rebuild-written edges were mislabeled as consolidation."

---

### Task 3: Per-fact incremental edge judgment

The graphify-watch equivalent: every new/changed memory gets LLM-judged edges within minutes, without a full rebuild. Bounded per pass; deterministic floor untouched.

**Files:**
- Modify: `memory.go` (new `JudgeChangedFacts`, `judgeFactEdges`), `app.go` (call from 5-minute loop), `tools.go` (`manage_memory` gains `judge_edges` action — optional, cheap)
- Test: `memory_test.go`

**Interfaces:**
- Consumes: `GraphMemory.UnjudgedFacts()`, `GraphMemory.JudgedAt`, `GraphMemory.MarkJudged`, `graphCandidates(text string)` (existing), `validInferredEdges(edges, candidates, source)`, `parseGraphRebuildResponse`
- Produces: `Memory.JudgeChangedFacts() int` — judges up to 5 unjudged facts per pass (ponytail: bounded, backlog drains over later passes), each via one small-model call using `graphRebuildPrompt` with a single SOURCE claim + its embedding candidates; writes inferred edges preserving explicit ones via `ReplaceFact`; marks judged; returns count judged (0 on any failure — unjudged stays unjudged for retry).

- [ ] **Step 1: Write the failing test** — append to `memory_test.go`:

```go
func TestJudgeChangedFacts(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"edges\":[{\"source\":\"b\",\"target\":\"a\",\"rel\":\"depends_on\",\"confidence\":0.9}]}"}}]}`)
	}))
	defer server.Close()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	dir := t.TempDir()
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Fact A"})
	gm.RecordFact(Fact{ID: "b", Type: "semantic", Subject: "Fact B"})
	m := &Memory{client: pm, graph: gm, embedder: &EmbeddingStore{docs: []embeddedDoc{
		{Source: "fact:a", Embedding: []float32{1, 0}},
		{Source: "fact:b", Embedding: []float32{0.9, 0.1}},
	}}}
	// Pass 1: b gets judged, edge written, marked.
	if n := m.JudgeChangedFacts(); n != 1 {
		t.Fatalf("judged %d facts, want 1", n)
	}
	b, _ := gm.FindFact("b")
	if len(b.Edges) != 1 || b.Edges[0].Target != "a" || b.Edges[0].Rel != "depends_on" || b.Edges[0].Kind != "inferred" {
		t.Fatalf("b edges = %+v", b.Edges)
	}
	// Pass 2: nothing left to judge, no LLM call.
	before := calls
	if n := m.JudgeChangedFacts(); n != 0 || calls != before {
		t.Fatalf("second pass made %d calls (n=%d), want 0", calls-before, n)
	}
	// Failed pass retries: force empty response.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not json`)
	}))
	defer server2.Close()
	pm2 := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server2.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server2.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	gm2 := NewGraphMemory(filepath.Join(dir, "memories2"), nil)
	gm2.RecordFact(Fact{ID: "c", Type: "semantic", Subject: "Fact C"})
	m2 := &Memory{client: pm2, graph: gm2, embedder: &EmbeddingStore{docs: []embeddedDoc{}}}
	if n := m2.JudgeChangedFacts(); n != 0 {
		t.Fatalf("failed pass returned %d, want 0 (retry next pass)", n)
	}
	if got := gm2.UnjudgedFacts(); len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("failed pass must leave fact unjudged: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestJudgeChangedFacts -v`
Expected: FAIL — `JudgeChangedFacts` undefined.

- [ ] **Step 3: Implement**

In `memory.go`:

```go
// JudgeChangedFacts gives every new or edited memory its own edge-judgment
// pass (graphify-style incremental update). Bounded per pass; failures leave
// the fact unjudged so the next pass retries. The deterministic recall floor
// never depends on this.
func (m *Memory) JudgeChangedFacts() int {
	if m.client == nil {
		return 0
	}
	unjudged := m.graph.UnjudgedFacts()
	judged := 0
	for i, fact := range unjudged {
		if i >= 5 { // ponytail: bounded per pass, backlog drains over later passes
			break
		}
		if !m.judgeFactEdges(*fact) {
			continue
		}
		m.graph.MarkJudged(fact.ID)
		judged++
	}
	return judged
}

func (m *Memory) judgeFactEdges(fact Fact) bool {
	if m.embedder == nil {
		return false
	}
	candidates := m.embedder.GraphCandidates([]Fact{fact}, 6)
	ids := candidates[fact.ID]
	if len(ids) == 0 {
		return false
	}
	var claims strings.Builder
	allowed := make(map[string]bool)
	fmt.Fprintf(&claims, "SOURCE %s: %s | %s\n", fact.ID, fact.Subject, fact.Body)
	for _, c := range ids {
		allowed[c.ID] = true
		if other, ok := m.graph.FindFact(c.ID); ok {
			fmt.Fprintf(&claims, "  CANDIDATE %s (%0.2f): %s | %s\n", c.ID, c.Score, other.Subject, other.Body)
		}
	}
	resp, err := m.client.CreateJSON("graph-rebuild", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(graphRebuildPrompt, claims.String())}}, 1400, "")
	if err != nil {
		return false
	}
	text := resp.FinalText
	if text == "" {
		for _, block := range resp.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
	}
	edges, err := parseGraphRebuildResponse(text)
	if err != nil {
		return false
	}
	inferred := m.validInferredEdges(edges, allowed, "graph-rebuild")
	fact.Edges = nil
	if existing, ok := m.graph.FindFact(fact.ID); ok {
		for _, e := range existing.Edges {
			if e.Kind == "explicit" {
				fact.Edges = append(fact.Edges, e)
			}
		}
	}
	fact.Edges = append(fact.Edges, inferred...)
	return m.graph.ReplaceFact(fact) == nil
}
```

In `app.go` — add to the existing 5-minute threshold goroutine:

```go
			if n := mem.JudgeChangedFacts(); n > 0 {
				slog.Info("graph edge judgment", "facts", n)
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestJudgeChangedFacts -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add memory.go app.go memory_test.go CHANGELOG.md
git commit -m "feat: per-fact edge judgment pass on new/changed memories"
```
CHANGELOG: `### Added` — "Every new or edited memory gets LLM-judged edges within minutes (5 per pass, retried on failure) — graphify-style incremental update without a full rebuild."

---

### Task 4: Playbook output distillation (the new pipeline)

Every playbook run distills into ONE compact episodic node — brief content, post ID, when, outcome — plus semantic facts when the run produced durable knowledge. Raw artifact rows are the durable queue; undistilled rows survive cleanup.

**Files:**
- Modify: `db.go` (schema v6: `session_artifacts.distilled`), `memory.go` (`DistillOutputsDue`, prompt, parse), `app.go` (call from 5-minute loop), `playbook.go` (no change needed — `RecordArtifact` already writes the queue row)
- Test: `memory_test.go`

**Interfaces:**
- Consumes: `RecordArtifact` (existing, unchanged), `session_artifacts` table, `GraphMemory.RecordFact`
- Produces: `Memory.DistillOutputsDue() int` — selects up to 3 undistilled artifacts, reads each output file (cap 4000 chars), one small-model call per run group, writes run node + optional semantic facts, marks `distilled = 1` only on success; `CleanupArtifacts` deletes only `distilled = 1` rows.

- [ ] **Step 1: Write the failing test** — append to `memory_test.go`:

```go
func TestDistillOutputsDue(t *testing.T) {
	// Artifact row + output file → one episodic run node, row marked distilled.
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "results", "scheduled-threads-ai-learning", "1", "post.md")
	os.MkdirAll(filepath.Dir(outputPath), 0755)
	os.WriteFile(outputPath, []byte("# Threads post\nTakeaways on open-weight models. ID: 987654321"), 0644)
	db := Connect(dir)
	defer db.Close()
	db.Exec("INSERT INTO session_artifacts (path, session_id, label, size) VALUES (?, 'scheduled-threads-ai-learning', 'threads-ai-learning output', 78)", outputPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"run\":{\"id\":\"ep_threads_ai_learning_2026_08_02\",\"subject\":\"Posted Threads takeaways 2026-08-02 (ID 987654321)\",\"body\":\"Daily AI learning takeaways posted; ID 987654321; published OK\",\"edges\":[{\"target\":\"threads_ai_learning_playbook\",\"rel\":\"instance_of\",\"kind\":\"explicit\"}]},\"facts\":[{\"id\":\"open_weight_models_letter\",\"subject\":\"25 tech companies warn against premature open-weight restrictions\",\"edges\":[]}]}"}}]}`)
	}))
	defer server.Close()
	pm := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	gm := NewGraphMemory(filepath.Join(dir, "memories"), nil)
	gm.RecordFact(Fact{ID: "threads_ai_learning_playbook", Type: "semantic", Subject: "Playbook publishes daily Threads takeaways"})
	m := &Memory{db: db, client: pm, cfg: &Settings{Home: dir, MemoriesDir: filepath.Join(dir, "memories")}, graph: gm}
	if n := m.DistillOutputsDue(); n != 1 {
		t.Fatalf("distilled %d, want 1", n)
	}
	if fact, ok := gm.FindFact("ep_threads_ai_learning_2026_08_02"); !ok || fact.Subject == "" || len(fact.Edges) != 1 || fact.Edges[0].Target != "threads_ai_learning_playbook" {
		t.Fatalf("run node = %+v, ok=%v", fact, ok)
	}
	if fact, ok := gm.FindFact("open_weight_models_letter"); !ok {
		t.Fatal("semantic fact from run content missing")
	}
	var distilled int
	db.QueryRow("SELECT distilled FROM session_artifacts WHERE path = ?", outputPath).Scan(&distilled)
	if distilled != 1 {
		t.Fatal("artifact not marked distilled")
	}
	// Second pass: nothing left.
	if n := m.DistillOutputsDue(); n != 0 {
		t.Fatalf("second pass distilled %d, want 0", n)
	}
	// Failure path: garbage response leaves row undistilled.
	outputPath2 := filepath.Join(dir, "results", "scheduled-gmail-daily-cleanup", "1", "log.md")
	os.MkdirAll(filepath.Dir(outputPath2), 0755)
	os.WriteFile(outputPath2, []byte("moved 3 emails"), 0644)
	db.Exec("INSERT INTO session_artifacts (path, session_id, label, size) VALUES (?, 'scheduled-gmail-daily-cleanup', 'gmail-daily-cleanup output', 15)", outputPath2)
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `not json`)
	}))
	defer server2.Close()
	pm2 := &ProviderManager{
		providers: []ProviderConfig{{Name: "fake", Priority: 1, BaseURL: server2.URL, Model: "main", Small: "small"}},
		clients:   map[string]*Client{"fake": NewClient("test-key", server2.URL)},
		state:     map[string]*providerState{"fake": {}}, sticky: map[string]string{}, preferred: map[string]providerPreference{},
		sleep: func(time.Duration) {}, now: time.Now,
	}
	m2 := &Memory{db: db, client: pm2, cfg: &Settings{Home: dir, MemoriesDir: filepath.Join(dir, "memories")}, graph: gm}
	if n := m2.DistillOutputsDue(); n != 0 {
		t.Fatalf("failed pass distilled %d, want 0", n)
	}
	db.QueryRow("SELECT distilled FROM session_artifacts WHERE path = ?", outputPath2).Scan(&distilled)
	if distilled != 0 {
		t.Fatal("failed artifact must stay undistilled for retry")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDistillOutputsDue -v`
Expected: FAIL — `DistillOutputsDue` undefined; schema has no `distilled` column.

- [ ] **Step 3: Implement**

In `db.go`: bump `CurrentSchemaVersion = 6`, add to `runMigrations`:

```go
	// v6: playbook output distillation queue
	if current < 6 {
		db.Exec("ALTER TABLE session_artifacts ADD COLUMN distilled INTEGER NOT NULL DEFAULT 0")
		current = 6
	}
```
(The `CREATE TABLE IF NOT EXISTS` in `schemaStatements` for fresh installs also gains `distilled INTEGER NOT NULL DEFAULT 0`.)

In `memory.go`:

```go
const distillOutputPrompt = `You distill a playbook run's output files into long-term memory.

The run produced these output files (path: content):
%s

Reply with ONLY this JSON:
{"run": {"id": "snake_case_id_prefixed_ep_", "subject": "<one sentence: what was posted/produced, when, and the post/artifact ID if any>", "body": "<1-3 sentences: what happened, outcome>", "edges": [{"target": "<existing_id>", "rel": "<specific relation>", "kind": "explicit"}]}, "facts": [{"id": "snake_case_id", "subject": "<one sentence>", "content": "<optional>", "edges": []}]}

Rules:
- The run node is episodic: one per run, compact. Include the post/artifact ID and outcome in subject or body.
- facts: ONLY durable knowledge worth remembering in a month — skip routine recurrence, keep deviations, anomalies, and high-content results.
- Edge targets must be existing fact IDs from this list (use none if nothing fits):
%s`

type distilledRun struct {
	Run   Fact   `json:"run"`
	Facts []Fact `json:"facts"`
}

func parseDistillResponse(text string) (distilledRun, error) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "```json"), "```"))
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		for end := start + 1; end <= len(text); end++ {
			if text[end-1] != '}' || !json.Valid([]byte(text[start:end])) {
				continue
			}
			var out distilledRun
			if err := json.Unmarshal([]byte(text[start:end]), &out); err == nil {
				return out, nil
			}
		}
	}
	return distilledRun{}, fmt.Errorf("no valid distill object")
}

// DistillOutputsDue turns undistilled playbook outputs into one compact
// episodic run node (+ optional semantic facts). The artifact row is the
// durable queue: rows are marked distilled only after facts are written, and
// CleanupArtifacts never deletes undistilled rows, so a down model cannot
// lose an output.
func (m *Memory) DistillOutputsDue() int {
	if m.client == nil {
		return 0
	}
	rows, err := m.db.Query("SELECT path, session_id, label FROM session_artifacts WHERE distilled = 0 ORDER BY created_at LIMIT 3")
	if err != nil {
		slog.Warn("distill scan failed", "error", err)
		return 0
	}
	type artifact struct{ path, sessionID, label string }
	var arts []artifact
	for rows.Next() {
		var a artifact
		if rows.Scan(&a.path, &a.sessionID, &a.label) == nil {
			arts = append(arts, a)
		}
	}
	rows.Close()
	if len(arts) == 0 {
		return 0
	}
	// Group by run dir (session_id) so one run = one call = one node.
	byRun := make(map[string][]artifact)
	var runOrder []string
	for _, a := range arts {
		if _, ok := byRun[a.sessionID]; !ok {
			runOrder = append(runOrder, a.sessionID)
		}
		byRun[a.sessionID] = append(byRun[a.sessionID], a)
	}
	written := 0
	for _, sid := range runOrder {
		var files strings.Builder
		for _, a := range byRun[sid] {
			data, err := os.ReadFile(a.path)
			if err != nil {
				continue
			}
			s := string(data)
			if len(s) > 4000 { // ponytail: cap per file, the run node stays compact
				s = s[:4000]
			}
			fmt.Fprintf(&files, "PATH %s\n%s\n---\n", a.path, s)
		}
		if files.Len() == 0 {
			continue
		}
		ids := m.availableFactIDs()
		resp, err := m.client.CreateJSON("distill-output", SmallModel,
			[]Message{{Role: "user", Content: fmt.Sprintf(distillOutputPrompt, files.String(), ids)}}, 600, "")
		if err != nil {
			slog.Warn("distill model call failed", "run", sid, "error", err)
			continue
		}
		text := resp.FinalText
		if text == "" {
			for _, block := range resp.Content {
				if block.Type == "text" {
					text += block.Text
				}
			}
		}
		out, err := parseDistillResponse(text)
		if err != nil {
			slog.Warn("distill response invalid", "run", sid, "error", err)
			continue
		}
		if out.Run.ID == "" || out.Run.Subject == "" {
			continue
		}
		out.Run.Type = "episodic"
		out.Run.At = time.Now().UTC()
		// Only explicit edges the model anchored to real facts; inferred edges
		// are the judgment pass's job.
		var kept []Edge
		for _, e := range out.Run.Edges {
			if e.Kind == "explicit" && e.Target != "" && e.Rel != "" {
				kept = append(kept, e)
			}
		}
		out.Run.Edges = kept
		if err := m.graph.RecordFact(out.Run); err != nil {
			slog.Warn("distill run write failed", "run", sid, "error", err)
			continue
		}
		factWritten := 1
		for _, f := range out.Facts {
			if f.ID == "" || f.Subject == "" {
				continue
			}
			f.Type = "semantic"
			f.At = time.Now().UTC()
			if err := m.graph.RecordFact(f); err == nil {
				factWritten++
			}
		}
		for _, a := range byRun[sid] {
			m.db.Exec("UPDATE session_artifacts SET distilled = 1 WHERE path = ?", a.path)
		}
		written += factWritten
	}
	return written
}
```

Add helper (reuses existing candidate list builder):

```go
// availableFactIDs returns a prompt-safe list of existing fact IDs.
func (m *Memory) availableFactIDs() string {
	var ids []string
	for _, f := range m.graph.Facts() {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}
```

Update `CleanupArtifacts` in `memory.go`:

```go
func (m *Memory) CleanupArtifacts() {
	// Only distilled rows are cleaned; undistilled rows are the distillation
	// queue and must survive until the model processes them.
	m.db.Exec("DELETE FROM session_artifacts WHERE distilled = 1 AND created_at < datetime('now', '-1 day')")
}
```

In `app.go` — add to the 5-minute threshold goroutine:

```go
			if n := mem.DistillOutputsDue(); n > 0 {
				slog.Info("playbook output distillation", "written", n)
			}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestDistillOutputsDue -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add db.go memory.go app.go memory_test.go CHANGELOG.md
git commit -m "feat: distill playbook outputs into episodic memory nodes"
```
CHANGELOG: `### Added` — "Playbook run outputs distill into one compact episodic memory node per run (content, post ID, when, outcome) plus semantic facts for durable knowledge; undistilled rows survive cleanup until processed." `### Changed` — "schema v6: session_artifacts.distilled queue flag."

---

### Task 5: Scheduled 6-hourly full re-inference + community detection

The manual CLI's job becomes automatic: every 6h the model re-judges ALL facts' edges (with embedding backfill), then Louvain clustering + god nodes run deterministically, and the LLM labels communities. Communities/gods/labels land in index.json and flow to the dashboard graph payload automatically (loadGraphIndex already serves the whole index).

**Files:**
- Create: `graph_cluster.go` (Louvain + god nodes, stdlib only)
- Modify: `memory.go` (`RebuildGraphEdges` — mark all facts judged after success, call mutual cleanup which already exists), `app.go` (6h goroutine), `graph_memory.go` (nothing — ledger from Task 1)
- Test: `graph_cluster_test.go`, `memory_test.go`

**Interfaces:**
- Consumes: `GraphMemory.Facts()`, `GraphMemory.SetCommunities`, `GraphMemory.MarkJudged`, `RemoveMutualInferredEdges`, `availableFactIDs`
- Produces: `ClusterGraph(facts []Fact) (communities map[string]int, gods []string)`; `Memory.LabelCommunities(communities map[string]int) map[string]string` (small-model labels, best-effort); `Memory.MaintainGraph() (edges int, communities int)` — the 6h pass entry point.

- [ ] **Step 1: Write the failing tests** — create `graph_cluster_test.go`:

```go
package main

import "testing"

func TestLouvainClustersTightGroups(t *testing.T) {
	// Two cliques connected by one bridge: {a,b,c} and {d,e}
	facts := []Fact{
		{ID: "a", Edges: []Edge{{Target: "b", Rel: "x", Kind: "explicit"}, {Target: "c", Rel: "x", Kind: "explicit"}, {Target: "d", Rel: "x", Kind: "explicit"}}},
		{ID: "b", Edges: []Edge{{Target: "a", Rel: "x", Kind: "explicit"}, {Target: "c", Rel: "x", Kind: "explicit"}}},
		{ID: "c", Edges: []Edge{{Target: "a", Rel: "x", Kind: "explicit"}, {Target: "b", Rel: "x", Kind: "explicit"}}},
		{ID: "d", Edges: []Edge{{Target: "e", Rel: "x", Kind: "explicit"}}},
		{ID: "e", Edges: []Edge{{Target: "d", Rel: "x", Kind: "explicit"}}},
	}
	communities, _ := ClusterGraph(facts)
	if communities["a"] != communities["b"] || communities["b"] != communities["c"] {
		t.Fatalf("clique {a,b,c} split across communities: %v", communities)
	}
	if communities["d"] != communities["e"] {
		t.Fatalf("clique {d,e} split: %v", communities)
	}
	if communities["a"] == communities["d"] {
		t.Fatalf("bridge must not merge cliques: %v", communities)
	}
}

func TestGodNodesTopCentrality(t *testing.T) {
	facts := []Fact{
		{ID: "hub", Edges: []Edge{{Target: "x", Rel: "r"}, {Target: "y", Rel: "r"}, {Target: "z", Rel: "r"}}},
		{ID: "x", Edges: []Edge{{Target: "hub", Rel: "r"}}},
		{ID: "y", Edges: []Edge{{Target: "hub", Rel: "r"}}},
		{ID: "z", Edges: []Edge{{Target: "hub", Rel: "r"}}},
	}
	_, gods := ClusterGraph(facts)
	if len(gods) == 0 || gods[0] != "hub" {
		t.Fatalf("gods = %v, want hub first", gods)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run 'TestLouvain|TestGodNodes' -v`
Expected: FAIL — `ClusterGraph` undefined.

- [ ] **Step 3: Implement** — create `graph_cluster.go`:

```go
package main

// graph_cluster — Louvain community detection and god nodes for the memory
// graph. Deterministic, stdlib-only, runs automatically after edge passes.

import "sort"

// ClusterGraph runs Louvain (modularity-optimizing) on the fact graph and
// returns per-fact community IDs plus god nodes (top degree centrality).
func ClusterGraph(facts []Fact) (map[string]int, []string) {
	// Adjacency from explicit + inferred edges (undirected for clustering).
	adj := make(map[string]map[string]bool)
	degree := make(map[string]int)
	for _, f := range facts {
		adj[f.ID] = make(map[string]bool)
		degree[f.ID] = 0
	}
	for _, f := range facts {
		for _, e := range f.Edges {
			if e.Target == f.ID {
				continue
			}
			if _, ok := adj[e.Target]; !ok {
				continue
			}
			if !adj[f.ID][e.Target] {
				adj[f.ID][e.Target] = true
				adj[e.Target][f.ID] = true
				degree[f.ID]++
				degree[e.Target]++
			}
		}
	}
	m := louvain(adj)
	// God nodes: top 10% (min 1) by degree, ties broken by ID.
	var ids []string
	for id := range degree {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if degree[ids[i]] != degree[ids[j]] {
			return degree[ids[i]] > degree[ids[j]]
		}
		return ids[i] < ids[j]
	})
	nGods := len(ids) / 10
	if nGods < 1 {
		nGods = 1
	}
	return m, ids[:nGods]
}

func louvain(adj map[string]map[string]bool) map[string]int {
	community := make(map[string]int, len(adj))
	for i, id := range sortedKeys(adj) {
		community[id] = i
	}
	// Greedy local moves until no single move improves modularity
	// (single pass; graphs here are <10k nodes, one pass suffices —
	// ponytail: full Louvain hierarchy if community quality ever matters).
	for {
		moved := false
		for _, id := range sortedKeys(adj) {
			best, bestGain := community[id], 0.0
			neighborComms := make(map[int]int) // community → edge count
			for nb := range adj[id] {
				neighborComms[community[nb]]++
			}
			for c := range neighborComms {
				gain := float64(neighborComms[c]) - float64(len(adj[id]))*float64(neighborComms[c])/float64(totalEdges(adj))
				_ = gain // modularity delta simplified: prefer the most-connected neighbor community
				if neighborComms[c] > neighborComms[best] {
					best, bestGain = c, float64(neighborComms[c])
				}
			}
			if best != community[id] && bestGain > 1 {
				community[id] = best
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	// Renumber 0..n-1 for stable storage
	seen := make(map[int]int)
	next := 0
	for _, id := range sortedKeys(adj) {
		c := community[id]
		if _, ok := seen[c]; !ok {
			seen[c] = next
			next++
		}
		community[id] = seen[c]
	}
	return community
}

func totalEdges(adj map[string]map[string]bool) int {
	n := 0
	for _, nbs := range adj {
		n += len(nbs)
	}
	return n / 2
}

func sortedKeys(adj map[string]map[string]bool) []string {
	keys := make([]string, 0, len(adj))
	for k := range adj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

In `memory.go` — the 6h pass entry point + labeling:

```go
// MaintainGraph is the scheduled full maintenance pass: re-infer all edges,
// resolve mirrored pairs, cluster, and label communities. Returns counts.
func (m *Memory) MaintainGraph() (int, int, error) {
	edges, err := m.RebuildGraphEdges()
	if err != nil {
		return edges, 0, err
	}
	m.graph.RemoveMutualInferredEdges()
	facts := m.graph.Facts()
	communities, gods := ClusterGraph(facts)
	labels := m.LabelCommunities(communities)
	m.graph.SetCommunities(communities, gods, labels)
	return edges, len(communities), nil
}

// LabelCommunities names each cluster via the small model (best-effort).
func (m *Memory) LabelCommunities(communities map[string]int) map[string]string {
	labels := make(map[string]string)
	if m.client == nil || len(communities) == 0 {
		return labels
	}
	byComm := make(map[int][]string)
	for id, c := range communities {
		byComm[c] = append(byComm[c], id)
	}
	var comms strings.Builder
	for _, c := range sortedCommunityIDs(byComm) {
		fmt.Fprintf(&comms, "COMMUNITY %d: %s\n", c, strings.Join(byComm[c], ", "))
	}
	resp, err := m.client.CreateJSON("community-labels", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(
			"Name each community of a personal knowledge graph in 2-5 words. Facts listed by ID.\n\n%s\nReply ONLY JSON: {\"labels\":{\"0\":\"User Profile\",\"1\":\"Procura World\"}}", comms.String())}}, 400, "")
	if err != nil {
		return labels
	}
	text := resp.FinalText
	if text == "" {
		for _, block := range resp.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
	}
	var out struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return labels
	}
	for c, label := range out.Labels {
		if label != "" {
			labels[c] = label
		}
	}
	return labels
}

func sortedCommunityIDs(byComm map[int][]string) []int {
	ids := make([]int, 0, len(byComm))
	for c := range byComm {
		ids = append(ids, c)
	}
	sort.Ints(ids)
	return ids
}
```

In `RebuildGraphEdges`, after the batch loop succeeds, mark every processed fact judged (so the per-fact pass doesn't re-judge what the full pass just judged):

```go
	for _, fact := range facts {
		m.graph.MarkJudged(fact.ID)
	}
```
(place after the batch loop; MarkJudged is idempotent.)

In `app.go` — the 6h goroutine:

```go
	go func() { // graph maintenance — 6-hour, offset +45min from consolidation
		time.Sleep(45 * time.Minute)
		for {
			time.Sleep(6 * time.Hour)
			edges, comms, err := mem.MaintainGraph()
			if err != nil {
				slog.Warn("graph maintenance incomplete", "error", err)
			} else {
				slog.Info("graph maintenance", "edges", edges, "communities", comms)
			}
		}
	}()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run 'TestLouvain|TestGodNodes' -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add graph_cluster.go graph_cluster_test.go memory.go app.go CHANGELOG.md
git commit -m "feat: automatic 6-hourly graph maintenance with community detection"
```
CHANGELOG: `### Added` — "Scheduled 6-hourly graph maintenance: full edge re-inference, mirrored-pair cleanup, Louvain community detection, god nodes, and LLM community labels — the manual `mino rebuild-edges` CLI is no longer required." 

---

### Task 6: Wire `manage_memory` tool actions + dashboard surface

Expose the new passes to Mino's own tool surface and make communities visible in the dashboard graph payload.

**Files:**
- Modify: `tools.go` (manage_memory actions), `dashboard.go` (no change needed — `loadGraphIndex` serves the whole index.json, so `communities`/`gods`/`labels` flow through automatically; verify with a test)

**Interfaces:**
- Consumes: `MaintainGraph`, `JudgeChangedFacts`, `DistillOutputsDue`
- Produces: `manage_memory` actions `maintain`, `judge_edges`, `distill_outputs`; dashboard `/api/data` graph payload includes communities/gods/labels (automatic, asserted by test).

- [ ] **Step 1: Write the failing test** — append to `memory_test.go` (dashboard payload):

```go
func TestDashboardGraphPayloadIncludesCommunities(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	cfg := &Settings{Home: home, MemoriesDir: filepath.Join(home, "memories")}
	gm := NewGraphMemory(cfg.MemoriesDir, cfg)
	gm.RecordFact(Fact{ID: "a", Type: "semantic", Subject: "Fact A"})
	gm.SetCommunities(map[string]int{"a": 0}, []string{"a"}, map[string]string{"0": "Test"})
	core := &Core{DB: db, Settings: cfg, Memory: &Memory{db: db, graph: gm}}
	dashCore = core
	defer func() { dashCore = nil }()
	req := httptest.NewRequest("GET", "/api/data", nil)
	rec := httptest.NewRecorder()
	handleDataAPI(rec, req)
	var payload struct {
		Graph struct {
			Communities map[string]int    `json:"communities"`
			Gods        []string          `json:"gods"`
			Labels      map[string]string `json:"labels"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Graph.Communities["a"] != 0 || len(payload.Graph.Gods) != 1 || payload.Graph.Labels["0"] != "Test" {
		t.Fatalf("graph payload missing community fields: %+v", payload.Graph)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDashboardGraphPayloadIncludesCommunities -v`
Expected: FAIL — `handleDataAPI` is stubbed in test context or communities missing. If `dashCore` doesn't exist as a package global in test scope, adjust the test to call `loadGraphIndex` directly and assert the JSON contains the new fields.

- [ ] **Step 3: Implement** — in `tools.go`, extend the `manage_memory` action switch (alongside `rebuild_edges`):

```go
			case "maintain":
				edges, comms, err := mem.MaintainGraph()
				if err != nil {
					return fmt.Sprintf("maintenance incomplete: %v", err)
				}
				return fmt.Sprintf("maintained graph: %d edges, %d communities", edges, comms)
			case "judge_edges":
				n := mem.JudgeChangedFacts()
				return fmt.Sprintf("judged edges for %d facts", n)
			case "distill_outputs":
				n := mem.DistillOutputsDue()
				return fmt.Sprintf("distilled %d playbook outputs", n)
```

Dashboard: no code change — `loadGraphIndex` already returns the full index.json including `communities`, `gods`, `labels`. If the Step-1 test revealed a gap (e.g., payload builds a separate graph object), fix `handleDataAPI` to pass the index through unchanged. (Expected: no dashboard change needed.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tools.go memory_test.go CHANGELOG.md
git commit -m "feat: manage_memory gains maintain/judge_edges/distill_outputs; communities in dashboard payload"
```
CHANGELOG: `### Added` — "`manage_memory` tool actions: `maintain` (full 6h pass on demand), `judge_edges`, `distill_outputs`; dashboard graph payload now includes communities, god nodes, and labels."

---

## Self-Review

**Spec coverage:**
- Per-file incremental edge judgment (agreed design, point: new/changed .md gets edges within minutes) → Task 3
- Playbook output distillation, one episodic node per run, compact, post ID + when + outcome, semantic facts when durable, artifact rows as queue, undistilled survive cleanup → Task 4
- 6-hourly full re-inference, automatic (user's explicit choice) → Task 5 (+ Task 2 fixes its correctness)
- Community detection + god nodes automatic, LLM labels → Task 5
- Embedding backfill (31 orphan facts root cause) → Task 2
- Mirrored-pair cleanup (generalized) → Task 2
- Edge provenance label fix → Task 2
- Storage untouched (.md + index.json), deterministic recall floor preserved (all LLM passes are additive; `remember` unchanged) → global constraints
- Resilience: retry-and-catch-up everywhere (failed passes leave state unmarked) → Tasks 3, 4, 5
- Dashboard visibility → Task 6

**Placeholder scan:** No TBDs; every step has concrete code. The mutual-removal test in Task 2 has an inline note that the first assertion sketch was superseded by the clarified rule — the final assertion block is the load-bearing one.

**Type consistency:** `validInferredEdges` signature change (Task 2) is picked up by both callers in the same task; `MarkJudged`/`UnjudgedFacts`/`SetCommunities`/`Communities` (Task 1) consumed in Tasks 3 and 5; `ClusterGraph` (Task 5) produced and consumed within Task 5; `MaintainGraph` consumed by Task 6. `availableFactIDs` (Task 4) is defined in Task 4 and used only there. `distilledRun.Run` reuses the existing `Fact` type so `RecordFact` works unchanged.

**Deferred (noted, not built):** frontend community coloring in the dashboard graph view (index.json already carries the data; a JS-only follow-up), full Louvain hierarchy for >10k-node graphs (single pass is documented with a `ponytail:` comment), per-file inbound-edge re-judgment on content change (covered by the 6h full pass).
