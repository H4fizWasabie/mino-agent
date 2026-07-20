package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// auth.json — credential store, separate from providers.json.
// Mirrors pi's ~/.pi/agent/auth.json pattern: provider name → { type, key }.

type AuthEntry struct {
	Type string `json:"type"` // "api_key" or "oauth"
	Key  string `json:"key"`
}

type AuthStore struct {
	mu   sync.RWMutex
	path string
	data map[string]AuthEntry
}

func LoadAuthStore(home string) *AuthStore {
	s := &AuthStore{
		path: filepath.Join(home, "auth.json"),
		data: map[string]AuthEntry{},
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s // empty store is ok
	}
	json.Unmarshal(data, &s.data)
	return s
}

// Get returns the key for a provider, or "" if not found.
func (s *AuthStore) Get(provider string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[provider]
	if !ok || e.Key == "" {
		return ""
	}
	return e.Key
}

// Set stores a key for a provider and persists.
func (s *AuthStore) Set(provider, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[provider] = AuthEntry{Type: "api_key", Key: key}
	return s.save()
}

// Delete removes a provider's key and persists.
func (s *AuthStore) Delete(provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, provider)
	return s.save()
}

// List returns all entries (keys masked).
func (s *AuthStore) List() map[string]AuthEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]AuthEntry{}
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

func (s *AuthStore) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}
