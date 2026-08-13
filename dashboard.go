package main

// Dashboard — HTTP server serving static files + API endpoints.

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

var (
	dashCore    *Core
	dashEventQ  []map[string]any
	dashEventMu sync.Mutex
	dashCursor  int64
)

const maxDashEvents = 500 // ring buffer cap to prevent unbounded growth

type dashboardCatalogEntry struct {
	expires time.Time
	value   []map[string]any
}

var dashboardCatalogCache = struct {
	sync.Mutex
	skills    map[string]dashboardCatalogEntry
	playbooks map[string]dashboardCatalogEntry
}{skills: make(map[string]dashboardCatalogEntry), playbooks: make(map[string]dashboardCatalogEntry)}

const dashboardCatalogCacheTTL = 5 * time.Second

func pushDashEvent(e map[string]any) {
	dashEventMu.Lock()
	dashCursor++
	event := make(map[string]any, len(e)+2)
	for key, value := range e {
		event[key] = value
	}
	event["cursor"] = dashCursor
	event["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	dashEventQ = append(dashEventQ, event)
	if len(dashEventQ) > maxDashEvents {
		dashEventQ = dashEventQ[len(dashEventQ)-maxDashEvents:]
	}
	dashEventMu.Unlock()
}

func RunDashboard(w *Core) {
	dashCore = w
	registerDashboardRoutes(http.DefaultServeMux, w.Settings.MemoriesDir)

	// Telegram runs in main — don't double-start here

	port := dashCore.Settings.DashboardPort()
	if p := os.Getenv("MINO_DASHBOARD_PORT"); p != "" {
		port = p
	}
	host := os.Getenv("MINO_DASHBOARD_HOST")
	addr := net.JoinHostPort(host, port)
	slog.Info("dashboard", "addr", addr)
	url := "http://" + addr
	if strings.HasPrefix(addr, ":") {
		url = "http://localhost" + addr
	}
	if needsOnboarding(dashCore.Settings.Home) {
		fmt.Printf("\n  Mino is ready!\n  Open: %s\n  First run — onboarding will guide you.\n\n  If Mino saves you time, star the repo — it keeps this project alive: https://github.com/H4fizWasabie/mino-agent ⭐\n\n", url)
	} else {
		fmt.Printf("\n  Mino is ready!\n  Open: %s\n\n", url)
	}
	http.ListenAndServe(addr, nil)
}

func registerDashboardRoutes(mux *http.ServeMux, memDir string) {
	static, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	// Serve graph memory .md files so the frontend can fetch fact bodies on click
	mux.Handle("/memories/", http.StripPrefix("/memories/", http.FileServer(http.Dir(memDir))))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, _ := staticFiles.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})
	mux.HandleFunc("/api/chat/stream", handleChatStream)
	mux.HandleFunc("/api/chat", handleChat)
	mux.HandleFunc("/api/session", handleSession)
	mux.HandleFunc("/api/memory", handleMemoryAPI)
	mux.HandleFunc("/api/query", handleQueryAPI)
	mux.HandleFunc("/api/events", handleEventsAPI)
	mux.HandleFunc("/api/nerves", handleNervesAPI)
	mux.HandleFunc("/api/data", handleDataAPI)
	mux.HandleFunc("/api/universe", handleUniverseAPI)
	mux.HandleFunc("/api/responsibilities", handleResponsibilitiesAPI)
	mux.HandleFunc("/api/responsibility-evidence", handleResponsibilityEvidence)
	mux.HandleFunc("/api/reveal", handleRevealAPI)
	mux.HandleFunc("/api/files", handleFilesAPI)
	mux.HandleFunc("/api/active-tasks", handleActiveTasks)
	mux.HandleFunc("/api/settings", handleSettingsAPI)
	mux.HandleFunc("/callback", handleOAuthCallback)
	mux.HandleFunc("/api/auth", handleAuthAPI)
	mux.HandleFunc("/api/switch", handleSwitchAPI)
	mux.HandleFunc("/api/providers", handleProvidersAPI)
	mux.HandleFunc("/api/oauth/providers", handleOAuthProviders)
	mux.HandleFunc("/api/oauth/login/", handleOAuthLogin)
	mux.HandleFunc("/api/oauth/device/", handleOAuthDevice)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/metrics", handleMetrics)
	mux.HandleFunc("/api/eval/thumbs-up", handleEvalThumbsUp)
}

// --- API: Chat (non-stream, for dashboard load) ---

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	if dashCore.Client == nil {
		http.Error(w, "complete provider setup first", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Message == "" {
		http.Error(w, "empty message", 400)
		return
	}
	sid := body.SessionID
	if sid == "" {
		sid = "default"
	}
	if isStopMessage(body.Message) {
		stopped := dashCore.CancelTurn(sid)
		json.NewEncoder(w).Encode(map[string]any{"reply": map[bool]string{true: "Stopped.", false: "No active task."}[stopped], "status": "cancelled", "iterations": 0})
		return
	}

	// Interrupt routing (non-stream: block until reply ready)
	if query, ok := isInterrupt(body.Message); ok && dashCore.snapshot(sid) != nil {
		dashCore.handleInterrupt(sid, query, func(reply string) {
			json.NewEncoder(w).Encode(map[string]any{
				"reply": reply, "status": "interrupt", "iterations": 0,
			})
		})
		return
	}

	result := dashCore.RespondForContext(r.Context(), sid, body.Message, "dashboard", nil, false)

	json.NewEncoder(w).Encode(map[string]any{
		"reply":      result.Reply,
		"status":     result.Status,
		"iterations": result.Iterations,
		"tools":      dashboardTools(result.ToolCalls),
	})
}

// --- API: Chat (SSE streaming, matches Core's /api/chat/stream) ---

func handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	if dashCore.Client == nil {
		http.Error(w, "complete provider setup first", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Message == "" {
		http.Error(w, "empty message", 400)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sid := body.SessionID
	if sid == "" {
		sid = "default"
	}
	if isStopMessage(body.Message) {
		stopped := dashCore.CancelTurn(sid)
		reply := map[bool]string{true: "Stopped.", false: "No active task."}[stopped]
		data, _ := json.Marshal(map[string]any{"kind": "done", "reply": reply, "status": "cancelled", "iterations": 0})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	// Interrupt routing (SSE: push response as event)
	if query, ok := isInterrupt(body.Message); ok && dashCore.snapshot(sid) != nil {
		reply, ok := awaitInterrupt(r.Context(), func(reply func(string)) {
			dashCore.handleInterrupt(sid, query, reply)
		})
		if !ok {
			return
		}
		data, _ := json.Marshal(map[string]any{"kind": "interrupt", "reply": reply})
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		return
	}

	obs := func(kind string, data map[string]any) {
		data["kind"] = kind
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()

		// map SSE event kinds to STAGE types for architecture animation
		stageType := kind
		switch kind {
		case "text":
			stageType = "llm"
		case "done":
			stageType = "turn_end"
		}
		event := map[string]any{"type": stageType, "session_id": sid}
		for _, key := range []string{"decision", "status", "tool"} {
			if value, ok := data[key]; ok {
				event[key] = value
			}
		}
		pushDashEvent(event)
	}

	pushDashEvent(map[string]any{"type": "turn_start", "session_id": sid})

	result := dashCore.RespondForContext(r.Context(), sid, body.Message, "dashboard", obs, true)

	// done event — Core format: flat fields, no 'data' wrapper
	doneEv := map[string]any{
		"reply":      result.Reply,
		"status":     result.Status,
		"iterations": result.Iterations,
		"latency_ms": 0,
	}
	if len(result.ToolCalls) > 0 {
		doneEv["tools"] = dashboardTools(result.ToolCalls)
	}
	doneEv["kind"] = "done"

	// publish turn_end + done
	pushDashEvent(map[string]any{"type": "turn_end", "session_id": sid, "status": result.Status})

	b, _ := json.Marshal(doneEv)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// awaitInterrupt keeps the HTTP handler alive until the asynchronous
// interrupt has a reply or the client disconnects. ResponseWriter must not be
// used after the handler returns.
func awaitInterrupt(ctx context.Context, run func(func(string))) (string, bool) {
	replies := make(chan string, 1)
	go run(func(reply string) { replies <- reply })
	select {
	case reply := <-replies:
		return reply, true
	case <-ctx.Done():
		return "", false
	}
}

func dashboardTools(calls []ToolCall) []map[string]any {
	tools := make([]map[string]any, len(calls))
	for i, call := range calls {
		tools[i] = map[string]any{
			"tool": call.Name, "args": call.Args, "output": call.Output,
			"status": toolOutputStatus(call.Output), "summary": dashboardToolSummary(call.Output),
		}
	}
	return tools
}

// dashboardToolSummary keeps the chat activity row useful without exposing the
// full tool result. The complete result remains available in the disclosure.
func dashboardToolSummary(output string) string {
	summary := strings.TrimSpace(output)
	if receipt := strings.Index(summary, "[action_receipt"); receipt >= 0 {
		summary = strings.TrimSpace(summary[:receipt])
	}
	summary = strings.Join(strings.Fields(summary), " ")
	if end := strings.Index(summary, ". "); end >= 0 {
		summary = summary[:end+1]
	}
	if len(summary) > 120 {
		summary = strings.TrimSpace(summary[:117]) + "..."
	}
	return summary
}

// --- API: Session ---

func handleSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	switch body.Action {
	case "new":
		id := fmt.Sprintf("%x", time.Now().UnixNano())
		conversation := dashCore.Sessions.Get(id)
		conversation.Session.StartNew(id)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "session_id": id, "history": []any{}})
	case "switch":
		id := body.ID
		if id == "" {
			id = "default"
		}
		dashCore.Sessions.Get(id)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "session_id": id, "history": sessionHistory(dashCore.DB, id)})
	case "history":
		id := body.ID
		if id == "" {
			id = "default"
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "session_id": id, "history": sessionHistory(dashCore.DB, id)})
	default:
		http.Error(w, "unknown action", 400)
	}
}

