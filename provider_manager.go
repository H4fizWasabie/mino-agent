package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type ModelRole string

const (
	MainModel  ModelRole = "main"
	SmallModel ModelRole = "small"
	// VisionModel is synthetic: callers never pass it. Create/Stream flip to it
	// when messages carry images, giving vision turns their own sticky bucket
	// so an image turn can't downgrade a session's text routing.
	VisionModel ModelRole = "vision"
)

type ProviderConfig struct {
	Name      string `json:"name"`
	Priority  int    `json:"priority"`
	BaseURL   string `json:"base_url"`
	APIKeyEnv string `json:"api_key_env"`
	Model     string `json:"model"`
	Small     string `json:"small_model"`
	TextOnly  bool   `json:"text_only"` // provider rejects image input; skipped for vision turns
}

type providerFile struct {
	Providers []ProviderConfig `json:"providers"`
}
type providerState struct {
	failures  int
	openUntil time.Time
}

// ProviderManager applies priority, retries, fallback, a shared circuit breaker,
// and per-session stickiness around OpenAI-compatible clients.
type ProviderManager struct {
	providers []ProviderConfig
	clients   map[string]*Client
	state     map[string]*providerState
	sticky    map[string]string
	authStore *AuthStore
	mu        sync.Mutex
	authMu    sync.Mutex
	sleep     func(time.Duration)
	now       func() time.Time
}

func NewProviderManager(home string, legacy *Settings, authStore *AuthStore) (*ProviderManager, error) {
	configs, err := loadProviders(home, legacy)
	if err != nil {
		return nil, err
	}
	m := &ProviderManager{clients: map[string]*Client{}, state: map[string]*providerState{}, sticky: map[string]string{}, authStore: authStore, sleep: time.Sleep, now: time.Now}
	for _, p := range configs {
		key := ""
		if p.APIKeyEnv != "" {
			key = os.Getenv(p.APIKeyEnv)
		}
		if key == "" && authStore != nil {
			key = authStore.Get(p.Name)
		}
		if key == "" && p.APIKeyEnv != "" {
			return nil, fmt.Errorf("provider %q: %s is not set", p.Name, p.APIKeyEnv)
		}
		if p.Name == "" || p.BaseURL == "" || p.Model == "" {
			return nil, fmt.Errorf("provider config requires name, base_url, and model")
		}
		c := NewClient(key, p.BaseURL)
		c.usageLogPath = filepath.Join(home, "usage.jsonl")
		m.providers = append(m.providers, p)
		m.clients[p.Name] = c
		m.state[p.Name] = &providerState{}
	}
	sort.SliceStable(m.providers, func(i, j int) bool { return m.providers[i].Priority < m.providers[j].Priority })
	return m, nil
}

func loadProviders(home string, legacy *Settings) ([]ProviderConfig, error) {
	data, err := os.ReadFile(filepath.Join(home, "providers.json"))
	if os.IsNotExist(err) {
		if legacy.APIKey == "" || legacy.BaseURL == "" {
			return nil, fmt.Errorf("no providers.json and MINO_API_KEY/MINO_BASE_URL are required")
		}
		return []ProviderConfig{{Name: "mimo", Priority: 1, BaseURL: legacy.BaseURL, APIKeyEnv: "MINO_API_KEY", Model: legacy.Model, Small: legacy.SmallModel}}, nil
	}
	if err != nil {
		return nil, err
	}
	var file providerFile
	if err := json.Unmarshal(data, &file); err != nil || len(file.Providers) == 0 {
		return nil, fmt.Errorf("invalid providers.json")
	}
	return file.Providers, nil
}

func (m *ProviderManager) Create(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return m.call(session, routeRole(role, messages), func(c *Client, model string) (*LLMResponse, error) {
		return c.Create(model, messages, maxTokens, system, tools)
	})
}

func (m *ProviderManager) Stream(session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef, onText func(string)) (*LLMResponse, error) {
	return m.call(session, routeRole(role, messages), func(c *Client, model string) (*LLMResponse, error) {
		return c.Stream(model, messages, maxTokens, system, tools, onText)
	})
}

// routeRole flips any role to VisionModel when the outgoing messages carry
// images. Covers every image source (Telegram photos, view_image results).
func routeRole(role ModelRole, messages []Message) ModelRole {
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			return VisionModel
		}
	}
	return role
}

