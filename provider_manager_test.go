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

func failCall(*Client, string) (*LLMResponse, error) { return nil, errors.New("down") }

func TestRetryBackoff(t *testing.T) {
	m := testManager()
	var sleeps []time.Duration
	m.sleep = func(d time.Duration) { sleeps = append(sleeps, d) }
	calls := 0
	resp, err := m.call("s", MainModel, func(_ *Client, model string) (*LLMResponse, error) {
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
	resp, err := m.call("s", MainModel, func(_ *Client, model string) (*LLMResponse, error) {
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
	if _, err := m.call("s", MainModel, func(*Client, string) (*LLMResponse, error) {
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
