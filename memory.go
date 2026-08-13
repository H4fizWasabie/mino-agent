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
	// Only distilled rows are cleaned; undistilled rows are the distillation
	// queue and must survive until the model processes them.
	m.db.Exec("DELETE FROM session_artifacts WHERE distilled = 1 AND created_at < datetime('now', '-1 day')")
}

// --- Session working note (CTX-004: established facts survive across turns) ---
// Per-session, ephemeral, always injected at turn start. Distinct from
// save_note: the graph is durable and pull-based; this is the working state
// the next turn must not re-discover. Written by the harness (bash commands,
// mechanically) and by the model (note_session tool).

const sessionNoteCap = 2000

func (m *Memory) AppendSessionNote(sessionID, line string) {
	if sessionID == "" || line == "" {
		return
	}
	var existing string
	m.db.QueryRow("SELECT note FROM session_notes WHERE session_id = ?", sessionID).Scan(&existing)
	note := line
	if existing != "" {
		note = existing + "\n" + line
	}
	// Bounded: drop oldest whole lines until under the cap.
	lines := strings.Split(note, "\n")
	for len(note) > sessionNoteCap && len(lines) > 1 {
		lines = lines[1:]
		note = strings.Join(lines, "\n")
	}
	m.db.Exec("INSERT OR REPLACE INTO session_notes (session_id, note, updated_at) VALUES (?,?,datetime('now'))", sessionID, note)
}

// SessionNote returns the session's working note, head+tail truncated to
// maxChars. Empty when the session has no note.
func (m *Memory) SessionNote(sessionID string, maxChars int) string {
	if sessionID == "" || maxChars <= 0 {
		return ""
	}
	var note string
	if err := m.db.QueryRow("SELECT note FROM session_notes WHERE session_id = ?", sessionID).Scan(&note); err != nil {
		return ""
	}
	if len(note) <= maxChars {
		return note
	}
	head := maxChars / 2
	return note[:head] + "\n...\n" + note[len(note)-(maxChars-head):]
}

// --- Playbook output distillation (durable queue → episodic memory) ---