// --- Stub APIs (return empty/valid data so UI tabs render) ---

func handleMemoryAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var body struct {
			Action  string `json:"action"`
			ID      any    `json:"id"`
			Path    string `json:"path"`
			Content string `json:"content"`
			Playbook string `json:"playbook"`
			Run      string `json:"run"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		switch body.Action {
		case "delete_run":
			if body.Playbook == "" || body.Run == "" {
				http.Error(w, "playbook and run required", http.StatusBadRequest)
				return
			}
			runDir := playbookRunDir(dashCore.Settings.Home, body.Playbook, body.Run)
			if runDir == "" {
				http.Error(w, "invalid playbook run path", http.StatusBadRequest)
				return
			}
			if data, err := os.ReadFile(filepath.Join(runDir, "state.json")); err == nil {
				var run PlaybookRun
				if json.Unmarshal(data, &run) == nil && run.Status == "running" {
					http.Error(w, "run is still running", http.StatusBadRequest)
					return
				}
			}
			if err := os.RemoveAll(runDir); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		case "update_fact":
			if graphID, ok := body.ID.(string); ok {
				if dashCore.Memory == nil || dashCore.Memory.graph == nil {
					http.Error(w, "graph memory unavailable", http.StatusServiceUnavailable)
					return
				}
				if err := dashCore.Memory.graph.UpdateBody(graphID, body.Content); err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
			http.Error(w, "graph fact id required", http.StatusBadRequest)
			return
		case "delete_fact":
			factID, ok := body.ID.(string)
			if !ok || dashCore.Memory == nil || dashCore.Memory.graph == nil {
				http.Error(w, "invalid fact id", http.StatusBadRequest)
				return
			}
			_, err := dashCore.Memory.graph.DeleteFact(factID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		case "delete_episode":
			episodeID, ok := body.ID.(string)
			if !ok || dashCore.Memory == nil || dashCore.Memory.graph == nil {
				http.Error(w, "invalid episode id", http.StatusBadRequest)
				return
			}
			if _, err := dashCore.Memory.graph.DeleteFact(episodeID); err == nil {
				json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
				return
			}
			http.Error(w, "episode not found", http.StatusNotFound)
			return
		case "save_soul":
			os.WriteFile(dashCore.Settings.Home+"/SOUL.md", []byte(body.Content), 0644)
		case "save_skill":
			root := filepath.Join(dashCore.Settings.Home, "skills")
			target := filepath.Join(dashCore.Settings.Home, filepath.Clean(body.Path))
			rel, err := filepath.Rel(root, target)
			if err != nil || filepath.IsAbs(body.Path) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.Base(target) != "SKILL.md" {
				http.Error(w, "invalid skill path", http.StatusBadRequest)
				return
			}
			if err := os.WriteFile(target, []byte(body.Content), 0600); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// GET: return memory data
	facts := []map[string]any{}
	if dashCore.Memory != nil && dashCore.Memory.graph != nil {
		for _, fact := range dashCore.Memory.graph.Facts() {
			facts = append(facts, map[string]any{
				"id": fact.ID, "subject": fact.Subject, "content": fact.Body,
				"source": fact.Source, "created_at": fact.At.Format(time.RFC3339),
				"feedback": fact.Feedback, "edges": fact.Edges,
			})
		}
	}
	episodes := graphEpisodes(dashCore.Memory)
	skills := skillCatalog(dashCore.Settings.Home)
	playbooks := playbookCatalog(dashCore.Settings.Home)

	json.NewEncoder(w).Encode(map[string]any{
		"facts": facts, "episodes": episodes, "skills": skills, "playbooks": playbooks,
	})
}

func queryAll(db *sql.DB, query string) []map[string]any {
	rows, _ := db.Query(query)
	if rows == nil {
		return []map[string]any{}
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	results := make([]map[string]any, 0)
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)
		row := make(map[string]any)
		for i, c := range cols {
			row[c] = vals[i]
		}
		results = append(results, row)
	}
	return results
}

func handleQueryAPI(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SQL string `json:"sql"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	query := strings.TrimSpace(body.SQL)
	fields := strings.Fields(query)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "SELECT") || strings.Contains(strings.TrimSuffix(query, ";"), ";") {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "only one SELECT statement is allowed"})
		return
	}

	rows, err := dashCore.DB.Query(query)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	cols, _ := rows.Columns()
	var result [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		rows.Scan(ptrs...)
		row := make([]any, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = v
			}
		}
		result = append(result, row)
	}
	json.NewEncoder(w).Encode(map[string]any{"columns": cols, "rows": result})
}

// graphEpisodes lists episodic facts from the graph, newest first. The
// SQLite episodes table has no writer since the graph migration, so both
// dashboard readers must come from here.
func graphEpisodes(mem *Memory) []map[string]any {
	episodes := []map[string]any{}
	if mem == nil || mem.graph == nil {
		return episodes
	}
	for _, fact := range mem.graph.Facts() {
		if fact.Type != "episodic" {
			continue
		}
		episodes = append(episodes, map[string]any{
			"id": fact.ID, "happened_at": fact.At.Format(time.RFC3339), "summary": fact.Subject,
		})
	}
	sort.Slice(episodes, func(i, j int) bool {
		return episodes[i]["happened_at"].(string) > episodes[j]["happened_at"].(string)
	})
	return episodes
}

func handleEventsAPI(w http.ResponseWriter, r *http.Request) {
	requested, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	dashEventMu.Lock()
	events := make([]map[string]any, 0, len(dashEventQ))
	for _, event := range dashEventQ {
		cursor, _ := event["cursor"].(int64)
		if cursor > requested {
			events = append(events, event)
		}
	}
	cursor := dashCursor
	dashEventMu.Unlock()

	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"events": events,
		"cursor": cursor,
	})
}

// handleNervesAPI returns the live nervous-system snapshot for a session.
func handleNervesAPI(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")
	if sid == "" {
		sid = "default"
	}
	if dashCore == nil {
		json.NewEncoder(w).Encode(map[string]any{"active": false})
		return
	}
	snap := dashCore.snapshot(sid)
	if snap == nil {
		json.NewEncoder(w).Encode(map[string]any{"active": false})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"active":       true,
		"session_id":   sid,
		"iteration":    snap.Iteration,
		"status":       snap.Status,
		"current_tool": snap.CurrentTool,
		"last_output":  snap.LastOutput,
		"tool_history": snap.ToolHistory,
		"started_at":   snap.StartedAt.Format(time.RFC3339),
		"elapsed":      time.Since(snap.StartedAt).String(),
	})
}

