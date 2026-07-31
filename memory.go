package main

// Mino — memory/ — Core's exact memory system.
// Three pillars: semantic (FTS5), episodic, procedural (skills).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Memory struct {
	db            *sql.DB
	skills        *SkillLoader
	client        *ProviderManager
	cfg           *Settings
	embedder      *EmbeddingStore
	graph         *GraphMemory
	consolidateMu sync.Mutex
}

func NewMemory(db *sql.DB, client *ProviderManager, cfg *Settings) *Memory {
	m := &Memory{db: db, client: client, cfg: cfg}
	m.graph = NewGraphMemory(cfg.MemoriesDir, cfg)
	m.MigrateMissingFacts()
	return m
}

// --- Chat log (Core: log_chat) ---

func (m *Memory) LogChat(role, content, sessionID, source string) {
	m.db.Exec(
		"INSERT INTO chat_log (role, content, session_id, source) VALUES (?,?,?,?)",
		role, content, sessionID, source,
	)
}

type SessionArtifact struct {
	Label string
	Path  string
	Size  int
}

func (m *Memory) RecordArtifact(sessionID, label, path string, size int) {
	if path == "" || size <= 0 {
		return
	}
	m.db.Exec("INSERT OR REPLACE INTO session_artifacts (path, session_id, label, size) VALUES (?,?,?,?)", path, sessionID, label, size)
}

func (m *Memory) SessionArtifacts(sessionID string, maxChars int) string {
	rows, err := m.db.Query("SELECT label, path, size FROM session_artifacts WHERE session_id = ? AND created_at >= datetime('now', '-1 day') ORDER BY created_at DESC", sessionID)
	if err != nil {
		return ""
	}
	var out strings.Builder
	var stale []string
	out.WriteString("Live session artifacts (use read_file(path, offset, limit) when needed):\n")
	for rows.Next() {
		var artifact SessionArtifact
		rows.Scan(&artifact.Label, &artifact.Path, &artifact.Size)
		if _, err := os.Stat(artifact.Path); err != nil {
			stale = append(stale, artifact.Path)
			continue
		}
		line := fmt.Sprintf("- %s: %d chars at %s\n", artifact.Label, artifact.Size, artifact.Path)
		if out.Len()+len(line) > maxChars {
			break
		}
		out.WriteString(line)
	}
	rows.Close()
	for _, path := range stale {
		m.db.Exec("DELETE FROM session_artifacts WHERE path = ?", path)
	}
	if out.Len() == len("Live session artifacts (use read_file(path, offset, limit) when needed):\n") {
		return ""
	}
	return out.String()
}

func (m *Memory) CleanupArtifacts() {
	m.db.Exec("DELETE FROM session_artifacts WHERE created_at < datetime('now', '-1 day')")
}

// --- Semantic search (graph-backed) ---

func (m *Memory) Search(query string) string {
	return m.graph.Remember(query)
}

// --- Episodic search (deprecated — use remember for graph-aware retrieval) ---

// SearchEpisodes is kept for dashboard/migration use only.
func (m *Memory) SearchEpisodes(query string) string {
	rows, err := m.db.Query(
		"SELECT happened_at, summary FROM episodes_fts WHERE episodes_fts MATCH ? ORDER BY rank LIMIT 3",
		query,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var happenedAt, summary string
		rows.Scan(&happenedAt, &summary)
		out.WriteString(fmt.Sprintf("- **%s**: %s\n", happenedAt, summary))
	}
	return out.String()
}

// --- Skills (Core: procedural memory) ---

func (m *Memory) MatchingSkills(message string) string {
	if m.skills == nil {
		return ""
	}
	matched := m.skills.Match(message)
	return m.skills.Bodies(matched)
}

// --- Session history (Core: session_history) ---

