package main

// Checkpoint — DECISIONS.md §6: task survival across restarts.
// ~/.mino/active_tasks/{session_id}.json

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const checkpointMaxAge = 30 * 24 * time.Hour

// TaskSnapshot saved after each tool execution.
type TaskSnapshot struct {
	Goal        string   `json:"goal"`
	Round       int      `json:"round"`
	ToolsUsed   []string `json:"tools_used"`
	Discoveries []string `json:"discoveries"`
	Status      string   `json:"status"` // "active" or "complete"
	UpdatedAt   string   `json:"updated_at"`
}

// CheckpointManager handles save/load/clear.
type CheckpointManager struct {
	home      string
	sessionID string
	mu        sync.Mutex
}

func NewCheckpointManager(home, sessionID string) *CheckpointManager {
	return &CheckpointManager{home: home, sessionID: sessionID}
}

func (c *CheckpointManager) taskDir() string {
	dir := filepath.Join(c.home, "active_tasks")
	os.MkdirAll(dir, 0700)
	return dir
}

func (c *CheckpointManager) path() string {
	return filepath.Join(c.taskDir(), c.sessionID+".json")
}

// Save writes a snapshot. Returns true if saved.
func (c *CheckpointManager) Save(goal string, round int, toolsUsed, discoveries []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := readActiveCheckpoint(c.path()); existing != nil && existing.Goal != "" {
		goal = existing.Goal
	}
	snap := TaskSnapshot{
		Goal:        goal,
		Round:       round,
		ToolsUsed:   toolsUsed,
		Discoveries: discoveries,
		Status:      "active",
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(snap, "", "  ")
	tmp := c.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		slog.Warn("checkpoint save failed", "session", c.sessionID, "error", err)
	} else if err := os.Rename(tmp, c.path()); err != nil {
		slog.Warn("checkpoint replace failed", "session", c.sessionID, "error", err)
		os.Remove(tmp)
	}
}

// Load returns the active task, or nil if none exists.
func (c *CheckpointManager) Load() *TaskSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return readActiveCheckpoint(c.path())
}

// ResumePrompt builds the injection message for the system prompt.
func (c *CheckpointManager) ResumePrompt() string {
	t := c.Load()
	if t == nil {
		return ""
	}
	data, _ := json.Marshal(t)
	return "You were working on this before a restart:\n" + string(data) + "\n\nContinue."
}

// Clear retires a completed task.
func (c *CheckpointManager) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	os.Remove(c.path())
}

// ListActive returns all active task snapshots.
func ListActiveTasks(home string) []TaskSnapshot {
	dir := filepath.Join(home, "active_tasks")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var tasks []TaskSnapshot
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		if snap := readActiveCheckpoint(filepath.Join(dir, e.Name())); snap != nil {
			tasks = append(tasks, *snap)
		}
	}
	return tasks
}

func readActiveCheckpoint(path string) *TaskSnapshot {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var snap TaskSnapshot
	if json.Unmarshal(data, &snap) != nil || snap.Status != "active" {
		return nil
	}
	updated, err := time.Parse(time.RFC3339, snap.UpdatedAt)
	if err != nil {
		if info, statErr := os.Stat(path); statErr == nil {
			updated = info.ModTime()
		}
	}
	if updated.IsZero() || time.Since(updated) > checkpointMaxAge {
		os.Remove(path)
		return nil
	}
	return &snap
}
