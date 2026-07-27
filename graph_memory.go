package main

// graph_memory — graphify-style knowledge graph memory.
// One .md file per fact with YAML front matter carrying explicit edges.
// remember() traverses the graph; FTS5 provides the entry point.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// --- Fact ---

type Fact struct {
	ID      string    `yaml:"id"`
	Type    string    `yaml:"type"` // "semantic" or "episodic"
	Subject string    `yaml:"subject"`
	At      time.Time `yaml:"at"`
	Edges   []Edge    `yaml:"edge"`
	Body    string    `yaml:"-"` // everything after front matter
}

type Edge struct {
	Target string `yaml:"target"`
	Rel    string `yaml:"rel"`
}

// --- Graph memory ---

type GraphMemory struct {
	dir      string
	mu       sync.RWMutex
	facts    map[string]*Fact // id → Fact
	searchFn func(query string) string
	cfg      *Settings
}

func NewGraphMemory(dir string, cfg *Settings) *GraphMemory {
	os.MkdirAll(dir, 0755)
	gm := &GraphMemory{dir: dir, cfg: cfg, facts: make(map[string]*Fact)}
	gm.loadAll()
	return gm
}

// loadAll scans memories/ and parses every .md file into memory.
func (gm *GraphMemory) loadAll() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	entries, err := os.ReadDir(gm.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		fact, err := gm.readFile(filepath.Join(gm.dir, e.Name()))
		if err != nil || fact == nil {
			continue
		}
		gm.facts[fact.ID] = fact
	}
}

// readFile parses a single .md file into a Fact.
func (gm *GraphMemory) readFile(path string) (*Fact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return gm.parseFrontMatter(data)
}

// parseFrontMatter extracts YAML front matter and body from .md content.
func (gm *GraphMemory) parseFrontMatter(raw []byte) (*Fact, error) {
	text := string(raw)
	// YAML front matter is between --- delimiters
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("no front matter")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil, fmt.Errorf("unclosed front matter")
	}
	fm := text[4 : 4+end]
	body := strings.TrimSpace(text[4+end+5:])

	var fact Fact
	if err := yaml.Unmarshal([]byte(fm), &fact); err != nil {
		return nil, err
	}
	fact.Body = body
	return &fact, nil
}

// RecordFact writes a Fact to disk. If the file exists and args say "merge",
// it reads the existing fact and merges edges + body. Otherwise overwrites.
func (gm *GraphMemory) RecordFact(fact Fact) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if existing, ok := gm.facts[fact.ID]; ok {
		// Merge: keep existing edges, add new ones
		seen := make(map[string]bool)
		for _, e := range existing.Edges {
			seen[e.Target+e.Rel] = true
		}
		for _, e := range fact.Edges {
			if !seen[e.Target+e.Rel] {
				existing.Edges = append(existing.Edges, e)
				seen[e.Target+e.Rel] = true
			}
		}
		// Update subject and body if new info
		if fact.Subject != "" && fact.Subject != existing.Subject {
			existing.Subject = fact.Subject
		}
		if fact.Body != "" {
			existing.Body = fact.Body
		}
		existing.At = fact.At
		fact = *existing
	}

	if err := gm.writeFile(fact); err != nil {
		return err
	}
	gm.facts[fact.ID] = &fact
	return nil
}

// writeFile serializes a Fact to a .md file.
func (gm *GraphMemory) writeFile(fact Fact) error {
	path := filepath.Join(gm.dir, fact.ID+".md")
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("id: %s\n", fact.ID))
	b.WriteString(fmt.Sprintf("type: %s\n", fact.Type))
	b.WriteString(fmt.Sprintf("subject: %s\n", fact.Subject))
	b.WriteString(fmt.Sprintf("at: %s\n", fact.At.Format(time.RFC3339)))
	if len(fact.Edges) > 0 {
		b.WriteString("edge:\n")
		for _, e := range fact.Edges {
			b.WriteString(fmt.Sprintf("  - target: %s\n", e.Target))
			b.WriteString(fmt.Sprintf("    rel: %s\n", e.Rel))
		}
	}
	b.WriteString("---\n")
	if fact.Body != "" {
		b.WriteString("\n")
		b.WriteString(fact.Body)
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// SetSearch sets the FTS5 search backend (Memory) for entry-point queries.
func (gm *GraphMemory) SetSearch(fts5 func(query string) string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.searchFn = fts5
}

// Remember is the graph-aware recall tool. Returns an indented tree.
//
//	remember("Procura")
//	→ procura_is_authoritative
//	  → [supersedes] procurepilot_is_legacy
//	  → [depends_on] procura_db_location
//	    → [located_at] vps_server
func (gm *GraphMemory) Remember(query string) string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	// Step 1: FTS5 entry point — find start nodes
	startIDs := gm.fts5Entry(query)
	if len(startIDs) == 0 {
		return fmt.Sprintf("No memories found for: %s", query)
	}
	if len(startIDs) > 3 {
		startIDs = startIDs[:3]
	}

	// Step 2: BFS traversal from start nodes
	maxDepth := 2
	if gm.cfg != nil && gm.cfg.TopK > 0 {
		maxDepth = gm.cfg.TopK
	}
	visited := make(map[string]bool)
	var lines []string

	for _, startID := range startIDs {
		fact, ok := gm.facts[startID]
		if !ok {
			continue
		}
		lines = append(lines, fact.Subject+"  # "+fact.ID)
		visited[startID] = true
		gm.bfsEdges(fact, "  ", 1, maxDepth, visited, &lines)
	}

	if len(lines) == 0 {
		return fmt.Sprintf("No memories found for: %s", query)
	}
	return strings.Join(lines, "\n")
}

