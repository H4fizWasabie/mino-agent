package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCreateJSONRetriesWithoutResponseFormat(t *testing.T) {
	// DeepSeek v4 flash returns content:null in json_object mode (the budget
	// goes to reasoning). The client must retry once without response_format
	// so tolerant parsers can extract JSON from a normal reply.
	var first, second map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		json.Unmarshal(body, &p)
		calls++
		if calls == 1 {
			first = p
			w.Write([]byte(`{"choices":[{"message":{"content":null,"reasoning":"thinking..."},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
			return
		}
		second = p
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\": true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer srv.Close()

	c := &Client{apiKey: "k", baseURL: srv.URL, client: http.DefaultClient}
	resp, err := c.CreateJSON("deepseek/deepseek-v4-flash-0731", []Message{{Role: "user", Content: "json please"}}, 600, "")
	if err != nil {
		t.Fatalf("CreateJSON: %v", err)
	}
	if !strings.Contains(resp.FinalText, `"ok"`) {
		t.Fatalf("FinalText = %q, want JSON", resp.FinalText)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (json mode then plain)", calls)
	}
	if first["response_format"] == nil {
		t.Fatalf("first request missing response_format: %v", first)
	}
	if first["reasoning"] == nil {
		t.Fatalf("json call missing reasoning disabled: %v", first)
	}
	if second["response_format"] != nil {
		t.Fatalf("retry still has response_format: %v", second)
	}
}

// #495: every OpenRouter request carries repetition_penalty — a mitigation
// against the decode-time repetition collapse observed live 2026-08-31
// (GLM 5.3 Flash ran away to MINO_MAX_TOKENS on a non-streamed reply).
func TestCreateContextSendsRepetitionPenaltyDefault(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	c := &Client{apiKey: "k", baseURL: srv.URL, client: http.DefaultClient}
	if _, err := c.Create("model", []Message{{Role: "user", Content: "hi"}}, 100, "", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got["repetition_penalty"] != 1.1 {
		t.Fatalf("repetition_penalty = %v, want default 1.1", got["repetition_penalty"])
	}
}

func TestCreateContextSendsRepetitionPenaltyFromEnv(t *testing.T) {
	t.Setenv("MINO_REPETITION_PENALTY", "1.3")
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()

	c := &Client{apiKey: "k", baseURL: srv.URL, client: http.DefaultClient}
	if _, err := c.Create("model", []Message{{Role: "user", Content: "hi"}}, 100, "", nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got["repetition_penalty"] != 1.3 {
		t.Fatalf("repetition_penalty = %v, want configured 1.3", got["repetition_penalty"])
	}
}

// #440: GLM 5.3 flash rejects a disabled-reasoning JSON call outright
// ("Reasoning is mandatory for this endpoint and cannot be disabled") — the
// opposite requirement from DeepSeek. Both reasoning-disabled attempts must
// fail before the client drops the override and retries.
func TestCreateJSONRetriesWithoutReasoningOverrideWhenMandatory(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		json.Unmarshal(body, &p)
		calls = append(calls, p)
		if len(calls) <= 2 {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"Reasoning is mandatory for this endpoint and cannot be disabled.","code":400}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\": true}"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()

	c := &Client{apiKey: "k", baseURL: srv.URL, client: http.DefaultClient}
	resp, err := c.CreateJSON("z-ai/glm-5.3-flash", []Message{{Role: "user", Content: "json please"}}, 600, "")
	if err != nil {
		t.Fatalf("CreateJSON: %v", err)
	}
	if !strings.Contains(resp.FinalText, `"ok"`) {
		t.Fatalf("FinalText = %q, want JSON", resp.FinalText)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3 (2 reasoning-disabled attempts, then the fallback that succeeds)", len(calls))
	}
	if calls[0]["reasoning"] == nil || calls[1]["reasoning"] == nil {
		t.Fatalf("first two attempts must still try reasoning disabled: %v / %v", calls[0], calls[1])
	}
	if calls[2]["reasoning"] != nil {
		t.Fatalf("fallback attempt must drop the reasoning override: %v", calls[2])
	}
}

// Issue found during v3.6.2 live eval: GLM 5.3 flash puts its JSON answer
// under reasoning on every attempt of the retry ladder — reasoning disabled
// or not, response_format set or not — so all four attempts previously
// failed with "empty model response" even after #440's reasoning-override
// fallback. The true last resort (reasoning override dropped, no
// response_format) must promote reasoning into content instead of failing.
func TestCreateJSONPromotesReasoningOnFinalAttempt(t *testing.T) {
	var calls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		json.Unmarshal(body, &p)
		calls = append(calls, p)
		w.Write([]byte(`{"choices":[{"message":{"content":null,"reasoning":"thinking... {\"ok\": true}"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()

	c := &Client{apiKey: "k", baseURL: srv.URL, client: http.DefaultClient}
	resp, err := c.CreateJSON("z-ai/glm-5.3-flash", []Message{{Role: "user", Content: "json please"}}, 600, "")
	if err != nil {
		t.Fatalf("CreateJSON: %v", err)
	}
	if !strings.Contains(resp.FinalText, `"ok"`) {
		t.Fatalf("FinalText = %q, want the reasoning-embedded JSON promoted into content", resp.FinalText)
	}
	if len(calls) != 4 {
		t.Fatalf("calls = %d, want 4 (every ladder rung exhausted before promotion)", len(calls))
	}
}

func TestCreateJSONNoRetryOnSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\": 1}"},"finish_reason":"stop"}],"usage":{}}`))
	}))
	defer srv.Close()

	c := &Client{apiKey: "k", baseURL: srv.URL, client: http.DefaultClient}
	if _, err := c.CreateJSON("m", []Message{{Role: "user", Content: "hi"}}, 100, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on success)", calls)
	}
}

func TestParseResponseReadsReasoningContentFallback(t *testing.T) {
	// MiMo-style: content empty, answer in reasoning_content (legacy field)
	resp, err := parseResponse(strings.NewReader(`{"choices":[{"message":{"content":"","reasoning_content":"the answer"},"finish_reason":"stop"}],"usage":{}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinalText != "the answer" {
		t.Fatalf("FinalText = %q, want reasoning_content fallback", resp.FinalText)
	}
}

// #163: some OpenAI-compatible providers (e.g. qwen via OpenRouter) surface the
// thinking trace under "reasoning" instead of DeepSeek's "reasoning_content",
// leaving content null. Without capturing the alternate name the whole response
// was dropped as "empty model response" — wasting the only remaining fallback.
func TestParseResponseReadsReasoningAltField(t *testing.T) {
	resp, err := parseResponse(strings.NewReader(`{"choices":[{"message":{"content":null,"reasoning":"the answer in thought"},"finish_reason":"stop"}],"usage":{}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinalText != "the answer in thought" {
		t.Fatalf("FinalText = %q, want reasoning-alt fallback", resp.FinalText)
	}
}

// PRV-001: the wire family is declared in config, never sniffed from the URL.
// Default transport is OpenAI-compatible; "codex"/"anthropic" opt in explicitly.
func TestClientTransportIsDeclaredNotSniffed(t *testing.T) {
	// Default: OpenAI-compatible even for URLs that used to auto-detect.
	c := NewClient("k", "https://chatgpt.com/backend-api/codex")
	if c.isCodex() || c.isAnthropic() {
		t.Fatal("transport must default to openai — URL sniffing is gone (PRV-001)")
	}
	// Declared codex transport wins regardless of URL.
	c.transport = "codex"
	if !c.isCodex() {
		t.Fatal("declared codex transport not honored")
	}
	if c.isAnthropic() {
		t.Fatal("codex transport misdetected as anthropic")
	}
	// Declared anthropic transport.
	c2 := NewClient("k", "https://api.example.com")
	c2.transport = "anthropic"
	if !c2.isAnthropic() || c2.isCodex() {
		t.Fatal("declared anthropic transport not honored")
	}
}

// PRV-001: the codex model list lives in oauth.d config (written into
// providers.json on login) — nothing hardcoded in Go survives.
func TestCodexModelsComeFromOAuthConfig(t *testing.T) {
	data, err := os.ReadFile("oauth.d/codex.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"transport": "codex"`) {
		t.Fatal("oauth.d/codex.json must declare transport: codex (PRV-001)")
	}
	if !strings.Contains(string(data), `"models"`) {
		t.Fatal("oauth.d/codex.json must carry the model list (PRV-001)")
	}
}

// Wedge guard (2026-08-14): provider requests must declare identity encoding —
// the transport's gzip layer is what ate the body-close that should have
// unblocked a stalled read (the h2+gzip deadlock that wedged a live session
// until a service restart).
func TestProviderRequestsIdentityEncoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			t.Fatalf("request accepts gzip: %q — the wedge guard is missing", r.Header.Get("Accept-Encoding"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	client := NewClient("test-key", srv.URL)
	resp, err := client.create(context.Background(), "test-model", "", []Message{{Role: "user", Content: "hi"}}, 100, "", nil, false, false, nil)
	if err != nil || resp.FinalText != "ok" {
		t.Fatalf("call failed: err=%v resp=%+v", err, resp)
	}
}