func mcpConfigured(home string) bool {
	entries, err := os.ReadDir(filepath.Join(home, "mcp.d"))
	return err == nil && len(entries) > 0
}

func mcpServers(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, "mcp.d"))
	if err != nil {
		return []string{}
	}
	names := make([]string, 0)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			name := strings.TrimSuffix(e.Name(), ".json")
			data, _ := os.ReadFile(filepath.Join(home, "mcp.d", e.Name()))
			var cfg struct{ Name string }
			if json.Unmarshal(data, &cfg) == nil && cfg.Name != "" {
				name = cfg.Name
			}
			names = append(names, name)
		}
	}
	return names
}

func mcpLive(home string) bool { return mcpConfigured(home) && len(mcpServers(home)) > 0 }

func chatPending(db *sql.DB) int {
	var n int
	db.QueryRow("SELECT COUNT(*) FROM chat_log WHERE consolidated = 0").Scan(&n)
	return n
}

func skillCatalog(home string) []map[string]any {
	dashboardCatalogCache.Lock()
	if entry, ok := dashboardCatalogCache.skills[home]; ok && time.Now().Before(entry.expires) {
		dashboardCatalogCache.Unlock()
		return entry.value
	}
	dashboardCatalogCache.Unlock()

	value := loadSkillCatalog(home)
	dashboardCatalogCache.Lock()
	dashboardCatalogCache.skills[home] = dashboardCatalogEntry{expires: time.Now().Add(dashboardCatalogCacheTTL), value: value}
	dashboardCatalogCache.Unlock()
	return value
}

func loadSkillCatalog(home string) []map[string]any {
	sl := NewSkillLoader(home)
	var out []map[string]any
	for _, sk := range sl.Catalog() {
		rel, err := filepath.Rel(home, sk.Source)
		out = append(out, map[string]any{
			"name": sk.Name, "body": sk.Body, "description": sk.Description,
			"triggers": sk.Triggers, "state": sk.State, "use_count": sk.UseCount,
			"path": rel, "rel": rel, "editable": err == nil && !strings.HasPrefix(rel, ".."),
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

func playbookCatalog(home string) []map[string]any {
	dashboardCatalogCache.Lock()
	if entry, ok := dashboardCatalogCache.playbooks[home]; ok && time.Now().Before(entry.expires) {
		dashboardCatalogCache.Unlock()
		return entry.value
	}
	dashboardCatalogCache.Unlock()

	value := loadPlaybookCatalog(home)
	dashboardCatalogCache.Lock()
	dashboardCatalogCache.playbooks[home] = dashboardCatalogEntry{expires: time.Now().Add(dashboardCatalogCacheTTL), value: value}
	dashboardCatalogCache.Unlock()
	return value
}

// playbookRunDir returns the validated run directory for a playbook+run ID,
// or "" when the path would escape the playbooks tree.
func playbookRunDir(home, playbook, run string) string {
	if playbook == "" || run == "" || strings.ContainsAny(playbook, "/\\") || strings.ContainsAny(run, "/\\") {
		return ""
	}
	dir := filepath.Join(home, "playbooks", playbook, "runs", run)
	base := filepath.Join(home, "playbooks")
	rel, err := filepath.Rel(base, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return ""
	}
	return dir
}

func loadPlaybookCatalog(home string) []map[string]any {
	var out []map[string]any
	for _, name := range ListPlaybooks(home) {
		pb, err := loadPlaybookWorkspace(home, name)
		if err != nil {
			out = append(out, map[string]any{"name": name, "path": filepath.Join("playbooks", name), "error": err.Error()})
			continue
		}
		stages := make([]map[string]any, 0, len(pb.Stages))
		for _, stage := range pb.Stages {
			stages = append(stages, map[string]any{
				"number": stage.Number, "name": stage.Name, "inputs": stage.Inputs,
				"tools": stage.Tools, "outputs": stage.Outputs, "context": stage.Context,
			})
		}
		outputs := []string{}
		if run, _ := latestPlaybookRun(pb); run != nil {
			for _, stage := range run.Stages {
				for _, path := range stage.Outputs {
					if rel, err := filepath.Rel(home, path); err == nil {
						outputs = append(outputs, rel)
					}
				}
			}
		}
		out = append(out, map[string]any{
			"name": name, "path": filepath.Join("playbooks", name), "description": pb.Description,
			"schedule": pb.Schedule, "status": pb.Status, "notify": pb.Config["notify"] == "true",
			"stages": stages, "outputs": outputs, "runs": playbookRunList(home, name),
		})
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}

// playbookRunList returns recent run ids + status for a playbook (newest first).
func playbookRunList(home, name string) []map[string]any {
	dir := filepath.Join(home, "playbooks", name, "runs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	var runs []map[string]any
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(dir, id, "state.json"))
		if err != nil {
			continue
		}
		var run PlaybookRun
		if json.Unmarshal(data, &run) != nil {
			continue
		}
		runs = append(runs, map[string]any{
			"id": run.ID, "status": run.Status,
			"created": run.CreatedAt.Format(time.RFC3339),
		})
	}
	return runs
}

func sortedFiles(pattern string) []string {
	files, _ := filepath.Glob(pattern)
	sort.Strings(files)
	return files
}

// loadGraphIndex reads the memory graph index.json and returns it as a JSON object.
func loadGraphIndex(dir string) any {
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil
	}
	var index any
	if json.Unmarshal(data, &index) != nil {
		return nil
	}
	return index
}

func handleDataAPI(w http.ResponseWriter, r *http.Request) {
	db := dashCore.DB
	if dashCore.Memory != nil && dashCore.Memory.graph != nil {
		dashCore.Memory.graph.Refresh()
	}

	// chat_log from DB (last 80 messages, reversed for frontend)
	chatLog := queryAll(db,
		"SELECT role, content, consolidated, source, session_id, created_at FROM chat_log ORDER BY id DESC LIMIT 80")
	// reverse so oldest first
	for i, j := 0, len(chatLog)-1; i < j; i, j = i+1, j-1 {
		chatLog[i], chatLog[j] = chatLog[j], chatLog[i]
	}

	// sessions — grouped by session_id
	sessions := sessionList(db)

	// Semantic facts come from the graph. SQLite facts remain visible only as
	// migration diagnostics until the separate retirement release.
	factsData := []map[string]any{}
	if dashCore.Memory != nil && dashCore.Memory.graph != nil {
		for _, fact := range dashCore.Memory.graph.Facts() {
			factsData = append(factsData, map[string]any{
				"id": fact.ID, "subject": fact.Subject, "content": fact.Body,
				"source": fact.Source, "created_at": fact.At.Format(time.RFC3339),
				"feedback": fact.Feedback, "edges": fact.Edges,
			})
		}
	}
	legacyFactsData := queryAll(db, "SELECT id, subject, content, source, created_at FROM facts ORDER BY id DESC")
	episodesData := graphEpisodes(dashCore.Memory)
	calendarData := queryAll(db, "SELECT title, start, \"end\", attendees, created_at FROM calendar_events ORDER BY start")
	skillsData := skillCatalog(dashCore.Settings.Home)
	playbooksData := playbookCatalog(dashCore.Settings.Home)
	outboxData := outboxList(dashCore.Settings.Home)
	soulData, _ := os.ReadFile(filepath.Join(dashCore.Settings.Home, "SOUL.md"))
	activeTasks := listActiveTasksPlaybook(dashCore.Settings.Home)
	responsibilities := ResponsibilityViews{Today: []ResponsibilityEntry{}, Work: []ResponsibilityEntry{}}
	if dashCore.Responsibilities != nil {
		if views, err := dashCore.Responsibilities.Views(time.Now(), dashCore.Settings.Location()); err == nil {
			responsibilities = views
		} else {
			responsibilities.Error = err.Error()
		}
	}
	if activeTasks == nil {
		activeTasks = []map[string]any{}
	}
	if factsData == nil {
		factsData = []map[string]any{}
	}

	if episodesData == nil {
		episodesData = []map[string]any{}
	}
	if calendarData == nil {
		calendarData = []map[string]any{}
	}
	if outboxData == nil {
		outboxData = []map[string]any{}
	}

	// counts
	factsN := len(factsData)
	legacyFactsN, _ := countRows(db, "facts")
	episodesN := len(episodesData)
	calendarN, _ := countRows(db, "calendar_events")
	chatN, _ := countRows(db, "chat_log")

	// all tables
	allTables := []string{}
	rows, _ := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			rows.Scan(&name)
			allTables = append(allTables, name)
		}
	}

	activeProvider := ""
	activeModel := dashCore.Settings.Model
	activeReasoning := "default"
	if dashCore.Client != nil {
		activeProvider = dashCore.Client.ActiveProvider("default")
		if model := dashCore.Client.ActiveModel("default"); model != "" {
			activeModel = model
		}
		activeReasoning = dashCore.Client.ActiveReasoning("default")
	}
	resp := map[string]any{
		"provider":          dashCore.Settings.Provider,
		"model":             activeModel,
		"reasoning":         activeReasoning,
		"timezone":          dashCore.Settings.Timezone,
		"active_provider":   activeProvider,
		"home":              dashCore.Settings.Home,
		"chat_log":          chatLog,
		"sessions":          sessions,
		"consolidate_every": dashCore.Settings.ConsolidateEvery,
		"chat_pending":      chatPending(dashCore.DB),
		"current_session":   "default",
		"stats":             usageStats(dashCore.Settings.Home),
		"usage":             usageSummary(dashCore.Settings.Home),
		"turns":             traceTurns(dashCore.Settings.Home),
		"trace_tail":        traceTail(dashCore.Settings.Home),
		"trace_file":        traceFileName(dashCore.Settings.Home),
		"tables":            map[string]int{"facts": factsN, "legacy_facts": legacyFactsN, "episodes": episodesN, "calendar_events": calendarN, "chat_log": chatN},
		"facts":             factsData,
		"legacy_facts":      legacyFactsData,
		"episodes":          episodesData,
		"calendar":          calendarData,
		"outbox":            outboxData,
		"skills":            skillsData,
		"playbooks":         playbooksData,
		"soul":              string(soulData),
		"tools": map[string]any{
			"catalog":  dashCore.Tools.Catalog(),
			"mcp":      map[string]any{"configured": mcpConfigured(dashCore.Settings.Home), "servers": mcpServers(dashCore.Settings.Home), "live": mcpLive(dashCore.Settings.Home)},
			"apple_on": false,
		},
		"db":               databaseSnapshot(db, dashCore.Settings.Home, allTables),
		"settings":         map[string]any{"providers": providerSnapshot(), "config_file": filepath.Join(dashCore.Settings.Home, "providers.json")},
		"eval_report":      evalReport(dashCore.Settings.Home),
		"wake_scans":       []any{},
		"reports":          []any{},
		"active_tasks":     activeTasks,
		"responsibilities": responsibilities,
		"needs_onboarding": needsOnboarding(dashCore.Settings.Home),
		"graph":            loadGraphIndex(dashCore.Settings.MemoriesDir),
	}
	json.NewEncoder(w).Encode(resp)
}

func handleResponsibilitiesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if dashCore == nil || dashCore.Responsibilities == nil {
		http.Error(w, "responsibility state unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		views, err := dashCore.Responsibilities.Views(time.Now(), dashCore.Settings.Location())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(views)
		return
	}
	detail, err := dashCore.Responsibilities.Detail(id)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(detail)
}

func handleResponsibilityEvidence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	path := filepath.FromSlash(r.URL.Query().Get("path"))
	clean := filepath.Clean(path)
	parts := strings.Split(clean, string(filepath.Separator))
	if dashCore == nil || dashCore.Settings == nil || filepath.IsAbs(path) ||
		clean != path || len(parts) < 8 || parts[0] != "playbooks" || parts[2] != "runs" || parts[4] != "stages" || parts[6] != "output" {
		http.Error(w, "evidence path not allowed", http.StatusForbidden)
		return
	}
	home, err := filepath.EvalSymlinks(dashCore.Settings.Home)
	if err != nil {
		http.Error(w, "evidence storage unavailable", http.StatusInternalServerError)
		return
	}
	outputDir, outputErr := filepath.EvalSymlinks(filepath.Join(home, parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts[6]))
	resolved, resolveErr := filepath.EvalSymlinks(filepath.Join(home, clean))
	if resolveErr != nil {
		http.NotFound(w, r)
		return
	}
	if outputErr != nil || !pathWithin(home, outputDir) || !pathWithin(outputDir, resolved) {
		http.Error(w, "evidence path not allowed", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, resolved)
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// evalReport reads a release-evidence record written by the certification gate.
// It is deliberately separate from usage and traces: operational completion is
// not a claim that a model-produced answer was independently judged correct.
func evalReport(home string) map[string]any {
	data, err := os.ReadFile(filepath.Join(home, "eval_report.json"))
	if err != nil {
		return nil
	}
	var report map[string]any
	if json.Unmarshal(data, &report) != nil || report["deterministic"] == nil || report["judge"] == nil {
		return nil
	}
	return report
}

// handleEvalThumbsUp — §22: user clicks thumbs-up on a completed task in the dashboard.
// Generates a manual eval case from the interaction.
func handleEvalThumbsUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		Prompt    string   `json:"prompt"`
		ToolsUsed []string `json:"tools_used"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Prompt == "" {
		http.Error(w, `{"error":"bad request"}`, 400)
		return
	}
	c := GenerateEvalCase(req.Prompt, req.ToolsUsed, "manual")
	casesPath := filepath.Join(LoadSettings().Home, "eval", "cases.json")
	if err := AppendEvalCase(casesPath, c); err != nil {
		slog.Warn("thumbs-up eval case write failed", "error", err)
		http.Error(w, `{"error":"failed to save"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "name": c.Name})
}

