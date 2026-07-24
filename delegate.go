package main

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// delegateCacheTTL is how long a cached delegate result lives.
const delegateCacheTTL = 1 * time.Hour

func addDelegateTools(w *Core) {
	run := func(prompt, contextBrief string) string {
		// check cache first
		if cached := delegateCacheLookup(w.DB, prompt); cached != "" {
			return cached
		}

		id := fmt.Sprintf("delegate-%d", time.Now().UnixNano())
		// worker tools: read, search, fetch for investigation; recall for context; save_note to persist findings
		tools := w.Tools.Only("read_file", "bash", "search_web", "fetch_url", "recall", "save_note")

		system := loadSoul(w.Settings.Home) + "\n\nYou are an ephemeral worker. Investigate the request, use only the available tools, and return a concise answer."
		if contextBrief != "" {
			system += "\n\nContext from the main agent:\n" + contextBrief
		}
		system += "\n\nRules:\n- Use recall to check if relevant facts already exist.\n- Use save_note to persist findings the main agent will need.\n- Do not create schedules or send notifications.\n- Return a single concise answer, not a conversation."

		result := RunLoop(w.Client, id, system, []Message{{Role: "user", Content: prompt}}, tools, min(10, w.Settings.MaxIter), w.Settings.MaxTokens, nil, false, nil, w.Settings.Home, nil)
		output := compactToolOutput(w.Settings.Home, id, 1, "delegate", result.Reply)

		// cache the result
		delegateCacheStore(w.DB, prompt, output)

		return output
	}

	w.Tools.Register(&Tool{
		Name: "delegate",
		Description: "Run an isolated investigation with a fresh context. Use for lookups, searches, or simple tasks that don't need full session history. Returns a concise answer.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt":  map[string]any{"type": "string", "description": "What to investigate. Be specific — include search terms, file paths, or exact questions."},
				"context": map[string]any{"type": "string", "description": "Optional. 1-3 sentences of what the main agent already knows about this task. Helps the worker avoid repeating work."},
			},
			"required": []string{"prompt"},
		},
		Fn: func(args map[string]any) string {
			prompt, _ := args["prompt"].(string)
			contextBrief, _ := args["context"].(string)
			return run(prompt, contextBrief)
		},
	})
}

func delegateCacheKey(prompt string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return fmt.Sprintf("delegate-cache/%x", h[:16])
}

func delegateCacheLookup(db *sql.DB, prompt string) string {
	key := delegateCacheKey(prompt)
	var path, label string
	var createdAt time.Time
	err := db.QueryRow("SELECT path, label, created_at FROM session_artifacts WHERE path = ?", key).Scan(&path, &label, &createdAt)
	if err != nil {
		return ""
	}
	if time.Since(createdAt) > delegateCacheTTL {
		db.Exec("DELETE FROM session_artifacts WHERE path = ?", key)
		return ""
	}
	return label // label stores the cached output
}

func delegateCacheStore(db *sql.DB, prompt, output string) {
	key := delegateCacheKey(prompt)
	// label field holds the cached output (reuse session_artifacts table)
	db.Exec("INSERT OR REPLACE INTO session_artifacts (path, session_id, label, size, created_at) VALUES (?, 'delegate', ?, ?, datetime('now'))",
		key, output, len(output))
}

// ponytail: no separate cache table — reuses session_artifacts with a delegate-cache/ prefix.
// Add a dedicated delegate_cache table if TTL eviction becomes a performance concern.