// bfsEdges traverses edges recursively, depth-limited.
func (gm *GraphMemory) bfsEdges(fact *Fact, indent string, depth, maxDepth int, visited map[string]bool, lines *[]string) {
	if depth > maxDepth {
		return
	}
	nextIndent := indent + "  "
	for _, edge := range fact.Edges {
		target, ok := gm.facts[edge.Target]
		if !ok {
			continue
		}
		label := target.Subject
		if visited[edge.Target] {
			*lines = append(*lines, fmt.Sprintf("%s→ [%s] %s  # %s (already shown)", indent, edge.Rel, label, edge.Target))
			continue
		}
		visited[edge.Target] = true
		*lines = append(*lines, fmt.Sprintf("%s→ [%s] %s  # %s", indent, edge.Rel, label, edge.Target))
		gm.bfsEdges(target, nextIndent, depth+1, maxDepth, visited, lines)
	}
}

// fts5Entry finds matching fact IDs. Uses FTS5 if available, else substring search.
func (gm *GraphMemory) fts5Entry(query string) []string {
	// Try FTS5 first
	if gm.searchFn != nil {
		result := gm.searchFn(query)
		return gm.parseFTS5Result(query, result)
	}
	// Fallback: substring match on subjects
	return gm.substringMatch(query)
}

// parseFTS5Result extracts fact IDs from FTS5 output like "- **Subject**: content"
func (gm *GraphMemory) parseFTS5Result(query, result string) []string {
	var ids []string
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- **") {
			continue
		}
		// Match subject text to a fact ID
		subject := strings.TrimPrefix(line, "- **")
		if idx := strings.Index(subject, "**"); idx > 0 {
			subject = subject[:idx]
		}
		for id, fact := range gm.facts {
			if strings.EqualFold(fact.Subject, subject) || strings.Contains(strings.ToLower(fact.Subject), strings.ToLower(subject)) {
				ids = append(ids, id)
				break
			}
		}
	}
	if len(ids) == 0 {
		// FTS5 had results but no matching graph nodes — fallback to substring
		return gm.substringMatch(query)
	}
	return ids
}