func databaseSnapshot(db *sql.DB, home string, all []string) map[string]any {
	tables := make([]map[string]any, 0)
	fts := make([]string, 0)
	for _, name := range all {
		if strings.HasSuffix(name, "_fts") {
			fts = append(fts, name)
			continue
		}
		if strings.Contains(name, "_fts_") {
			continue
		}
		columns, types := make([]string, 0), map[string]string{}
		rows, _ := db.Query(fmt.Sprintf("PRAGMA table_info(%q)", name))
		if rows != nil {
			for rows.Next() {
				var cid, notnull, pk int
				var column, kind string
				var defaultValue any
				if rows.Scan(&cid, &column, &kind, &notnull, &defaultValue, &pk) == nil {
					columns = append(columns, column)
					types[column] = kind
				}
			}
			rows.Close()
		}
		count, _ := countRows(db, name)
		sample := queryAll(db, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid DESC LIMIT 50", name))
		for _, row := range sample {
			for column, value := range row {
				switch value := value.(type) {
				case string:
					if len(value) > 500 {
						row[column] = value[:500] + "…"
					}
				case []byte:
					limit, suffix := min(len(value), 250), ""
					if limit < len(value) {
						suffix = "…"
					}
					row[column] = fmt.Sprintf("%x%s", value[:limit], suffix)
				}
			}
		}
		tables = append(tables, map[string]any{
			"name": name, "count": count, "columns": columns, "types": types,
			"sample": sample,
		})
	}
	path := filepath.Join(home, "state.db")
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	return map[string]any{"path": path, "size": size, "tables": tables, "fts": fts, "all_tables": all}
}

func providerSnapshot() []map[string]any {
	if dashCore.Client == nil {
		return []map[string]any{}
	}
	m := dashCore.Client
	m.mu.Lock()
	defer m.mu.Unlock()
	sticky := map[string]int{}
	for _, name := range m.sticky {
		sticky[name]++
	}
	out := make([]map[string]any, 0, len(m.providers))
	for _, p := range m.providers {
		state := m.state[p.Name]
		status := "healthy"
		if state != nil && state.openUntil.After(m.now()) {
			status = "circuit open"
		}
		keySet := os.Getenv(p.APIKeyEnv) != ""
		if !keySet && dashCore.AuthStore != nil {
			keySet = dashCore.AuthStore.Get(p.Name) != ""
		}
		out = append(out, map[string]any{
			"name": p.Name, "priority": p.Priority, "base_url": p.BaseURL, "model": p.Model,
			"small_model": p.Small, "api_key_env": p.APIKeyEnv, "key_set": keySet,
			"status": status, "sticky_sessions": sticky[p.Name],
		})
	}
	return out
}

func sessionHistory(db *sql.DB, id string) []map[string]string {
	rows, err := db.Query("SELECT role, content FROM chat_log WHERE session_id = ? ORDER BY id", id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var history []map[string]string
	for rows.Next() {
		var role, content string
		rows.Scan(&role, &content)
		history = append(history, map[string]string{"role": role, "content": content})
	}
	return history
}

func sessionList(db *sql.DB) []map[string]any {
	rows, _ := db.Query("SELECT session_id, COUNT(*) AS messages, MAX(created_at) AS last_at, GROUP_CONCAT(DISTINCT source) FROM chat_log GROUP BY session_id ORDER BY last_at DESC")
	if rows == nil {
		return nil
	}

	// collect all rows first — avoid nested queries while rows open (SetMaxOpenConns=1)
	type sessRow struct {
		sid     string
		count   int
		lastAt  string
		sources []string
	}
	var raw []sessRow
	for rows.Next() {
		var r sessRow
		var sources string
		rows.Scan(&r.sid, &r.count, &r.lastAt, &sources)
		if sources != "" {
			r.sources = strings.Split(sources, ",")
		}
		raw = append(raw, r)
	}
	rows.Close()

	var sessions []map[string]any
	for _, s := range raw {

		// first user message as title
		var title string
		db.QueryRow("SELECT content FROM chat_log WHERE session_id=? AND role='user' ORDER BY id LIMIT 1", s.sid).Scan(&title)
		if len(title) > 60 {
			title = title[:60]
		}

		// last message preview
		var lastRole, lastContent string
		db.QueryRow("SELECT role, content FROM chat_log WHERE session_id=? ORDER BY id DESC LIMIT 1", s.sid).Scan(&lastRole, &lastContent)
		preview := ""
		if lastRole == "user" {
			preview = "you: "
		} else {
			preview = "mino: "
		}
		if len(lastContent) > 80 {
			lastContent = lastContent[:80]
		}
		preview += lastContent

		sessions = append(sessions, map[string]any{
			"id": s.sid, "title": title, "last": preview,
			"messages": s.count, "last_at": s.lastAt, "sources": s.sources,
		})
	}
	return sessions
}

func outboxList(home string) []map[string]any {
	entries, _ := os.ReadDir(home + "/outbox")
	outbox := make([]map[string]any, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, _ := os.ReadFile(home + "/outbox/" + e.Name())
		text := string(data)
		if len(text) > 400 {
			text = text[:400]
		}
		outbox = append(outbox, map[string]any{"name": e.Name(), "text": text})
	}
	return outbox
}

func handleActiveTasks(w http.ResponseWriter, r *http.Request) {
	tasks := listActiveTasksPlaybook(dashCore.Settings.Home)
	if tasks == nil {
		tasks = []map[string]any{}
	}
	json.NewEncoder(w).Encode(map[string]any{"tasks": tasks})
}

func needsOnboarding(home string) bool {
	_, err := os.Stat(filepath.Join(home, "providers.json"))
	hasStoredKey := dashCore != nil && dashCore.AuthStore != nil && dashCore.AuthStore.Get("default") != ""
	return os.IsNotExist(err) && os.Getenv("MINO_API_KEY") == "" && !hasStoredKey
}

func handleAuthAPI(w http.ResponseWriter, r *http.Request) {
	if dashCore.AuthStore == nil {
		http.Error(w, "auth store unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		// list providers with key status (keys masked)
		providers := providerSnapshot()
		for i, p := range providers {
			name, _ := p["name"].(string)
			p["key_set"] = dashCore.AuthStore.Get(name) != "" || os.Getenv(safeGet(p, "api_key_env")) != ""
			providers[i] = p
		}
		json.NewEncoder(w).Encode(map[string]any{"providers": providers})
	case "POST":
		var body struct {
			Provider string `json:"provider"`
			Key      string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" {
			http.Error(w, "provider name required", 400)
			return
		}
		if body.Key == "" {
			dashCore.AuthStore.Delete(body.Provider)
		} else {
			dashCore.AuthStore.Set(body.Provider, body.Key)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case "DELETE":
		provider := r.URL.Query().Get("provider")
		if provider == "" {
			http.Error(w, "?provider= required", 400)
			return
		}
		dashCore.AuthStore.Delete(provider)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		http.Error(w, "GET, POST, or DELETE", 405)
	}
}

func safeGet(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if dashCore.OAuth != nil {
		dashCore.OAuth.HandleCallback(w, r)
	} else {
		http.Error(w, "OAuth not configured", http.StatusServiceUnavailable)
	}
}

func handleSwitchAPI(w http.ResponseWriter, r *http.Request) {
	if dashCore.Client == nil {
		http.Error(w, "no providers configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case "GET":
		active := dashCore.Client.ActiveProvider("default")
		json.NewEncoder(w).Encode(map[string]any{
			"active": active, "active_model": dashCore.Client.ActiveModel("default"),
			"reasoning": dashCore.Client.ActiveReasoning("default"),
			"providers": dashCore.Client.ProviderNames(), "options": dashCore.Client.ProviderOptions(),
		})
	case "POST":
		var body struct {
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			Reasoning string `json:"reasoning"`
			Session   string `json:"session"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Session == "" {
			body.Session = "default"
		}
		if err := dashCore.Client.SetPreferredModel(body.Session, body.Provider, body.Model, body.Reasoning); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "active": body.Provider,
			"model": dashCore.Client.ActiveModel(body.Session), "reasoning": dashCore.Client.ActiveReasoning(body.Session)})
	default:
		http.Error(w, "GET or POST", 405)
	}
}

func handleProvidersAPI(w http.ResponseWriter, r *http.Request) {
	home := dashCore.Settings.Home
	switch r.Method {
	case "POST":
		var body struct {
			Name       string `json:"name"`
			BaseURL    string `json:"base_url"`
			Model      string `json:"model"`
			SmallModel string `json:"small_model"`
			APIKey     string `json:"api_key"`
			Priority   int    `json:"priority"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Name == "" || body.BaseURL == "" || body.Model == "" {
			http.Error(w, "name, base_url, and model are required", 400)
			return
		}
		if body.Priority == 0 {
			body.Priority = 10
		}
		// load existing
		existing := map[string]any{}
		path := filepath.Join(home, "providers.json")
		if data, err := os.ReadFile(path); err == nil {
			json.Unmarshal(data, &existing)
		}
		list, _ := existing["providers"].([]any)
		// dedup by name
		filtered := make([]any, 0)
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && m["name"] == body.Name {
				continue
			}
			filtered = append(filtered, item)
		}
		filtered = append(filtered, map[string]any{
			"name":        body.Name,
			"priority":    body.Priority,
			"base_url":    body.BaseURL,
			"api_key_env": "",
			"model":       body.Model,
			"small_model": body.SmallModel,
		})
		existing["providers"] = filtered
		data, _ := json.MarshalIndent(existing, "", "  ")
		os.WriteFile(path, data, 0644)
		// save key to auth.json
		if body.APIKey != "" && dashCore.AuthStore != nil {
			dashCore.AuthStore.Set(body.Name, body.APIKey)
		}
		dashCore.Client.ReloadProviders(dashCore.Settings.Home)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case "DELETE":
		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "?name= required", 400)
			return
		}
		path := filepath.Join(home, "providers.json")
		existing := map[string]any{}
		if data, err := os.ReadFile(path); err == nil {
			json.Unmarshal(data, &existing)
		}
		list, _ := existing["providers"].([]any)
		filtered := make([]any, 0)
		for _, item := range list {
			if m, ok := item.(map[string]any); ok && m["name"] == name {
				continue
			}
			filtered = append(filtered, item)
		}
		existing["providers"] = filtered
		data, _ := json.MarshalIndent(existing, "", "  ")
		os.WriteFile(path, data, 0644)
		// also remove from auth.json
		if dashCore.AuthStore != nil {
			dashCore.AuthStore.Delete(name)
		}
		dashCore.Client.ReloadProviders(dashCore.Settings.Home)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		http.Error(w, "POST or DELETE", 405)
	}
}

func handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	if dashCore.OAuth == nil {
		json.NewEncoder(w).Encode(map[string]any{"providers": []any{}})
		return
	}
	providers := dashCore.OAuth.Providers()
	out := make([]map[string]any, 0, len(providers))
	for _, p := range providers {
		out = append(out, map[string]any{
			"name":         p.Name,
			"display_name": p.DisplayName,
			"auth_type":    p.AuthType,
			"models":       p.Models,
			"logged_in":    dashCore.AuthStore.Get(p.Name) != "",
		})
	}
	json.NewEncoder(w).Encode(map[string]any{"providers": out})
}

func handleOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if dashCore.OAuth == nil {
		http.Error(w, "oauth not available", http.StatusServiceUnavailable)
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/api/oauth/login/")
	if provider == "" {
		http.Error(w, "provider name required in path", 400)
		return
	}

	if provider == "gemini" {
		// Step 2: user submits the redirect URL from their local browser
		if r.Method == "POST" && r.URL.Query().Get("step") == "complete" {
			var body struct {
				State       string `json:"state"`
				RedirectURL string `json:"redirect_url"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if err := dashCore.OAuth.CompleteGeminiADC(body.State, body.RedirectURL); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "Logged in to Google Gemini!"})
			return
		}
		// Step 1: start ADC login, return URL
		authURL, state, err := dashCore.OAuth.BeginGeminiADC()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"url":     authURL,
			"state":   state,
			"message": "Open this URL in your browser. After login, Google will redirect you to localhost — copy the FULL redirect URL from the address bar and paste it back.",
		})
		return
	}

	if provider == "codex" {
		verificationURL, userCode, deviceCode, interval, err := dashCore.OAuth.BeginCodexDeviceLogin()
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "message": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":          true,
			"url":         verificationURL,
			"user_code":   userCode,
			"device_code": deviceCode,
			"interval":    interval,
			"message":     "Open the link and enter the code.",
		})
		return
	}

	authURL, err := dashCore.OAuth.BeginPKCE(provider)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": authURL, "message": "Complete login in your browser."})
}

func handleOAuthDevice(w http.ResponseWriter, r *http.Request) {
	if dashCore.OAuth == nil {
		http.Error(w, "oauth not available", http.StatusServiceUnavailable)
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/api/oauth/device/")

	switch r.Method {
	case "POST":
		verificationURL, userCode, err := dashCore.OAuth.BeginDeviceCode(provider)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":               true,
			"user_code":        userCode,
			"verification_url": verificationURL,
			"device_code":      userCode,
		})
	case "GET":
		deviceCode := r.URL.Query().Get("device_code")
		if deviceCode == "" {
			http.Error(w, "?device_code= required", 400)
			return
		}
		if provider == "codex" {
			done, err := dashCore.OAuth.PollCodexDeviceLogin(deviceCode)
			if err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "pending": true, "error": err.Error()})
				return
			}
			if done {
				dashCore.OAuth.EnsureProvider(dashCore.OAuth.providerMap["codex"])
				dashCore.Client.ReloadProviders(dashCore.Settings.Home)
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": done, "pending": !done})
			return
		}
		token, err := dashCore.OAuth.PollDeviceCode(deviceCode)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "pending": true, "error": err.Error()})
			return
		}
		if err := dashCore.AuthStore.Set(provider, token); err != nil {
			http.Error(w, "failed to save key", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		http.Error(w, "POST or GET", 405)
	}
}

func handleSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		ProviderName  string `json:"provider_name"`
		APIKey        string `json:"api_key"`
		BaseURL       string `json:"base_url"`
		Model         string `json:"model"`
		SmallModel    string `json:"small_model"`
		TelegramToken string `json:"telegram_token"`
		TavilyKey     string `json:"tavily_key"`
		CfToken       string `json:"cf_token"`
		CfAccountID   string `json:"cf_account_id"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	// api_key is optional (keyless providers like Ollama)
	if body.BaseURL == "" || body.Model == "" {
		http.Error(w, "base_url and model are required", 400)
		return
	}
	name := body.ProviderName
	if name == "" {
		name = "default"
	}
	// merge into existing providers (don't overwrite)
	home := dashCore.Settings.Home
	path := filepath.Join(home, "providers.json")
	existing := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &existing)
	}
	list, _ := existing["providers"].([]any)
	filtered := make([]any, 0)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok && m["name"] == name {
			continue
		}
		filtered = append(filtered, item)
	}
	apiKeyEnv := ""
	if body.APIKey != "" {
		apiKeyEnv = "MINO_API_KEY"
	}
	filtered = append(filtered, map[string]any{
		"name":        name,
		"priority":    1,
		"base_url":    body.BaseURL,
		"api_key_env": apiKeyEnv,
		"model":       body.Model,
		"small_model": body.SmallModel,
	})
	existing["providers"] = filtered
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(path, data, 0644)
	// save key to auth.json
	if body.APIKey != "" && dashCore.AuthStore != nil {
		dashCore.AuthStore.Set(name, body.APIKey)
	}
	// write mino.env so systemd picks it up; merge instead of rewriting so
	// unrelated keys (CLOUDFLARE_*, THREADS_*, MINO_OPENROUTER_KEY, ...) survive
	updates := map[string]string{
		"MINO_HOME":          home,
		"MINO_API_KEY":       body.APIKey,
		"MINO_BASE_URL":      body.BaseURL,
		"MINO_MODEL":         body.Model,
		"MINO_SMALL_MODEL":   body.SmallModel,
		"MINO_TIMEZONE":      dashCore.Settings.Timezone,
		"TELEGRAM_BOT_TOKEN": body.TelegramToken,
		"TAVILY_API_KEY":     body.TavilyKey,
		"CLOUDFLARE_API_TOKEN":  body.CfToken,
		"CLOUDFLARE_ACCOUNT_ID": body.CfAccountID,
	}
	if err := mergeEnvFile(filepath.Join(home, "mino.env"), updates); err != nil {
		slog.Warn("mino.env merge failed", "error", err)
	}
	if body.TavilyKey != "" {
		os.Setenv("TAVILY_API_KEY", body.TavilyKey)
	}
	if body.CfToken != "" {
		os.Setenv("CLOUDFLARE_API_TOKEN", body.CfToken)
	}
	if body.CfAccountID != "" {
		os.Setenv("CLOUDFLARE_ACCOUNT_ID", body.CfAccountID)
	}
	// pick up changes without restart
	if dashCore.Client != nil {
		dashCore.Client.ReloadProviders(home)
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "Saved."})
}

// mergeEnvFile updates KEY=VALUE lines in an env file in place, preserving
// every other key. Empty values leave the existing key untouched.
func mergeEnvFile(path string, updates map[string]string) error {
	env := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(line, "=")
			k = strings.TrimSpace(k)
			if ok && k != "" && strings.TrimSpace(v) != "" {
				env[k] = v
			}
		}
	}
	for k, v := range updates {
		if v != "" {
			env[k] = v
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k + "=" + env[k] + "\n")
	}
	return os.WriteFile(path, []byte(sb.String()), 0600)
}

