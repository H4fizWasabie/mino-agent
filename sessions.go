package main

import "sync"

// Conversation owns the mutable state for one long-running gateway session.
type Conversation struct {
	Session    *Session
	Checkpoint *CheckpointManager
	mu         sync.Mutex
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