func (m *Memory) SessionHistory(sessionID string) [][2]string {
	rows, err := m.db.Query(
		"SELECT role, content FROM chat_log WHERE session_id = ? ORDER BY id",
		sessionID,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var pairs [][2]string
	var pending string
	for rows.Next() {
		var role, content string
		rows.Scan(&role, &content)
		if role == "user" {
			pending = content
		} else if role == "assistant" && pending != "" {
			pairs = append(pairs, [2]string{pending, content})
			pending = ""
		}
	}
	return pairs
}

// --- MEMORY.md export (Core: export_markdown) ---

// --- Consolidation (Core: consolidation.py, rescheduled) ---
// Core ran this synchronously after every turn; single-session, so it could
// not race. Mino has concurrent gateways sharing one chat_log, so instead of
// leases we run this from one background loop (app.go) — structurally
// race-free, same 50-line body as Core.

const summarizerPrompt = `You distill a personal assistant's recent conversation into long-term memory.

From the exchanges below, extract:
1. durable facts about the user, their people, projects, or preferences —
   only things worth remembering in a month; skip chit-chat and one-offs.
   For each fact, include only high-confidence edges to candidate facts listed below.
2. one single-sentence episode summarizing what happened in this conversation.

Available fact IDs (use as edge targets when relevant):
%s

Reply with ONLY this JSON:
{"facts": [{"id": "snake_case_id", "subject": "<one sentence>", "content": "<optional body>", "edges": [{"target": "<existing_id>", "rel": "<specific relation>", "kind": "inferred", "confidence": 0.0}]}], "episode": "<one sentence>"}

Only emit an edge when confidence is at least 0.85. Never emit generic related_to.

Edge relations (pick the MOST SPECIFIC one that fits):
- attributed_to: property or attribute belonging to target
  ex: "User's Gmail account" attributed_to "User profile"
- prefers: user preference about the target
  ex: "User prefers dark theme" prefers "UI settings"
- maintains: user is responsible for the target project/system
  ex: "User" maintains "Project repo"
- depends_on: this requires the target to function
  ex: "Deploy script" depends_on "Production server"
- supersedes: this replaces or obsoletes the target
  ex: "New CRM system" supersedes "Old spreadsheet tracker"
- located_at: physical or logical location
  ex: "Project database" located_at "Cloud server"
- requires: needs the target as a prerequisite
  ex: "API integration" requires "API access key"
- deployed_on: software running on the target infrastructure
  ex: "Web app" deployed_on "VPS instance"
- scheduled_at: task scheduled at a specific time or cadence
  ex: "Weekly report" scheduled_at "Monday 9am"
- calls: invokes or uses the target as a function/service
  ex: "Backup script" calls "rsync utility"
- used_in: this is used within the target context
  ex: "SQLite library" used_in "Database module"
- related_to: generic relationship — only when nothing above fits

Exchanges:
%s`

// ConsolidateDue distills every session with at least ConsolidateEvery
// unconsolidated exchanges into facts + one episode. Any failure leaves the
// rows unconsolidated for the next pass — the raw log is never lost.
func (m *Memory) ConsolidateDue() int {
	if m.client == nil || m.cfg.ConsolidateEvery <= 0 {
		return 0
	}
	m.consolidateMu.Lock()
	defer m.consolidateMu.Unlock()
	rows, err := m.db.Query("SELECT session_id FROM chat_log WHERE consolidated = 0 GROUP BY session_id HAVING COUNT(*) >= ?",
		m.cfg.ConsolidateEvery*2) // each exchange = user + assistant row
	if err != nil {
		slog.Error("consolidation scan failed", "error", err)
		return 0
	}
	var sessions []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			sessions = append(sessions, id)
		}
	}
	rows.Close()
	written := 0
	for i, sid := range sessions {
		if m.cfg.ConsolidateLimit > 0 && i >= m.cfg.ConsolidateLimit {
			break
		}
		written += m.consolidateSession(sid)
	}
	return written
}

// ConsolidateIfFull triggers consolidation when unconsolidated chars exceed 80%
// of the context budget for any session. Use between the 6-hour full passes.
func (m *Memory) ConsolidateIfFull(contextChars int) int {
	if m.client == nil || contextChars <= 0 {
		return 0
	}
	threshold := contextChars * 80 / 100
	m.consolidateMu.Lock()
	defer m.consolidateMu.Unlock()
	rows, err := m.db.Query(`SELECT session_id, SUM(length(content)) AS chars
		FROM chat_log WHERE consolidated = 0
		GROUP BY session_id HAVING chars >= ?`, threshold)
	if err != nil {
		slog.Error("consolidation threshold scan failed", "error", err)
		return 0
	}
	var sessions []string
	for rows.Next() {
		var id string
		var chars int
		if rows.Scan(&id, &chars) == nil {
			sessions = append(sessions, id)
		}
	}
	rows.Close()
	written := 0
	for i, sid := range sessions {
		if m.cfg.ConsolidateLimit > 0 && i >= m.cfg.ConsolidateLimit {
			break
		}
		written += m.consolidateSession(sid)
	}
	return written
}