const distillOutputPrompt = `You distill a playbook run's output files into long-term memory.

The run produced these output files (path: content):
%s

Reply with ONLY this JSON:
{"run": {"id": "ep_ai_news_daily_20260805", "subject": "<one sentence: what was posted/produced, when, and the post/artifact ID if any>", "body": "<1-3 sentences: what happened, outcome>", "edges": [{"target": "<existing_id>", "rel": "<specific relation>", "kind": "explicit"}]}, "facts": [{"id": "<short_snake_case_id>", "subject": "<one sentence>", "content": "<optional>", "edges": []}]}

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
				// Guard: reject IDs that carry the prompt template's placeholder
				// text (observed 2026-08-07: the model copied
				// "snake_case_id_prefixed_ep_..." verbatim as a real fact ID,
				// polluting 7 facts and a god node). A leaked placeholder is
				// not a durable ID — treat the whole response as invalid.
				if templateIDLeak(out.Run.ID) || templateIDLeak(out.Run.Subject) {
					return distilledRun{}, fmt.Errorf("distill response contains template placeholder ID")
				}
				for _, f := range out.Facts {
					if templateIDLeak(f.ID) {
						return distilledRun{}, fmt.Errorf("distill response contains template placeholder ID")
					}
				}
				return out, nil
			}
		}
	}
	return distilledRun{}, fmt.Errorf("no valid distill object")
}

// templateIDLeak reports whether the string carries the distill prompt's
// example placeholder text ("snake_case_id_prefixed_ep_") — the marker that
// a model copied the template instead of generating a real ID.
func templateIDLeak(s string) bool {
	return strings.Contains(s, "snake_case_id_prefixed")
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
	defer rows.Close()
	type artifact struct{ path, sessionID, label string }
	var arts []artifact
	for rows.Next() {
		var a artifact
		if rows.Scan(&a.path, &a.sessionID, &a.label) == nil {
			arts = append(arts, a)
		}
	}
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
				if os.IsNotExist(err) {
					// Dead row: the artifact file is gone (e.g. /tmp cleaned on
					// reboot). Tombstone it so the queue advances — otherwise
					// it is re-selected every pass and blocks all newer
					// artifacts forever.
					m.db.Exec("UPDATE session_artifacts SET distilled = 1 WHERE path = ?", a.path)
				}
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
		// Semantic facts distill only from playbooks whose config.md whitelists
		// them (issue #178): routine runs post run nodes, not durable facts.
		if m.cfg != nil && playbookDistillSemantic(m.cfg.Home, artifactPlaybookName(sid, byRun[sid][0].label)) {
			for _, f := range out.Facts {
				if f.ID == "" || f.Subject == "" {
					continue
				}
				f.Type = "semantic"
				f.At = time.Now().UTC()
				if err := m.graph.RecordFact(f); err != nil {
					slog.Warn("distill fact write failed", "fact", f.ID, "error", err)
				}
			}
		}
		for _, a := range byRun[sid] {
			m.db.Exec("UPDATE session_artifacts SET distilled = 1 WHERE path = ?", a.path)
		}
		written++ // one run = one node
	}
	return written
}

// artifactPlaybookName recovers the playbook name an artifact row was
// recorded under: scheduled runs carry it in the session id, manual runs in
// the label ("<name> output").
func artifactPlaybookName(sessionID, label string) string {
	if name := strings.TrimPrefix(sessionID, "scheduled-"); name != sessionID {
		return name
	}
	return strings.TrimSuffix(label, " output")
}

// playbookDistillSemantic reports whether the playbook's config.md whitelists
// semantic-fact distillation. Default false: only explicitly whitelisted
// playbooks (e.g. daily-ai-concept) produce semantic facts from distill —
// routine runs post run nodes only (issue #178).
func playbookDistillSemantic(home, name string) bool {
	data, err := os.ReadFile(filepath.Join(home, "playbooks", name, "config.md"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) == 2 && parts[0] == "distill_semantic" && strings.TrimSpace(parts[1]) == "true" {
			return true
		}
	}
	return false
}

// availableFactIDs returns a prompt-safe list of existing fact IDs.
func (m *Memory) availableFactIDs() string {
	var ids []string
	for _, f := range m.graph.Facts() {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// --- Semantic search (graph-backed) ---

func (m *Memory) Search(query string) string {
	return m.graph.Remember(query, "")
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
// consolidateMinAge: a session's unconsolidated history becomes eligible for
// consolidation once its oldest unconsolidated row is at least this old. Gated
// by recency, not a per-session row-count floor — short interactive chat
// sessions rarely accumulate enough rows to meet a floor, so their history
// stayed perpetually unconsolidated (witnessed: 78 rows stuck; the tool then
// reported a fabricated "consolidated 8" on a 0-result). One hour keeps an
// active conversation from being consolidated mid-stream while still draining
// history on the next scheduled pass.
const consolidateMinAge = 1 * time.Hour

func (m *Memory) ConsolidateDue() int {
	if m.client == nil || m.cfg.ConsolidateEvery <= 0 {
		return 0
	}
	m.consolidateMu.Lock()
	defer m.consolidateMu.Unlock()
	// Eligible = sessions with unconsolidated rows whose oldest unconsolidated
	// row is at least consolidateMinAge old. Replaces the old
	// `HAVING COUNT(*) >= ConsolidateEvery*2` floor that short chats never met
	// (each exchange = user + assistant row, so the floor needed ~6 exchanges).
	// Modifier is inlined (not a bound param) — SQLite treats a bound date
	// modifier as NULL, which makes the HAVING comparison NULL and selects nothing.
	rows, err := m.db.Query(fmt.Sprintf(`SELECT session_id FROM chat_log
		WHERE consolidated = 0
		GROUP BY session_id
		HAVING MIN(created_at) <= datetime('now', '-%d hours')`, int(consolidateMinAge.Hours())))
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
		fact.Edges = m.validInferredEdges(f.Edges, availableIDs.ids, "consolidation")
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

func (m *Memory) validInferredEdges(edges []Edge, candidates map[string]bool, source string) []Edge {
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
		edge.Source = source
		valid = append(valid, edge)
	}
	return valid
}

const graphJudgmentPrompt = `You are maintaining a personal knowledge graph.
Return only JSON: {"edges":[{"source":"claim_id","target":"candidate_id","rel":"specific_relation","confidence":0.0}],"facts":[{"id":"claim_id","why":"...","use_when":["...","..."]}]}.
EDGES: For every candidate pair, actively check whether the claim bodies establish a direct relationship. Emit an edge when the relationship is clear and useful; do not require the exact relationship words to appear verbatim.
Use a specific relation such as depends_on, maintains, prefers, attributed_to, located_at, requires, supersedes, deployed_on, scheduled_at, calls, or used_in. Never use related_to. Confidence must be at least 0.85. Do not invent IDs or relationships that are merely topical.
FACTS: For EVERY source claim, emit one facts entry. "why" is a 1-2 sentence reason the fact matters to the owner. When a source has a USER WHY line, keep its intent and refine only for clarity; never contradict it. When it has none, write the why from the claim itself. "use_when" is 2-5 short trigger phrases naming the situations or questions where this fact should be recalled.

