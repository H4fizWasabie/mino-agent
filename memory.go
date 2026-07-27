package main

// Mino — memory/ — Core's exact memory system.
// Three pillars: semantic (FTS5), episodic, procedural (skills).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// --- Semantic search (Core: FTS5) ---

func (m *Memory) Search(query string) string {
	rows, err := m.db.Query(
		"SELECT subject, content FROM facts_fts WHERE facts_fts MATCH ? ORDER BY rank LIMIT ?",
		query, m.cfg.TopK,
	)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var subject, content string
		rows.Scan(&subject, &content)
		out.WriteString(fmt.Sprintf("- **%s**: %s\n", subject, content))
	}
	return out.String()
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
   For each fact, include edges to existing facts listed below when there's a clear relationship.
2. one single-sentence episode summarizing what happened in this conversation.

Available fact IDs (use as edge targets when relevant):
%s

Reply with ONLY this JSON:
{"facts": [{"id": "snake_case_id", "subject": "<one sentence>", "content": "<optional body>", "edges": [{"target": "<existing_id>", "rel": "<relation>"}]}], "episode": "<one sentence>"}

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

	// Build list of available fact IDs for edge targets
	var availableIDs strings.Builder
	m.graph.mu.RLock()
	for id, fact := range m.graph.facts {
		fmt.Fprintf(&availableIDs, "- %s (%s)\n", id, fact.Subject)
	}
	m.graph.mu.RUnlock()

	resp, err := m.client.Create("consolidation", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(summarizerPrompt, availableIDs.String(), log.String())}}, 600, "", nil)
	if err != nil {
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
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return 0
	}
	var distilled struct {
		Facts []struct {
			ID      string `json:"id"`
			Subject string `json:"subject"`
			Content string `json:"content"`
			Edges   []Edge `json:"edges"`
		} `json:"facts"`
		Episode string `json:"episode"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &distilled) != nil {
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
			Edges:   f.Edges,
			Body:    f.Content,
		}
		if err := m.graph.RecordFact(fact); err != nil {
			continue
		}
		written++
		if m.embedder != nil {
			m.embedder.Index("fact", f.Subject+": "+f.Content)
		}
	}
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
		m.graph.RecordFact(epFact)
		if m.embedder != nil {
			m.embedder.Index("episode", distilled.Episode)
		}
	}
	m.db.Exec("UPDATE chat_log SET consolidated = 1 WHERE id IN (" + strings.Join(ids, ",") + ")")
	return written
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

	// Ensure all facts are embedded
	m.graph.mu.RLock()
	for _, f := range m.graph.facts {
		text := f.Subject + ": " + f.Body
		m.embedder.Index("fact", text) // idempotent — skips if cached
	}
	m.graph.mu.RUnlock()

	// Get all fact embeddings
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
func (m *Memory) mergeCluster(docs []embeddedDoc) bool {
	m.graph.mu.Lock()
	// Map content → fact ID
	contentToID := make(map[string]string)
	var facts []*Fact
	for _, d := range docs {
		// Find the fact by matching content against subject+body
		for id, f := range m.graph.facts {
			candidate := f.Subject + ": " + f.Body
			if candidate == d.Content {
				contentToID[d.Content] = id
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
		m.embedder.Remove("fact", f.Subject+": "+f.Body)
	}

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
	m.embedder.Index("fact", merged.Subject+": "+merged.Content)
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
