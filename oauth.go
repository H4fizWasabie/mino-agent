package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OAuthProvider config — one JSON file in oauth.d/ per provider.
type OAuthProvider struct {
	Name        string   `json:"name"`         // machine name ("codex", "claude")
	DisplayName string   `json:"display_name"` // "ChatGPT (Codex)"
	AuthType    string   `json:"auth_type"`    // "pkce" | "device_code"
	AuthorizeURL string  `json:"authorize_url"`
	TokenURL    string   `json:"token_url"`
	DeviceCodeURL string `json:"device_code_url,omitempty"` // for device_code flow
	ClientID    string   `json:"client_id"`
	Scopes      []string `json:"scopes"`
	APIBaseURL  string   `json:"api_base_url"`  // where to send LLM requests
	APIKeyURL   string   `json:"api_key_url,omitempty"` // exchange oauth token for api key (Codex)
	Models      []string `json:"models"`        // available models
	Extra       map[string]any `json:"extra,omitempty"`
}

type pendingOAuth struct {
	provider     string
	state        string
	codeVerifier string
	createdAt    time.Time
	deviceCode   string // for polling
	interval     int
	expiresAt    time.Time
}

// OAuthEngine handles browser-based login flows.
// Uses the dashboard's HTTP server for callbacks (no separate listener).
type OAuthEngine struct {
	home            string
	authStore       *AuthStore
	providerMap     map[string]*OAuthProvider
	pending         map[string]*pendingOAuth
	redirectBaseURL string
	mu              sync.Mutex
}

func LoadOAuthEngine(home string, authStore *AuthStore, redirectBaseURL string) *OAuthEngine {
	e := &OAuthEngine{
		home:            home,
		authStore:       authStore,
		providerMap:     map[string]*OAuthProvider{},
		pending:         map[string]*pendingOAuth{},
		redirectBaseURL: strings.TrimRight(redirectBaseURL, "/"),
	}
	e.loadProviders()
	return e
}

func (e *OAuthEngine) loadProviders() {
	dir := filepath.Join(e.home, "oauth.d")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no providers configured
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var p OAuthProvider
		if json.Unmarshal(data, &p) != nil || p.Name == "" {
			continue
		}
		e.providerMap[p.Name] = &p
	}
	slog.Info("oauth providers loaded", "count", len(e.providerMap), "callback", e.redirectBaseURL+"/callback")
}

