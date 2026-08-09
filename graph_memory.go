package main

// graph_memory — graphify-style knowledge graph memory.
// One .md file per fact with YAML front matter carrying explicit edges.
// remember() traverses the graph; FTS5 provides the entry point.

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

	"gopkg.in/yaml.v3"
)

// --- Fact ---

type Fact struct {
	ID       string    `yaml:"id"`
	Type     string    `yaml:"type"` // "semantic" or "episodic"
	Subject  string    `yaml:"subject"`
	At       time.Time `yaml:"at"`
	Why      string    `yaml:"why,omitempty"`
	UseWhen  []string  `yaml:"use_when,omitempty"` // GLM-written trigger phrases (MEM-02)
	Source   string    `yaml:"source,omitempty"`
	Feedback int       `yaml:"feedback,omitempty"`
	Edges    []Edge    `yaml:"edge"`
	Body     string    `yaml:"-"` // everything after front matter
}

type Edge struct {
	Target     string  `yaml:"target" json:"target"`
	Rel        string  `yaml:"rel" json:"rel"`
	Kind       string  `yaml:"kind,omitempty" json:"kind,omitempty"`
	Confidence float64 `yaml:"confidence,omitempty" json:"confidence,omitempty"`
	Source     string  `yaml:"source,omitempty" json:"source,omitempty"`
}

// --- Graph memory ---

type GraphMemory struct {
	dir         string
	mu          sync.RWMutex
	facts       map[string]*Fact // id → Fact
	files       map[string]fileStamp
	judgedAt    map[string]string // id → RFC3339, empty = needs LLM edge judgment
	communities map[string]int
	gods        []string
	labels      map[string]string
	cfg         *Settings
	embedder    *EmbeddingStore
}

// --- Index cache types ---

type indexEntry struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Subject  string   `json:"subject"`
	At       string   `json:"at"`
	Why      string   `json:"why,omitempty"`
	UseWhen  []string `json:"use_when,omitempty"`
	Source   string   `json:"source,omitempty"`
	Feedback int      `json:"feedback,omitempty"`
	Edges    []Edge   `json:"edges,omitempty"`
	JudgedAt string   `json:"judged_at,omitempty"`
}

