package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCodexToken(accountID string) string {
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": accountID},
	})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func withCodexURLs(t *testing.T, serverURL string) {
	t.Helper()
	oldStart, oldPoll, oldToken, oldVerify := codexDeviceStartURL, codexDevicePollURL, codexTokenURL, codexDeviceURL
	codexDeviceStartURL = serverURL + "/start"
	codexDevicePollURL = serverURL + "/poll"
	codexTokenURL = serverURL + "/token"
	codexDeviceURL = serverURL + "/verify"
	t.Cleanup(func() {
		codexDeviceStartURL, codexDevicePollURL, codexTokenURL, codexDeviceURL = oldStart, oldPoll, oldToken, oldVerify
	})
}

func newCodexTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	return server
}

func TestCodexDeviceLoginStoresOAuthSession(t *testing.T) {
	access := testCodexToken("account-123")
	server := newCodexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "device-1", "user_code": "ABCD-EFGH", "interval": "5"})
		case "/poll":
			json.NewEncoder(w).Encode(map[string]string{"authorization_code": "code-1", "code_verifier": "verifier-1"})
		case "/token":
			if r.FormValue("redirect_uri") != codexDeviceRedirect {
				t.Errorf("unexpected redirect URI: %q", r.FormValue("redirect_uri"))
				http.Error(w, "bad redirect", http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "refresh-1", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	withCodexURLs(t, server.URL)

	home := t.TempDir()
	store := LoadAuthStore(home)
	engine := LoadOAuthEngine(home, store, "http://localhost")
	engine.providerMap["codex"] = &OAuthProvider{Name: "codex", APIBaseURL: "https://chatgpt.com/backend-api/codex", Models: []string{"gpt-5.4"}}
	verifyURL, userCode, deviceCode, interval, err := engine.BeginCodexDeviceLogin()
	if err != nil {
		t.Fatal(err)
	}
	if verifyURL != server.URL+"/verify" || userCode != "ABCD-EFGH" || deviceCode != "device-1" || interval != 5 {
		t.Fatalf("unexpected device response: %q %q %q %d", verifyURL, userCode, deviceCode, interval)
	}
	done, err := engine.PollCodexDeviceLogin(deviceCode)
	if err != nil || !done {
		t.Fatalf("poll: done=%v err=%v", done, err)
	}
	entry, ok := store.GetEntry("codex")
	if !ok || entry.Type != "oauth" || entry.Key != access || entry.Refresh != "refresh-1" || entry.AccountID != "account-123" {
		t.Fatalf("unexpected stored credential: %+v", entry)
	}
	info, err := os.Stat(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode: %v", info.Mode().Perm())
	}
}

func TestCodexOAuthRefresh(t *testing.T) {
	access := testCodexToken("account-new")
	server := newCodexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh-old" {
			t.Errorf("unexpected refresh form: %v", r.Form)
			http.Error(w, "bad refresh", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": "refresh-new", "expires_in": 3600})
	}))
	defer server.Close()
	withCodexURLs(t, server.URL)

	store := LoadAuthStore(t.TempDir())
	if err := store.SetOAuth("codex", "expired", "refresh-old", time.Now().Add(-time.Hour).Unix(), "account-old"); err != nil {
		t.Fatal(err)
	}
	manager := &ProviderManager{authStore: store}
	key, err := manager.resolveKey(ProviderConfig{Name: "codex"})
	if err != nil || key != access {
		t.Fatalf("refresh: key=%q err=%v", key, err)
	}
	entry, _ := store.GetEntry("codex")
	if entry.Refresh != "refresh-new" || entry.AccountID != "account-new" {
		t.Fatalf("refresh was not persisted: %+v", entry)
	}
}

func TestCodexResponsesTransport(t *testing.T) {
	token := testCodexToken("account-123")
	server := newCodexTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("chatgpt-account-id") != "account-123" {
			t.Error("missing Codex auth headers")
			http.Error(w, "missing headers", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		reasoning, _ := body["reasoning"].(map[string]any)
		_, hasMaxTokens := body["max_output_tokens"]
		if body["stream"] != true || body["instructions"] != "system" || reasoning["effort"] != "high" || hasMaxTokens {
			t.Errorf("unexpected request body: %v", body)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"type":"response.output_text.delta","delta":"Hello"}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3}}}`)
		fmt.Fprintln(w)
	}))
	defer server.Close()

	client := NewClient(token, server.URL+"/codex")
	var streamed strings.Builder
	response, err := client.create("gpt-5.6-sol", "high", []Message{{Role: "user", Content: "hello"}}, 100, "system", []ToolDef{{Name: "read_file"}}, true, func(delta string) { streamed.WriteString(delta) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Hello" || response.FinalText != "Hello" || response.StopReason != "tool_use" {
		t.Fatalf("unexpected response: streamed=%q response=%+v", streamed.String(), response)
	}
	if response.Usage.InputTokens != 7 || len(extractToolUses(response.Content)) != 1 {
		t.Fatalf("missing usage or tool call: %+v", response)
	}
}

func TestCodexAccountIDRejectsAPIKey(t *testing.T) {
	if _, err := codexAccountID(url.QueryEscape("sk-not-oauth")); err == nil {
		t.Fatal("expected a non-OAuth key to be rejected")
	}
}

func TestEnsureProviderRefreshesModelChoices(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"codex","priority":10,"base_url":"https://chatgpt.com/backend-api/codex","model":"gpt-5.4"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	engine := LoadOAuthEngine(home, LoadAuthStore(home), "http://localhost")
	engine.EnsureProvider(&OAuthProvider{Name: "codex", Models: []string{"gpt-5.6-sol", "gpt-5.6-terra"}, Reasoning: []string{"default", "high"}})
	providers, err := loadProviders(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if providers[0].Model != "gpt-5.6-sol" || providers[0].Small != "gpt-5.6-terra" || len(providers[0].Models) != 2 || providers[0].ReasoningLevels[1] != "high" {
		t.Fatalf("provider choices were not refreshed: %+v", providers[0])
	}
}
