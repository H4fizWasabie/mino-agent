package main

// Adapters — DECISIONS.md §3-4: working memory, patterns, embeddings.
// Phase 3: file-based adapters + OpenRouter embeddings for retrieval.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- Working Memory (batch: append-only, sections) ---

func workingMemoryPath(home string) string { return filepath.Join(home, "working_memory.md") }
func patternsPath(home string) string      { return filepath.Join(home, "patterns.md") }

// LoadWorkingMemory returns the full content or empty string.
func LoadWorkingMemory(home string) string {
	data, _ := os.ReadFile(workingMemoryPath(home))
	return string(data)
}

// AppendWorkingMemory adds a timestamped operational note under the section.
func AppendWorkingMemory(home, section, line string) bool {
	path := workingMemoryPath(home)
	existing, _ := os.ReadFile(path)
	content := string(existing)

	header := "## " + section
	if !strings.Contains(content, header) {
		content += "\n" + header + "\n"
	}
	entry := time.Now().UTC().Format("2006-01-02 15:04") + " | " + line
	if strings.Contains(content, "- "+entry) {
		return false
	}
	content += "- " + entry + "\n"
	os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0644)
	return true
}

// LoadPatterns returns all patterns.
func LoadPatterns(home string) string {
	data, _ := os.ReadFile(patternsPath(home))
	return string(data)
}

// AddPattern appends a unique "When X, do Y" rule.
func AddPattern(home, rule string) bool {
	path := patternsPath(home)
	existing, _ := os.ReadFile(path)
	content := string(existing)
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(line, "- ")) == strings.TrimSpace(rule) {
			return false
		}
	}
	content += "- " + rule + "\n"
	os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0644)
	return true
}

// PruneRecentFixes removes timestamped Recent Fixes older than the retention.
// Other sections stay durable operational context.
func PruneRecentFixes(home string, retention time.Duration) []string {
	path := workingMemoryPath(home)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var kept, removed []string
	inRecent := false
	cutoff := time.Now().UTC().Add(-retention)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			inRecent = strings.HasPrefix(line, "## Recent Fixes")
			kept = append(kept, line)
			continue
		}
		if !inRecent || !strings.HasPrefix(line, "- ") {
			kept = append(kept, line)
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(line, "- "), " | ", 2)
		when, parseErr := time.Parse("2006-01-02 15:04", parts[0])
		if parseErr != nil || len(parts) != 2 || !when.Before(cutoff) {
			kept = append(kept, line)
			continue
		}
		removed = append(removed, parts[1])
	}
	if len(removed) > 0 {
		os.WriteFile(path, []byte(strings.TrimSpace(strings.Join(kept, "\n"))+"\n"), 0644)
	}
	return removed
}

// --- Embedding adapter (OpenRouter, DECISIONS.md §4) ---

// ponytail: single struct, no interface, stdlib HTTP only

type EmbeddingStore struct {
	db     *sql.DB
	apiKey string
	model  string
	docs   []embeddedDoc
}

type embeddedDoc struct {
	Source    string    `json:"source"` // "working_memory", "patterns", "facts"
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding,omitempty"`
}

type scoredDoc struct {
	doc   embeddedDoc
	score float64
}

func NewEmbeddingStore(db *sql.DB, apiKey, model string) *EmbeddingStore {
	es := &EmbeddingStore{db: db, apiKey: apiKey, model: model}
	es.loadCache()
	return es
}

func (es *EmbeddingStore) loadCache() {
	rows, err := es.db.Query("SELECT source, content, embedding FROM memory_embeddings")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var d embeddedDoc
		var raw string
		if rows.Scan(&d.Source, &d.Content, &raw) == nil && json.Unmarshal([]byte(raw), &d.Embedding) == nil {
			es.docs = append(es.docs, d)
		}
	}
}

func (es *EmbeddingStore) saveCache() {
	es.db.Exec("DELETE FROM memory_embeddings")
	for _, d := range es.docs {
		raw, _ := json.Marshal(d.Embedding)
		es.db.Exec("INSERT INTO memory_embeddings (source, content, embedding) VALUES (?,?,?)", d.Source, d.Content, string(raw))
	}
}

// Index embeds a document and stores it. Skips if already indexed.
func (es *EmbeddingStore) Index(source, content string) {
	// dedup
	for _, d := range es.docs {
		if d.Source == source && d.Content == content {
			return
		}
	}
	emb, err := es.embed(content)
	if err != nil {
		slog.Warn("embed failed", "source", source, "error", err)
		return
	}
	es.docs = append(es.docs, embeddedDoc{Source: source, Content: content, Embedding: emb})
	es.saveCache()
}