type fileStamp struct {
	ID      string `json:"id"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
}

type index struct {
	Version     int                   `json:"version"`
	Facts       map[string]indexEntry `json:"facts"`
	Files       map[string]fileStamp  `json:"files"`
	Communities map[string]int        `json:"communities,omitempty"`
	Gods        []string              `json:"gods,omitempty"`
	Labels      map[string]string     `json:"labels,omitempty"`
}

func (gm *GraphMemory) indexPath() string {
	return filepath.Join(gm.dir, "index.json")
}

// SetEmbedder wires the embedding store for semantic fallback in remember().
func (gm *GraphMemory) SetEmbedder(e *EmbeddingStore) {
	gm.embedder = e
}

func NewGraphMemory(dir string, cfg *Settings) *GraphMemory {
	os.MkdirAll(dir, 0755)
	gm := &GraphMemory{dir: dir, cfg: cfg, facts: make(map[string]*Fact), files: make(map[string]fileStamp), judgedAt: make(map[string]string), communities: make(map[string]int), labels: make(map[string]string)}
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
	if err := json.Unmarshal(data, &idx); err != nil || idx.Version != 3 || idx.Files == nil {
		return false
	}
	entries, err := os.ReadDir(gm.dir)
	if err != nil {
		return false
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		seen[entry.Name()] = true
		stamp, ok := idx.Files[entry.Name()]
		if !ok {
			return false
		}
		info, err := entry.Info()
		if err != nil || info.Size() != stamp.Size || info.ModTime().UnixNano() != stamp.ModTime {
			return false
		}
	}
	if len(seen) != len(idx.Files) {
		return false
	}
	if len(idx.Facts) != len(idx.Files) {
		return false
	}
	for _, stamp := range idx.Files {
		if _, ok := idx.Facts[stamp.ID]; !ok {
			return false
		}
	}
	cleaned := false
	for id, entry := range idx.Facts {
		at, _ := time.Parse(time.RFC3339, entry.At)
		if at.IsZero() {
			at = time.Now()
		}
		edges := cleanStoredEdges(entry.Edges)
		if !sameEdges(edges, entry.Edges) {
			cleaned = true
		}
		gm.facts[id] = &Fact{
			ID:       entry.ID,
			Type:     entry.Type,
			Subject:  entry.Subject,
			At:       at,
			Why:      entry.Why,
			UseWhen:  entry.UseWhen,
			Source:   entry.Source,
			Feedback: entry.Feedback,
			Edges:    edges,
		}
		gm.judgedAt[id] = entry.JudgedAt
		if loaded, err := gm.readFile(filepath.Join(gm.dir, entry.ID+".md")); err == nil {
			gm.facts[id].Body = loaded.Body
		}
	}
	gm.files = idx.Files
	gm.communities = idx.Communities
	gm.gods = idx.Gods
	gm.labels = idx.Labels
	if cleaned {
		for _, fact := range gm.facts {
			if err := gm.writeFile(*fact); err != nil {
				return false
			}
			gm.files[fact.ID+".md"] = gm.fileStamp(fact.ID)
		}
		gm.saveIndex()
	}
	return len(gm.facts) > 0
}

// saveIndex writes the current facts (minus bodies) to index.json.
func (gm *GraphMemory) saveIndex() {
	entries := make(map[string]indexEntry, len(gm.facts))
	for id, f := range gm.facts {
		entries[id] = indexEntry{
			ID:       f.ID,
			Type:     f.Type,
			Subject:  f.Subject,
			At:       f.At.Format(time.RFC3339),
			Why:      f.Why,
			UseWhen:  f.UseWhen,
			Source:   f.Source,
			Feedback: f.Feedback,
			Edges:    f.Edges,
			JudgedAt: gm.judgedAt[id],
		}
	}
	data, err := json.MarshalIndent(index{Version: 3, Facts: entries, Files: gm.files, Communities: gm.communities, Gods: gm.gods, Labels: gm.labels}, "", "  ")
	if err != nil {
		return
	}
	tmp := gm.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	if err := os.Rename(tmp, gm.indexPath()); err != nil {
		os.Remove(tmp)
	}
}

// loadAll scans memories/ and parses every .md file into memory,
// then discovers edges between unlinked facts.
func (gm *GraphMemory) loadAll() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.judgedAt = make(map[string]string)
	gm.communities = make(map[string]int)
	gm.labels = make(map[string]string)
	gm.gods = nil
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
		fact.Edges = cleanStoredEdges(fact.Edges)
		gm.facts[fact.ID] = fact
		gm.files[e.Name()] = gm.fileStamp(fact.ID)
	}
}

// Refresh reconciles changed Markdown files into the in-memory graph and index.
// It is deterministic and never calls an LLM.
func (gm *GraphMemory) Refresh() error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	return gm.refreshLocked()
}

func (gm *GraphMemory) refreshLocked() error {
	entries, err := os.ReadDir(gm.dir)
	if err != nil {
		return err
	}
	current := make(map[string]fileStamp)
	changed := false
	var firstErr error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		stamp, tracked := gm.files[entry.Name()]
		if tracked && stamp.Size == info.Size() && stamp.ModTime == info.ModTime().UnixNano() {
			current[entry.Name()] = stamp
			continue
		}
		path := filepath.Join(gm.dir, entry.Name())
		fact, err := gm.readFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("parse %s: %w", entry.Name(), err)
			}
			if tracked {
				current[entry.Name()] = stamp
			}
			continue
		}
		if tracked && stamp.ID != fact.ID {
			delete(gm.facts, stamp.ID)
			delete(gm.judgedAt, stamp.ID)
		}
		gm.judgedAt[fact.ID] = ""
		fact.Edges = cleanStoredEdges(fact.Edges)
		gm.facts[fact.ID] = fact
		current[entry.Name()] = fileStamp{ID: fact.ID, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		changed = true
	}
	for name, stamp := range gm.files {
		if _, ok := current[name]; !ok {
			delete(gm.facts, stamp.ID)
			delete(gm.judgedAt, stamp.ID)
			changed = true
		}
	}
	gm.files = current
	if changed {
		gm.saveIndex()
	}
	if firstErr != nil {
		slog.Warn("graph memory reconciliation", "error", firstErr)
	}
	return firstErr
}

// JudgedAt returns the RFC3339 timestamp of the last LLM edge judgment for a
// fact, or "" if the fact still needs judging.
func (gm *GraphMemory) JudgedAt(id string) string {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.judgedAt[id]
}

// MarkJudged records that a fact's edges have been judged by the LLM.
func (gm *GraphMemory) MarkJudged(id string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.judgedAt[id] = time.Now().UTC().Format(time.RFC3339)
	gm.saveIndex()
}

// UnjudgedFacts returns facts whose edges still need LLM judgment, sorted by ID.
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

// Communities returns a copy of the id → community map.
func (gm *GraphMemory) Communities() map[string]int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	out := make(map[string]int, len(gm.communities))
	for k, v := range gm.communities {
		out[k] = v
	}
	return out
}

// SetCommunities stores community membership, god-node IDs, and community
// labels in the index cache.
func (gm *GraphMemory) SetCommunities(communities map[string]int, gods []string, labels map[string]string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.communities = communities
	gm.gods = gods
	gm.labels = labels
	gm.saveIndex()
}

// StartReconciler keeps external Markdown edits visible while Mino runs.
func (gm *GraphMemory) StartReconciler(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	safeGo("graph-refresh", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			gm.Refresh()
		}
	})
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
		// Older writers emitted plain scalars such as `subject: A: B`.
		// Quote only known top-level scalar fields before retrying; bodies and
		// edge structure remain untouched.
		if repaired, ok := repairFrontMatter(fm); ok {
			if retryErr := yaml.Unmarshal([]byte(repaired), &fact); retryErr == nil {
				fact.Body = body
				return &fact, nil
			}
		}
		return nil, err
	}
	fact.Body = body
	return &fact, nil
}

func repairFrontMatter(fm string) (string, bool) {
	var out []string
	changed := false
	for _, line := range strings.Split(fm, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			out = append(out, line)
			continue
		}
		for _, key := range []string{"id", "type", "subject", "at", "why", "source"} {
			prefix := key + ":"
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if value != "" && !strings.HasPrefix(value, "\"") && !strings.HasPrefix(value, "'") && strings.Contains(value, ": ") {
				out = append(out, prefix+" "+fmt.Sprintf("%q", value))
				changed = true
			} else {
				out = append(out, line)
			}
			goto next
		}
		out = append(out, line)
	next:
	}
	return strings.Join(out, "\n"), changed
}

// RecordFact writes a Fact to disk. If the file exists and args say "merge",
// it reads the existing fact and merges edges + body. Otherwise overwrites.
func (gm *GraphMemory) RecordFact(fact Fact) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	if fact.At.IsZero() {
		fact.At = time.Now().UTC()
	}

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
		existing.Body = fact.Body
		existing.Why = fact.Why
		existing.Source = fact.Source
		existing.Feedback = fact.Feedback
		existing.At = fact.At
		fact = *existing
	}

	// Drop edges that reference non-existent facts
	fact.Edges = gm.validEdges(fact.Edges)

	if err := gm.writeFile(fact); err != nil {
		return err
	}
	gm.facts[fact.ID] = &fact
	gm.files[fact.ID+".md"] = gm.fileStamp(fact.ID)
	gm.saveIndex()
	return nil
}

// ReplaceFact overwrites a claim, including its edges, without touching SQLite.
func (gm *GraphMemory) ReplaceFact(fact Fact) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	if fact.At.IsZero() {
		fact.At = time.Now().UTC()
	}
	fact.Edges = gm.validEdges(fact.Edges)
	if err := gm.writeFile(fact); err != nil {
		return err
	}
	gm.facts[fact.ID] = &fact
	gm.files[fact.ID+".md"] = gm.fileStamp(fact.ID)
	gm.saveIndex()
	return nil
}

// UpdateBody changes only the body of an existing graph fact.
func (gm *GraphMemory) UpdateBody(id, body string) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	fact, ok := gm.facts[id]
	if !ok {
		return fmt.Errorf("memory fact not found: %s", id)
	}
	fact.Body = body
	if err := gm.writeFile(*fact); err != nil {
		return err
	}
	gm.files[id+".md"] = gm.fileStamp(id)
	gm.saveIndex()
	return nil
}

// FindFact resolves a graph claim by stable ID or exact subject.
func (gm *GraphMemory) FindFact(query string) (*Fact, bool) {
	gm.Refresh()
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	if fact, ok := gm.facts[query]; ok {
		copy := *fact
		return &copy, true
	}
	for _, fact := range gm.facts {
		if strings.EqualFold(fact.Subject, strings.TrimSpace(query)) {
			copy := *fact
			return &copy, true
		}
	}
	return nil, false
}

// DeleteFact removes a claim and all inbound references deterministically.
func (gm *GraphMemory) DeleteFact(id string) (*Fact, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	fact, ok := gm.facts[id]
	if !ok {
		return nil, fmt.Errorf("memory fact not found: %s", id)
	}
	copy := *fact
	for _, other := range gm.facts {
		if other.ID == id {
			continue
		}
		filtered := other.Edges[:0]
		for _, edge := range other.Edges {
			if edge.Target != id {
				filtered = append(filtered, edge)
			}
		}
		other.Edges = filtered
		if err := gm.writeFile(*other); err != nil {
			return nil, err
		}
		gm.files[other.ID+".md"] = gm.fileStamp(other.ID)
	}
	if err := os.Remove(filepath.Join(gm.dir, id+".md")); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	delete(gm.facts, id)
	delete(gm.files, id+".md")
	gm.saveIndex()
	return &copy, nil
}

// Feedback records confirmation or rejection on the graph claim itself.
// A negative signal is active expiry (MEM-08): the fact is archived
// immediately — no waiting for the judgment sweep.
func (gm *GraphMemory) Feedback(id string, delta int) (*Fact, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	fact, ok := gm.facts[id]
	if !ok {
		return nil, fmt.Errorf("memory fact not found: %s", id)
	}
	fact.Feedback += delta
	if fact.Feedback > 5 {
		fact.Feedback = 5
	}
	if fact.Feedback < -5 {
		fact.Feedback = -5
	}
	if fact.Feedback < 0 {
		copy := *fact
		return gm.archiveLocked(copy, "user rejection")
	}
	if err := gm.writeFile(*fact); err != nil {
		return nil, err
	}
	gm.files[id+".md"] = gm.fileStamp(id)
	gm.saveIndex()
	copy := *fact
	return &copy, nil
}

// --- Archive (MEM-08: why-expiry lifecycle) ---

// archiveDir holds expired facts: a subdirectory of the live memory dir.
// Every loader/reconciler skips directories, so archived .md files are
// invisible to the live graph without extra filtering.
func (gm *GraphMemory) archiveDir() string {
	return filepath.Join(gm.dir, "archive")
}

// ArchiveFact moves a fact out of the live graph into the archive — the same
// markdown-archive machinery the legacy migration uses, never deletion. The
// archived fact stays answerable through remember's archive fallback. reason
// names the archive cause (judgment expiry vs user rejection) for the digest.
func (gm *GraphMemory) ArchiveFact(fact Fact, reason string) (*Fact, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	return gm.archiveLocked(fact, reason)
}

func (gm *GraphMemory) archiveLocked(fact Fact, reason string) (*Fact, error) {
	if _, ok := gm.facts[fact.ID]; !ok {
		return nil, fmt.Errorf("memory fact not found: %s", fact.ID)
	}
	dir := gm.archiveDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	if err := writeMarkdownFact(filepath.Join(dir, fact.ID+".md"), fact); err != nil {
		return nil, err
	}
	for _, other := range gm.facts {
		if other.ID == fact.ID {
			continue
		}
		filtered := other.Edges[:0]
		for _, edge := range other.Edges {
			if edge.Target != fact.ID {
				filtered = append(filtered, edge)
			}
		}
		other.Edges = filtered
		if err := gm.writeFile(*other); err != nil {
			return nil, err
		}
		gm.files[other.ID+".md"] = gm.fileStamp(other.ID)
	}
	if err := os.Remove(filepath.Join(gm.dir, fact.ID+".md")); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	delete(gm.facts, fact.ID)
	delete(gm.files, fact.ID+".md")
	delete(gm.judgedAt, fact.ID)
	gm.saveIndex()

	copy := fact
	gm.appendPendingDigestLocked(fmt.Sprintf("%s|%s|%s", fact.ID, oneLine(fact.Subject, 120), reason))
	return &copy, nil
}

// appendPendingDigestLocked queues one digest line (outbox pattern: the
// daily digest drains it; a failed send puts it back).
func (gm *GraphMemory) appendPendingDigestLocked(line string) {
	path := filepath.Join(gm.archiveDir(), "digest-pending.txt")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line + "\n")
}

// TakePendingDigest returns the queued archive-digest lines and clears the
// queue. The caller delivers them; on delivery failure they must be put back
// with AppendPendingDigest.
func (gm *GraphMemory) TakePendingDigest() []string {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	path := filepath.Join(gm.archiveDir(), "digest-pending.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	os.Remove(path)
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// AppendPendingDigest puts digest lines back after a failed delivery.
func (gm *GraphMemory) AppendPendingDigest(lines []string) {
	if len(lines) == 0 {
		return
	}
	gm.mu.Lock()
	defer gm.mu.Unlock()
	for _, l := range lines {
		gm.appendPendingDigestLocked(l)
	}
}

// archiveFactsLocked loads archived facts as a scan of the archive directory.
// Caller holds RLock.
func (gm *GraphMemory) archiveFactsLocked() map[string]*Fact {
	out := make(map[string]*Fact)
	entries, err := os.ReadDir(gm.archiveDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if fact, err := gm.readFile(filepath.Join(gm.archiveDir(), e.Name())); err == nil {
			out[fact.ID] = fact
		}
	}
	return out
}

// RemoveMutualInferredEdges resolves mirrored inferred pairs (A→B and B→A,
// any relation): the lower-confidence edge is dropped, explicit edges win.
// Equal-confidence mirrors are intentionally both kept — the strict >
// comparison keeps ties: there is no signal to prefer one direction, and
// dropping both would lose information.
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

func cleanStoredEdges(edges []Edge) []Edge {
	cleaned := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		if edge.Target == "" || edge.Rel == "" {
			continue
		}
		if edge.Rel == "related_to" && edge.Kind == "" {
			continue
		}
		if edge.Kind == "inferred" && edge.Confidence < 0.85 {
			continue
		}
		if edge.Kind == "" {
			edge.Kind = "explicit"
			if edge.Confidence == 0 {
				edge.Confidence = 1
			}
			if edge.Source == "" {
				edge.Source = "legacy"
			}
		}
		cleaned = append(cleaned, edge)
	}
	return cleaned
}

func sameEdges(a, b []Edge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// writeFile serializes a Fact to a .md file.
func (gm *GraphMemory) writeFile(fact Fact) error {
	path := filepath.Join(gm.dir, fact.ID+".md")
	return writeMarkdownFact(path, fact)
}

func writeMarkdownFact(path string, fact Fact) error {
	front := struct {
		ID       string    `yaml:"id"`
		Type     string    `yaml:"type"`
		Subject  string    `yaml:"subject"`
		At       time.Time `yaml:"at"`
		Why      string    `yaml:"why,omitempty"`
		UseWhen  []string  `yaml:"use_when,omitempty"`
		Source   string    `yaml:"source,omitempty"`
		Feedback int       `yaml:"feedback,omitempty"`
		Edges    []Edge    `yaml:"edge,omitempty"`
	}{fact.ID, fact.Type, fact.Subject, fact.At, fact.Why, fact.UseWhen, fact.Source, fact.Feedback, fact.Edges}
	fm, err := yaml.Marshal(front)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if fact.Body != "" {
		b.WriteString("\n")
		b.WriteString(fact.Body)
		b.WriteString("\n")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (gm *GraphMemory) fileStamp(id string) fileStamp {
	info, err := os.Stat(filepath.Join(gm.dir, id+".md"))
	if err != nil {
		return fileStamp{ID: id}
	}
	return fileStamp{ID: id, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

// thinLiveScore: live recall below this (less than one strong signal word —
// subject/why/use_when words score 10) counts as thin and triggers the
// archive fallback (MEM-08).
const thinLiveScore = 10

// Remember is the graph-aware recall tool. Returns an indented tree with each
// matched fact's why, body, and match rationale, plus its graph neighborhood.
//
//	remember("Procura")
//	→ procura_is_authoritative
//	  why: The system of record for procurement.
//	  matched: subject (procura); use_when (procurement)
//	  → [supersedes] procurepilot_is_legacy
//	  → [depends_on] procura_db_location
//	    → [located_at] vps_server
//
// turn is the user's active turn text; its words also score against why/use_when
// (MEM-03: the intent signals are co-authored recall triggers, not just the query).
// When live recall comes up empty or thin, Remember falls back to the archive
// and tags every hit [archived] (MEM-08) — expired facts stay answerable, but
// the marker keeps the timing honest.
func (gm *GraphMemory) Remember(query, turn string) string {
	gm.Refresh()
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	// Step 1: merged ranking — substring + why/use_when overlap (query & turn),
	// with embedding similarity filling thin results (see entryRanking).
	starts := gm.entryRanking(query, turn, gm.facts, true)
	facts := gm.facts
	fromArchive := false
	if len(starts) == 0 || starts[0].score < thinLiveScore {
		if archive := gm.archiveFactsLocked(); len(archive) > 0 {
			if hits := gm.entryRanking(query, turn, archive, false); len(hits) > 0 {
				starts = hits
				facts = archive
				fromArchive = true
			}
		}
	}
	if len(starts) == 0 {
		return fmt.Sprintf("No memories found for: %s", query)
	}
	if len(starts) > 3 {
		starts = starts[:3]
	}

	// Step 2: BFS traversal from start nodes
	maxDepth := 2
	if gm.cfg != nil && gm.cfg.TopK > 0 {
		maxDepth = gm.cfg.TopK
	}
	visited := make(map[string]bool)
	var lines []string
	if fromArchive {
		lines = append(lines, "[archived] — no current memories matched; showing archived facts")
	}

	for _, start := range starts {
		fact, ok := facts[start.id]
		if !ok {
			continue
		}
		label := fact.Subject
		if fromArchive {
			label += "  [archived]"
		}
		lines = append(lines, label+"  # "+fact.ID)
		if fact.Why != "" {
			lines = append(lines, "  why: "+oneLine(fact.Why, 160))
		}
		if fact.Body != "" {
			lines = append(lines, "  body: "+oneLine(fact.Body, 200))
		}
		if len(start.signals) > 0 {
			lines = append(lines, "  matched: "+strings.Join(start.signals, "; "))
		}
		visited[fact.ID] = true
		gm.bfsEdges(fact, "  ", 1, maxDepth, visited, &lines)
		gm.bfsInbound(fact, "  ", 1, maxDepth, visited, &lines)
	}

	if len(lines) == 0 {
		return fmt.Sprintf("No memories found for: %s", query)
	}
	return strings.Join(lines, "\n")
}

// oneLine flattens a fact field to a single space-joined line, truncated to max
// runes at a word boundary (MEM-04 token budget per result).
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)[:max]
	if i := strings.LastIndex(string(runes), " "); i > max/2 {
		runes = runes[:i]
	}
	return string(runes) + "…"
}

// bfsEdges traverses edges recursively, depth-limited.
func (gm *GraphMemory) bfsEdges(fact *Fact, indent string, depth, maxDepth int, visited map[string]bool, lines *[]string) {
	if depth > maxDepth {
		return
	}
	nextIndent := indent + "  "
	for _, edge := range fact.Edges {
		if !edgeTraversable(edge) {
			continue
		}
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

func (gm *GraphMemory) bfsInbound(fact *Fact, indent string, depth, maxDepth int, visited map[string]bool, lines *[]string) {
	if depth > maxDepth {
		return
	}
	nextIndent := indent + "  "
	for _, source := range gm.facts {
		for _, edge := range source.Edges {
			if edge.Target != fact.ID || !edgeTraversable(edge) {
				continue
			}
			if visited[source.ID] {
				continue
			}
			visited[source.ID] = true
			*lines = append(*lines, fmt.Sprintf("%s← [%s] %s  # %s", indent, edge.Rel, source.Subject, source.ID))
			gm.bfsInbound(source, nextIndent, depth+1, maxDepth, visited, lines)
			gm.bfsEdges(source, nextIndent, depth+1, maxDepth, visited, lines)
		}
	}
}

func edgeTraversable(edge Edge) bool {
	if edge.Kind == "ambiguous" {
		return false
	}
	return edge.Kind != "inferred" || edge.Confidence >= 0.85
}

// entryRanking scores every fact in the given set on three free signals —
// substring match on subject/body, why/use_when overlap with the query, and
// why/use_when overlap with the active turn — then merges embedding similarity
// (20×cosine) when the free ranking leaves room in the top-3. Weights: subject
// word 10, body word 3, exact subject 100, why/use_when word 10 per signal.
// Returns matches best-first with the per-signal breakdown (MEM-04 renders it
// as the match rationale). useEmbedder gates the embedding merge: it only
// applies to the live graph (archived facts carry no vectors).
func (gm *GraphMemory) entryRanking(query, turn string, facts map[string]*Fact, useEmbedder bool) []rankedFact {
	queryWords := memoryTokenize(query)
	turnWords := memoryTokenize(turn)
	var ranked []rankedFact
	for id, fact := range facts {
		score := 0
		subj := strings.ToLower(fact.Subject)
		body := strings.ToLower(fact.Body)
		useWhen := strings.ToLower(strings.Join(fact.UseWhen, " "))
		why := strings.ToLower(fact.Why)
		var signals []string

		// Exact subject match bonus
		if subj == strings.ToLower(strings.TrimSpace(query)) {
			score += 100
			signals = append(signals, "exact subject")
		}
		if sw := matchedWords(queryWords, subj); len(sw) > 0 {
			score += 10 * len(sw)
			signals = append(signals, "subject: "+strings.Join(sw, ", "))
		}
		if bw := matchedWords(queryWords, body); len(bw) > 0 {
			score += 3 * len(bw)
			signals = append(signals, "body: "+strings.Join(bw, ", "))
		}
		// Intent overlap: why/use_when are written to match the questions that
		// should recall this fact (MEM-02), so a word there is worth a subject word.
		if uw := matchedWords(queryWords, useWhen); len(uw) > 0 {
			score += 10 * len(uw)
			signals = append(signals, "use_when: "+strings.Join(uw, ", "))
		}
		if wy := matchedWords(queryWords, why); len(wy) > 0 {
			score += 10 * len(wy)
			signals = append(signals, "why: "+strings.Join(wy, ", "))
		}
		// The active turn's words against the same intent text (MEM-03).
		if tw := matchedWords(turnWords, useWhen+" "+why); len(tw) > 0 {
			score += 10 * len(tw)
			signals = append(signals, "your words: "+strings.Join(tw, ", "))
		}
		if score > 0 {
			ranked = append(ranked, rankedFact{id: id, score: score, signals: signals})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

	// Embedding similarity fills vocabulary gaps only when the free ranking is
	// thin (room in the top-3) — keeps the per-remember embed API call bounded.
	if len(ranked) < 3 && useEmbedder && gm.embedder != nil {
		for _, sd := range gm.embedder.SearchScored(query, 8) {
			if sd.score < 0.5 {
				continue
			}
			id := ""
			if strings.HasPrefix(sd.doc.Source, "fact:") {
				id = strings.TrimPrefix(sd.doc.Source, "fact:")
			} else {
				for fid, f := range facts { // legacy "fact" sources: map by content
					if f.Subject+": "+f.Body == sd.doc.Content {
						id = fid
						break
					}
				}
			}
			if _, ok := facts[id]; !ok {
				continue
			}
			embScore := int(20 * sd.score)
			found := false
			for i := range ranked {
				if ranked[i].id == id {
					ranked[i].score += embScore
					ranked[i].signals = append(ranked[i].signals, fmt.Sprintf("similarity: %.2f", sd.score))
					found = true
					break
				}
			}
			if !found {
				ranked = append(ranked, rankedFact{id: id, score: embScore, signals: []string{fmt.Sprintf("similarity: %.2f", sd.score)}})
			}
		}
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	}
	return ranked
}

// rankedFact is one recall match: its merged score and the per-signal word
// breakdown that becomes the "matched" rationale line (MEM-04).
type rankedFact struct {
	id      string
	score   int
	signals []string
}

// matchedWords returns the query/turn words contained in text, sorted — the
// deterministic signal breakdown for the match rationale.
func matchedWords(words map[string]bool, text string) []string {
	var out []string
	for w := range words {
		if strings.Contains(text, w) {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// memoryTokenize splits text into lowercase word keys: len>=3 (2-letter words
// like "me"/"is" match inside longer words — "home", "memory", "misbehaves" —
// and only add noise), punctuation stripped, common filler dropped (reuses the
// tool-selection stopword set).
func memoryTokenize(s string) map[string]bool {
	words := make(map[string]bool)
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,!?;:'\"()[]-—")
		if len(w) >= 3 && !toolSearchStopWords[w] {
			words[w] = true
		}
	}
	return words
}

// RememberPath finds shortest path between two facts. Returns indented path.
func (gm *GraphMemory) RememberPath(from, to string) string {
	gm.Refresh()
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

// Facts returns a stable snapshot for APIs and dashboards.
func (gm *GraphMemory) Facts() []Fact {
	gm.Refresh()
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	facts := make([]Fact, 0, len(gm.facts))
	for _, fact := range gm.facts {
		copy := *fact
		if copy.Body == "" {
			if loaded, err := gm.readFile(filepath.Join(gm.dir, copy.ID+".md")); err == nil {
				copy.Body = loaded.Body
			}
		}
		facts = append(facts, copy)
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].ID < facts[j].ID })
	return facts
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

type legacyManifest struct {
	Version int                          `json:"version"`
	Facts   map[string]legacyFactMapping `json:"facts"`
}

type legacyFactMapping struct {
	SQLiteID    int64  `json:"sqlite_id"`
	Archive     string `json:"archive"`
	CanonicalID string `json:"canonical_id"`
	Status      string `json:"status"`
}

type MigrationReport struct {
	Archived      int
	Canonicalized int
	Duplicates    int
}

// MigrateLegacyFacts preserves every SQLite fact in an inactive archive and
// maps it to a collision-safe active graph node. SQLite is never modified.
func MigrateLegacyFacts(db *sql.DB, home, memoriesDir string) (MigrationReport, error) {
	var report MigrationReport
	if db == nil {
		return report, fmt.Errorf("nil database")
	}
	archiveDir := filepath.Join(home, "memory-migration", "legacy")
	manifestPath := filepath.Join(home, "memory-migration", "manifest.json")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return report, err
	}
	if err := os.MkdirAll(memoriesDir, 0755); err != nil {
		return report, err
	}
	manifest := legacyManifest{Version: 1, Facts: make(map[string]legacyFactMapping)}
	if data, err := os.ReadFile(manifestPath); err == nil {
		if json.Unmarshal(data, &manifest) != nil || manifest.Version != 1 || manifest.Facts == nil {
			manifest = legacyManifest{Version: 1, Facts: make(map[string]legacyFactMapping)}
		}
	}
	gm := NewGraphMemory(memoriesDir, nil)
	// Fresh installs have no legacy SQLite facts table; nothing to migrate.
	var hasFacts bool
	if err := db.QueryRow("SELECT 1 FROM sqlite_master WHERE type='table' AND name='facts'").Scan(&hasFacts); err != nil || !hasFacts {
		return report, nil
	}
	rows, err := db.Query("SELECT id, subject, content, source, created_at FROM facts ORDER BY id")
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var subject, content, source, createdAt string
		if err := rows.Scan(&id, &subject, &content, &source, &createdAt); err != nil {
			return report, err
		}
		archiveName := fmt.Sprintf("fact_%d.md", id)
		archiveFact := Fact{ID: fmt.Sprintf("legacy_fact_%d", id), Type: "semantic", Subject: subject, At: parseMemoryTime(createdAt), Why: fmt.Sprintf("sqlite:fact:%d", id), Source: source, Body: content}
		archivePath := filepath.Join(archiveDir, archiveName)
		if _, err := os.Stat(archivePath); os.IsNotExist(err) {
			if err := writeMarkdownFact(archivePath, archiveFact); err != nil {
				return report, err
			}
			report.Archived++
		}

		mapping, mapped := manifest.Facts[fmt.Sprint(id)]
		if mapped && mapping.CanonicalID != "" && graphHasFact(gm, mapping.CanonicalID) {
			continue
		}
		canonicalID, duplicate := migrationCanonicalID(gm, subject, content, id)
		if duplicate {
			manifest.Facts[fmt.Sprint(id)] = legacyFactMapping{SQLiteID: id, Archive: archiveName, CanonicalID: canonicalID, Status: "duplicate"}
			report.Duplicates++
			continue
		}
		fact := archiveFact
		fact.ID = canonicalID
		if err := gm.RecordFact(fact); err != nil {
			return report, err
		}
		manifest.Facts[fmt.Sprint(id)] = legacyFactMapping{SQLiteID: id, Archive: archiveName, CanonicalID: canonicalID, Status: "canonical"}
		report.Canonicalized++
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return report, err
	}
	if err := writeAtomic(manifestPath, data, 0644); err != nil {
		return report, err
	}
	return report, nil
}

func graphHasFact(gm *GraphMemory, id string) bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	_, ok := gm.facts[id]
	return ok
}

func migrationCanonicalID(gm *GraphMemory, subject, body string, sqliteID int64) (string, bool) {
	base := slugify(subject)
	if base == "" {
		base = fmt.Sprintf("legacy_fact_%d", sqliteID)
	}
	candidate := base
	for n := 0; ; n++ {
		gm.mu.RLock()
		existing := gm.facts[candidate]
		gm.mu.RUnlock()
		if existing == nil {
			return candidate, false
		}
		existingBody := existing.Body
		if existingBody == "" {
			if loaded, err := gm.readFile(filepath.Join(gm.dir, candidate+".md")); err == nil {
				existingBody = loaded.Body
			}
		}
		if existing.Subject == subject && existingBody == body {
			return candidate, true
		}
		candidate = fmt.Sprintf("%s__legacy_%d", base, sqliteID)
		if n > 0 {
			candidate = fmt.Sprintf("%s_%d", candidate, n+1)
		}
	}
}

func parseMemoryTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at
		}
	}
	return time.Now().UTC()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// MigrateMemories is the explicit migration command. Episodes remain in
// SQLite as operational history; only durable facts enter the graph.
func MigrateMemories(home, memoriesDir string) {
	db := Connect(home)
	defer db.Close()
	report, err := MigrateLegacyFacts(db, home, memoriesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Memory migration failed: %v\n", err)
		return
	}
	fmt.Printf("Archived %d facts; canonicalized %d; duplicates %d\n", report.Archived, report.Canonicalized, report.Duplicates)
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
