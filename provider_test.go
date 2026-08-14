package main

import (
	"encoding/json"
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