func countRows(db *sql.DB, table string) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func handleRevealAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		dashboardArtifactError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if dashCore == nil || dashCore.Settings == nil {
		dashboardArtifactError(w, http.StatusServiceUnavailable, "dashboard is not ready")
		return
	}
	action := r.URL.Query().Get("action")
	if action == "" {
		action = "inspect"
	}
	if action != "inspect" && action != "download" {
		dashboardArtifactError(w, http.StatusBadRequest, "unsupported artifact action")
		return
	}
	path, info, status, message := resolveDashboardArtifact(dashCore.Settings.Home, dashCore.Settings.MemoriesDir, r.URL.Query().Get("path"))
	if status != http.StatusOK {
		dashboardArtifactError(w, status, message)
		return
	}
	if action == "download" {
		if info.IsDir() {
			dashboardArtifactError(w, http.StatusBadRequest, "directories cannot be downloaded")
			return
		}
		serveDashboardArtifactFile(w, r, path, true)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "action": action, "kind": map[bool]string{true: "directory", false: "file"}[info.IsDir()],
		"path": path, "name": info.Name(), "size": info.Size(), "mod_time": info.ModTime().Format("2006-01-02 15:04"),
	})
}

func handleFilesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		dashboardArtifactError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	if dashCore == nil || dashCore.Settings == nil {
		dashboardArtifactError(w, http.StatusServiceUnavailable, "dashboard is not ready")
		return
	}
	action := r.URL.Query().Get("action")
	if action != "" && action != "view" && action != "download" {
		dashboardArtifactError(w, http.StatusBadRequest, "unsupported artifact action")
		return
	}
	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		requestedPath = "/tmp/mino/results"
	}
	abs, info, status, message := resolveDashboardArtifact(dashCore.Settings.Home, dashCore.Settings.MemoriesDir, requestedPath)
	if status != http.StatusOK {
		dashboardArtifactError(w, status, message)
		return
	}
	if !info.IsDir() {
		serveDashboardArtifactFile(w, r, abs, action == "download")
		return
	}
	entries, _ := os.ReadDir(abs)
	type node struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		IsDir    bool   `json:"is_dir"`
		ModTime  string `json:"mod_time"`
		Children []node `json:"children,omitempty"`
	}
	tree := make([]node, 0, len(entries))
	for _, e := range entries {
		fi, _ := e.Info()
		n := node{Name: e.Name(), Path: filepath.Join(abs, e.Name()), IsDir: e.IsDir()}
		if fi != nil {
			n.Size = fi.Size()
			n.ModTime = fi.ModTime().Format("2006-01-02 15:04")
		}
		if e.IsDir() {
			// shallow — children loaded by frontend on expand
			n.Children = []node{}
		}
		tree = append(tree, n)
	}
	sort.Slice(tree, func(i, j int) bool {
		if tree[i].IsDir != tree[j].IsDir {
			return tree[i].IsDir
		}
		return tree[i].Name < tree[j].Name
	})
	json.NewEncoder(w).Encode(tree)
}