// BeginPKCE starts a PKCE OAuth flow. Returns the URL to open in browser.
func (e *OAuthEngine) BeginPKCE(providerName string) (authURL string, err error) {
	p := e.providerMap[providerName]
	if p == nil {
		return "", fmt.Errorf("unknown oauth provider: %s", providerName)
	}
	if p.AuthType != "pkce" {
		return "", fmt.Errorf("provider %s uses %s, not pkce", providerName, p.AuthType)
	}

	state := randHex(16)
	verifier := randHex(64)
	challenge := sha256b64(verifier)

	e.mu.Lock()
	e.pending[state] = &pendingOAuth{
		provider:     providerName,
		state:        state,
		codeVerifier: verifier,
		createdAt:    time.Now(),
	}
	e.mu.Unlock()

	redirectURI := e.redirectBaseURL + "/callback"
	u, _ := url.Parse(p.AuthorizeURL)
	q := u.Query()
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if len(p.Scopes) > 0 {
		q.Set("scope", strings.Join(p.Scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// BeginDeviceCode starts a device code flow. Returns userCode and verificationURL to show user.
func (e *OAuthEngine) BeginDeviceCode(providerName string) (verificationURL, userCode string, err error) {
	p := e.providerMap[providerName]
	if p == nil {
		return "", "", fmt.Errorf("unknown oauth provider: %s", providerName)
	}
	if p.AuthType != "device_code" {
		return "", "", fmt.Errorf("provider %s uses %s, not device_code", providerName, p.AuthType)
	}

	body := url.Values{
		"client_id": {p.ClientID},
	}
	if len(p.Scopes) > 0 {
		body.Set("scope", strings.Join(p.Scopes, " "))
	}
	resp, err := http.PostForm(p.DeviceCodeURL, body)
	if err != nil {
		return "", "", fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("parse device code: %w", err)
	}

	e.mu.Lock()
	e.pending[result.DeviceCode] = &pendingOAuth{
		provider:   providerName,
		deviceCode: result.DeviceCode,
		interval:   result.Interval,
		expiresAt:  time.Now().Add(time.Duration(result.ExpiresIn) * time.Second),
		createdAt:  time.Now(),
	}
	e.mu.Unlock()

	return result.VerificationURI, result.UserCode, nil
}

// PollDeviceCode polls until the user completes authentication. Returns the access token.
func (e *OAuthEngine) PollDeviceCode(deviceCode string) (accessToken string, err error) {
	e.mu.Lock()
	pending, ok := e.pending[deviceCode]
	e.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown device code")
	}

	p := e.providerMap[pending.provider]
	if p == nil {
		return "", fmt.Errorf("provider not found")
	}

	pollInterval := time.Duration(max(pending.interval, 5)) * time.Second

	for {
		if time.Now().After(pending.expiresAt) {
			return "", fmt.Errorf("device code expired")
		}

		resp, err := http.PostForm(p.TokenURL, url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {deviceCode},
			"client_id":   {p.ClientID},
		})
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}
		var result struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.Error == "authorization_pending" {
			time.Sleep(pollInterval)
			continue
		}
		if result.Error == "slow_down" {
			pollInterval += 5 * time.Second
			time.Sleep(pollInterval)
			continue
		}
		if result.Error != "" {
			return "", fmt.Errorf("token error: %s", result.Error)
		}
		return result.AccessToken, nil
	}
}

// HandleCallback serves the OAuth redirect from the browser.
func (e *OAuthEngine) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errMsg := r.URL.Query().Get("error")

	if errMsg != "" {
		http.Error(w, "OAuth error: "+errMsg, 400)
		return
	}

	e.mu.Lock()
	pending, ok := e.pending[state]
	delete(e.pending, state)
	e.mu.Unlock()

	if !ok {
		http.Error(w, "Unknown OAuth state. Try logging in again.", 400)
		return
	}

	p := e.providerMap[pending.provider]

	// exchange code for token (may be id_token for Codex, access_token for others)
	tokenResp, err := e.exchangeCode(p, code, pending.codeVerifier)
	if err != nil {
		slog.Error("oauth token exchange", "error", err, "provider", pending.provider)
		http.Error(w, "Token exchange failed: "+err.Error(), 500)
		return
	}

	finalKey := tokenResp.AccessToken

	// Codex: exchange id_token for API key via token exchange grant
	if tokenResp.IDToken != "" && p.Extra != nil && p.Extra["token_exchange_grant"] != nil {
		if key, err := e.exchangeIDTokenForAPIKey(p, tokenResp.IDToken); err == nil {
			finalKey = key
		} else {
			slog.Error("codex api key exchange", "error", err)
			http.Error(w, "API key exchange failed: "+err.Error(), 500)
			return
		}
	} else if p.APIKeyURL != "" {
		// Claude: exchange access_token for API key
		if key, err := e.exchangeForAPIKey(p, tokenResp.AccessToken); err == nil {
			finalKey = key
		}
	}

	if err := e.authStore.Set(p.Name, finalKey); err != nil {
		slog.Error("save auth", "error", err)
		http.Error(w, "Failed to save credentials", 500)
		return
	}

	// auto-add provider to providers.json if not present
	e.EnsureProvider(p)

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "<html><body><h1>✅ Logged in to %s!</h1><p>You can close this tab.</p></body></html>", p.DisplayName)
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
}

func (e *OAuthEngine) exchangeCode(p *OAuthProvider, code, verifier string) (*tokenResponse, error) {
	redirectURI := e.redirectBaseURL + "/callback"
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {p.ClientID},
		"code_verifier": {verifier},
	}

	req, _ := http.NewRequest("POST", p.TokenURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if beta, ok := p.Extra["oauth_beta_header"].(string); ok && beta != "" {
		req.Header.Set("anthropic-beta", beta)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result tokenResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse token response: %w (body: %.200s)", err, string(body))
	}
	if result.AccessToken == "" && result.IDToken == "" {
		return nil, fmt.Errorf("no token in response: %.200s", string(body))
	}
	return &result, nil
}

