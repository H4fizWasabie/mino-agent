package main

// threads-extension — Mino extension for Threads (Meta) publishing.
// Implements the Mino extension protocol (§3):
//   GET  /tools     → tool schemas
//   POST /execute   → run a tool
//   GET  /check     → health
//   GET  /auth      → OAuth setup URL (run once to get a token)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ─── config (env vars, no config file) ────────────────────────────

var (
	port          = envOr("THREADS_PORT", "9200")
	appID         = os.Getenv("THREADS_APP_ID")         // Meta app ID
	appSecret     = os.Getenv("THREADS_APP_SECRET")     // Meta app secret
	redirectURI   = envOr("THREADS_REDIRECT_URI", fmt.Sprintf("http://localhost:%s/callback", port))
	verifyToken   = envOr("THREADS_VERIFY_TOKEN", "mino-threads-verify")
)

// token file lives next to the extension binary
func tokenPath() string {
	dir := os.Getenv("MINO_HOME")
	if dir == "" {
		dir = os.Getenv("HOME") + "/.mino"
	}
	return filepath.Join(dir, "threads_token.json")
}

type tokenData struct {
	AccessToken  string `json:"access_token"`
	ThreadsUserID string `json:"threads_user_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ─── main ─────────────────────────────────────────────────────────

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	http.HandleFunc("/tools", handleTools)
	http.HandleFunc("/execute", handleExecute)
	http.HandleFunc("/check", handleCheck)
	http.HandleFunc("/auth", handleAuth)        // start OAuth flow
	http.HandleFunc("/callback", handleCallback) // OAuth redirect lands here

	go func() {
		certDir := os.Getenv("MINO_HOME")
		if certDir == "" {
			certDir = os.Getenv("HOME") + "/.mino"
		}
		certFile := filepath.Join(certDir, "localhost.crt")
		keyFile := filepath.Join(certDir, "localhost.key")
		if _, err := os.Stat(certFile); err == nil {
			slog.Info("threads extension listening (TLS)", "port", port)
			if err := http.ListenAndServeTLS(":"+port, certFile, keyFile, nil); err != nil {
				slog.Error("server error", "error", err)
				os.Exit(1)
			}
		} else {
			slog.Info("threads extension listening", "port", port)
			if err := http.ListenAndServe(":"+port, nil); err != nil {
				slog.Error("server error", "error", err)
				os.Exit(1)
			}
		}
	}()

	// idle until SIGTERM (systemd stops us)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
}

// ─── extension protocol ───────────────────────────────────────────

func handleTools(w http.ResponseWriter, r *http.Request) {
	tools := []map[string]any{
		{
			"name":        "threads_post",
			"description": "Publish a text post to Threads. Optionally attach an image URL. Returns the post ID.",
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":       map[string]any{"type": "string", "description": "Post text (max 500 chars)"},
					"image_url":  map[string]any{"type": "string", "description": "Public URL of image to attach"},
					"reply_to_id": map[string]any{"type": "string", "description": "Thread ID to reply to (optional)"},
				},
				"required": []string{"text"},
			},
		},
		{
			"name":        "threads_get_replies",
			"description": "Get recent replies to one of your Threads posts.",
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"thread_id": map[string]any{"type": "string", "description": "Threads post ID"},
					"limit":     map[string]any{"type": "integer", "description": "Max replies to return (default 10)"},
				},
				"required": []string{"thread_id"},
			},
		},
	}
	writeJSON(w, tools)
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", 405)
		return
	}
	var req struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "bad json"})
		return
	}

	tok, err := loadToken()
	if err != nil {
		writeJSON(w, map[string]string{"error": "no valid token — visit /auth to authorize"})
		return
	}

	var result string
	switch req.Tool {
	case "threads_post":
		result = threadsPost(tok, req.Args)
	case "threads_get_replies":
		result = threadsGetReplies(tok, req.Args)
	default:
		result = fmt.Sprintf("unknown tool: %s", req.Tool)
	}

	writeJSON(w, map[string]string{"result": result})
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	_, err := loadToken()
	alert := err != nil
	msg := "ok"
	if alert {
		msg = "no valid token — visit /auth"
	}
	writeJSON(w, map[string]any{"alert": alert, "message": msg})
}

// ─── OAuth flow ───────────────────────────────────────────────────

func handleAuth(w http.ResponseWriter, r *http.Request) {
	if appID == "" || appSecret == "" {
		http.Error(w, "Set THREADS_APP_ID and THREADS_APP_SECRET", 500)
		return
	}
	// Threads OAuth requires state param for security
	state := fmt.Sprintf("%d", time.Now().UnixNano())
	authURL := fmt.Sprintf(
		"https://www.threads.net/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&response_type=code&state=%s",
		appID,
		url.QueryEscape(redirectURI),
		"threads_basic,threads_content_publish,threads_manage_replies",
		state,
	)
	http.Redirect(w, r, authURL, 302)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", 400)
		return
	}

	// Step 1: Exchange code for short-lived token via Threads endpoint
	data := url.Values{
		"client_id":     {appID},
		"client_secret": {appSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}
	resp, err := http.PostForm("https://graph.threads.net/oauth/access_token", data)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), 500)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var short struct {
		AccessToken string `json:"access_token"`
		UserID      string `json:"user_id"`
	}
	json.Unmarshal(body, &short)
	if short.AccessToken == "" {
		http.Error(w, "token exchange failed: "+string(body), 500)
		return
	}

	// Step 2: Exchange for long-lived (60 day) token
	llData := url.Values{
		"client_secret": {appSecret},
		"grant_type":    {"th_exchange_token"},
		"access_token":  {short.AccessToken},
	}
	llResp, err := http.Get("https://graph.threads.net/access_token?" + llData.Encode())
	if err != nil {
		http.Error(w, "long-lived exchange failed: "+err.Error(), 500)
		return
	}
	defer llResp.Body.Close()
	llBody, _ := io.ReadAll(llResp.Body)
	var long struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	json.Unmarshal(llBody, &long)
	if long.AccessToken == "" {
		http.Error(w, "long-lived exchange failed: "+string(llBody), 500)
		return
	}

	tok := tokenData{
		AccessToken:   long.AccessToken,
		ThreadsUserID: short.UserID,
		ExpiresAt:     time.Now().Add(time.Duration(long.ExpiresIn) * time.Second),
	}
	saveToken(tok)

	slog.Info("threads authorized", "user_id", short.UserID, "expires", tok.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintf(w, "✅ Threads authorized! User ID: %s\nToken expires: %s\nYou can close this window.",
		short.UserID, tok.ExpiresAt.Format(time.RFC3339))
}

// ─── Threads API calls ────────────────────────────────────────────

func threadsPost(tok tokenData, args map[string]any) string {
	text, _ := args["text"].(string)
	imageURL, _ := args["image_url"].(string)
	replyToID, _ := args["reply_to_id"].(string)

	if text == "" {
		return "Error: text is required"
	}

	var mediaID string

	if imageURL != "" {
		// Step 1: create media container with image
		container := createMediaContainer(tok, "IMAGE", text, imageURL, replyToID)
		if strings.HasPrefix(container, "Error") {
			return container
		}
		mediaID = container
	} else {
		// Text-only: create media container
		container := createMediaContainer(tok, "TEXT", text, "", replyToID)
		if strings.HasPrefix(container, "Error") {
			return container
		}
		mediaID = container
	}

	// Step 2: publish
	return publishMedia(tok, mediaID)
}

func createMediaContainer(tok tokenData, mediaType, text, imageURL, replyToID string) string {
	params := url.Values{
		"media_type":  {mediaType},
		"text":        {text},
		"access_token": {tok.AccessToken},
	}
	if imageURL != "" {
		params.Set("image_url", imageURL)
	}
	if replyToID != "" {
		params.Set("reply_to_id", replyToID)
	}

	apiURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads?%s",
		tok.ThreadsUserID, params.Encode())

	resp, err := http.Post(apiURL, "application/x-www-form-urlencoded", nil)
	if err != nil {
		return fmt.Sprintf("Error creating container: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		ID    string `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &result)

	if result.Error.Message != "" {
		return fmt.Sprintf("Error creating container: %s", result.Error.Message)
	}
	return result.ID
}

