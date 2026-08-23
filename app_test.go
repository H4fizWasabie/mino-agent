package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// #240 — the threshold warning template is LOCKED (owner decision): exactly
// two safe options, never skipping verification or rushing. This guardrail
// keeps a future edit from degrading the wording silently.
func TestContextBudgetBlockGuardrail(t *testing.T) {
	block := contextBudgetBlock(95000, 100000)
	if !strings.Contains(block, "WARNING: context at 95% of the ceiling") {
		t.Fatalf("warning missing or wrong pct: %q", block)
	}
	for _, want := range []string{"compact", "manage_memory/consolidate", "status report", "what's done and what remains"} {
		if !strings.Contains(block, want) {
			t.Fatalf("warning must contain %q: %q", want, block)
		}
	}
	for _, banned := range []string{"skip", "quickly", "rush"} {
		if strings.Contains(strings.ToLower(block), banned) {
			t.Fatalf("warning must not contain %q: %q", banned, block)
		}
	}
}

func TestContextBudgetBlockNumbersAndThresholds(t *testing.T) {
	// small turn: block with real numbers, no warning
	block := contextBudgetBlock(3000, 100000)
	if block != "context budget: 3000 chars used of 100000 ceiling (3%), 97000 headroom" {
		t.Fatalf("small-turn block = %q", block)
	}
	if strings.Contains(block, "WARNING") {
		t.Fatalf("small turn must not warn: %q", block)
	}
	// at the 70% threshold the warning fires
	if !strings.Contains(contextBudgetBlock(70000, 100000), "WARNING: context at 70% of the ceiling") {
		t.Fatalf("70%% turn must warn")
	}
	// at the 90% level the same locked template fires with the real N
	if !strings.Contains(contextBudgetBlock(90000, 100000), "WARNING: context at 90% of the ceiling") {
		t.Fatalf("90%% turn must warn")
	}
	// clamping: usage over the ceiling reports 100%, never more
	if !strings.Contains(contextBudgetBlock(150000, 100000), "(100%), 0 headroom") {
		t.Fatalf("over-ceiling usage must clamp to 100%%")
	}
	// no usable ceiling: no block at all
	if got := contextBudgetBlock(500, 0); got != "" {
		t.Fatalf("zero ceiling must yield no block, got %q", got)
	}
}

// #240 — the budget block must land in the per-turn tail with real numbers,
// and the warning only when the turn is close to the ceiling.
func TestContextBudgetBlockInTurnTail(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer srv.Close()
	t.Setenv("MINO_TEST_KEY", "k")
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "providers.json"), []byte(`{"providers":[{"name":"t","priority":1,"base_url":"`+srv.URL+`","api_key_env":"MINO_TEST_KEY","model":"test-model"}]}`), 0600)
	pm, err := NewProviderManager(home, &Settings{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := &Settings{Home: home, ContextChars: 20000, MaxTokens: 100, MaxIter: 5, Timezone: "Asia/Kuala_Lumpur"}
	core := &Core{Settings: settings, Client: pm, Sessions: NewSessionManager(settings, nil), Tools: NewRegistry()}

	lastUser := func() string {
		t.Helper()
		if len(bodies) == 0 {
			t.Fatal("no provider request captured")
		}
		var p struct {
			Messages []struct {
				Role    string
				Content string
			}
		}
		if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &p); err != nil {
			t.Fatalf("parse request: %v", err)
		}
		return p.Messages[len(p.Messages)-1].Content
	}

	// small turn: block present with real numbers, no warning
	core.RespondForContext(context.Background(), "small", "hello", "test", nil, false)
	tail := lastUser()
	budgetRe := regexp.MustCompile(`context budget: \d+ chars used of 20000 ceiling \(\d+%\), \d+ headroom`)
	if !budgetRe.MatchString(tail) {
		t.Fatalf("turn tail missing budget block: %q", tail)
	}
	if strings.Contains(tail, "WARNING") {
		t.Fatalf("small turn must not warn: %q", tail)
	}

	// heavy turn: seed history until the next turn sits near the ceiling
	sess := core.Sessions.Get("heavy").Session
	for i := 0; i < 8; i++ {
		sess.AddExchange("seed", strings.Repeat("user "+strconv.Itoa(i)+" ", 400), strings.Repeat("assistant "+strconv.Itoa(i)+" ", 400), nil, "test")
	}
	core.RespondForContext(context.Background(), "heavy", "wrap up", "test", nil, false)
	tail = lastUser()
	warnRe := regexp.MustCompile(`WARNING: context at ([0-9]+)% of the ceiling — compact or consolidate \(manage_memory/consolidate\), or wrap up with a status report of what's done and what remains\.`)
	m := warnRe.FindStringSubmatch(tail)
	if m == nil {
		t.Fatalf("heavy turn missing threshold warning: %q", tail)
	}
	pct, _ := strconv.Atoi(m[1])
	if pct < 70 {
		t.Fatalf("warning pct = %d, want >= 70", pct)
	}
}

// CTX-022 C (round 3): the harness signs contradictions between the draft
// reply and owner-established facts. Parse layer locks the verdict handling.
func TestParseVerifyResponse(t *testing.T) {
	cases := []struct {
		text string
		ok   bool
	}{
		{`{"ok": true}`, true},
		{`{"ok": false, "reason": "reply says alive but owner deleted it"}`, false},
		{`Sure thing! {"ok": false, "reason": "contradicts deletion"} trailing`, false},
		{`no json here`, true},
	}
	for _, c := range cases {
		ok, reason := parseVerifyResponse(c.text)
		if ok != c.ok {
			t.Fatalf("parseVerifyResponse(%q) ok=%v want %v", c.text, ok, c.ok)
		}
		if !c.ok && reason == "" {
			t.Fatalf("non-ok verdict missing reason: %q", c.text)
		}
	}
}