// Prune drops cached embeddings whose source record no longer exists.
// Derived data is reconciled against the source of truth at every startup,
// so drift from old binaries, crashes, or manual DB edits self-heals.
func (es *EmbeddingStore) Prune(valid map[string]bool) {
	kept := es.docs[:0]
	for _, d := range es.docs {
		if valid[d.Source+"\x00"+d.Content] {
			kept = append(kept, d)
		}
	}
	if len(kept) != len(es.docs) {
		es.docs = kept
		es.saveCache()
	}
}

func (es *EmbeddingStore) Remove(source, content string) {
	filtered := es.docs[:0]
	for _, doc := range es.docs {
		if doc.Source != source || doc.Content != content {
			filtered = append(filtered, doc)
		}
	}
	es.docs = filtered
	es.saveCache()
}

func (es *EmbeddingStore) SearchScored(query string, topK int) []scoredDoc {
	if len(es.docs) == 0 {
		return nil
	}
	qEmb, err := es.embed(query)
	if err != nil {
		return nil
	}
	var scores []scoredDoc
	for _, d := range es.docs {
		if len(d.Embedding) == 0 {
			continue
		}
		s := cosineSimilarity(qEmb, d.Embedding)
		scores = append(scores, scoredDoc{doc: d, score: s})
	}
	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })
	if len(scores) > topK {
		scores = scores[:topK]
	}
	return scores
}

// BackfillEmbeddings indexes durable records created before embeddings were
// configured and prunes embeddings for records that no longer exist.
// Index is idempotent, so normal restarts make no duplicate calls.
func (m *Memory) BackfillEmbeddings() {
	if m.embedder == nil {
		return
	}
	var docs []embeddedDoc
	rows, err := m.db.Query("SELECT subject, content FROM facts")
	if err == nil {
		for rows.Next() {
			var subject, content string
			if rows.Scan(&subject, &content) == nil {
				docs = append(docs, embeddedDoc{Source: "fact", Content: subject + ": " + content})
			}
		}
		rows.Close()
	}
	rows, err = m.db.Query("SELECT summary FROM episodes")
	if err == nil {
		for rows.Next() {
			var summary string
			if rows.Scan(&summary) == nil {
				docs = append(docs, embeddedDoc{Source: "episode", Content: summary})
			}
		}
		rows.Close()
	}
	for _, line := range strings.Split(LoadWorkingMemory(m.cfg.Home), "\n") {
		if content := memoryFileEntry(line); content != "" {
			docs = append(docs, embeddedDoc{Source: "working_memory", Content: content})
		}
	}
	for _, line := range strings.Split(LoadPatterns(m.cfg.Home), "\n") {
		if content := memoryFileEntry(line); content != "" {
			docs = append(docs, embeddedDoc{Source: "patterns", Content: content})
		}
	}
	valid := make(map[string]bool, len(docs))
	for _, doc := range docs {
		valid[doc.Source+"\x00"+doc.Content] = true
	}
	m.embedder.Prune(valid)
	for _, doc := range docs {
		m.embedder.Index(doc.Source, doc.Content)
	}
}

func memoryFileEntry(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "- ") {
		return ""
	}
	content := strings.TrimPrefix(line, "- ")
	if parts := strings.SplitN(content, " | ", 2); len(parts) == 2 {
		if _, err := time.Parse("2006-01-02 15:04", parts[0]); err == nil {
			return parts[1]
		}
	}
	return content
}