// exchangeIDTokenForAPIKey does the Codex-specific token exchange.
func (e *OAuthEngine) exchangeIDTokenForAPIKey(p *OAuthProvider, idToken string) (string, error) {
	grantType, _ := p.Extra["token_exchange_grant"].(string)
	requestedToken, _ := p.Extra["requested_token"].(string)
	subjectTokenType, _ := p.Extra["subject_token_type"].(string)

	data := url.Values{
		"grant_type":          {grantType},
		"client_id":           {p.ClientID},
		"requested_token":     {requestedToken},
		"subject_token":       {idToken},
		"subject_token_type":  {subjectTokenType},
	}

	req, _ := http.NewRequest("POST", p.APIKeyURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse api key response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("api key exchange: %s", result.Error)
	}
	return result.AccessToken, nil
}

func (e *OAuthEngine) exchangeForAPIKey(p *OAuthProvider, accessToken string) (string, error) {
	req, _ := http.NewRequest("POST", p.APIKeyURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	if beta, ok := p.Extra["oauth_beta_header"].(string); ok && beta != "" {
		req.Header.Set("anthropic-beta", beta)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Key, nil
}

// EnsureProvider adds the OAuth provider to providers.json if not already there.
func (e *OAuthEngine) EnsureProvider(p *OAuthProvider) {
	providersPath := filepath.Join(e.home, "providers.json")
	existing := map[string]any{}
	if data, err := os.ReadFile(providersPath); err == nil {
		json.Unmarshal(data, &existing)
	}
	list, _ := existing["providers"].([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok && m["name"] == p.Name {
			return // already exists
		}
	}
	// add new provider entry
	newProvider := map[string]any{
		"name":        p.Name,
		"priority":    10,
		"base_url":    p.APIBaseURL,
		"api_key_env": "",
		"model":       p.Models[0],
	}
	if len(p.Models) > 1 {
		newProvider["small_model"] = p.Models[len(p.Models)-1]
	}
	list = append(list, newProvider)
	existing["providers"] = list
	if data, err := json.MarshalIndent(existing, "", "  "); err == nil {
		os.WriteFile(providersPath, data, 0644)
	}
}

// HandleCodexLogin reads credentials from ~/.codex/auth.json (set up by `codex login` CLI).
// If not found, prompts the user to run codex login first.
func (e *OAuthEngine) HandleCodexLogin() (string, error) {
	p := e.providerMap["codex"]
	if p == nil {
		return "", fmt.Errorf("codex provider not configured")
	}
	// try reading Codex CLI's auth file
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(os.Getenv("HOME"), ".codex")
	}
	authPath := filepath.Join(codexHome, "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		// try running codex login
		cmd := exec.Command("codex", "login")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("codex login failed: %w — install codex CLI or paste API key in Settings", err)
		}
		data, err = os.ReadFile(authPath)
		if err != nil {
			return "", fmt.Errorf("codex auth not found after login: %w", err)
		}
	}
	var auth struct {
		OpenAIAPIKey string `json:"openai_api_key"`
	}
	json.Unmarshal(data, &auth)
	if auth.OpenAIAPIKey == "" {
		return "", fmt.Errorf("no openai_api_key in %s — run 'codex login' first", authPath)
	}
	return auth.OpenAIAPIKey, nil
}

// HandleGeminiADC runs gcloud auth application-default login for Gemini.
func (e *OAuthEngine) HandleGeminiADC() (string, error) {
	p := e.providerMap["gemini"]
	if p == nil {
		return "", fmt.Errorf("gemini provider not configured")
	}
	// run gcloud auth
	cmd := exec.Command("gcloud", "auth", "application-default", "login")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gcloud auth failed: %w", err)
	}
	// read the ADC token
	adcPath := filepath.Join(os.Getenv("HOME"), ".config", "gcloud", "application_default_credentials.json")
	data, err := os.ReadFile(adcPath)
	if err != nil {
		return "", fmt.Errorf("read ADC credentials: %w", err)
	}
	var adc struct {
		RefreshToken string `json:"refresh_token"`
	}
	json.Unmarshal(data, &adc)
	// for Gemini, we use the GEMINI_API_KEY env var method — ADC is stored separately
	// For now, just confirm success
	slog.Info("gemini ADC configured")
	return adc.RefreshToken, nil
}

// Providers returns the list of configured OAuth providers.
func (e *OAuthEngine) Providers() []*OAuthProvider {
	out := make([]*OAuthProvider, 0, len(e.providerMap))
	for _, p := range e.providerMap {
		out = append(out, p)
	}
	return out
}

// OpenBrowser opens a URL in the default browser.
func OpenBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}

// --- helpers ---

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func sha256b64(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
