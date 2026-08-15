// mino-memory — MCP stdio server exposing mino's graph-memory retrieval as
// MCP tools. CTX-022 A: any agent (external, same model, other tools) queries
// the graph with the same intent-ranking/BFS/provenance quality as the
// in-loop `remember` tool, without scraping memory files.
//
// It is a thin bridge: the retrieval itself runs in-process inside mino
// (GET /api/memory/remember, read-only, deterministic, no LLM call). This
// server speaks MCP (JSON-RPC over stdio) and forwards.
//
// Install: drop into ~/.mino/mcp.d/ as
//   {"name": "mino-memory", "command": "/path/to/mino-memory"}
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const baseURL = "http://127.0.0.1:7779"

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result any             `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

var client = &http.Client{Timeout: 90 * time.Second}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp := handle(req)
		if resp == nil {
			continue // notification
		}
		out, _ := json.Marshal(resp)
		fmt.Println(string(out))
	}
}

func handle(req rpcRequest) *rpcResponse {
	switch req.Method {
	case "initialize":
		return &rpcResponse{ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mino-memory", "version": "0.1.0"},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "tools/list":
		return &rpcResponse{ID: req.ID, Result: map[string]any{"tools": []toolDef{
			{
				Name:        "memory_remember",
				Description: "Recall facts from mino's graph memory as a connected subgraph (intent-ranked, BFS-expanded, provenance and conflict flags) — identical output to mino's in-loop remember tool. Read-only, no LLM call.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "What to search for"},
						"why":   map[string]any{"type": "string", "description": "Optional current-turn words to augment the query (MEM-03)"},
					},
					"required": []string{"query"},
				},
			},
			{
				Name:        "memory_path",
				Description: "Find the shortest path between two facts in mino's memory graph.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from": map[string]any{"type": "string"},
						"to":   map[string]any{"type": "string"},
					},
					"required": []string{"from", "to"},
				},
			},
		}}}
	case "tools/call":
		var params struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(req.Params, &params)
		var args map[string]any
		json.Unmarshal(params.Args, &args)
		q, _ := args["query"].(string)
		why, _ := args["why"].(string)
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		var out string
		switch params.Name {
		case "memory_remember":
			out = fetchText("/api/memory/remember?q=" + url.QueryEscape(q) + "&why=" + url.QueryEscape(why))
		case "memory_path":
			out = fetchText("/api/memory/path?from=" + url.QueryEscape(from) + "&to=" + url.QueryEscape(to))
		default:
			return &rpcResponse{ID: req.ID, Error: &rpcError{Code: -32602, Message: "unknown tool: " + params.Name}}
		}
		return &rpcResponse{ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": out}},
		}}
	default:
		return &rpcResponse{ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

func fetchText(path string) string {
	resp, err := client.Get(baseURL + path)
	if err != nil {
		return "Error: mino dashboard unreachable (" + err.Error() + ") — is mino running?"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Sprintf("Error: mino %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return string(data)
}