// embed calls OpenRouter embeddings API.
func (es *EmbeddingStore) embed(text string) ([]float32, error) {
	if es.apiKey == "" || text == "" {
		return nil, fmt.Errorf("no api key or empty text")
	}
	payload := map[string]any{
		"model":    es.model,
		"input":    text,
		"provider": map[string]any{"zdr": true},
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", "https://openrouter.ai/api/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+es.apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding request: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse embedding: %w, body: %.200s", err, string(data))
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return result.Data[0].Embedding, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// --- Hybrid fact retrieval: FTS5 candidates + embeddings, ranked once ---

func (m *Memory) SemanticSearch(query string, es *EmbeddingStore) string {
	if opaqueIdentifierQuery(query) {
		es = nil // IDs belong to FTS; vector similarity would only add noise.
	}
	hits := m.hybridFactCandidates(query, es)
	if len(hits) > m.cfg.TopK {
		hits = hits[:m.cfg.TopK]
	}
	ids := make([]string, 0, len(hits))
	var out strings.Builder
	for _, hit := range hits {
		ids = append(ids, fmt.Sprint(hit.id))
		out.WriteString(fmt.Sprintf("- **%s**: %s\n", hit.subject, hit.content))
	}
	if len(ids) > 0 {
		m.db.Exec("UPDATE facts SET last_accessed = datetime('now') WHERE id IN (" + strings.Join(ids, ",") + ")")
	}
	// Facts receive the durable four-signal ranking. Other recall sources are
	// still useful, but only fill unused slots until they have equivalent metadata.
	shown := len(hits)
	// episodes: dated timeline recall via FTS5
	if shown < m.cfg.TopK {
		for _, line := range strings.Split(m.SearchEpisodes(query), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			out.WriteString(fmt.Sprintf("- [episode] %s\n", strings.TrimPrefix(line, "- ")))
			shown++
			if shown >= m.cfg.TopK {
				break
			}
		}
	}
	if es != nil && shown < m.cfg.TopK {
		for _, doc := range es.SearchScored(query, m.cfg.TopK*2) {
			// ponytail: cosine floor is embedding-model-dependent; retune MINO_MIN_SIMILARITY after any model swap + reindex
			if doc.doc.Source == "fact" || doc.score < m.cfg.MinSimilarity {
				continue
			}
			out.WriteString(fmt.Sprintf("- [%s] %s\n", doc.doc.Source, doc.doc.Content))
			shown++
			if shown >= m.cfg.TopK {
				break
			}
		}
	}
	if out.Len() == 0 {
		return "No matches found."
	}
	return out.String()
}

type factHit struct {
	id                            int
	subject, content, createdAt   string
	importance, feedback          int
	keyword, semantic, finalScore float64
}

func (m *Memory) hybridFactCandidates(query string, es *EmbeddingStore) []factHit {
	hits := make(map[int]factHit)
	terms := ftsTerms(query)
	if terms != "" {
		rows, err := m.db.Query(`SELECT f.id, f.subject, f.content, f.created_at, f.importance, f.feedback
			FROM facts_fts JOIN facts f ON f.id = facts_fts.rowid
			WHERE facts_fts MATCH ? ORDER BY bm25(facts_fts) LIMIT ?`, terms, m.cfg.TopK*2)
		if err == nil {
			rank := 0
			for rows.Next() {
				var hit factHit
				if rows.Scan(&hit.id, &hit.subject, &hit.content, &hit.createdAt, &hit.importance, &hit.feedback) == nil {
					rank++
					hit.keyword = 1 - float64(rank-1)/float64(max(1, m.cfg.TopK*2))
					hits[hit.id] = hit
				}
			}
			rows.Close()
		}
	}
	if es != nil {
		for _, doc := range es.SearchScored(query, m.cfg.TopK*2) {
			if doc.doc.Source != "fact" {
				continue
			}
			var hit factHit
			err := m.db.QueryRow(`SELECT id, subject, content, created_at, importance, feedback FROM facts
				WHERE subject || ': ' || content = ?`, doc.doc.Content).Scan(&hit.id, &hit.subject, &hit.content, &hit.createdAt, &hit.importance, &hit.feedback)
			if err != nil {
				continue
			}
			hit.semantic = min(1, max(0, (doc.score+1)/2))
			if existing, ok := hits[hit.id]; ok {
				hit.keyword = existing.keyword
			}
			hits[hit.id] = hit
		}
	}
	result := make([]factHit, 0, len(hits))
	for _, hit := range hits {
		hit.finalScore = scoreFact(hit)
		result = append(result, hit)
	}
	unique := make(map[string]factHit)
	for _, hit := range result {
		key := strings.ToLower(strings.Join(strings.Fields(hit.content), " "))
		if existing, ok := unique[key]; !ok || hit.finalScore > existing.finalScore {
			unique[key] = hit
		}
	}
	result = result[:0]
	for _, hit := range unique {
		result = append(result, hit)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].finalScore > result[j].finalScore })
	return result
}

func scoreFact(hit factHit) float64 {
	similarity := max(hit.keyword, hit.semantic)
	importance := float64(min(5, max(1, hit.importance))) / 5
	feedback := float64(min(5, max(-5, hit.feedback))+5) / 10
	created, err := time.Parse("2006-01-02 15:04:05", hit.createdAt)
	if err != nil {
		created = time.Now()
	}
	recency := math.Exp(-max(0, time.Since(created).Hours()) / (24 * 180))
	return 0.55*similarity + 0.20*importance + 0.15*recency + 0.10*feedback
}

func ftsTerms(query string) string {
	terms := strings.FieldsFunc(query, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_')
	})
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "be": true, "for": true,
		"how": true, "i": true, "in": true, "is": true, "me": true, "my": true,
		"of": true, "on": true, "should": true, "the": true, "to": true, "we": true,
		"what": true, "with": true, "you": true, "your": true,
	}
	filtered := make([]string, 0, len(terms))
	for _, term := range terms {
		if !stop[strings.ToLower(term)] {
			filtered = append(filtered, term)
		}
	}
	return strings.Join(filtered, " OR ")
}

func opaqueIdentifierQuery(query string) bool {
	hasLetter, hasDigit, hasSeparator := false, false, false
	for _, r := range query {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '-' || r == '_' || r == '/':
			hasSeparator = true
		}
	}
	return hasLetter && hasDigit && hasSeparator
}