Claims and bounded candidates:
%s`

type graphRebuildEdge struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Rel        string  `json:"rel"`
	Confidence float64 `json:"confidence"`
}

type graphJudgmentFact struct {
	ID      string   `json:"id"`
	Why     string   `json:"why"`
	UseWhen []string `json:"use_when"`
	Expired bool     `json:"expired"` // MEM-08: why no longer holds → archive
}

type graphJudgment struct {
	Edges []graphRebuildEdge  `json:"edges"`
	Facts []graphJudgmentFact `json:"facts"`
}

func parseGraphJudgmentResponse(text string) (graphJudgment, error) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "```json"), "```"))
	for start := 0; start < len(text); start++ {
		if text[start] != '{' {
			continue
		}
		for end := start + 1; end <= len(text); end++ {
			if text[end-1] != '}' || !json.Valid([]byte(text[start:end])) {
				continue
			}
			var out graphJudgment
			if err := json.Unmarshal([]byte(text[start:end]), &out); err == nil {
				return out, nil
			}
		}
	}
	return graphJudgment{}, fmt.Errorf("no valid graph judgment object")
}

func (m *Memory) RebuildGraphEdges() (int, error) {
	if m.client == nil || m.embedder == nil {
		return 0, fmt.Errorf("graph rebuild requires provider and embedding store")
	}
	facts := m.graph.Facts()
	// Backfill embeddings for facts that never got vectors (migrated facts,
	// installs where the embedder was configured later). Without a vector a
	// fact is invisible to GraphCandidates and can never gain edges.
	for _, fact := range facts {
		if !m.embedder.HasFactEmbedding(fact.ID) {
			m.embedder.IndexFact(fact.ID, fact)
		}
	}
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
			if byID[sourceID].Why != "" {
				fmt.Fprintf(&claims, "  USER WHY: %s\n", byID[sourceID].Why)
			}
			allowed[sourceID] = make(map[string]bool)
			for _, candidate := range candidates[sourceID] {
				allowed[sourceID][candidate.ID] = true
				fmt.Fprintf(&claims, "  CANDIDATE %s (%0.2f): %s | %s\n", candidate.ID, candidate.Score, byID[candidate.ID].Subject, byID[candidate.ID].Body)
			}
		}
		resp, err := m.client.CreateJSON("graph-rebuild", SmallModel, []Message{{Role: "user", Content: fmt.Sprintf(graphJudgmentPrompt, claims.String())}}, 1600, "")
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
		out, err := parseGraphJudgmentResponse(text)
		if err != nil || len(out.Edges) == 0 {
			failed++
			continue
		}
		inferred := make(map[string][]Edge)
		for _, edge := range out.Edges {
			if !allowed[edge.Source][edge.Target] {
				continue
			}
			valid := m.validInferredEdges([]Edge{{Target: edge.Target, Rel: edge.Rel, Confidence: edge.Confidence}}, allowed[edge.Source], "graph-rebuild")
			if len(valid) > 0 {
				inferred[edge.Source] = append(inferred[edge.Source], valid...)
			}
		}
		meta := make(map[string]graphJudgmentFact, len(out.Facts))
		for _, f := range out.Facts {
			if f.ID != "" {
				meta[f.ID] = f
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
			// Why/use_when: the rebuild must keep the original Source (provenance
			// for the why work) and only overwrite why/use_when when the model
			// returned fresh values for this fact.
			if jf, ok := meta[sourceID]; ok {
				if jf.Why != "" {
					fact.Why = jf.Why
				}
				if len(jf.UseWhen) > 0 {
					fact.UseWhen = jf.UseWhen
				}
				if jf.Expired {
					// MEM-08: the why no longer holds — archive, never delete; the
					// fact stays answerable via remember's archive fallback. A failed
					// archive leaves the fact live for the next pass.
					if _, err := m.graph.ArchiveFact(fact, "judgment: why no longer holds"); err == nil {
						m.embedder.RemoveFact(sourceID)
						continue
					}
				}
			}
			if err := m.graph.ReplaceFact(fact); err == nil {
				edgesWritten += len(inferred[sourceID])
			}
		}
	}
	// Only mark judged when the rebuild wrote every batch: a partial failure
	// must leave facts eligible for the incremental judgment pass.
	if failed == 0 {
		for _, fact := range facts {
			m.graph.MarkJudged(fact.ID)
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

// MaintainGraph is the scheduled full maintenance pass: re-infer all edges,
// resolve mirrored pairs, cluster, and label communities. Returns counts.
func (m *Memory) MaintainGraph() (int, int, error) {
	// Deterministic lifecycle first: expired episodes leave the live graph
	// before the rebuild judges them (issue #178).
	if archived := m.graph.ArchiveExpiredEpisodic(time.Now().Add(-staleAgeThreshold)); archived > 0 {
		slog.Info("archived expired episodic facts", "count", archived)
	}
	edges, err := m.RebuildGraphEdges()
	if err != nil {
		// Deterministic steps must still run: a failed LLM batch means some
		// facts were not re-judged (they retry next cycle), but clustering,
		// cleanup, and labels never depend on LLM batch success. Gating them
		// behind a fully-clean rebuild would starve communities forever when
		// one batch of unrelated facts legitimately returns zero edges.
		slog.Warn("graph rebuild partial; running deterministic maintenance", "error", err)
	}
	m.graph.RemoveMutualInferredEdges()
	facts := m.graph.Facts()
	communities, gods := ClusterGraph(facts)
	labels := m.LabelCommunities(communities)
	m.graph.SetCommunities(communities, gods, labels)
	seen := make(map[int]struct{})
	for _, c := range communities {
		seen[c] = struct{}{}
	}
	return edges, len(seen), err
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
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "```json"), "```"))
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

// JudgeChangedFacts gives every new or edited memory its own edge-judgment
// pass (graphify-style incremental update). Bounded per pass; failures leave
// the fact unjudged so the next pass retries. The deterministic recall floor
// never depends on this. Returns the number of inferred edges written.
func (m *Memory) JudgeChangedFacts() int {
	if m.client == nil {
		return 0
	}
	unjudged := m.graph.UnjudgedFacts()
	facts := m.graph.Facts()
	judged := 0
	for i, fact := range unjudged {
		if i >= 5 { // ponytail: bounded per pass, backlog drains over later passes
			break
		}
		if m.graph.JudgedAt(fact.ID) != "" {
			continue // the 6h maintenance pass judged it first
		}
		edges, updated, ok := m.judgeFactEdges(*fact, facts)
		if !ok {
			continue
		}
		if updated == nil {
			// Expiry judgment archived the fact (MEM-08); it is judged now.
			m.graph.MarkJudged(fact.ID)
			continue
		}
		if m.graph.JudgedAt(fact.ID) != "" {
			continue // 6h pass beat us to it; its edges win, skip the write
		}
		m.graph.ReplaceFact(*updated)
		m.graph.MarkJudged(fact.ID)
		judged += edges
	}
	return judged
}

// judgeFactEdges runs one small-model judgment pass for a single SOURCE fact
// against its embedding candidates (from GraphCandidates over all facts, as in
// RebuildGraphEdges). Returns the number of inferred edges, the fact to write,
// and whether the judgment succeeded. A nil fact with ok=true means the expiry
// judgment archived the fact (MEM-08) — the caller marks it judged without
// writing it live. The caller writes the fact only if no
// other pass judged it meanwhile (5-min/6h race guard). A successful pass
// with zero edges means the LLM judged the fact and found no relationships —
// the fact is still marked judged. Only failures (no candidates, LLM/parse/write
// errors) leave the fact unjudged for retry.
func (m *Memory) judgeFactEdges(fact Fact, all []Fact) (int, *Fact, bool) {
	if m.embedder == nil {
		return 0, nil, false
	}
	candidates := m.embedder.GraphCandidates(all, 6)
	ids := candidates[fact.ID]
	if len(ids) == 0 {
		return 0, nil, false
	}
	var claims strings.Builder
	allowed := make(map[string]bool)
	fmt.Fprintf(&claims, "SOURCE %s: %s | %s\n", fact.ID, fact.Subject, fact.Body)
	if fact.Why != "" {
		fmt.Fprintf(&claims, "  USER WHY: %s\n", fact.Why)
	}
	for _, c := range ids {
		allowed[c.ID] = true
		if other, ok := m.graph.FindFact(c.ID); ok {
			fmt.Fprintf(&claims, "  CANDIDATE %s (%0.2f): %s | %s\n", c.ID, c.Score, other.Subject, other.Body)
		}
	}
	resp, err := m.client.CreateJSON("graph-rebuild", SmallModel,
		[]Message{{Role: "user", Content: fmt.Sprintf(graphJudgmentPrompt, claims.String())}}, 1600, "")
	if err != nil {
		return 0, nil, false
	}
	text := resp.FinalText
	if text == "" {
		for _, block := range resp.Content {
			if block.Type == "text" {
				text += block.Text
			}
		}
	}
	out, err := parseGraphJudgmentResponse(text)
	if err != nil {
		return 0, nil, false
	}
	inferred := make([]Edge, 0, len(out.Edges))
	for _, edge := range out.Edges {
		inferred = append(inferred, Edge{Target: edge.Target, Rel: edge.Rel, Confidence: edge.Confidence})
	}
	inferred = m.validInferredEdges(inferred, allowed, "graph-rebuild")
	fact.Edges = nil
	if existing, ok := m.graph.FindFact(fact.ID); ok {
		for _, e := range existing.Edges {
			if e.Kind == "explicit" {
				fact.Edges = append(fact.Edges, e)
			}
		}
	}
	fact.Edges = append(fact.Edges, inferred...)
	// Why/use_when: overwrite only when the model returned fresh values, so a
	// partial response (edges only) keeps the existing why/use_when intact.
	for _, jf := range out.Facts {
		if jf.ID != fact.ID {
			continue
		}
		if jf.Expired {
			// MEM-08: the judgment says the why no longer holds — archive, never
			// delete. The caller marks the fact judged without writing it live.
			if _, err := m.graph.ArchiveFact(fact, "judgment: why no longer holds"); err == nil {
				if m.embedder != nil {
					m.embedder.RemoveFact(fact.ID)
				}
				return 0, nil, true
			}
		}
		if jf.Why != "" {
			fact.Why = jf.Why
		}
		if len(jf.UseWhen) > 0 {
			fact.UseWhen = jf.UseWhen
		}
	}
	return len(inferred), &fact, true
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

	// Write merged fact. The survivor keeps its why/use_when so dedup never
	// destroys provenance; the 6h rebuild refreshes both for the new body.
	mergedFact := Fact{
		ID:      merged.ID,
		Type:    "semantic",
		Subject: merged.Subject,
		At:      time.Now(),
		Edges:   allEdges,
		Body:    merged.Content,
		Why:     m.graph.facts[merged.ID].Why,
		UseWhen: m.graph.facts[merged.ID].UseWhen,
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

// MaintainMemory runs the full scheduled maintenance pass on demand: edge
// re-inference, mirrored-pair cleanup, community detection, and labels.
func MaintainMemory(s *Settings) {
	db := Connect(s.Home)
	defer db.Close()
	authStore := LoadAuthStore(s.Home)
	client, err := NewProviderManager(s.Home, s, authStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graph maintenance unavailable: %v\n", err)
		return
	}
	m := NewMemory(db, client, s)
	key := os.Getenv("MINO_OPENROUTER_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "graph maintenance requires MINO_OPENROUTER_KEY")
		return
	}
	m.embedder = NewEmbeddingStore(db, key, envOr("MINO_EMBED_MODEL", "openai/text-embedding-3-large"))
	edges, communities, err := m.MaintainGraph()
	if err != nil {
		fmt.Fprintf(os.Stderr, "graph maintenance incomplete: %v\n", err)
	}
	fmt.Printf("Maintained graph: %d edges, %d communities\n", edges, communities)
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

// --- Archive digest (MEM-08) ---

// SendArchiveDigest delivers the pending archive digest — one Telegram note of
// everything archived since the last successful send, so the owner can dispute
// or restore without being pinged per fact. Outbox pattern: entries survive a
// failed send and retry next cycle. No-op without Telegram config.
func (m *Memory) SendArchiveDigest() {
	if m.cfg == nil || m.cfg.Telegram == "" || m.cfg.TelegramChatID <= 0 {
		return
	}
	pending := m.graph.TakePendingDigest()
	if len(pending) == 0 {
		return
	}
	text := "🗄️ Archived memories:\n" + strings.Join(pending, "\n") +
		"\n\nArchived facts still answer remember queries (tagged [archived]); say the word to restore any."
	if !sendTelegramText(m.cfg.Telegram, m.cfg.TelegramChatID, text, false) {
		m.graph.AppendPendingDigest(pending)
	}
}

// runArchiveDigest is the daily dispatch loop for the archive digest.
func runArchiveDigest(core *Core) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		core.Memory.SendArchiveDigest()
	}
}
