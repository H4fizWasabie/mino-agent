package main

// Checkpoint — DECISIONS.md §6: task survival across restarts.
// ~/.mino/active_tasks/{session_id}.json

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const checkpointMaxAge = 30 * 24 * time.Hour

// TaskStep — DECISIONS.md §20: structured plan step.
type TaskStep struct {
	Description string `json:"description"` // LLM-written summary
	Tool        string `json:"tool"`        // tool called (or empty if pending)
	Status      string `json:"status"`      // "done" | "active" | "pending"
	Output      string `json:"output"`      // inline (≤8KB) or artifact ref (>8KB)
}

// TaskSnapshot saved after each tool execution.
type TaskSnapshot struct {
	Goal        string      `json:"goal"`
	Round       int         `json:"round"`
	ToolsUsed   []string    `json:"tools_used"`
	Plan        []TaskStep  `json:"plan,omitempty"`
	Discoveries []string    `json:"discoveries"`
	Status      string      `json:"status"` // "active" or "complete"
	UpdatedAt   string      `json:"updated_at"`
}

// CheckpointManager handles save/load/clear.
type CheckpointManager struct {
	home      string
	sessionID string
	plan      []TaskStep // §20: current plan from LLM
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

// SetPlan stores the current task plan from the LLM.
func (c *CheckpointManager) SetPlan(plan []TaskStep) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plan = plan
}

// Save writes a snapshot.
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
		Plan:        c.plan,
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
	prompt := "You were working on this before a restart:\n" + string(data) + "\n\nContinue."
	if len(t.Plan) > 0 {
		steps := "\nPlan progress:\n"
		for _, s := range t.Plan {
			steps += fmt.Sprintf("- [%s] %s", s.Status, s.Description)
			if s.Tool != "" {
				steps += fmt.Sprintf(" (tool: %s)", s.Tool)
			}
			steps += "\n"
		}
		prompt += steps
	}
	return prompt
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