func (m *Memory) consolidateSession(sid string) int {
	// ponytail: LIMIT 200 bounds the prompt; a longer backlog drains over later passes
	rows, err := m.db.Query("SELECT id, role, content FROM chat_log WHERE consolidated = 0 AND session_id = ? ORDER BY id LIMIT 200", sid)
	if err != nil {
		return 0
	}
	var ids []string
	var log strings.Builder
	for rows.Next() {
		var id int
		var role, content string
		if rows.Scan(&id, &role, &content) == nil {
			ids = append(ids, fmt.Sprint(id))
			fmt.Fprintf(&log, "%s: %s\n", role, content)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return 0
	}

	// Embeddings select a bounded candidate set; they never create edges.
	availableIDs := m.graphCandidates(log.String())

	// ponytail: cap the prompt — DeepSeek v4 flash enters an endless reasoning
	// spiral (content:null, finish:length at any token budget) on very large
	// consolidation prompts. Keep the recent tail; older exchanges drain over
	// later passes. Rows are only marked consolidated when facts are written.
	if log.Len() > 100000 {
		s := log.String()
		log.Reset()
		log.WriteString(s[len(s)-100000:])
	}

	resp, err := m.client.CreateJSON("consolidation", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(summarizerPrompt, availableIDs.prompt, log.String())}}, 600, "")
	if err != nil {
		slog.Warn("consolidation model call failed", "session", sid, "error", err)
		return 0
	}
	text := resp.FinalText
	if text == "" { // MiMo sometimes answers via reasoning only
		for _, b := range resp.Content {
			if b.Type == "text" {
				text += b.Text
			}
		}
	}
	distilled, err := parseConsolidationResponse(text)
	if err != nil {
		slog.Warn("consolidation response JSON invalid", "session", sid, "error", err)
		return 0
	}
	// reasoning models sometimes echo the prompt's JSON template verbatim
	placeholder := func(s string) bool { return s == "" || strings.Contains(s, "<") }
	written := 0
	for _, f := range distilled.Facts {
		if placeholder(f.ID) || placeholder(f.Subject) {
			continue
		}
		fact := Fact{
			ID:      f.ID,
			Type:    "semantic",
			Subject: f.Subject,
			At:      time.Now(),
			Body:    f.Content,
		}
		fact.Edges = m.validInferredEdges(f.Edges, availableIDs.ids)
		if existing, ok := m.graph.FindFact(f.ID); ok {
			fact.At = existing.At
			fact.Why = existing.Why
			fact.Source = "consolidation"
			for _, edge := range existing.Edges {
				if edge.Kind == "explicit" {
					fact.Edges = append(fact.Edges, edge)
				}
			}
		}
		if err := m.graph.ReplaceFact(fact); err != nil {
			continue
		}
		written++
		if m.embedder != nil {
			m.embedder.IndexFact(f.ID, fact)
		}
	}
	episodeWritten := false
	if !placeholder(distilled.Episode) {
		epID := fmt.Sprintf("ep_%s", strings.ToLower(strings.ReplaceAll(
			strings.ReplaceAll(distilled.Episode[:min(40, len(distilled.Episode))], " ", "_"), ",", "")))
		epFact := Fact{
			ID:      epID,
			Type:    "episodic",
			Subject: distilled.Episode,
			At:      time.Now(),
			Body:    fmt.Sprintf("Session: %s", sid),
		}
		if err := m.graph.RecordFact(epFact); err == nil {
			episodeWritten = true
			if m.embedder != nil {
				m.embedder.Index("episode", distilled.Episode)
			}
		}
	}
	if written == 0 && !episodeWritten {
		// Successful parse but nothing usable: the small model echoed the
		// template or all facts were placeholders. Log it — a silent return
		// here looks like a healthy pass while rows stay unconsolidated.
		slog.Warn("consolidation produced no facts or episode", "session", sid)
		return 0
	}
	m.db.Exec("UPDATE chat_log SET consolidated = 1 WHERE id IN (" + strings.Join(ids, ",") + ")")
	return written
}

