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
	emb, err := es.Embed(content)
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

// DocsBySource returns all cached embeddings for the given source.
func (es *EmbeddingStore) DocsBySource(source string) []embeddedDoc {
	var out []embeddedDoc
	for _, d := range es.docs {
		if d.Source == source && len(d.Embedding) > 0 {
			out = append(out, d)
		}
	}
	return out
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
	qEmb, err := es.Embed(query)
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

// EmbedBatch calls OpenRouter embeddings API with multiple inputs in one request.
func (es *EmbeddingStore) EmbedBatch(texts []string) ([][]float32, error) {
	if es.apiKey == "" || len(texts) == 0 {
		return nil, fmt.Errorf("no api key or empty texts")
	}
	// ponytail: single HTTP call, 86 tools in <2s vs 49s sequentially
	payload := map[string]any{
		"model":    es.model,
		"input":    texts,
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
		return nil, fmt.Errorf("batch embedding: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse batch embedding: %w", err)
	}
	out := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// Embed calls OpenRouter embeddings API.
func (es *EmbeddingStore) Embed(text string) ([]float32, error) {
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
