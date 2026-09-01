package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	Name            string   `json:"name"`
	Priority        int      `json:"priority"`
	BaseURL         string   `json:"base_url"`
	APIKeyEnv       string   `json:"api_key_env"`
	Model           string   `json:"model"`
	Models          []string `json:"models,omitempty"`
	Small           string   `json:"small_model"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	SmallReasoning  string   `json:"small_reasoning_effort,omitempty"`
	ReasoningLevels []string `json:"reasoning_levels,omitempty"`
	TextOnly        bool     `json:"text_only"`                        // provider rejects image input; skipped for vision turns
	Transport       string   `json:"transport,omitempty"`              // wire family: "openai" (default) | "anthropic" | "codex" — declared, never sniffed (PRV-001)
	ProviderRouting []string `json:"provider_routing,omitempty"`       // openrouter: force specific providers
	SmallRouting    []string `json:"small_provider_routing,omitempty"` // openrouter route for background calls
	AllowFallbacks  bool     `json:"allow_fallbacks,omitempty"`        // openrouter: may fall back OUTSIDE the routing list (default false = privacy-safe: only the listed hosts)
}

type providerFile struct {
	Providers []ProviderConfig `json:"providers"`
}

type providerState struct {
	failures  int
	openUntil time.Time
}
type providerPreference struct {
	provider  string
	model     string
	reasoning string
}
type ProviderOption struct {
	Name            string   `json:"name"`
	Model           string   `json:"model"`
	Models          []string `json:"models"`
	ReasoningLevels []string `json:"reasoning_levels"`
}

// ProviderManager applies priority, retries, fallback, a shared circuit breaker,
// and per-session stickiness around OpenAI-compatible clients.
type ProviderManager struct {
	db            *sql.DB // shared state.db handle, handed to clients for usage logging (#344)
	providers     []ProviderConfig
	clients       map[string]*Client
	state         map[string]*providerState
	sticky        map[string]string
	stickyAt      map[string]time.Time
	preferred     map[string]providerPreference
	providersHash string
	// config-change push signal (#204): set when ReloadProviders sees the
	// providers.json content change; consumed by the loop's next turn so the
	// brain re-verifies model-stack memory facts instead of answering stale.
	configChangedAt time.Time
	authStore       *AuthStore
	mu              sync.Mutex
	authMu          sync.Mutex
	now             func() time.Time
}

func NewProviderManager(home string, legacy *Settings, authStore *AuthStore, db *sql.DB) (*ProviderManager, error) {
	configs, err := loadProviders(home, legacy)
	if err != nil {
		return nil, err
	}
	m := &ProviderManager{db: db, clients: map[string]*Client{}, state: map[string]*providerState{}, sticky: map[string]string{}, stickyAt: map[string]time.Time{}, preferred: map[string]providerPreference{}, authStore: authStore, now: time.Now}
	m.providersHash = fileHash(filepath.Join(home, "providers.json"))
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
		c.usageDB = m.db
		c.transport = p.Transport
		c.providerRouting = p.ProviderRouting
		c.allowFallbacks = p.AllowFallbacks
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

func (m *ProviderManager) CreateJSON(session string, role ModelRole, messages []Message, maxTokens int, system string) (*LLMResponse, error) {
	return m.callWithConfig(session, routeRole(role, messages), func(c *Client, model, reasoning string, p ProviderConfig) (*LLMResponse, error) {
		return c.createWithRouting(context.Background(), model, reasoningForRole(p, role), messages, maxTokens, system, nil, true, routingForRole(p, role), "")
	})
}

func (m *ProviderManager) CreateContext(ctx context.Context, session string, role ModelRole, messages []Message, maxTokens int, system string, tools []ToolDef) (*LLMResponse, error) {
	return m.callContextWithConfig(ctx, session, routeRole(role, messages), func(c *Client, model, reasoning string, p ProviderConfig) (*LLMResponse, error) {
		return c.createWithRouting(ctx, model, reasoningForRole(p, role), messages, maxTokens, system, tools, false, routingForRole(p, role), "")
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
		if p.Transport == "codex" && entry.Type == "oauth" && entry.Refresh != "" && entry.ExpiresAt <= time.Now().Add(time.Minute).Unix() {
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

func (m *ProviderManager) callWithConfig(session string, role ModelRole, call func(*Client, string, string, ProviderConfig) (*LLMResponse, error)) (*LLMResponse, error) {
	return m.callContextWithConfig(context.Background(), session, role, call)
}

func (m *ProviderManager) callContextWithConfig(ctx context.Context, session string, role ModelRole, call func(*Client, string, string, ProviderConfig) (*LLMResponse, error)) (*LLMResponse, error) {
	var lastErr error
	for _, p := range m.candidates(session, role) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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
			// #159: same unpin-on-retry as callWithConfig.
			pmodel := modelFor(p, role)
			if attempt >= 1 {
				pmodel = stripProviderPin(pmodel)
			}
			resp, err := call(client, pmodel, p.ReasoningEffort, p)
			if err == nil {
				m.success(session, role, p.Name)
				return resp, nil
			}
			// CTX-010: log every failure with the error string so a silent
			// failover is diagnosable without post-hoc guessing.
			slog.Warn("provider call failed", "provider", p.Name, "role", role, "model", pmodel, "attempt", attempt+1, "error", err)
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// #285: a TIMEOUT is a failover trigger, not an in-place retry
			// spiral. One in-place retry (the 2026-08-20 evidence: a dead
			// deepseek was retried 3x on the same provider while healthy
			// fallbacks sat idle), then move to the next-priority provider —
			// the circuit breaker still trips on repeated failures, but the
			// call lands elsewhere instead of burning 3x the wall clock on a
			// dead endpoint. Only non-timeout errors keep the full 3x in-place
			// budget (transient 5xx-style blips deserve local retries).
			var netErr net.Error
			isTimeout := errors.As(err, &netErr) && netErr.Timeout()
			if isTimeout && attempt >= 1 {
				slog.Warn("provider timeout — failing over to next provider", "provider", p.Name, "role", role, "error", err)
				break
			}
			if attempt < 2 {
				timer := time.NewTimer(time.Duration(1<<attempt) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
		}
		m.failure(session, role, p.Name)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("all %s providers failed: %w", role, lastErr)
	}
	return nil, fmt.Errorf("all %s providers failed", role)
}

// ConsumeConfigChange returns and clears the providers.json change time —
// the loop calls it once per process so the first turn after a config change
// carries the re-verify notice (never spammed).
func (m *ProviderManager) ConsumeConfigChange() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.configChangedAt
	m.configChangedAt = time.Time{}
	return t
}

func fileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func routingForRole(p ProviderConfig, role ModelRole) []string {
	if role == SmallModel && len(p.SmallRouting) > 0 {
		return p.SmallRouting
	}
	return p.ProviderRouting
}

func reasoningForRole(p ProviderConfig, role ModelRole) string {
	if role == SmallModel {
		return p.SmallReasoning
	}
	return p.ReasoningEffort
}

func modelFor(p ProviderConfig, role ModelRole) string {
	if role == SmallModel && p.Small != "" {
		return p.Small
	}
	return p.Model
}

// stripProviderPin removes the OpenRouter ":provider" routing suffix from a
// model string (e.g. "deepseek/deepseek-v4-flash-0731:deepinfra" ->
// "deepseek/deepseek-v4-flash-0731"). Models without a pin are unchanged.
// Used by the retry loop (#159): on a dead pinned route, retry the SAME model
// unpinned so OpenRouter's allow_fallbacks order routes to a healthy provider
// instead of wasting attempts on — and then failing over from — the same dead
// provider. Generic: applies to any provider-suffixed model, any user.
func stripProviderPin(model string) string {
	// OpenRouter convention: the pin is the last ":tag" AFTER the last "/"
	// (owner/name:provider). A ':' before the last slash is part of the id.
	lastSlash := strings.LastIndex(model, "/")
	lastColon := strings.LastIndex(model, ":")
	if lastSlash != -1 && lastColon > lastSlash {
		return model[:lastColon]
	}
	return model
}
func (m *ProviderManager) key(session string, role ModelRole) string {
	return session + ":" + string(role)
}

// primaryName returns the name of the highest-priority provider eligible for
// role (m.providers is kept sorted by priority ascending), or "" if none.
// Caller must hold m.mu.
func (m *ProviderManager) primaryName(role ModelRole) string {
	for _, p := range m.providers {
		if role == VisionModel && p.TextOnly {
			continue
		}
		return p.Name
	}
	return ""
}

// stickyFallbackTTL bounds how long a (session, role) key stays pinned to a
// non-primary provider before candidates() lets it retry the primary again
// (#463). Fixed-key background tasks (consolidation, reply-verify, etc. —
// memory.go/app.go pass a hardcoded literal instead of a per-turn session
// id) share one sticky slot forever: a single transient primary failure used
// to pin that slot to the fallback permanently, since success() on the
// fallback just re-confirmed the same pin every time with no expiry — so the
// slot never got a chance to notice the primary's problem was fixed hours
// later. A pin to the primary itself never needs this check (there's nothing
// better to route back to).
const stickyFallbackTTL = 5 * time.Minute

func (m *ProviderManager) candidates(session string, role ModelRole) []ProviderConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var out []ProviderConfig
	key := m.key(session, role)
	name := m.sticky[key]
	if name != "" {
		if st := m.state[name]; st == nil || !st.openUntil.Before(now) {
			name = "" // stale sticky (provider removed or cooling down): fall through to the normal chain
		}
	}
	if _, explicit := m.preferred[key]; !explicit && name != "" && name != m.primaryName(role) && now.Sub(m.stickyAt[key]) > stickyFallbackTTL {
		name = "" // #463: give the primary a periodic chance to prove it's healthy again
	}
	if name != "" {
		for _, p := range m.providers {
			if p.Name == name && !(role == VisionModel && p.TextOnly) {
				if pref := m.preferred[m.key(session, role)]; pref.provider == p.Name {
					p.Model, p.ReasoningEffort = pref.model, pref.reasoning
				}
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
	key := m.key(session, role)
	if m.stickyAt == nil {
		m.stickyAt = map[string]time.Time{}
	}
	// stickyAt marks when the pin FIRST landed on this provider, not the
	// latest success — refreshing it on every success would keep the
	// stickyFallbackTTL clock permanently at zero and recreate #463.
	if m.sticky[key] != name {
		m.stickyAt[key] = m.now()
	}
	m.sticky[key] = name
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
		// CTX-010: the circuit-breaker trip is the failover moment — log it.
		slog.Warn("provider circuit opened", "provider", name, "role", role, "session", session, "open_for", 60*time.Second)
	}
}

// SetPreferred forces a session to use a specific provider.
func (m *ProviderManager) SetPreferred(session, provider string) error {
	return m.SetPreferredModel(session, provider, "", "")
}

// SetPreferredModel selects a provider plus one of its advertised model and reasoning options.
func (m *ProviderManager) SetPreferredModel(session, provider, model, reasoning string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *ProviderConfig
	for _, p := range m.providers {
		if p.Name == provider {
			copy := p
			selected = &copy
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("unknown provider: %s", provider)
	}
	if model == "" {
		model = selected.Model
	}
	allowedModel := model == selected.Model
	for _, candidate := range selected.Models {
		allowedModel = allowedModel || model == candidate
	}
	if !allowedModel {
		return fmt.Errorf("model %s is not configured for provider %s", model, provider)
	}
	if reasoning == "" {
		reasoning = selected.ReasoningEffort
	}
	if reasoning == "" {
		reasoning = "default"
	}
	if reasoning != "default" {
		allowedReasoning := false
		for _, candidate := range selected.ReasoningLevels {
			allowedReasoning = allowedReasoning || reasoning == candidate
		}
		if !allowedReasoning {
			return fmt.Errorf("reasoning %s is not configured for provider %s", reasoning, provider)
		}
	}
	key, err := m.resolveKey(*selected)
	if err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("provider %s has no API key configured", provider)
	}
	selectionKey := m.key(session, MainModel)
	m.sticky[selectionKey] = provider
	if m.preferred == nil {
		m.preferred = map[string]providerPreference{}
	}
	m.preferred[selectionKey] = providerPreference{provider: provider, model: model, reasoning: reasoning}
	return nil
}

// ActiveProvider returns the current sticky provider for a session, or "" if none.
func (m *ProviderManager) ActiveProvider(session string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sticky[m.key(session, MainModel)]
}

// ActiveModel returns the main model configured for the session's sticky provider.
func (m *ProviderManager) ActiveModel(session string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	selectionKey := m.key(session, MainModel)
	name := m.sticky[selectionKey]
	if pref := m.preferred[selectionKey]; pref.provider == name && pref.model != "" {
		return pref.model
	}
	for _, p := range m.providers {
		if p.Name == name {
			return p.Model
		}
	}
	return ""
}

func (m *ProviderManager) ActiveReasoning(session string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	selectionKey := m.key(session, MainModel)
	name := m.sticky[selectionKey]
	if pref := m.preferred[selectionKey]; pref.provider == name && pref.reasoning != "" {
		return pref.reasoning
	}
	for _, p := range m.providers {
		if p.Name == name && p.ReasoningEffort != "" {
			return p.ReasoningEffort
		}
	}
	return "default"
}

func (m *ProviderManager) ProviderOptions() []ProviderOption {
	m.mu.Lock()
	defer m.mu.Unlock()
	options := make([]ProviderOption, 0, len(m.providers))
	for _, p := range m.providers {
		models := append([]string(nil), p.Models...)
		if len(models) == 0 {
			models = []string{p.Model}
		}
		levels := append([]string(nil), p.ReasoningLevels...)
		if len(levels) == 0 {
			levels = []string{"default"}
		}
		options = append(options, ProviderOption{Name: p.Name, Model: p.Model, Models: models, ReasoningLevels: levels})
	}
	return options
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

// ReloadProviders re-reads providers.json and adds any new providers.
func (m *ProviderManager) ReloadProviders(home string) error {
	configs, err := loadProviders(home, nil)
	if err != nil {
		return err
	}
	h := fileHash(filepath.Join(home, "providers.json"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if h != "" && m.providersHash != "" && h != m.providersHash {
		m.configChangedAt = time.Now()
	}
	m.providersHash = h
	// prune removed providers
	seen := map[string]bool{}
	for _, p := range configs {
		seen[p.Name] = true
	}
	for name := range m.clients {
		if !seen[name] {
			delete(m.clients, name)
			delete(m.state, name)
			// drop sticky/preferred entries pointing at the removed provider
			// so candidates() never sees a stale name (panic guard at the
			// lookup site is the backstop; pruning is the cleanup).
			for k, v := range m.sticky {
				if v == name {
					delete(m.sticky, k)
				}
			}
			for k, v := range m.preferred {
				if v.provider == name {
					delete(m.preferred, k)
				}
			}
		}
	}
	m.providers = m.providers[:0]
	for _, p := range configs {
		if _, exists := m.clients[p.Name]; exists {
			m.providers = append(m.providers, p)
			continue
		}
		key, _ := m.resolveKey(p)
		c := NewClient(key, p.BaseURL)
		c.usageDB = m.db
		c.transport = p.Transport
		c.providerRouting = p.ProviderRouting
		c.allowFallbacks = p.AllowFallbacks
		m.clients[p.Name] = c
		m.state[p.Name] = &providerState{}
		m.providers = append(m.providers, p)
	}
	sort.SliceStable(m.providers, func(i, j int) bool { return m.providers[i].Priority < m.providers[j].Priority })
	return nil
}
