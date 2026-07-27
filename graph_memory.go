package main

// graph_memory — graphify-style knowledge graph memory.
// One .md file per fact with YAML front matter carrying explicit edges.
// remember() traverses the graph; FTS5 provides the entry point.

import (
	"encoding/json"
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
	cfg      *Settings
	embedder interface {
		SearchScored(query string, topK int) []scoredDoc
		Index(source, content string)
	}
}

// --- Index cache types ---

type indexEntry struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Subject string `json:"subject"`
	At      string `json:"at"`
	Edges   []Edge `json:"edges,omitempty"`
}

type index struct {
	Version int                   `json:"version"`
	Facts   map[string]indexEntry `json:"facts"`
}

func (gm *GraphMemory) indexPath() string {
	return filepath.Join(gm.dir, "index.json")
}

// SetEmbedder wires the embedding store for semantic fallback in remember().
func (gm *GraphMemory) SetEmbedder(e interface {
	SearchScored(query string, topK int) []scoredDoc
	Index(source, content string)
}) {
	gm.embedder = e
}

func NewGraphMemory(dir string, cfg *Settings) *GraphMemory {
	os.MkdirAll(dir, 0755)
	gm := &GraphMemory{dir: dir, cfg: cfg, facts: make(map[string]*Fact)}
	if gm.loadIndex() {
		return gm
	}
	gm.loadAll()
	gm.saveIndex()
	return gm
}

// loadIndex reads the index.json cache. Returns false if missing or stale.
func (gm *GraphMemory) loadIndex() bool {
	data, err := os.ReadFile(gm.indexPath())
	if err != nil {
		return false
	}
	var idx index
	if err := json.Unmarshal(data, &idx); err != nil || idx.Version != 1 {
		return false
	}
	for id, entry := range idx.Facts {
		at, _ := time.Parse(time.RFC3339, entry.At)
		if at.IsZero() {
			at = time.Now()
		}
		gm.facts[id] = &Fact{
			ID:      entry.ID,
			Type:    entry.Type,
			Subject: entry.Subject,
			At:      at,
			Edges:   entry.Edges,
		}
	}
	return len(gm.facts) > 0
}

// saveIndex writes the current facts (minus bodies) to index.json.
func (gm *GraphMemory) saveIndex() {
	entries := make(map[string]indexEntry, len(gm.facts))
	for id, f := range gm.facts {
		entries[id] = indexEntry{
			ID:      f.ID,
			Type:    f.Type,
			Subject: f.Subject,
			At:      f.At.Format(time.RFC3339),
			Edges:   f.Edges,
		}
	}
	data, err := json.MarshalIndent(index{Version: 1, Facts: entries}, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(gm.indexPath(), data, 0644)
}

// loadAll scans memories/ and parses every .md file into memory,
// then discovers edges between unlinked facts.
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
	gm.discoverEdges()
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

	// Drop edges that reference non-existent facts
	fact.Edges = gm.validEdges(fact.Edges)

	if err := gm.writeFile(fact); err != nil {
		return err
	}
	// Auto-discover edges for new facts with none declared
	if len(fact.Edges) == 0 {
		gm.discoverEdgesFor(&fact)
	}
	gm.facts[fact.ID] = &fact
	gm.saveIndex()
	return nil
}

// validEdges filters edges to only those whose targets exist in gm.facts.
func (gm *GraphMemory) validEdges(edges []Edge) []Edge {
	if len(edges) == 0 {
		return edges
	}
	filtered := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := gm.facts[e.Target]; ok {
			filtered = append(filtered, e)
		}
	}
	return filtered
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

