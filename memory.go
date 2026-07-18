package main

// Mino — memory/ — Core's exact memory system.
// Three pillars: semantic (FTS5), episodic, procedural (skills).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

type Memory struct {
	db            *sql.DB
	skills        *SkillLoader
	client        *ProviderManager
	cfg           *Settings
	embedder      *EmbeddingStore
	consolidateMu sync.Mutex
}

func NewMemory(db *sql.DB, client *ProviderManager, cfg *Settings) *Memory {
	return &Memory{db: db, client: client, cfg: cfg}
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

// --- Episodic search ---

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
		} else if pending != "" {
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
2. one single-sentence episode summarizing what happened in this conversation.

Reply with ONLY this JSON:
{"facts": [{"subject": "<who/what>", "content": "<one sentence>"}], "episode": "<one sentence>"}

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
	for _, sid := range sessions {
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
	resp, err := m.client.Create("consolidation", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(summarizerPrompt, log.String())}}, 600, "", nil)
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
			Subject string `json:"subject"`
			Content string `json:"content"`
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
		if placeholder(f.Subject) || placeholder(f.Content) {
			continue
		}
		res, err := m.db.Exec(`INSERT INTO facts (subject, content, source) SELECT ?, ?, 'consolidation'
			WHERE NOT EXISTS (SELECT 1 FROM facts WHERE subject = ? AND content = ?)`,
			f.Subject, f.Content, f.Subject, f.Content)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			written++
			if m.embedder != nil {
				m.embedder.Index("fact", f.Subject+": "+f.Content)
			}
		}
	}
	if !placeholder(distilled.Episode) {
		m.db.Exec("INSERT INTO episodes (happened_at, summary, session_id, source) VALUES (date('now'), ?, ?, 'consolidation')",
			distilled.Episode, sid)
		if m.embedder != nil {
			m.embedder.Index("episode", distilled.Episode)
		}
	}
	m.db.Exec("UPDATE chat_log SET consolidated = 1 WHERE id IN (" + strings.Join(ids, ",") + ")")
	return written
}