func dashboardArtifactError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": message})
}

func serveDashboardArtifactFile(w http.ResponseWriter, r *http.Request, path string, download bool) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if download {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	} else {
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(path)))
	}
	http.ServeFile(w, r, path)
}

// resolveDashboardArtifact limits dashboard file access to Mino's home and
// generated result roots, including symlink targets that remain inside them.
func resolveDashboardArtifact(home, memoriesDir, raw string) (string, os.FileInfo, int, string) {
	if home == "" {
		return "", nil, http.StatusServiceUnavailable, "Mino home is not configured"
	}
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", nil, http.StatusBadRequest, "an artifact path is required"
	} else if !filepath.IsAbs(path) {
		if memoriesDir != "" && (path == "memories" || strings.HasPrefix(path, "memories"+string(os.PathSeparator))) {
			suffix := strings.TrimPrefix(path, "memories")
			suffix = strings.TrimPrefix(suffix, string(os.PathSeparator))
			path = filepath.Join(memoriesDir, suffix)
		} else {
			path = filepath.Join(home, path)
		}
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", nil, http.StatusBadRequest, "invalid artifact path"
	}
	roots := []string{
		filepath.Join(home, "playbooks"),
		filepath.Join(home, "skills"),
		filepath.Join(home, "traces"),
		filepath.Join(home, "outbox"),
		"/tmp/mino/results",
	}
	if memoriesDir != "" {
		roots = append(roots, memoriesDir)
	}
	allowedArtifactPath := func(candidate string) bool {
		if artifactPathWithin(candidate, filepath.Join(home, "SOUL.md")) ||
			artifactPathWithin(candidate, filepath.Join(home, "calendar.ics")) ||
			artifactPathWithin(candidate, filepath.Join(home, "usage.jsonl")) {
			return true
		}
		for _, root := range roots {
			if artifactPathWithin(candidate, root) {
				return true
			}
		}
		return false
	}
	if !allowedArtifactPath(abs) {
		return "", nil, http.StatusForbidden, "artifact path is outside Mino-authorized roots"
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, http.StatusNotFound, "artifact was not found"
		}
		return "", nil, http.StatusForbidden, "artifact is unavailable"
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", nil, http.StatusForbidden, "artifact link is unavailable"
	}
	if allowedArtifactPath(resolved) {
		return resolved, info, http.StatusOK, ""
	}
	return "", nil, http.StatusForbidden, "artifact link leaves Mino-authorized roots"
}

