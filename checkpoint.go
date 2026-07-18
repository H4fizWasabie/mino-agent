package main

// Checkpoint — DECISIONS.md §6: task survival across restarts.
// ~/.mino/active_tasks/{session_id}.json

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// TaskSnapshot saved after each tool execution.
type TaskSnapshot struct {
	Goal        string   `json:"goal"`
	Round       int      `json:"round"`
	ToolsUsed   []string `json:"tools_used"`
	Discoveries []string `json:"discoveries"`
	Status      string   `json:"status"` // "active" or "complete"
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
	snap := TaskSnapshot{
		Goal:        goal,
		Round:       round,
		ToolsUsed:   toolsUsed,
		Discoveries: discoveries,
		Status:      "active",
	}
	data, _ := json.MarshalIndent(snap, "", "  ")
	if err := os.WriteFile(c.path(), data, 0644); err != nil {
		slog.Warn("checkpoint save failed", "session", c.sessionID, "error", err)
	}
}

// Load returns the active task, or nil if none exists.
func (c *CheckpointManager) Load() *TaskSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := os.ReadFile(c.path())
	if err != nil {
		return nil
	}
	var snap TaskSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	if snap.Status != "active" {
		return nil
	}
	return &snap
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

// Clear marks the task complete and removes the file.
func (c *CheckpointManager) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// read directly instead of calling Load (avoids deadlock)
	data, err := os.ReadFile(c.path())
	if err != nil {
		return
	}
	var snap TaskSnapshot
	if json.Unmarshal(data, &snap) == nil && snap.Status == "active" {
		snap.Status = "complete"
		data, _ = json.MarshalIndent(snap, "", "  ")
		os.WriteFile(c.path(), data, 0644)
	}
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
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var snap TaskSnapshot
		if json.Unmarshal(data, &snap) == nil && snap.Status == "active" {
			tasks = append(tasks, snap)
		}
	}
	return tasks
}
