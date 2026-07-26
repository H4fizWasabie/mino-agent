package main

import (
	"context"
	"sync"
)

// Conversation owns the mutable state for one long-running gateway session.
type Conversation struct {
	Session    *Session
	Checkpoint *CheckpointManager
	mu         sync.Mutex
	activeMu   sync.Mutex
	cancel     context.CancelFunc
	resumed    bool
}

func (c *Conversation) beginTurn(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	c.activeMu.Lock()
	c.cancel = cancel
	c.activeMu.Unlock()
	return ctx, func() {
		cancel()
		c.activeMu.Lock()
		c.cancel = nil
		c.activeMu.Unlock()
	}
}

func (c *Conversation) cancelTurn() bool {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	active := c.cancel != nil
	if active {
		c.cancel()
	}
	// An explicit stop retires interrupted work, including a stale checkpoint
	// left by a previous process or test run.
	c.Checkpoint.Clear()
	return active
}

func (c *Conversation) resumePrompt() string {
	if c.resumed {
		return ""
	}
	prompt := c.Checkpoint.ResumePrompt()
	if prompt != "" {
		c.resumed = true
	}
	return prompt
}

type SessionManager struct {
	settings *Settings
	mem      *Memory
	items    map[string]*Conversation
	mu       sync.Mutex
}

func NewSessionManager(settings *Settings, mem *Memory) *SessionManager {
	return &SessionManager{settings: settings, mem: mem, items: make(map[string]*Conversation)}
}

func (m *SessionManager) Get(id string) *Conversation {
	if id == "" {
		id = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if conversation := m.items[id]; conversation != nil {
		return conversation
	}
	session := NewSession(m.settings, m.mem)
	session.Switch(id) // restores a gateway conversation after process restart
	conversation := &Conversation{Session: session, Checkpoint: NewCheckpointManager(m.settings.Home, id)}
	m.items[id] = conversation
	return conversation
}