func publishMedia(tok tokenData, containerID string) string {
	params := url.Values{
		"creation_id":  {containerID},
		"access_token": {tok.AccessToken},
	}
	apiURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish?%s",
		tok.ThreadsUserID, params.Encode())

	resp, err := http.Post(apiURL, "application/x-www-form-urlencoded", nil)
	if err != nil {
		return fmt.Sprintf("Error publishing: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		ID    string `json:"id"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &result)

	if result.Error.Message != "" {
		return fmt.Sprintf("Error publishing: %s", result.Error.Message)
	}
	return fmt.Sprintf("Posted to Threads! ID: %s", result.ID)
}

func threadsGetReplies(tok tokenData, args map[string]any) string {
	threadID, _ := args["thread_id"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	fields := "id,text,timestamp,username,like_count"
	apiURL := fmt.Sprintf(
		"https://graph.threads.net/v1.0/%s/replies?fields=%s&access_token=%s&limit=%d",
		threadID, fields, tok.AccessToken, limit,
	)

	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Sprintf("Error fetching replies: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return prettyJSON(body)
}

// ─── token storage ────────────────────────────────────────────────

func loadToken() (tokenData, error) {
	data, err := os.ReadFile(tokenPath())
	if err != nil {
		return tokenData{}, err
	}
	var tok tokenData
	if err := json.Unmarshal(data, &tok); err != nil {
		return tokenData{}, err
	}
	if time.Now().After(tok.ExpiresAt) {
		// Try refresh
		newTok, err := refreshToken(tok)
		if err != nil {
			return tokenData{}, fmt.Errorf("token expired and refresh failed: %w", err)
		}
		return newTok, nil
	}
	return tok, nil
}

func saveToken(tok tokenData) {
	data, _ := json.Marshal(tok)
	os.WriteFile(tokenPath(), data, 0600)
	slog.Info("token saved", "path", tokenPath())
}

func refreshToken(tok tokenData) (tokenData, error) {
	params := url.Values{
		"grant_type":    {"th_refresh_token"},
		"access_token":  {tok.AccessToken},
	}
	resp, err := http.Get("https://graph.threads.net/refresh_access_token?" + params.Encode())
	if err != nil {
		return tokenData{}, err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return tokenData{}, err
	}

	tok.AccessToken = result.AccessToken
	tok.ExpiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	saveToken(tok)
	slog.Info("token refreshed", "expires", tok.ExpiresAt.Format(time.RFC3339))
	return tok, nil
}

// ─── helpers ──────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func prettyJSON(raw []byte) string {
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	return out.String()
}
