package main

// Mino — ops/dashboard.py + waku/gateway/telegram.py
// Serves Core's exact static files + API endpoints.

import (
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

func RunDashboard(w *Core) {
	dashCore = w

	static, _ := fs.Sub(staticFiles, "static")
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, _ := staticFiles.ReadFile("static/index.html")
		w.Header().Set("Content-Type", "text/html")
		w.Write(data)
	})
	http.HandleFunc("/api/chat/stream", handleChatStream)
	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/session", handleSession)
	http.HandleFunc("/api/memory", handleMemoryAPI)
	http.HandleFunc("/api/query", handleQueryAPI)
	http.HandleFunc("/api/events", handleEventsAPI)
	http.HandleFunc("/api/data", handleDataAPI)
	http.HandleFunc("/api/reveal", handleRevealAPI)
	http.HandleFunc("/api/files", handleFilesAPI)
	http.HandleFunc("/api/active-tasks", handleActiveTasks)
	http.HandleFunc("/api/settings", handleSettingsAPI)

	// Telegram runs in main — don't double-start here

	port := "7777"
	if p := os.Getenv("MINO_DASHBOARD_PORT"); p != "" {
		port = p
	}
	host := os.Getenv("MINO_DASHBOARD_HOST")
	addr := net.JoinHostPort(host, port)
	slog.Info("dashboard", "addr", addr)
	http.ListenAndServe(addr, nil)
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
	result := dashCore.RespondFor(sid, body.Message, "dashboard", nil, false)

	tools := make([]map[string]any, 0)
	for _, tc := range result.ToolCalls {
		tools = append(tools, map[string]any{
			"tool": tc.Name, "args": tc.Args, "output": tc.Output,
			"status": "ok", "summary": tc.Output,
		})
	}

	json.NewEncoder(w).Encode(map[string]any{
		"reply":      result.Reply,
		"iterations": result.Iterations,
		"tools":      tools,
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
		dashEventMu.Lock()
		dashEventQ = append(dashEventQ, map[string]any{"type": stageType, "decision": data["decision"]})
		dashEventMu.Unlock()
	}

	// emit turn_start for architecture SVG
	dashEventMu.Lock()
	dashEventQ = append(dashEventQ, map[string]any{"type": "turn_start"})
	dashEventMu.Unlock()

	result := dashCore.RespondFor(sid, body.Message, "dashboard", obs, true)

	// done event — Core format: flat fields, no 'data' wrapper
	doneEv := map[string]any{
		"reply":      result.Reply,
		"iterations": result.Iterations,
		"latency_ms": 0,
	}
	if len(result.ToolCalls) > 0 {
		tools := make([]map[string]any, len(result.ToolCalls))
		for i, tc := range result.ToolCalls {
			tools[i] = map[string]any{
				"tool": tc.Name, "args": tc.Args, "output": tc.Output,
				"status": toolOutputStatus(tc.Output), "summary": tc.Output,
			}
		}
		doneEv["tools"] = tools
	}
	doneEv["kind"] = "done"

	// publish turn_end + done
	dashEventMu.Lock()
	dashEventQ = append(dashEventQ, map[string]any{"type": "turn_end"})
	dashEventMu.Unlock()

	b, _ := json.Marshal(doneEv)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
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
			ID      int    `json:"id"`
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		db := dashCore.DB
		switch body.Action {
		case "update_fact":
			var subject, old string
			if db.QueryRow("SELECT subject, content FROM facts WHERE id = ?", body.ID).Scan(&subject, &old) == nil {
				db.Exec("UPDATE facts SET content = ?, feedback = 0 WHERE id = ?", body.Content, body.ID)
				if dashCore.Memory.embedder != nil {
					dashCore.Memory.embedder.Remove("fact", subject+": "+old)
					dashCore.Memory.embedder.Index("fact", subject+": "+body.Content)
				}
			}
		case "delete_fact":
			var subject, content string
			if db.QueryRow("SELECT subject, content FROM facts WHERE id = ?", body.ID).Scan(&subject, &content) == nil {
				db.Exec("DELETE FROM facts WHERE id = ?", body.ID)
				if dashCore.Memory.embedder != nil {
					dashCore.Memory.embedder.Remove("fact", subject+": "+content)
				}
			}
		case "delete_episode":
			var summary string
			if db.QueryRow("SELECT summary FROM episodes WHERE id = ?", body.ID).Scan(&summary) == nil {
				db.Exec("DELETE FROM episodes WHERE id = ?", body.ID)
				if dashCore.Memory != nil && dashCore.Memory.embedder != nil {
					dashCore.Memory.embedder.Remove("episode", summary)
				}
			}
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
	facts := queryAll(dashCore.DB, "SELECT id, subject, content, source, created_at FROM facts ORDER BY id")
	episodes := queryAll(dashCore.DB, "SELECT id, summary, happened_at FROM episodes ORDER BY id DESC")
	skills := skillCatalog(dashCore.Settings.Home)

	json.NewEncoder(w).Encode(map[string]any{
		"facts": facts, "episodes": episodes, "skills": skills,
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

func handleEventsAPI(w http.ResponseWriter, r *http.Request) {
	dashEventMu.Lock()
	events := make([]map[string]any, len(dashEventQ))
	copy(events, dashEventQ)
	dashEventQ = nil // consumed
	dashCursor++
	dashEventMu.Unlock()

	json.NewEncoder(w).Encode(map[string]any{
		"events": events,
		"cursor": fmt.Sprintf("%d", dashCursor),
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
	sl := NewSkillLoader(home, nil)
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

func handleDataAPI(w http.ResponseWriter, r *http.Request) {
	db := dashCore.DB

	// chat_log from DB (last 80 messages, reversed for frontend)
	chatLog := queryAll(db,
		"SELECT role, content, consolidated, source, session_id, created_at FROM chat_log ORDER BY id DESC LIMIT 80")
	// reverse so oldest first
	for i, j := 0, len(chatLog)-1; i < j; i, j = i+1, j-1 {
		chatLog[i], chatLog[j] = chatLog[j], chatLog[i]
	}

	// sessions — grouped by session_id
	sessions := sessionList(db)

	// facts, episodes, calendar — ensure non-nil
	factsData := queryAll(db, "SELECT id, subject, content, source, created_at FROM facts ORDER BY id DESC")
	episodesData := queryAll(db, "SELECT id, happened_at, summary FROM episodes ORDER BY happened_at DESC")
	calendarData := queryAll(db, "SELECT title, start, \"end\", attendees, created_at FROM calendar_events ORDER BY start")
	skillsData := skillCatalog(dashCore.Settings.Home)
	outboxData := outboxList(dashCore.Settings.Home)
	soulData, _ := os.ReadFile(filepath.Join(dashCore.Settings.Home, "SOUL.md"))
	activeTasks := ListActiveTasks(dashCore.Settings.Home)
	if activeTasks == nil {
		activeTasks = []TaskSnapshot{}
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
	factsN, _ := countRows(db, "facts")
	episodesN, _ := countRows(db, "episodes")
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

	resp := map[string]any{
		"provider":          dashCore.Settings.Provider,
		"model":             dashCore.Settings.Model,
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
		"tables":            map[string]int{"facts": factsN, "episodes": episodesN, "calendar_events": calendarN, "chat_log": chatN},
		"facts":             factsData,
		"episodes":          episodesData,
		"calendar":          calendarData,
		"outbox":            outboxData,
		"skills":            skillsData,
		"soul":              string(soulData),
		"tools": map[string]any{
			"catalog":  dashCore.Tools.Catalog(),
			"mcp":      map[string]any{"configured": mcpConfigured(dashCore.Settings.Home), "servers": mcpServers(dashCore.Settings.Home), "live": mcpLive(dashCore.Settings.Home)},
			"apple_on": false,
		},
		"db":               databaseSnapshot(db, dashCore.Settings.Home, allTables),
		"settings":         map[string]any{"providers": providerSnapshot(), "config_file": filepath.Join(dashCore.Settings.Home, "providers.json")},
		"eval_report":      nil,
		"wake_scans":       []any{},
		"reports":          []any{},
		"active_tasks":     activeTasks,
		"needs_onboarding": needsOnboarding(dashCore.Settings.Home),
	}
	json.NewEncoder(w).Encode(resp)
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
		out = append(out, map[string]any{
			"name": p.Name, "priority": p.Priority, "base_url": p.BaseURL, "model": p.Model,
			"small_model": p.Small, "api_key_env": p.APIKeyEnv, "key_set": os.Getenv(p.APIKeyEnv) != "",
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
	tasks := ListActiveTasks(dashCore.Settings.Home)
	if tasks == nil {
		tasks = []TaskSnapshot{}
	}
	json.NewEncoder(w).Encode(map[string]any{"tasks": tasks})
}

func needsOnboarding(home string) bool {
	_, err := os.Stat(filepath.Join(home, "providers.json"))
	return os.IsNotExist(err) && os.Getenv("MINO_API_KEY") == ""
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
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.APIKey == "" || body.BaseURL == "" || body.Model == "" {
		http.Error(w, "api_key, base_url, and model are required", 400)
		return
	}
	name := body.ProviderName
	if name == "" {
		name = "default"
	}
	cfg := []ProviderConfig{{
		Name:      name,
		Priority:  1,
		BaseURL:   body.BaseURL,
		APIKeyEnv: "MINO_API_KEY",
		Model:     body.Model,
		Small:     body.SmallModel,
	}}
	data, _ := json.MarshalIndent(map[string]any{"providers": cfg}, "", "  ")
	path := filepath.Join(dashCore.Settings.Home, "providers.json")
	os.WriteFile(path, data, 0644)
	// also write the key to mino.env so systemd picks it up
	envPath := filepath.Join(dashCore.Settings.Home, "mino.env")
	envData := fmt.Sprintf("MINO_HOME=%s\nMINO_API_KEY=%s\nMINO_BASE_URL=%s\nMINO_MODEL=%s\nMINO_SMALL_MODEL=%s\n",
		dashCore.Settings.Home, body.APIKey, body.BaseURL, body.Model, body.SmallModel)
	if body.TelegramToken != "" {
		envData += fmt.Sprintf("TELEGRAM_BOT_TOKEN=%s\n", body.TelegramToken)
	}
	os.WriteFile(envPath, []byte(envData), 0600)

	json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": "Saved. Restarting..."})

	// auto-restart after response is sent
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
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
	w.Write([]byte("{}"))
}

func handleFilesAPI(w http.ResponseWriter, r *http.Request) {
	root := "/tmp/mino/results"
	path := r.URL.Query().Get("path")
	if path == "" {
		path = root
	}
	// prevent traversal outside root
	abs, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(abs, root) {
		http.Error(w, "bad path", 400)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if !info.IsDir() {
		http.ServeFile(w, r, abs)
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
	var tokensIn, tokensOut float64
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

	return map[string]any{
		"turns":          turns,
		"tool_calls":     toolCalls,
		"tool_errors":    toolErrors,
		"gate_skips":     gateSkips,
		"gate_retrieves": gateRetrieves,
		"tokens_in":      int(tokensIn),
		"tokens_out":     int(tokensOut),
		"total_cost":     cost,
		"latency_avg":    int(avgLatency),
		"latency_p95":    int(p95),
		"trace_files":    traceFiles,
		"eval_reports":   0,
	}
}

func usageSummary(home string) map[string]any {
	recs := usageRecords(home)
	var totalIn, totalOut float64
	byDay := map[string]map[string]any{}
	byProvider := map[string]map[string]any{}

	for _, r := range recs {
		in, _ := r["in"].(float64)
		out, _ := r["out"].(float64)
		totalIn += in
		totalOut += out

		ts, _ := r["ts"].(string)
		day := ""
		if len(ts) >= 10 {
			day = ts[:10]
		}
		if day != "" {
			if byDay[day] == nil {
				byDay[day] = map[string]any{"date": day, "calls": 0, "in": 0, "out": 0, "cost": 0.0}
			}
			b := byDay[day]
			b["calls"] = b["calls"].(int) + 1
			b["in"] = b["in"].(int) + int(in)
			b["out"] = b["out"].(int) + int(out)
			b["cost"] = b["cost"].(float64) + in/1e6*2.0 + out/1e6*15.0
		}
		provider, _ := r["provider"].(string)
		if provider == "" {
			provider = "unknown"
		}
		if byProvider[provider] == nil {
			byProvider[provider] = map[string]any{"provider": provider, "calls": 0, "in": 0, "out": 0, "cost": 0.0}
		}
		p := byProvider[provider]
		p["calls"] = p["calls"].(int) + 1
		p["in"] = p["in"].(int) + int(in)
		p["out"] = p["out"].(int) + int(out)
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
		"total_cost": totalIn/1e6*2.0 + totalOut/1e6*15.0, "by_day": days, "by_provider": providers,
	}
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
			if inTurn && ev["tool"] == "recall" {
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
		tail = append(tail, map[string]any{"type": ev["type"], "ts": ev["ts"], "detail": detail})
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
				current["iterations"] = ev["iterations"]
				current["llm_calls"] = llmCalls
				current["tools"] = tools
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