func (m *ProviderManager) resolveKey(p ProviderConfig) (string, error) {
	if p.APIKeyEnv != "" {
		if k := os.Getenv(p.APIKeyEnv); k != "" {
			return k, nil
		}
	}
	if m.authStore != nil {
		entry, ok := m.authStore.GetEntry(p.Name)
		if !ok {
			return "", nil
		}
		if p.Name == "codex" && entry.Type == "oauth" && entry.Refresh != "" && entry.ExpiresAt <= time.Now().Add(time.Minute).Unix() {
			m.authMu.Lock()
			defer m.authMu.Unlock()
			entry, _ = m.authStore.GetEntry(p.Name)
			if entry.ExpiresAt <= time.Now().Add(time.Minute).Unix() {
				fresh, err := refreshCodexToken(entry.Refresh)
				if err != nil {
					return "", err
				}
				if err := m.authStore.SetOAuth(p.Name, fresh.Key, fresh.Refresh, fresh.ExpiresAt, fresh.AccountID); err != nil {
					return "", err
				}
				entry = fresh
			}
		}
		return entry.Key, nil
	}
	return "", nil
}

func (m *ProviderManager) call(session string, role ModelRole, call func(*Client, string) (*LLMResponse, error)) (*LLMResponse, error) {
	var lastErr error
	for _, p := range m.candidates(session, role) {
		// refresh key from env/auth.json (supports runtime key changes)
		key, err := m.resolveKey(p)
		if err != nil {
			lastErr = err
			continue
		}
		client := m.clients[p.Name]
		if client != nil {
			client.apiKey = key
		}
		for attempt := 0; attempt < 3; attempt++ {
			resp, err := call(client, modelFor(p, role))
			if err == nil {
				m.success(session, role, p.Name)
				return resp, nil
			}
			lastErr = err
			if attempt < 2 {
				m.sleep(time.Duration(1<<attempt) * time.Second)
			}
		}
		m.failure(session, role, p.Name)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("all %s providers failed: %w", role, lastErr)
	}
	return nil, fmt.Errorf("all %s providers failed", role)
}

func modelFor(p ProviderConfig, role ModelRole) string {
	if role == SmallModel && p.Small != "" {
		return p.Small
	}
	return p.Model
}
func (m *ProviderManager) key(session string, role ModelRole) string {
	return session + ":" + string(role)
}
func (m *ProviderManager) candidates(session string, role ModelRole) []ProviderConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var out []ProviderConfig
	if name := m.sticky[m.key(session, role)]; name != "" && m.state[name].openUntil.Before(now) {
		for _, p := range m.providers {
			if p.Name == name && !(role == VisionModel && p.TextOnly) {
				out = append(out, p)
			}
		}
	}
	for _, p := range m.providers {
		if role == VisionModel && p.TextOnly {
			continue
		}
		if m.state[p.Name].openUntil.Before(now) && (len(out) == 0 || out[0].Name != p.Name) {
			out = append(out, p)
		}
	}
	return out
}
func (m *ProviderManager) success(session string, role ModelRole, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state[name].failures = 0
	m.sticky[m.key(session, role)] = name
}
func (m *ProviderManager) failure(session string, role ModelRole, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state[name]
	s.failures++
	if s.failures >= 3 {
		s.failures = 0
		s.openUntil = m.now().Add(60 * time.Second)
		delete(m.sticky, m.key(session, role))
	}
}

// SetPreferred forces a session to use a specific provider.
func (m *ProviderManager) SetPreferred(session, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for _, p := range m.providers {
		if p.Name == provider {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown provider: %s", provider)
	}
	for _, p := range m.providers {
		if p.Name == provider {
			key, err := m.resolveKey(p)
			if err != nil {
				return err
			}
			if key == "" {
				return fmt.Errorf("provider %s has no API key configured", provider)
			}
		}
	}
	m.sticky[m.key(session, MainModel)] = provider
	return nil
}

// ActiveProvider returns the current sticky provider for a session, or "" if none.
func (m *ProviderManager) ActiveProvider(session string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sticky[m.key(session, MainModel)]
}

// ProviderNames returns all configured provider names.
func (m *ProviderManager) ProviderNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, len(m.providers))
	for i, p := range m.providers {
		names[i] = p.Name
	}
	return names
}