type graphCandidateSet struct {
	ids    map[string]bool
	prompt string
}

type distilledMemory struct {
	Facts []struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
		Content string `json:"content"`
		Edges   []Edge `json:"edges"`
	} `json:"facts"`
	Episode string `json:"episode"`
}

func parseConsolidationResponse(text string) (distilledMemory, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "```json"))
	text = strings.TrimSpace(strings.TrimSuffix(text, "```"))
	if text == "" {
		return distilledMemory{}, fmt.Errorf("empty response")
	}
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		for end := start + 1; end <= len(text); end++ {
			if text[end-1] != '}' || !json.Valid([]byte(text[start:end])) {
				continue
			}
			var distilled distilledMemory
			if err := json.Unmarshal([]byte(text[start:end]), &distilled); err == nil &&
				(len(distilled.Facts) > 0 || strings.TrimSpace(distilled.Episode) != "") {
				return distilled, nil
			}
		}
	}
	return distilledMemory{}, fmt.Errorf("no valid consolidation object")
}

func (m *Memory) graphCandidates(text string) graphCandidateSet {
	set := graphCandidateSet{ids: make(map[string]bool)}
	if m.embedder == nil {
		return set
	}
	for _, hit := range m.embedder.SearchScored(text, 8) {
		if !strings.HasPrefix(hit.doc.Source, "fact:") {
			continue
		}
		id := strings.TrimPrefix(hit.doc.Source, "fact:")
		set.ids[id] = true
		set.prompt += fmt.Sprintf("- %s (%s)\n", id, hit.doc.Content)
	}
	return set
}

func (m *Memory) validInferredEdges(edges []Edge, candidates map[string]bool) []Edge {
	valid := make([]Edge, 0, len(edges))
	seen := make(map[string]bool)
	contradictory := make(map[string]bool)
	relations := make(map[string]string)
	for _, edge := range edges {
		if edge.Target == "" || edge.Rel == "" || edge.Rel == "related_to" || edge.Confidence < 0.85 || !candidates[edge.Target] {
			continue
		}
		if edge.Kind != "" && edge.Kind != "inferred" {
			continue
		}
		if previous, ok := relations[edge.Target]; ok && previous != edge.Rel {
			contradictory[edge.Target] = true
		}
		relations[edge.Target] = edge.Rel
	}
	for _, edge := range edges {
		if edge.Target == "" || edge.Rel == "" || edge.Rel == "related_to" || edge.Confidence < 0.85 || !candidates[edge.Target] || (edge.Kind != "" && edge.Kind != "inferred") || contradictory[edge.Target] {
			continue
		}
		key := edge.Target + "\x00" + edge.Rel
		if seen[key] {
			continue
		}
		seen[key] = true
		edge.Kind = "inferred"
		edge.Source = "consolidation"
		valid = append(valid, edge)
	}
	return valid
}

const graphRebuildPrompt = `You are rebuilding relationships in a personal knowledge graph.
Return only JSON: {"edges":[{"source":"claim_id","target":"candidate_id","rel":"specific_relation","confidence":0.0}]}.
For every candidate pair, actively check whether the claim bodies establish a direct relationship. Emit an edge when the relationship is clear and useful; do not require the exact relationship words to appear verbatim.
Use a specific relation such as depends_on, maintains, prefers, attributed_to, located_at, requires, supersedes, deployed_on, scheduled_at, calls, or used_in. Never use related_to. Confidence must be at least 0.85. Do not invent IDs or relationships that are merely topical.

Claims and bounded candidates:
%s`

type graphRebuildEdge struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Rel        string  `json:"rel"`
	Confidence float64 `json:"confidence"`
}