func artifactPathWithin(path, root string) bool {
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// --- Usage stats from usage.jsonl (Core-compatible) ---

func usageRecords(home string) []map[string]any {
	data, _ := os.ReadFile(home + "/usage.jsonl")
	var recs []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r map[string]any
		if json.Unmarshal([]byte(line), &r) == nil {
			recs = append(recs, r)
		}
	}
	return recs
}

func usageStats(home string) map[string]any {
	recs := usageRecords(home)
	var tokensIn, tokensOut, cachedIn float64
	var latencies []float64
	toolCalls := 0
	_ = dashCore.DB.QueryRow("SELECT COUNT(*) FROM chat_log WHERE content LIKE '%[tools used:%'").Scan(&toolCalls)
	turns := len(recs)
	gateSkips, gateRetrieves, toolErrors := traceTelemetry(home)

	for _, r := range recs {
		if v, ok := r["in"].(float64); ok {
			tokensIn += v
		}
		if v, ok := r["out"].(float64); ok {
			tokensOut += v
		}
		if v, ok := r["cache_read"].(float64); ok {
			cachedIn += v
		}
		if v, ok := r["latency_ms"].(float64); ok {
			latencies = append(latencies, v)
		}
	}

	avgLatency := 0.0
	p95 := 0.0
	if len(latencies) > 0 {
		for _, l := range latencies {
			avgLatency += l
		}
		avgLatency /= float64(len(latencies))
		sort.Float64s(latencies)
		idx := int(float64(len(latencies)) * 0.95)
		if idx >= len(latencies) {
			idx = len(latencies) - 1
		}
		p95 = latencies[idx]
	}

	traceFiles := 0
	if entries, err := os.ReadDir(filepath.Join(home, "traces")); err == nil {
		traceFiles = len(entries)
	}
	// pricing: MiMo ≈ $2/$15 per million
	cost := tokensIn/1e6*2.0 + tokensOut/1e6*15.0

	evalReports := 0
	if evalReport(home) != nil {
		evalReports = 1
	}
	return map[string]any{
		"turns":          turns,
		"tool_calls":     toolCalls,
		"tool_errors":    toolErrors,
		"gate_skips":     gateSkips,
		"gate_retrieves": gateRetrieves,
		"tokens_in":      int(tokensIn),
		"tokens_out":     int(tokensOut),
		"cached_tokens":  int(cachedIn),
		"cache_hit_pct":  cacheHitPercent(cachedIn, tokensIn),
		"total_cost":     cost,
		"latency_avg":    int(avgLatency),
		"latency_p95":    int(p95),
		"trace_files":    traceFiles,
		"eval_reports":   evalReports,
	}
}

