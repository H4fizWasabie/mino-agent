package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PRV-001: loadProviders is a pure config read — model lists are NOT injected
// from Go constants anymore; the codex login flow (EnsureProvider) writes
// models/transport into providers.json from oauth.d config. An explicit
// config carries through untouched; an empty one stays empty (the UI refresh
// fixes it on the next login).
func TestLoadProvidersDoesNotInjectModels(t *testing.T) {
	home := t.TempDir()
	data, _ := json.Marshal(providerFile{Providers: []ProviderConfig{{
		Name: "codex", BaseURL: "https://chatgpt.com/backend-api/codex", Model: "gpt-5.5",
	}}})
	if err := os.WriteFile(filepath.Join(home, "providers.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	providers, err := loadProviders(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || len(providers[0].Models) != 0 {
		t.Fatalf("models = %#v, want none injected (config is the truth, PRV-001)", providers[0].Models)
	}
}

func TestParseSSEStreamAcceptsReasoningContent(t *testing.T) {
	var streamed strings.Builder
	response, err := parseSSEStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Hello from streaming\"}}]}\n\ndata: [DONE]\n"), func(delta string) {
		streamed.WriteString(delta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 1 || response.Content[0].Text != "Hello from streaming" || streamed.String() != response.Content[0].Text {
		t.Fatalf("streamed=%q response=%+v", streamed.String(), response)
	}
}

func testManager() *ProviderManager {
	return &ProviderManager{
		providers: []ProviderConfig{{Name: "mimo", Priority: 1, Model: "mimo"}, {Name: "backup", Priority: 2, Model: "backup"}},
		state:     map[string]*providerState{"mimo": {}, "backup": {}}, sticky: map[string]string{}, now: func() time.Time { return time.Unix(100, 0) },
	}
}

func TestProviderCandidates(t *testing.T) {
	m := testManager()
	if got := m.candidates("s", MainModel); got[0].Name != "mimo" {
		t.Fatalf("first = %s", got[0].Name)
	}
	m.success("s", MainModel, "backup")
	if got := m.candidates("s", MainModel); got[0].Name != "backup" {
		t.Fatalf("sticky = %s", got[0].Name)
	}
	for range 3 {
		m.failure("s", MainModel, "backup")
	}
	got := m.candidates("s", MainModel)
	if len(got) != 1 || got[0].Name != "mimo" {
		t.Fatalf("open circuit candidates = %#v", got)
	}
}

func TestModelFor(t *testing.T) {
	p := ProviderConfig{Model: "main", Small: "small"}
	if got := modelFor(p, SmallModel); got != "small" {
		t.Fatalf("small model = %q", got)
	}
	if got := modelFor(ProviderConfig{Model: "main"}, SmallModel); got != "main" {
		t.Fatalf("fallback model = %q", got)
	}
}

func TestPreferredModelAndReasoningFollowProvider(t *testing.T) {
	m := testManager()
	m.providers[1].Models = []string{"backup", "backup-fast"}
	m.providers[1].ReasoningLevels = []string{"default", "low", "high"}
	m.authStore = &AuthStore{data: map[string]AuthEntry{"backup": {Key: "token"}}}
	if err := m.SetPreferredModel("s", "backup", "backup-fast", "high"); err != nil {
		t.Fatal(err)
	}
	if got := m.ActiveModel("s"); got != "backup-fast" {
		t.Fatalf("active model = %q", got)
	}
	if got := m.ActiveReasoning("s"); got != "high" {
		t.Fatalf("active reasoning = %q", got)
	}
	response, err := m.callWithConfig("s", MainModel, func(_ *Client, model, reasoning string, _ ProviderConfig) (*LLMResponse, error) {
		return &LLMResponse{FinalText: model + ":" + reasoning}, nil
	})
	if err != nil || response.FinalText != "backup-fast:high" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func failCall(*Client, string, string, ProviderConfig) (*LLMResponse, error) { return nil, errors.New("down") }

func TestRetryBackoff(t *testing.T) {
	m := testManager()
	calls := 0
	start := time.Now()
	resp, err := m.callWithConfig("s", MainModel, func(_ *Client, model, _ string, _ ProviderConfig) (*LLMResponse, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("down")
		}
		return &LLMResponse{FinalText: model}, nil
	})
	elapsed := time.Since(start)
	if err != nil || resp.FinalText != "mimo" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	// Backoff is 1s then 2s between the three attempts (timer+select path;
	// the old m.sleep seam was removed with the call/callWithConfig split).
	if elapsed < 3*time.Second {
		t.Fatalf("retries did not back off: elapsed %v, want >= 3s", elapsed)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if m.state["mimo"].failures != 0 || m.sticky[m.key("s", MainModel)] != "mimo" {
		t.Fatalf("state=%+v sticky=%v", m.state["mimo"], m.sticky)
	}
}

func TestFallback(t *testing.T) {
	m := testManager()
	resp, err := m.callWithConfig("s", MainModel, func(_ *Client, model, _ string, _ ProviderConfig) (*LLMResponse, error) {
		if model == "mimo" {
			return nil, errors.New("down")
		}
		return &LLMResponse{FinalText: model}, nil
	})
	if err != nil || resp.FinalText != "backup" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if m.state["mimo"].failures != 1 {
		t.Fatalf("mimo failures = %d", m.state["mimo"].failures)
	}
	if m.sticky[m.key("s", MainModel)] != "backup" {
		t.Fatalf("sticky = %q", m.sticky[m.key("s", MainModel)])
	}
}

func TestCircuitOpenAndRecovery(t *testing.T) {
	m := testManager()
	now := time.Unix(100, 0)
	m.now = func() time.Time { return now }
	m.success("s", MainModel, "mimo") // sticky must clear when circuit opens
	for range 3 {
		if _, err := m.callWithConfig("s", MainModel, failCall); err == nil {
			t.Fatal("want error while providers failing")
		}
	}
	if got := m.candidates("s", MainModel); len(got) != 0 {
		t.Fatalf("candidates with both circuits open = %v", got)
	}
	if len(m.sticky) != 0 {
		t.Fatalf("sticky not cleared: %v", m.sticky)
	}
	calls := 0
	if _, err := m.callWithConfig("s", MainModel, func(*Client, string, string, ProviderConfig) (*LLMResponse, error) {
		calls++
		return nil, errors.New("down")
	}); err == nil || calls != 0 {
		t.Fatalf("open circuit must fail fast: err=%v calls=%d", err, calls)
	}
	now = now.Add(61 * time.Second)
	if got := m.candidates("s", MainModel); len(got) != 2 {
		t.Fatalf("candidates after cooldown = %v", got)
	}
}

func visionManager() *ProviderManager {
	return &ProviderManager{
		providers: []ProviderConfig{
			{Name: "pro", Priority: 1, Model: "mimo-v2.5-pro", TextOnly: true},
			{Name: "omni", Priority: 2, Model: "mimo-v2.5"},
		},
		state:  map[string]*providerState{"pro": {}, "omni": {}},
		sticky: map[string]string{},
		now:    func() time.Time { return time.Unix(100, 0) },
		
	}
}

func TestRouteRole(t *testing.T) {
	cases := []struct {
		name     string
		role     ModelRole
		messages []Message
		want     ModelRole
	}{
		{"text stays main", MainModel, []Message{{Role: "user", Content: "hi"}}, MainModel},
		{"image flips to vision", MainModel, []Message{{Role: "user", Content: "look", Images: []string{"data:image/png;base64,x"}}}, VisionModel},
		{"image in any message counts", MainModel, []Message{{Role: "user", Content: "a"}, {Role: "user", Content: "b", Images: []string{"d"}}}, VisionModel},
		{"small with image flips too", SmallModel, []Message{{Role: "user", Images: []string{"d"}}}, VisionModel},
		{"small text stays small", SmallModel, []Message{{Role: "user", Content: "hi"}}, SmallModel},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := routeRole(c.role, c.messages); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestVisionCandidatesSkipTextOnly(t *testing.T) {
	m := visionManager()
	got := m.candidates("s", VisionModel)
	if len(got) != 1 || got[0].Name != "omni" {
		t.Fatalf("vision candidates = %#v, want only omni", got)
	}
	if got := m.candidates("s", MainModel); got[0].Name != "pro" {
		t.Fatalf("main first = %s, want pro", got[0].Name)
	}
}

func TestVisionStickyDoesNotPoisonMain(t *testing.T) {
	m := visionManager()
	m.success("s", VisionModel, "omni") // image turn landed on omni
	if got := m.candidates("s", MainModel); got[0].Name != "pro" {
		t.Fatalf("main after vision turn = %s, want pro", got[0].Name)
	}
}

func TestAllTextOnlyVisionFails(t *testing.T) {
	m := visionManager()
	m.providers = m.providers[:1] // only text-only pro remains
	if _, err := m.callWithConfig("s", VisionModel, failCall); err == nil {
		t.Fatal("expected error when no vision-capable provider")
	}
}

func TestRoutingForRoleUsesSmallRoute(t *testing.T) {
	p := ProviderConfig{ProviderRouting: []string{"OpenAI"}, SmallRouting: []string{"GMICloud"}}
	if got := routingForRole(p, MainModel); len(got) != 1 || got[0] != "OpenAI" {
		t.Fatalf("main routing = %#v, want OpenAI", got)
	}
	if got := routingForRole(p, SmallModel); len(got) != 1 || got[0] != "GMICloud" {
		t.Fatalf("small routing = %#v, want GMICloud", got)
	}
}

func TestReasoningForRoleKeepsSmallModelIndependent(t *testing.T) {
	p := ProviderConfig{ReasoningEffort: "medium"}
	if got := reasoningForRole(p, MainModel); got != "medium" {
		t.Fatalf("main reasoning = %q, want medium", got)
	}
	if got := reasoningForRole(p, SmallModel); got != "" {
		t.Fatalf("small reasoning = %q, want omitted", got)
	}
}

func TestNoSessionIDSentToOpenRouter(t *testing.T) {
	// session_id was removed: empirically it makes DeepInfra prompt-cache hits
	// unreliable (alternating hit/miss on identical prefixes; OpenRouter's
	// session pinning spreads requests across upstream replicas). Without it,
	// OpenRouter's default conversation-hash stickiness keeps the cache warm
	// (verified: 100% hits on repeated prefixes, 5x cheaper cached reads).
	var payload map[string]any
	c := NewClient("key", "https://openrouter.ai/api/v1")
	c.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{}}`)), Header: make(http.Header)}, nil
	})}
	if _, err := c.createWithRouting(context.Background(), "model-a", "", nil, 10, "system", nil, false, false, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["session_id"]; ok {
		t.Fatalf("session_id = %#v, want absent (it breaks DeepInfra prompt caching)", payload["session_id"])
	}
}

func TestParseResponseReadsOpenAICompatibleCacheUsage(t *testing.T) {
	resp, err := parseResponse(strings.NewReader(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":80,"cache_write_tokens":20}}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CacheReadTokens != 80 || resp.Usage.CacheCreationTokens != 20 {
		t.Fatalf("cache usage = %+v", resp.Usage)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// #163: the streaming path must also accept the "reasoning" field name (not
// only "reasoning_content") for providers that send thinking under it.
func TestParseSSEStreamAcceptsReasoningAltField(t *testing.T) {
	var streamed strings.Builder
	response, err := parseSSEStream(strings.NewReader("data: {\"choices\":[{\"delta\":{\"reasoning\":\"thought stream\"}}]}\n\ndata: [DONE]\n"), func(delta string) {
		streamed.WriteString(delta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Content) != 1 || response.Content[0].Text != "thought stream" {
		t.Fatalf("Content = %+v, want reasoning-alt fallback in stream", response.Content)
	}
}

// #159: the ":provider" routing pin is stripped from the model string on retry
// so a dead pinned provider doesn't burn all attempts then fail over to a
// different model. stripProviderPin must only touch the post-slash ":tag".
func TestStripProviderPin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"deepseek/deepseek-v4-flash-0731:deepinfra", "deepseek/deepseek-v4-flash-0731"},
		{"deepseek/deepseek-v4-flash-0731", "deepseek/deepseek-v4-flash-0731"},
		{"mimo-v2.5", "mimo-v2.5"},                     // no slash, no pin
		{"deepseek-v4-flash", "deepseek-v4-flash"},      // direct-API model, no pin
		{"o3:nightly", "o3:nightly"},                    // ':' before slash is part of id
	}
	for _, c := range cases {
		if got := stripProviderPin(c.in); got != c.want {
			t.Errorf("stripProviderPin(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// #159: a pinned model is retried unpinned after the first failure, so the
// retry keeps the SAME model (routed to a healthy provider) instead of waiting
// out the dead pin and then failing over to a different model.
func TestRetryDropsProviderPin(t *testing.T) {
	m := testManager()
	m.providers = []ProviderConfig{{Name: "mimo", Priority: 1, Model: "deepseek/agent:dead"}}
	var models []string
	_, err := m.callWithConfig("s", MainModel, func(_ *Client, model, _ string, _ ProviderConfig) (*LLMResponse, error) {
		models = append(models, model)
		return nil, errors.New("down") // always fail -> 3 attempts then error
	})
	if err == nil {
		t.Fatal("want error when provider always down")
	}
	if len(models) != 3 {
		t.Fatalf("attempts = %d, want 3", len(models))
	}
	if models[0] != "deepseek/agent:dead" {
		t.Fatalf("attempt 1 should keep pin: %q", models[0])
	}
	for _, mdl := range models[1:] {
		if mdl != "deepseek/agent" {
			t.Fatalf("retry should unpin: got %q", mdl)
		}
	}
}

// ISSUE-195: a sticky entry pointing at a provider removed by ReloadProviders
// must not panic candidates() — it falls through to the normal chain.
func TestCandidatesStickyToRemovedProviderFallsThrough(t *testing.T) {
	m := visionManager()
	m.sticky[m.key("s", MainModel)] = "gone" // removed provider: not in m.state
	got := m.candidates("s", MainModel)
	if len(got) != 2 || got[0].Name != "pro" || got[1].Name != "omni" {
		t.Fatalf("candidates with stale sticky = %#v, want [pro omni]", got)
	}
}

// ISSUE-195: ReloadProviders prunes sticky/preferred entries referencing
// providers that no longer exist in providers.json.
func TestReloadProvidersPrunesStickyForRemovedProvider(t *testing.T) {
	home := t.TempDir()
	write := func(models []string) {
		provs := []map[string]any{}
		for _, n := range models {
			provs = append(provs, map[string]any{"name": n, "priority": 1, "base_url": "http://x", "model": n + "/m"})
		}
		data, _ := json.Marshal(map[string]any{"providers": provs})
		if err := os.WriteFile(filepath.Join(home, "providers.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	write([]string{"pro", "omni"})
	m := &ProviderManager{
		clients:   map[string]*Client{},
		state:     map[string]*providerState{},
		sticky:    map[string]string{},
		preferred: map[string]providerPreference{},
		now:       func() time.Time { return time.Unix(100, 0) },
	}
	if err := m.ReloadProviders(home); err != nil {
		t.Fatal(err)
	}
	m.sticky[m.key("s", MainModel)] = "pro"
	m.preferred[m.key("s", MainModel)] = providerPreference{provider: "pro", model: "pro/m", reasoning: "high"}
	write([]string{"omni"}) // pro removed
	if err := m.ReloadProviders(home); err != nil {
		t.Fatal(err)
	}
	if got := m.candidates("s", MainModel); len(got) != 1 || got[0].Name != "omni" {
		t.Fatalf("candidates after prune = %#v, want only omni (no panic, no stale pro)", got)
	}
}

// ISSUE-204: ReloadProviders stamps a change time when providers.json content
// changes; ConsumeConfigChange returns it exactly once (the loop's first turn
// after the change carries the re-verify notice).
func TestReloadProvidersSignalsConfigChangeOnce(t *testing.T) {
	home := t.TempDir()
	writeProviders := func(model string) {
		data, _ := json.Marshal(map[string]any{"providers": []map[string]any{
			{"name": "pro", "priority": 1, "base_url": "http://x", "model": model},
		}})
		if err := os.WriteFile(filepath.Join(home, "providers.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeProviders("alpha")
	m := &ProviderManager{
		clients:   map[string]*Client{},
		state:     map[string]*providerState{},
		sticky:    map[string]string{},
		preferred: map[string]providerPreference{},
		now:       func() time.Time { return time.Unix(100, 0) },
	}
	if err := m.ReloadProviders(home); err != nil {
		t.Fatal(err)
	}
	if got := m.ConsumeConfigChange(); !got.IsZero() {
		t.Fatalf("first load must not signal a change, got %v", got)
	}
	writeProviders("beta")
	if err := m.ReloadProviders(home); err != nil {
		t.Fatal(err)
	}
	got := m.ConsumeConfigChange()
	if got.IsZero() {
		t.Fatal("config change not signaled")
	}
	if again := m.ConsumeConfigChange(); !again.IsZero() {
		t.Fatalf("signal must be consumed once, got %v", again)
	}
	// identical reload: no new signal
	if err := m.ReloadProviders(home); err != nil {
		t.Fatal(err)
	}
	if again := m.ConsumeConfigChange(); !again.IsZero() {
		t.Fatalf("unchanged reload must not signal, got %v", again)
	}
}