// fts5Entry finds matching fact IDs by substring search, with embedding fallback
// for vocabulary gaps (e.g., "programming philosophy" matching "coding style").
func (gm *GraphMemory) fts5Entry(query string) []string {
	results := gm.substringMatch(query)

	// If top hit is weak or no results, fall back to embedding search
	if (len(results) == 0 || len(results) < 2) && gm.embedder != nil {
		docs := gm.embedder.SearchScored(query, 5)
		seen := make(map[string]bool)
		for _, r := range results {
			seen[r] = true
		}
		for _, sd := range docs {
			if sd.score < 0.5 {
				continue
			}
			// Map embedded content back to fact ID
			for id, f := range gm.facts {
				candidate := f.Subject + ": " + f.Body
				if (candidate == sd.doc.Content || strings.Contains(strings.ToLower(f.Subject), strings.ToLower(query))) && !seen[id] {
					results = append(results, id)
					seen[id] = true
					break
				}
			}
		}
	}
	return results
}

// substringMatch finds fact IDs by matching individual query words against subjects.
func (gm *GraphMemory) substringMatch(query string) []string {
	type scored struct {
		id    string
		score int
	}
	var ranked []scored
	// Tokenize query into individual words
	queryWords := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if len(w) >= 2 {
			queryWords[w] = true
		}
	}
	for id, fact := range gm.facts {
		subj := strings.ToLower(fact.Subject)
		body := strings.ToLower(fact.Body)
		score := 0
		for w := range queryWords {
			if strings.Contains(subj, w) {
				score += 10
			}
			if strings.Contains(body, w) {
				score += 3
			}
		}
		// Bonus for exact subject match
		if subj == strings.ToLower(strings.TrimSpace(query)) {
			score += 100
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

// --- Edge discovery (startup pass) ---

// discoverEdges links unconnected facts by word-overlap similarity.
// Only runs for facts that have zero existing edges.
func (gm *GraphMemory) discoverEdges() {
	// Build word sets for all facts
	type indexed struct {
		id    string
		words map[string]struct{}
	}
	var facts []indexed
	for id, fact := range gm.facts {
		words := wordSet(fact.Subject + " " + fact.Body)
		facts = append(facts, indexed{id, words})
	}

	// Compare each fact with zero edges against all others
	for i, a := range facts {
		fact := gm.facts[a.id]
		if len(fact.Edges) > 0 {
			continue
		}
		for j, b := range facts {
			if i == j {
				continue
			}
			overlap := 0
			for w := range a.words {
				if _, ok := b.words[w]; ok {
					overlap++
				}
			}
			total := len(a.words) + len(b.words) - overlap
			if total == 0 {
				continue
			}
			jaccard := float64(overlap) / float64(total)
			if jaccard > 0.25 {
				fact.Edges = append(fact.Edges, Edge{Target: b.id, Rel: "related_to"})
			}
		}
		// Limit edges to avoid dense hubs
		if len(fact.Edges) > 8 {
			fact.Edges = fact.Edges[:8]
		}
		// Persist discovered edges to disk
		if len(fact.Edges) > 0 {
			gm.writeFile(*fact)
		}
	}
}

// wordSet returns a set of lowercase words (min 3 chars) from text.
func wordSet(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, w := range strings.Fields(strings.ToLower(text)) {
		if len(w) >= 3 {
			set[w] = struct{}{}
		}
	}
	return set
}

// discoverEdgesFor links a single new fact to existing facts by word overlap.
func (gm *GraphMemory) discoverEdgesFor(fact *Fact) {
	words := wordSet(fact.Subject + " " + fact.Body)
	for id, other := range gm.facts {
		if id == fact.ID {
			continue
		}
		otherWords := wordSet(other.Subject + " " + other.Body)
		overlap := 0
		for w := range words {
			if _, ok := otherWords[w]; ok {
				overlap++
			}
		}
		total := len(words) + len(otherWords) - overlap
		if total == 0 {
			continue
		}
		if float64(overlap)/float64(total) > 0.25 {
			fact.Edges = append(fact.Edges, Edge{Target: id, Rel: "related_to"})
		}
	}
	if len(fact.Edges) > 8 {
		fact.Edges = fact.Edges[:8]
	}
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
