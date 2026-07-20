package main

import (
	"errors"
	"testing"
	"time"
)

func testManager() *ProviderManager {
	return &ProviderManager{
		providers: []ProviderConfig{{Name: "mimo", Priority: 1, Model: "mimo"}, {Name: "backup", Priority: 2, Model: "backup"}},
		state:     map[string]*providerState{"mimo": {}, "backup": {}}, sticky: map[string]string{}, now: func() time.Time { return time.Unix(100, 0) }, sleep: func(time.Duration) {},
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
	response, err := m.call("s", MainModel, func(_ *Client, model, reasoning string) (*LLMResponse, error) {
		return &LLMResponse{FinalText: model + ":" + reasoning}, nil
	})
	if err != nil || response.FinalText != "backup-fast:high" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func failCall(*Client, string, string) (*LLMResponse, error) { return nil, errors.New("down") }

func TestRetryBackoff(t *testing.T) {
	m := testManager()
	var sleeps []time.Duration
	m.sleep = func(d time.Duration) { sleeps = append(sleeps, d) }
	calls := 0
	resp, err := m.call("s", MainModel, func(_ *Client, model, _ string) (*LLMResponse, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("down")
		}
		return &LLMResponse{FinalText: model}, nil
	})
	if err != nil || resp.FinalText != "mimo" {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	want := []time.Duration{time.Second, 2 * time.Second}
	if len(sleeps) != 2 || sleeps[0] != want[0] || sleeps[1] != want[1] {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	if m.state["mimo"].failures != 0 || m.sticky[m.key("s", MainModel)] != "mimo" {
		t.Fatalf("state=%+v sticky=%v", m.state["mimo"], m.sticky)
	}
}

func TestFallback(t *testing.T) {
	m := testManager()
	resp, err := m.call("s", MainModel, func(_ *Client, model, _ string) (*LLMResponse, error) {
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
		if _, err := m.call("s", MainModel, failCall); err == nil {
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
	if _, err := m.call("s", MainModel, func(*Client, string, string) (*LLMResponse, error) {
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
		sleep:  func(time.Duration) {},
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
	if _, err := m.call("s", VisionModel, failCall); err == nil {
		t.Fatal("expected error when no vision-capable provider")
	}
}