func parseGraphRebuildResponse(text string) ([]graphRebuildEdge, error) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "```json"), "```"))
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		for end := start + 1; end <= len(text); end++ {
			if text[end-1] != '}' || !json.Valid([]byte(text[start:end])) {
				continue
			}
			var output struct {
				Edges []graphRebuildEdge `json:"edges"`
			}
			if err := json.Unmarshal([]byte(text[start:end]), &output); err == nil {
				return output.Edges, nil
			}
		}
	}
	return nil, fmt.Errorf("no valid graph rebuild object")
}

func (m *Memory) RebuildGraphEdges() (int, error) {
	if m.client == nil || m.embedder == nil {
		return 0, fmt.Errorf("graph rebuild requires provider and embedding store")
	}
	facts := m.graph.Facts()
	rekeyed := m.embedder.RekeyFacts(facts)
	candidates := m.embedder.GraphCandidates(facts, 6)
	byID := make(map[string]Fact, len(facts))
	for _, fact := range facts {
		byID[fact.ID] = fact
	}
	var ids []string
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	edgesWritten := 0
	failed := 0
	for start := 0; start < len(ids); start += 16 {
		end := min(start+16, len(ids))
		var claims strings.Builder
		allowed := make(map[string]map[string]bool)
		for _, sourceID := range ids[start:end] {
			fmt.Fprintf(&claims, "SOURCE %s: %s | %s\n", sourceID, byID[sourceID].Subject, byID[sourceID].Body)
			allowed[sourceID] = make(map[string]bool)
			for _, candidate := range candidates[sourceID] {
				allowed[sourceID][candidate.ID] = true
				fmt.Fprintf(&claims, "  CANDIDATE %s (%0.2f): %s | %s\n", candidate.ID, candidate.Score, byID[candidate.ID].Subject, byID[candidate.ID].Body)
			}
		}
		resp, err := m.client.CreateJSON("graph-rebuild", SmallModel, []Message{{Role: "user", Content: fmt.Sprintf(graphRebuildPrompt, claims.String())}}, 1400, "")
		if err != nil {
			failed++
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
		edges, err := parseGraphRebuildResponse(text)
		if err != nil || len(edges) == 0 {
			failed++
			continue
		}
		inferred := make(map[string][]Edge)
		for _, edge := range edges {
			if !allowed[edge.Source][edge.Target] {
				continue
			}
			valid := m.validInferredEdges([]Edge{{Target: edge.Target, Rel: edge.Rel, Confidence: edge.Confidence}}, allowed[edge.Source])
			if len(valid) > 0 {
				inferred[edge.Source] = append(inferred[edge.Source], valid...)
			}
		}
		for _, sourceID := range ids[start:end] {
			fact := byID[sourceID]
			fact.Edges = nil
			for _, existing := range facts {
				if existing.ID == sourceID {
					for _, edge := range existing.Edges {
						if edge.Kind == "explicit" {
							fact.Edges = append(fact.Edges, edge)
						}
					}
					break
				}
			}
			fact.Edges = append(fact.Edges, inferred[sourceID]...)
			fact.Source = "graph-rebuild"
			if err := m.graph.ReplaceFact(fact); err == nil {
				edgesWritten += len(inferred[sourceID])
			}
		}
	}
	if rekeyed > 0 {
		slog.Info("graph embeddings rekeyed", "facts", rekeyed)
	}
	removed := m.graph.RemoveMutualInferredEdges()
	if removed > 0 {
		slog.Warn("graph rebuild removed contradictory reciprocal edges", "edges", removed)
	}
	if failed > 0 {
		return edgesWritten, fmt.Errorf("graph rebuild had %d failed batches", failed)
	}
	return edgesWritten, nil
}

// --- Dedup (background, every 6h, offset from consolidation) ---

const dedupPrompt = `Merge these duplicate facts from a knowledge graph.
They say the same thing with slightly different wording.
Produce ONE clean fact preserving all unique information.
Keep the best existing ID.

Duplicate facts:
%s

Reply with ONLY this JSON:
{"id": "keep_one_existing_id", "subject": "<merged one-sentence subject>", "content": "<merged body, 1-3 sentences>"}`

// DedupDue clusters near-duplicate facts by embedding similarity and
// merges each cluster into one clean fact via the small model.
func (m *Memory) DedupDue() int {
	if m.embedder == nil || m.client == nil {
		return 0
	}

	// Get existing fact embeddings. Claims without vectors join a later pass
	// after their normal write path indexes them; dedup never creates an API
	// request burst just to start a maintenance run.
	docs := m.embedder.DocsBySource("fact")
	if len(docs) < 2 {
		return 0
	}

	// Cluster by cosine similarity > 0.85
	clusters := clusterDocs(docs, 0.85)

	merged := 0
	limit := 5 // ponytail: cap per run, backlog drains over later passes
	for _, cluster := range clusters {
		if len(cluster) < 2 || merged >= limit {
			continue
		}
		if m.mergeCluster(cluster) {
			merged++
		}
	}
	return merged
}

// mergeCluster sends duplicate facts to the small model for merging,
// then replaces them with the merged result.
// filterMergedEdges keeps only edges whose target still exists and is not the
// merged fact itself — the merge deletes sibling facts, so edges pointing at
// them (or at the survivor) would dangle or self-loop.
func filterMergedEdges(edges []Edge, existing map[string]*Fact, keepID string) []Edge {
	out := edges[:0]
	for _, e := range edges {
		if e.Target == keepID {
			continue
		}
		if _, ok := existing[e.Target]; !ok {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (m *Memory) mergeCluster(docs []embeddedDoc) bool {
	m.graph.mu.Lock()
	// Map content → fact ID
	contentToID := make(map[string]string)
	var facts []*Fact
	seenFacts := make(map[string]bool)
	for _, d := range docs {
		if strings.HasPrefix(d.Source, "fact:") {
			id := strings.TrimPrefix(d.Source, "fact:")
			if fact, ok := m.graph.facts[id]; ok && !seenFacts[id] {
				seenFacts[id] = true
				facts = append(facts, fact)
				continue
			}
		}
		// Find the fact by matching content against subject+body
		for id, f := range m.graph.facts {
			candidate := f.Subject + ": " + f.Body
			if candidate == d.Content && !seenFacts[id] {
				contentToID[d.Content] = id
				seenFacts[id] = true
				facts = append(facts, f)
				break
			}
		}
	}
	m.graph.mu.Unlock()

	if len(facts) < 2 {
		return false
	}

	// Build prompt
	var sb strings.Builder
	for _, f := range facts {
		sb.WriteString(fmt.Sprintf("ID: %s\nSubject: %s\nBody: %s\n\n", f.ID, f.Subject, f.Body))
	}

	resp, err := m.client.Create("dedup", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(dedupPrompt, sb.String())}}, 600, "", nil)
	if err != nil {
		return false
	}
	text := resp.FinalText
	if text == "" {
		for _, b := range resp.Content {
			if b.Type == "text" {
				text += b.Text
			}
		}
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return false
	}
	var merged struct {
		ID      string `json:"id"`
		Subject string `json:"subject"`
		Content string `json:"content"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &merged) != nil {
		return false
	}
	if merged.ID == "" || merged.Subject == "" {
		return false
	}
	if _, ok := m.graph.facts[merged.ID]; !ok {
		return false
	}

	// Collect edges from all merged facts
	m.graph.mu.Lock()
	var allEdges []Edge
	seenEdges := make(map[string]bool)
	for _, f := range facts {
		for _, e := range f.Edges {
			key := e.Target + e.Rel
			if !seenEdges[key] {
				allEdges = append(allEdges, e)
				seenEdges[key] = true
			}
		}
	}

	// Delete old .md files (except the one we're keeping)
	for _, f := range facts {
		if f.ID == merged.ID {
			continue
		}
		os.Remove(filepath.Join(m.graph.dir, f.ID+".md"))
		delete(m.graph.facts, f.ID)
		delete(m.graph.files, f.ID+".md")
		m.embedder.RemoveFact(f.ID)
		m.embedder.Remove("fact", f.Subject+": "+f.Body)
	}
	allEdges = filterMergedEdges(allEdges, m.graph.facts, merged.ID)

	// Write merged fact
	mergedFact := Fact{
		ID:      merged.ID,
		Type:    "semantic",
		Subject: merged.Subject,
		At:      time.Now(),
		Edges:   allEdges,
		Body:    merged.Content,
	}
	m.graph.facts[merged.ID] = &mergedFact
	m.graph.writeFile(mergedFact)
	m.graph.saveIndex()
	m.embedder.IndexFact(merged.ID, mergedFact)
	m.graph.mu.Unlock()

	return true
}

// clusterDocs groups documents by cosine similarity using union-find.
func clusterDocs(docs []embeddedDoc, threshold float64) [][]embeddedDoc {
	n := len(docs)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		parent[find(a)] = find(b)
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if cosineSimilarity(docs[i].Embedding, docs[j].Embedding) > threshold {
				union(i, j)
			}
		}
	}

	groups := make(map[int][]embeddedDoc)
	for i, doc := range docs {
		root := find(i)
		groups[root] = append(groups[root], doc)
	}

	var out [][]embeddedDoc
	for _, g := range groups {
		out = append(out, g)
	}
	return out
}

// MigrateMissingFacts reads facts from SQLite that are not yet in the graph
// memory system and writes them as .md files. SELECT-only on SQLite — never
// writes to the database.
func (m *Memory) MigrateMissingFacts() {
	if m.db == nil || m.graph == nil {
		return
	}
	report, err := MigrateLegacyFacts(m.db, m.cfg.Home, m.cfg.MemoriesDir)
	if err != nil {
		slog.Warn("graph migration", "error", err)
		return
	}
	if report.Archived > 0 || report.Canonicalized > 0 || report.Duplicates > 0 {
		slog.Info("graph migration", "archived", report.Archived, "canonicalized", report.Canonicalized, "duplicates", report.Duplicates, "dir", m.graph.dir)
	}
}

// RebuildMemoryEdges is an explicit, bounded maintenance pass for existing
// graph claims. It never touches SQLite facts or chat history.
func RebuildMemoryEdges(s *Settings) {
	db := Connect(s.Home)
	defer db.Close()
	authStore := LoadAuthStore(s.Home)
	client, err := NewProviderManager(s.Home, s, authStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graph edge rebuild unavailable: %v\n", err)
		return
	}
	m := NewMemory(db, client, s)
	key := os.Getenv("MINO_OPENROUTER_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "graph edge rebuild requires MINO_OPENROUTER_KEY")
		return
	}
	m.embedder = NewEmbeddingStore(db, key, envOr("MINO_EMBED_MODEL", "openai/text-embedding-3-large"))
	n, err := m.RebuildGraphEdges()
	if err != nil {
		fmt.Fprintf(os.Stderr, "graph edge rebuild incomplete: %v\n", err)
	}
	fmt.Printf("Rebuilt %d inferred graph edges\n", n)
}

func CleanMemoryEdges(s *Settings) {
	db := Connect(s.Home)
	defer db.Close()
	m := NewMemory(db, nil, s)
	removed := m.graph.RemoveMutualInferredEdges()
	fmt.Printf("Removed %d contradictory inferred edges\n", removed)
}

func DeduplicateMemory(s *Settings) {
	db := Connect(s.Home)
	defer db.Close()
	authStore := LoadAuthStore(s.Home)
	client, err := NewProviderManager(s.Home, s, authStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deduplication unavailable: %v\n", err)
		return
	}
	m := NewMemory(db, client, s)
	key := os.Getenv("MINO_OPENROUTER_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "deduplication requires MINO_OPENROUTER_KEY")
		return
	}
	m.embedder = NewEmbeddingStore(db, key, envOr("MINO_EMBED_MODEL", "openai/text-embedding-3-large"))
	merged := m.DedupDue()
	fmt.Printf("Deduplicated %d graph clusters\n", merged)
}

func ConsolidateMemory(s *Settings) {
	db := Connect(s.Home)
	defer db.Close()
	authStore := LoadAuthStore(s.Home)
	client, err := NewProviderManager(s.Home, s, authStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "consolidation unavailable: %v\n", err)
		return
	}
	m := NewMemory(db, client, s)
	key := os.Getenv("MINO_OPENROUTER_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "consolidation requires MINO_OPENROUTER_KEY")
		return
	}
	m.embedder = NewEmbeddingStore(db, key, envOr("MINO_EMBED_MODEL", "openai/text-embedding-3-large"))
	written := m.ConsolidateDue()
	fmt.Printf("Consolidated %d durable graph facts\n", written)
}