func usageSummary(home string) map[string]any {
	recs := usageRecords(home)
	var totalIn, totalOut, totalCached float64
	byDay := map[string]map[string]any{}
	byProvider := map[string]map[string]any{}

	for _, r := range recs {
		in, _ := r["in"].(float64)
		out, _ := r["out"].(float64)
		cached, _ := r["cache_read"].(float64)
		totalIn += in
		totalOut += out
		totalCached += cached

		ts, _ := r["ts"].(string)
		day := ""
		if len(ts) >= 10 {
			day = ts[:10]
		}
		if day != "" {
			if byDay[day] == nil {
				byDay[day] = map[string]any{"date": day, "calls": 0, "in": 0, "out": 0, "cached": 0, "cost": 0.0}
			}
			b := byDay[day]
			b["calls"] = b["calls"].(int) + 1
			b["in"] = b["in"].(int) + int(in)
			b["out"] = b["out"].(int) + int(out)
			b["cached"] = b["cached"].(int) + int(cached)
			b["cost"] = b["cost"].(float64) + in/1e6*2.0 + out/1e6*15.0
		}
		provider, _ := r["provider"].(string)
		if provider == "" {
			provider = "unknown"
		}
		if byProvider[provider] == nil {
			byProvider[provider] = map[string]any{"provider": provider, "calls": 0, "in": 0, "out": 0, "cached": 0, "cost": 0.0}
		}
		p := byProvider[provider]
		p["calls"] = p["calls"].(int) + 1
		p["in"] = p["in"].(int) + int(in)
		p["out"] = p["out"].(int) + int(out)
		p["cached"] = p["cached"].(int) + int(cached)
		p["cost"] = p["cost"].(float64) + in/1e6*2.0 + out/1e6*15.0
	}

	// convert to sorted slice
	var days []map[string]any
	for _, v := range byDay {
		days = append(days, v)
	}
	sort.Slice(days, func(i, j int) bool { return days[i]["date"].(string) < days[j]["date"].(string) })
	providers := make([]map[string]any, 0, len(byProvider))
	for _, v := range byProvider {
		providers = append(providers, v)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i]["calls"].(int) > providers[j]["calls"].(int) })

	return map[string]any{
		"calls": len(recs), "total_in": int(totalIn), "total_out": int(totalOut),
		"cached_tokens": int(totalCached), "cache_hit_pct": cacheHitPercent(totalCached, totalIn),
		"total_cost": totalIn/1e6*2.0 + totalOut/1e6*15.0, "by_day": days, "by_provider": providers,
	}
}

func cacheHitPercent(cached, input float64) float64 {
	if input <= 0 {
		return 0
	}
	return cached / input * 100
}

// --- Trace helpers ---

func traceEvents(home string) []map[string]any {
	today := time.Now().Format("2006-01-02") + ".jsonl"
	data, _ := os.ReadFile(home + "/traces/" + today)
	var events []map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) == nil {
			events = append(events, ev)
		}
	}
	return events
}

func traceFileName(home string) string {
	return time.Now().Format("2006-01-02") + ".jsonl"
}

// traceLLMInputs collects today's per-iteration input tokens from llm trace
// events, in trace order.
func traceLLMInputs(home string) []int {
	var vals []int
	for _, ev := range traceEvents(home) {
		if ev["type"] != "llm" {
			continue
		}
		if in, ok := ev["in"].(float64); ok && in > 0 {
			vals = append(vals, int(in))
		}
	}
	return vals
}

// medianP90 returns the upper median and 90th percentile of vals (sorted copy).
// Upper median (sorted[len/2]) keeps the gauge simple and deterministic; the
// caller treats 0,0 as "no data".
func medianP90(vals []int) (median, p90 int) {
	if len(vals) == 0 {
		return 0, 0
	}
	sorted := append([]int(nil), vals...)
	sort.Ints(sorted)
	median = sorted[len(sorted)/2]
	idx := (len(sorted) * 9) / 10
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return median, sorted[idx]
}

func traceTelemetry(home string) (skips, retrieves, toolErrors int) {
	inTurn, recalled := false, false
	for _, ev := range traceEvents(home) {
		switch ev["type"] {
		case "turn_start":
			inTurn, recalled = true, false
		case "tool":
			if ev["status"] == "error" {
				toolErrors++
			}
			if inTurn && ev["tool"] == "remember" {
				recalled = true
			}
		case "turn_end":
			if !inTurn {
				continue
			}
			if recalled {
				retrieves++
			} else {
				skips++
			}
			inTurn = false
		}
	}
	return
}

func traceTail(home string) []map[string]any {
	events := traceEvents(home)
	if len(events) > 18 {
		events = events[len(events)-18:]
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	var tail []map[string]any
	for _, ev := range events {
		detail := ""
		switch ev["type"] {
		case "turn_start":
			detail = fmt.Sprintf("%v", ev["user_message"])
		case "llm":
			detail = fmt.Sprintf("in=%v out=%v", ev["in"], ev["out"])
		case "tool":
			detail = fmt.Sprintf("%v", ev["tool"])
			if ev["status"] == "error" {
				detail += ": " + fmt.Sprintf("%v", ev["output"])
			}
		case "gate":
			detail = fmt.Sprintf("%v: %v", ev["decision"], ev["reason"])
		case "turn_end":
			detail = fmt.Sprintf("%v", ev["reply"])
		}
		entry := map[string]any{"type": ev["type"], "ts": ev["ts"], "detail": detail}
		// stage attribution: events inside a playbook stage carry its identity
		if pb, ok := ev["playbook"]; ok {
			entry["playbook"] = pb
		}
		if st, ok := ev["stage"]; ok {
			entry["stage"] = st
		}
		tail = append(tail, entry)
	}
	return tail
}

func traceTurns(home string) []map[string]any {
	events := traceEvents(home)
	turns := make([]map[string]any, 0)
	var current map[string]any
	var llmCalls []map[string]any
	var tools []map[string]any
	for _, ev := range events {
		switch ev["type"] {
		case "turn_start":
			current = map[string]any{"user_message": ev["user_message"], "ts": ev["ts"]}
			llmCalls = nil
			tools = nil
		case "llm":
			llmCalls = append(llmCalls, ev)
		case "tool":
			tools = append(tools, ev)
		case "gate":
			if current != nil {
				current["gate"] = map[string]any{"decision": ev["decision"], "reason": ev["reason"]}
			}
		case "turn_end":
			if current != nil {
				var tokensIn, tokensOut int
				for _, call := range llmCalls {
					if value, ok := call["in"].(float64); ok {
						tokensIn += int(value)
					}
					if value, ok := call["out"].(float64); ok {
						tokensOut += int(value)
					}
				}
				current["reply"] = ev["reply"]
				current["status"] = ev["status"]
				current["iterations"] = ev["iterations"]
				current["llm_calls"] = llmCalls
				current["tools"] = tools
				// stage attribution for the turn's tool calls (grouping in the UI)
				for _, tool := range tools {
					if pb, ok := tool["playbook"]; ok {
						current["playbook"] = pb
					}
					if st, ok := tool["stage"]; ok {
						current["stage"] = st
					}
				}
				current["tokens_in"] = tokensIn
				current["tokens_out"] = tokensOut
				current["cost"] = float64(tokensIn)/1e6*2.0 + float64(tokensOut)/1e6*15.0
				start, startOK := current["ts"].(string)
				end, endOK := ev["ts"].(string)
				if started, err := time.Parse(time.RFC3339, start); startOK && endOK && err == nil {
					if finished, err := time.Parse(time.RFC3339, end); err == nil {
						current["latency_ms"] = finished.Sub(started).Milliseconds()
					}
				}
				turns = append(turns, current)
				current = nil
			}
		}
	}
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	if len(turns) > 50 {
		turns = turns[:50]
	}
	return turns
}

// --- Health and metrics endpoints (§18.2) ---

var processStartTime = time.Now()

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"version":        Version,
		"uptime_seconds": int(time.Since(processStartTime).Seconds()),
	})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	// uptime
	b.WriteString(fmt.Sprintf("mino_uptime_seconds %d\n", int(time.Since(processStartTime).Seconds())))
	// tool calls from DB
	if dashCore != nil && dashCore.DB != nil {
		rows, err := dashCore.DB.Query(`SELECT tool_name, status, COUNT(*) FROM tool_calls WHERE created_at > datetime('now', '-1 hour') GROUP BY tool_name, status`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name, status string
				var count int
				if rows.Scan(&name, &status, &count) == nil {
					b.WriteString(fmt.Sprintf("mino_tool_calls_total{tool_name=%q,status=%q} %d\n", name, status, count))
				}
			}
		}

	}
	// Per-iteration input tokens from today's trace: daily median/p90 track
	// context-budget health, the destination of the context-bloat effort.
	if dashCore != nil && dashCore.Settings != nil && dashCore.Settings.Home != "" {
		if inputs := traceLLMInputs(dashCore.Settings.Home); len(inputs) > 0 {
			median, p90 := medianP90(inputs)
			b.WriteString(fmt.Sprintf("mino_llm_input_tokens_median %d\n", median))
			b.WriteString(fmt.Sprintf("mino_llm_input_tokens_p90 %d\n", p90))
			b.WriteString(fmt.Sprintf("mino_llm_iterations_today %d\n", len(inputs)))
		}
	}
	w.Write([]byte(b.String()))
}
