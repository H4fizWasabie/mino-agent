package main

import (
	"fmt"
	"time"
)

func addDelegateTools(w *Core) {
	run := func(prompt string) string {
		id := fmt.Sprintf("delegate-%d", time.Now().UnixNano())
		tools := w.Tools.Only("read_file", "bash", "search_web")
		system := loadSoul(w.Settings.Home) + "\n\nYou are an ephemeral worker. Investigate the request, use only the available tools, and return a concise answer. Do not save memory or create schedules."
		result := RunLoop(w.Client, id, system, []Message{{Role: "user", Content: prompt}}, tools, min(6, w.Settings.MaxIter), w.Settings.MaxTokens, nil, false, nil, w.Settings.Home, nil)
		return compactToolOutput(w.Settings.Home, id, 1, "delegate", result.Reply)
	}
	w.Tools.Register(&Tool{Name: "delegate", Description: "Run an isolated investigation with a fresh context. Returns only its concise answer or an artifact pointer.", Schema: map[string]any{"type": "object", "properties": map[string]any{"prompt": map[string]any{"type": "string"}}, "required": []string{"prompt"}}, Fn: func(args map[string]any) string { prompt, _ := args["prompt"].(string); return run(prompt) }})
}
