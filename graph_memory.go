package main

// graph_memory — graphify-style knowledge graph memory.
// One .md file per fact with YAML front matter carrying explicit edges.
// remember() traverses the graph; FTS5 provides the entry point.

import (
	"sync/atomic"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// --- Fact ---

type Fact struct {
	ID         string    `yaml:"id"`
	Type       string    `yaml:"type"` // "semantic" or "episodic"
	Subject    string    `yaml:"subject"`
	At         time.Time `yaml:"at"`
	StaleAfter time.Time `yaml:"stale_after,omitempty"` // DRF-002: volatile facts declare their own expiry ("current X" facts)
	Why        string    `yaml:"why,omitempty"`
	UseWhen    []string  `yaml:"use_when,omitempty"` // GLM-written trigger phrases (MEM-02)
	Source     string    `yaml:"source,omitempty"`
	Feedback   int       `yaml:"feedback,omitempty"`
	Edges      []Edge    `yaml:"edge"`
	Body       string    `yaml:"-"` // everything after front matter
}

// userProvenancedSource reports whether a Source value marks user authorship:
// save_note stamps "user" (issue #178); corrections stamp "user-correction"
// with an optional date suffix (e.g. "user-correction-20260812").
func userProvenancedSource(s string) bool {
	return s == "user" || strings.HasPrefix(s, "user-correction")
}

// playbookDepth counts nested playbook stage runs; save_note consults it to
// stamp model-distill facts instead of user (DRF-002).
var playbookDepth atomic.Int32

// authoritativeSource reports whether a Source value marks human or agent
// authorship — the class that is never auto-staled (DRF-002). Agent
// corrections are stamped "agent-correction-YYYYMMDD" (formalized 2026-08-14
// after an agent-authored correction was witnessed live).
func authoritativeSource(s string) bool {
	return userProvenancedSource(s) || strings.HasPrefix(s, "agent-correction")
}

// correctionSource reports whether a Source marks an explicit correction
// (user-correction-* or agent-correction-*). Only corrections demote
// conflicting model facts — a plain user save states new knowledge, it does
// not claim the old one is wrong (DRF-002).
func correctionSource(s string) bool {
	return strings.HasPrefix(s, "user-correction") || strings.HasPrefix(s, "agent-correction")
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
	parseWarned map[string]bool // ids already warned on (once per process run)
	cfg         *Settings
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

func NewGraphMemory(dir string, cfg *Settings) *GraphMemory {
	os.MkdirAll(dir, 0755)
	gm := &GraphMemory{dir: dir, cfg: cfg, facts: make(map[string]*Fact), files: make(map[string]fileStamp), judgedAt: make(map[string]string), communities: make(map[string]int), labels: make(map[string]string), parseWarned: make(map[string]bool)}
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
		// Lenient timestamp (self-healing): a malformed `at:` must not drop the
		// whole fact — load it with At zeroed and warn once; the rebuild pass
		// stamps a valid timestamp on the next write (ReplaceFact sets now on
		// zero At). A fact with a wrong clock is still a fact.
		if strings.Contains(err.Error(), "cannot parse") {
			if f, ok := gm.unmarshalLenientAt(fm); ok {
				f.Body = body
				gm.warnOnce(f.ID, fmt.Sprintf("graph memory: malformed at: timestamp on %s, loaded with zero time (self-heals on next rebuild): %v", f.ID, err))
				return f, nil
			}
		}
		return nil, err
	}
	fact.Body = body
	return &fact, nil
}

// unmarshalLenientAt parses front matter with `at` read as a raw string, so a
// malformed timestamp cannot fail the whole unmarshal. Returns false when the
// front matter is broken in any other way (those stay strict).
func (gm *GraphMemory) unmarshalLenientAt(fm string) (*Fact, bool) {
	var raw struct {
		ID       string `yaml:"id"`
		Type     string `yaml:"type"`
		Subject  string `yaml:"subject"`
		At       string `yaml:"at"`
		Why      string `yaml:"why,omitempty"`
		Source   string `yaml:"source,omitempty"`
		Feedback int    `yaml:"feedback,omitempty"`
		Edges    []Edge `yaml:"edge"`
	}
	if err := yaml.Unmarshal([]byte(fm), &raw); err != nil {
		return nil, false
	}
	var at time.Time
	if raw.At != "" {
		if t, err := time.Parse(time.RFC3339, raw.At); err == nil {
			at = t
		}
	}
	return &Fact{ID: raw.ID, Type: raw.Type, Subject: raw.Subject, At: at, Why: raw.Why, Source: raw.Source, Feedback: raw.Feedback, Edges: raw.Edges}, true
}

// warnOnce logs a per-fact warning only once per process run — the reconciler
// re-parses every file every 5s, so a persistent file problem must not flood
// the log. Callers already hold gm.mu (parseFrontMatter runs under Refresh/
// loadAll), so this never locks.
func (gm *GraphMemory) warnOnce(id, msg string) {
	if gm.parseWarned == nil {
		gm.parseWarned = make(map[string]bool)
	}
	if gm.parseWarned[id] {
		return
	}
	gm.parseWarned[id] = true
	slog.Warn(msg)
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

	// A caller (save_note) sometimes embeds the whole file — front-matter
	// included — in the body, which would produce a double front-matter and
	// break indexing (observed 2026-08-14 twice, both times self-healed by the
	// model; the harness should never write the bug). A body that opens with a
	// YAML front-matter block is never legitimate prose.
	fact.Body = stripLeadingFrontMatter(fact.Body)

	// DRF-002: an explicit correction (user-correction / agent-correction)
	// demotes conflicting model-authored facts on the same subject — the
	// asymmetry stays: a model re-entry never touches other facts, and a plain
	// user save states new knowledge without claiming the old is wrong. The
	// correction's own write happens below (it is not in gm.facts yet), so the
	// scan only sees pre-existing facts.
	if correctionSource(fact.Source) && fact.Subject != "" {
		subjectWords := memoryTokenize(fact.Subject)
		var victims []*Fact
		for _, other := range gm.facts {
			if authoritativeSource(other.Source) {
				continue
			}
			if subjectOverlap(subjectWords, memoryTokenize(other.Subject)) >= 2 {
				victims = append(victims, other)
			}
		}
		for _, v := range victims {
			gm.archiveLocked(*v, "superseded") // archiveLocked removes from the live map itself
		}
	}

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

// ArchiveExpiredEpisodic moves episodic facts older than cutoff into the
// archive with reason "expiry" (issue #178: procedural facts age out after
// 30 days; semantic facts never expire). Zero At is skipped — unknown age is
// not old age. Returns the number archived.
func (gm *GraphMemory) ArchiveExpiredEpisodic(cutoff time.Time) int {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	var expired []Fact
	for _, f := range gm.facts {
		if f.Type == "episodic" && !f.At.IsZero() && f.At.Before(cutoff) {
			expired = append(expired, *f)
		}
	}
	archived := 0
	for _, f := range expired {
		if _, err := gm.archiveLocked(f, "expiry"); err == nil {
			archived++
		}
	}
	return archived
}

// ArchiveStaleSemantic archives model-authored semantic facts past their
// staleness point with reason "stale" (DRF-002). The staleness point is the
// fact's declared stale_after when set (volatile facts expire on their own
// date), else its At past the 30d backstop. Authoritative facts (user /
// user-correction / agent-correction) are never auto-staled. Archived facts
// stay answerable via remember's archive fallback — knowledge is demoted,
// never destroyed. Returns the number archived.
func (gm *GraphMemory) ArchiveStaleSemantic(cutoff time.Time) int {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	now := time.Now()
	var stale []Fact
	for _, f := range gm.facts {
		if f.Type != "semantic" || authoritativeSource(f.Source) {
			continue
		}
		if !f.StaleAfter.IsZero() {
			if now.After(f.StaleAfter) {
				stale = append(stale, *f)
			}
		} else if !f.At.IsZero() && f.At.Before(cutoff) {
			stale = append(stale, *f)
		}
	}
	n := 0
	for _, f := range stale {
		if _, err := gm.archiveLocked(f, "stale"); err == nil {
			n++
		}
	}
	return n
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

// userProvenanced reports whether the fact was authored by the user.
func (gm *GraphMemory) userProvenanced(id string) bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	f, ok := gm.facts[id]
	return ok && userProvenancedSource(f.Source)
}

// RemoveSupersedesIntoUserFacts drops inferred supersedes edges whose target
// is user-provenanced — the one-off repair for inverted edges the rebuild
// wrote before the guard existed (issue #180). Runs from clean-memory-edges.
func (gm *GraphMemory) RemoveSupersedesIntoUserFacts() int {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	removed := 0
	for _, fact := range gm.facts {
		filtered := fact.Edges[:0]
		for _, edge := range fact.Edges {
			target := gm.facts[edge.Target]
			if edge.Kind == "inferred" && edge.Rel == "supersedes" && target != nil && userProvenancedSource(target.Source) {
				removed++
				continue
			}
			filtered = append(filtered, edge)
		}
		if len(filtered) != len(fact.Edges) {
			fact.Edges = filtered
			if err := gm.writeFile(*fact); err == nil {
				gm.files[fact.ID+".md"] = gm.fileStamp(fact.ID)
			}
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
		ID         string    `yaml:"id"`
		Type       string    `yaml:"type"`
		Subject    string    `yaml:"subject"`
		At         time.Time `yaml:"at"`
		StaleAfter time.Time `yaml:"stale_after,omitempty"`
		Why        string    `yaml:"why,omitempty"`
		UseWhen    []string  `yaml:"use_when,omitempty"`
		Source     string    `yaml:"source,omitempty"`
		Feedback   int       `yaml:"feedback,omitempty"`
		Edges      []Edge    `yaml:"edge,omitempty"`
	}{fact.ID, fact.Type, fact.Subject, fact.At, fact.StaleAfter, fact.Why, fact.UseWhen, fact.Source, fact.Feedback, fact.Edges}
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

// CTX-014 freshness thresholds: live recall surfaces a fact's age so the model
// doesn't trust a stale-but-unrejected fact blindly (the FB photo-post incident
// rode a week-old URL). freshGrace avoids noise on new facts; staleAgeThreshold
// flags facts old enough to warrant a re-check. The At field already exists on
// every Fact — this only wires it into the rationale; ranking score is untouched.
const freshGrace = 24 * time.Hour
const staleAgeThreshold = 30 * 24 * time.Hour

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
	markConflictSignals(starts, facts)

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

// urlHostRe captures the host of an http(s) URL — the unit the conflict
// marker compares (issue #180).
var urlHostRe = regexp.MustCompile(`https?://([^/\s"'\)\]>]+)`)

// markConflictSignals flags pairs of top-ranked facts that either carry
// different URL domains (issue #180) or share a subject cluster with
// materially different bodies (DRF-002): each gets "⚠ conflicts with <id>" in
// its match rationale so the brain arbitrates the contradiction instead of
// trusting rank. Subject overlap is loose by design — a false flag costs one
// glance, a missed contradiction costs a wrong fact being trusted.
func markConflictSignals(starts []rankedFact, facts map[string]*Fact) {
	hosts := make(map[string]map[string]bool, len(starts))
	subjects := make(map[string]map[string]bool, len(starts))
	for _, s := range starts {
		if f, ok := facts[s.id]; ok {
			hosts[s.id] = extractHosts(f.Subject + " " + f.Body)
			subjects[s.id] = memoryTokenize(f.Subject)
		}
	}
	for i := range starts {
		for j := i + 1; j < len(starts); j++ {
			hi, hj := hosts[starts[i].id], hosts[starts[j].id]
			if len(hi) > 0 && len(hj) > 0 && !shareAny(hi, hj) {
				starts[i].signals = append(starts[i].signals, "⚠ conflicts with "+starts[j].id)
				starts[j].signals = append(starts[j].signals, "⚠ conflicts with "+starts[i].id)
				continue
			}
			// DRF-002: same-subject contradiction — two top facts sharing >= 2
			// significant subject words with materially different bodies.
			si, sj := subjects[starts[i].id], subjects[starts[j].id]
			if len(si) > 0 && len(sj) > 0 && subjectOverlap(si, sj) >= 2 {
				fi, fok := facts[starts[i].id]
				fj, gok := facts[starts[j].id]
				if fok && gok && materiallyDifferent(fi, fj) {
					starts[i].signals = append(starts[i].signals, "⚠ conflicts with "+starts[j].id)
					starts[j].signals = append(starts[j].signals, "⚠ conflicts with "+starts[i].id)
				}
			}
		}
	}
}

// subjectOverlap counts shared significant words between two subject token sets.
func subjectOverlap(a, b map[string]bool) int {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

// materiallyDifferent reports whether two facts differ in value: distinct
// non-empty bodies, neither containing the other.
func materiallyDifferent(a, b *Fact) bool {
	ba := strings.ToLower(strings.TrimSpace(a.Body))
	bb := strings.ToLower(strings.TrimSpace(b.Body))
	if ba == "" || bb == "" {
		return a.Subject != b.Subject
	}
	return ba != bb && !strings.Contains(ba, bb) && !strings.Contains(bb, ba)
}

func extractHosts(s string) map[string]bool {
	hosts := make(map[string]bool)
	for _, m := range urlHostRe.FindAllStringSubmatch(strings.ToLower(s), -1) {
		hosts[m[1]] = true
	}
	return hosts
}

func shareAny(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
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
// why/use_when overlap with the active turn. Weights: subject word 10, body
// word 3, exact subject 100, why/use_when word 10 per signal. Returns matches
// best-first with the per-signal breakdown (MEM-04 renders it as the match
// rationale). liveGraph gates the CTX-014 age signal: it only applies to live
// recall (archived facts carry no freshness promise).
func (gm *GraphMemory) entryRanking(query, turn string, facts map[string]*Fact, liveGraph bool) []rankedFact {
	queryWords := memoryTokenize(query)
	turnWords := memoryTokenize(turn)
	var ranked []rankedFact
	for id, fact := range facts {
		// Procedural facts are traversal-only: they stay visible as BFS
		// neighborhood context but never start a recall (issue #178).
		if fact.Type == "episodic" {
			continue
		}
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
		// CTX-014: surface recency so a stale-but-unrejected fact isn't trusted
		// blindly. Gated to the live graph; zero At is skipped.
		if liveGraph && !fact.At.IsZero() {
			if age := time.Since(fact.At); age >= freshGrace {
				sig := fmt.Sprintf("age: %dd", int(age.Hours()/24))
				if age > staleAgeThreshold {
					sig += " (possibly stale)"
				}
				signals = append(signals, sig)
			}
		}
		// issue #180: user authorship outranks model re-entry of the same
		// knowledge — a correction must not lose to a newer distill fact.
		if userProvenancedSource(fact.Source) {
			score += 30
			signals = append(signals, "user-provenanced")
		}
		if score > 0 {
			ranked = append(ranked, rankedFact{id: id, score: score, signals: signals})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })

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

// stripLeadingFrontMatter removes a YAML front-matter block that a caller
// mistakenly embedded at the start of a fact body. The graph's own writer
// emits the front-matter; a body that opens with "---\n" is a double-write
// (observed 2026-08-14 via save_note content carrying the whole file).
// Returns the body unchanged when no closing delimiter exists — a lone "---"
// divider is left alone.
func stripLeadingFrontMatter(body string) string {
	s := strings.TrimSpace(body)
	if !strings.HasPrefix(s, "---\n") {
		return body
	}
	end := strings.Index(s, "\n---")
	if end < 0 {
		return body
	}
	return strings.TrimSpace(s[end+4:])
}