// substringMatch finds fact IDs by substring matching against subject and body.
func (gm *GraphMemory) substringMatch(query string) []string {
	type scored struct {
		id    string
		score int
	}
	var ranked []scored
	qlower := strings.ToLower(query)
	for id, fact := range gm.facts {
		subj := strings.ToLower(fact.Subject)
		// Score: exact match > word match > substring match
		score := 0
		if subj == qlower {
			score = 100
		} else if strings.Contains(subj, " "+qlower+" ") || strings.HasPrefix(subj, qlower+" ") {
			score = 50
		} else if strings.Contains(subj, qlower) {
			score = 10
		}
		// Also search body
		if strings.Contains(strings.ToLower(fact.Body), qlower) {
			score += 5
		}
		if score > 0 {
			ranked = append(ranked, scored{id, score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	ids := make([]string, len(ranked))
	for i, r := range ranked {
		ids[i] = r.id
	}
	return ids
}

// RememberPath finds shortest path between two facts. Returns indented path.
func (gm *GraphMemory) RememberPath(from, to string) string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	// BFS shortest path
	fromID := gm.matchID(from)
	toID := gm.matchID(to)
	if fromID == "" || toID == "" {
		return fmt.Sprintf("Could not find: %s → %s", from, to)
	}

	parent := make(map[string]string)
	queue := []string{fromID}
	visited := map[string]bool{fromID: true}
	found := false

	for len(queue) > 0 && !found {
		curr := queue[0]
		queue = queue[1:]
		fact, ok := gm.facts[curr]
		if !ok {
			continue
		}
		for _, edge := range fact.Edges {
			if visited[edge.Target] {
				continue
			}
			parent[edge.Target] = curr
			if edge.Target == toID {
				found = true
				break
			}
			visited[edge.Target] = true
			queue = append(queue, edge.Target)
		}
	}

	if !found {
		return fmt.Sprintf("No path found between %s and %s", from, to)
	}

	// Reconstruct path
	var path []string
	for curr := toID; curr != ""; curr = parent[curr] {
		path = append([]string{curr}, path...)
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Path (%d hops):", len(path)-1))
	for i := 0; i < len(path); i++ {
		fact := gm.facts[path[i]]
		label := path[i]
		if fact != nil {
			label = fact.Subject
		}
		if i < len(path)-1 {
			next := gm.facts[path[i+1]]
			rel := "related_to"
			if next != nil {
				for _, e := range next.Edges {
					if e.Target == path[i] {
						rel = e.Rel
						break
					}
				}
				// Also check forward edges
				if rel == "related_to" && fact != nil {
					for _, e := range fact.Edges {
						if e.Target == path[i+1] {
							rel = e.Rel
							break
						}
					}
				}
			}
			lines = append(lines, fmt.Sprintf("  %s --[%s]-->", label, rel))
		} else {
			lines = append(lines, fmt.Sprintf("  %s", label))
		}
	}
	return strings.Join(lines, "\n")
}

// matchID finds the best-matching fact ID for a query string.
func (gm *GraphMemory) matchID(query string) string {
	if _, ok := gm.facts[query]; ok {
		return query
	}
	qlower := strings.ToLower(query)
	for id, fact := range gm.facts {
		if strings.ToLower(fact.Subject) == qlower || strings.ToLower(id) == qlower {
			return id
		}
	}
	return ""
}

// Stat returns the number of facts currently loaded.
func (gm *GraphMemory) Stat() int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return len(gm.facts)
}

// --- Migration from flat DB ---

// MigrateMemories converts existing facts and episodes from the SQLite DB
// into .md files in the memories directory.
func MigrateMemories(home, memoriesDir string) {
	db := Connect(home)
	defer db.Close()

	os.MkdirAll(memoriesDir, 0755)
	gm := NewGraphMemory(memoriesDir, nil)

	// Migrate facts
	rows, err := db.Query("SELECT subject, content, created_at FROM facts ORDER BY id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading facts: %v\n", err)
		return
	}
	defer rows.Close()

	factCount := 0
	for rows.Next() {
		var subject, content, createdAt string
		if rows.Scan(&subject, &content, &createdAt) != nil {
			continue
		}
		at, _ := time.Parse("2006-01-02 15:04:05", createdAt)
		if at.IsZero() {
			at = time.Now()
		}
		id := slugify(subject)
		fact := Fact{
			ID:      id,
			Type:    "semantic",
			Subject: subject,
			At:      at,
			Body:    content,
		}
		if err := gm.RecordFact(fact); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", id, err)
			continue
		}
		factCount++
	}
	fmt.Printf("Migrated %d facts\n", factCount)

	// Migrate episodes
	epRows, err := db.Query("SELECT happened_at, summary FROM episodes ORDER BY id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading episodes: %v\n", err)
		return
	}
	defer epRows.Close()

	epCount := 0
	for epRows.Next() {
		var happenedAt, summary string
		if epRows.Scan(&happenedAt, &summary) != nil {
			continue
		}
		at, _ := time.Parse("2006-01-02", happenedAt)
		if at.IsZero() {
			at = time.Now()
		}
		id := "ep_" + slugify(summary)
		if len(id) > 60 {
			id = id[:60]
		}
		fact := Fact{
			ID:      id,
			Type:    "episodic",
			Subject: summary,
			At:      at,
			Body:    summary,
		}
		if err := gm.RecordFact(fact); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing episode %s: %v\n", id, err)
			continue
		}
		epCount++
	}
	fmt.Printf("Migrated %d episodes\n", epCount)
	fmt.Printf("Total: %d memories in %s/\n", gm.Stat(), memoriesDir)
}

// slugify converts a string to a snake_case identifier.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	// Replace non-alphanumeric with underscore
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' {
			b.WriteRune('_')
		}
	}
	result := b.String()
	// Collapse multiple underscores
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}
	result = strings.Trim(result, "_")
	if len(result) > 60 {
		result = result[:60]
	}
	return result
}
