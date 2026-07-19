package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// minowrap — universal adapter: one tool = one JSON entry in tools.json
// Mino discovers tools via GET /tools, calls them via POST /execute.
// Template args like {name} are extracted from the Mino args payload.
// tools.json is re-read on every /tools call — new tools appear instantly.

type ToolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Run         string `json:"run"` // shell command with optional {arg} placeholders
}

type ExtensionTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema"`
}

var reArg = regexp.MustCompile(`\{(\w+)\}`)
var reRawArg = regexp.MustCompile(`\{!(\w+)\}`)

func main() {
	dir := os.Getenv("MINOWRAP_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".minowrap")
	}
	os.MkdirAll(dir, 0700)

	configPath := filepath.Join(dir, "tools.json")

	port := os.Getenv("MINOWRAP_PORT")
	if port == "" {
		port = "9876"
	}

	// Ensure tools.json exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		os.WriteFile(configPath, []byte("[]\n"), 0644)
	}

	mux := http.NewServeMux()

	// GET /tools — returns all tools with auto-generated schemas from template args
	mux.HandleFunc("/tools", func(w http.ResponseWriter, r *http.Request) {
		tools := loadTools(configPath)
		extTools := make([]ExtensionTool, 0, len(tools))
		for _, t := range tools {
			extTools = append(extTools, toExtensionTool(t))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(extTools)
	})

	// POST /execute — runs a tool with args from Mino
	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, "bad request")
			return
		}

		tools := loadTools(configPath)
		var entry *ToolEntry
		for i := range tools {
			if tools[i].Name == req.Tool {
				entry = &tools[i]
				break
			}
		}
		if entry == nil {
			writeError(w, fmt.Sprintf("unknown tool: %s", req.Tool))
			return
		}

		cmd := interpolate(entry.Run, req.Args)
		result := runCommand(cmd)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"result": result})
	})

	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"alert": false, "message": ""})
	})

	slog.Info("minowrap listening", "port", port, "dir", dir)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("minowrap failed", "error", err)
		os.Exit(1)
	}
}

func loadTools(path string) []ToolEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tools []ToolEntry
	json.Unmarshal(data, &tools)
	return tools
}

// toExtensionTool converts a ToolEntry to the extension protocol format,
// auto-generating the JSON Schema from template args in the run field.
// {name} = safe-quoted arg, {!name} = raw/unquoted arg (for code).
func toExtensionTool(t ToolEntry) ExtensionTool {
	props := map[string]any{}
	required := []string{}

	for _, m := range reArg.FindAllStringSubmatch(t.Run, -1) {
		name := m[1]
		props[name] = map[string]any{"type": "string", "description": name + " parameter"}
		required = append(required, name)
	}
	for _, m := range reRawArg.FindAllStringSubmatch(t.Run, -1) {
		name := m[1]
		if _, ok := props[name]; !ok {
			props[name] = map[string]any{"type": "string", "description": name + " (code — raw, no escaping)"}
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	return ExtensionTool{
		Name:        t.Name,
		Description: t.Description,
		Schema:      schema,
	}
}

// interpolate replaces {arg} placeholders with safe-quoted values and
// {!arg} with raw/unquoted values (for code snippets).
func interpolate(template string, args map[string]any) string {
	// Raw args first: {!name} → raw value (no quoting)
	template = reRawArg.ReplaceAllStringFunc(template, func(match string) string {
		name := reRawArg.FindStringSubmatch(match)[1]
		v, ok := args[name]
		if !ok {
			return match
		}
		return fmt.Sprint(v)
	})
	// Safe args: {name} → single-quoted with escaping
	return reArg.ReplaceAllStringFunc(template, func(match string) string {
		name := match[1 : len(match)-1] // strip { and }
		v, ok := args[name]
		if !ok {
			return match
		}
		s := fmt.Sprint(v)
		s = strings.ReplaceAll(s, "'", "'\\''")
		return "'" + s + "'"
	})
}

func runCommand(cmd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	out, err := c.CombinedOutput()
	result := string(out)
	if err != nil {
		result += fmt.Sprintf("\n(exit: %v)", err)
	}
	if len(result) > 1<<20 {
		result = result[:1<<20] + "\n... (truncated)"
	}
	return result
}

func writeError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
