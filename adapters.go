package main

// Adapters — docs/decisions.md §3-4: working memory and patterns.
// File-based adapters.

import (
	"os"
	"path/filepath"
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

