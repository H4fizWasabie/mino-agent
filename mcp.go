package main

// MCP bridge (Model Context Protocol, docs/decisions.md §8).
// Pure Go — loads server configs from ~/.mino/mcp.d/, connects via stdio
// or HTTP (SSE/StreamableHTTP), discovers tools, and registers them
// prefixed as MCP_<server>_<tool>.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type mcpServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"` // stdio transport
	Args    []string          `json:"args"`    // stdio transport
	Env     map[string]string `json:"env"`     // stdio transport
	URL     string            `json:"url"`     // HTTP transport (SSE/streamable)
	Headers map[string]string `json:"headers"` // HTTP transport headers (e.g. x-api-key)
}

type mcpActive struct {
	cfg    mcpServerConfig
	client *client.Client
}

// MCPBridge loads server configs from mcp.d/ on boot and connects them.
// Each server's tools are registered on the ToolRegistry so the agent
// and dashboard treat them like any other tool.
type MCPBridge struct {
	dir       string
	registry  *Registry
	servers   map[string]*mcpActive
	mu        sync.Mutex
	closeOnce sync.Once
}

func NewMCPBridge(home string, registry *Registry) *MCPBridge {
	return &MCPBridge{
		dir:      filepath.Join(home, "mcp.d"),
		registry: registry,
		servers:  map[string]*mcpActive{},
	}
}

// Start loads every config in mcp.d/, connects each server, and registers its
// tools. Servers that don't start are skipped with a warning.
func (b *MCPBridge) Start() {
	b.mu.Lock()
	defer b.mu.Unlock()

	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.dir, e.Name()))
		if err != nil {
			continue
		}
		var cfg mcpServerConfig
		if json.Unmarshal(data, &cfg) != nil {
			continue
		}
		if cfg.Command == "" && cfg.URL == "" {
			continue
		}
		if cfg.Name == "" {
			cfg.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		b.connect(cfg)
	}
}

func (b *MCPBridge) connect(cfg mcpServerConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var c *client.Client
	var err error

	if cfg.URL != "" {
		// HTTP transport (StreamableHTTP, which falls back to SSE)
		var opts []transport.StreamableHTTPCOption
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(cfg.Headers))
		}
		c, err = client.NewStreamableHttpClient(cfg.URL, opts...)
	} else {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		c, err = client.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
	}
	if err != nil {
		slog.Warn("mcp connect failed", "server", cfg.Name, "error", err)
		return
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mino", Version: "1.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		slog.Warn("mcp init failed", "server", cfg.Name, "error", err)
		c.Close()
		return
	}

	toolsResp, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		slog.Warn("mcp list tools failed", "server", cfg.Name, "error", err)
		c.Close()
		return
	}

	count := 0
	for _, t := range toolsResp.Tools {
		fullName := fmt.Sprintf("MCP_%s_%s", cfg.Name, t.Name)
		if _, ok := b.registry.tools[fullName]; ok {
			continue // already registered (e.g. reload)
		}
		// capture for closure
		serverName := cfg.Name
		toolName := t.Name
		active := &mcpActive{cfg: cfg, client: c}
		b.registry.Register(&Tool{
			Name:        fullName,
			Description: fmt.Sprintf("[MCP:%s] %s", cfg.Name, t.Description),
			Schema:      toolSchema(t.InputSchema),
			Fn: func(args map[string]any) string {
				return b.call(serverName, toolName, args)
			},
		})
		b.servers[cfg.Name] = active
		count++
		// Nested-executor tools (a tools[] array carrying tool_slug + arguments,
		// e.g. Composio's COMPOSIO_MULTI_EXECUTE_TOOL) force the model to build
		// nested argument objects, which some models emit empty (observed with
		// gpt-5.6-luna: correct slug, arguments:{} every time). Register a flat
		// companion that takes arguments as a JSON string — a shape every model
		// produces reliably — and re-wraps into the nested form on call.
		if schemaHasNestedExecutor(t.InputSchema) {
			flatName := fullName + "_FLAT"
			if _, ok := b.registry.tools[flatName]; !ok {
				b.registry.Register(&Tool{
					Name:        flatName,
					Description: fmt.Sprintf("[MCP:%s flat] %s — call with tool_slug plus arguments as a JSON string", cfg.Name, t.Name),
					Schema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tool_slug":      map[string]any{"type": "string", "description": "The inner tool slug, e.g. INSTAGRAM_POST_IG_USER_MEDIA"},
							"arguments_json": map[string]any{"type": "string", "description": "JSON string of the inner tool's arguments, e.g. {\"caption\":\"...\",\"image_url\":\"...\"}"},
						},
						"required": []string{"tool_slug", "arguments_json"},
					},
					Fn: func(args map[string]any) string {
						slug, _ := args["tool_slug"].(string)
						raw, _ := args["arguments_json"].(string)
						wrapped, err := wrapExecutorArgs(slug, raw)
						if err != nil {
							return fmt.Sprintf("Error: %v", err)
						}
						return b.call(serverName, toolName, wrapped)
					},
				})
			}
		}
	}
	slog.Info("mcp tools registered", "server", cfg.Name, "tools", count)
}

// wrapExecutorArgs parses a flat arguments JSON string and re-wraps it into the
// nested executor shape (tools[] with tool_slug + arguments) that the MCP
// server expects. This is the flat-companion translation: the model supplies
// (tool_slug, arguments_json) and never constructs nested objects.
func wrapExecutorArgs(toolSlug, argumentsJSON string) (map[string]any, error) {
	var inner map[string]any
	if err := json.Unmarshal([]byte(argumentsJSON), &inner); err != nil {
		return nil, fmt.Errorf("arguments_json is not valid JSON: %v", err)
	}
	return map[string]any{
		"tools": []any{map[string]any{"tool_slug": toolSlug, "arguments": inner}},
	}, nil
}

// schemaHasNestedExecutor reports whether a tool schema carries the
// nested-executor shape: a "tools" array whose items have tool_slug + arguments.
func schemaHasNestedExecutor(schema mcp.ToolInputSchema) bool {
	raw, ok := schema.Properties["tools"]
	if !ok {
		return false
	}
	item := map[string]any{}
	if m, ok := raw.(map[string]any); ok {
		if items, ok := m["items"].(map[string]any); ok {
			item = items
		}
	}
	if itemProps, ok := item["properties"].(map[string]any); ok {
		_, hasSlug := itemProps["tool_slug"]
		_, hasArgs := itemProps["arguments"]
		return hasSlug && hasArgs
	}
	return false
}

func (b *MCPBridge) call(server, tool string, args map[string]any) string {
	b.mu.Lock()
	active := b.servers[server]
	b.mu.Unlock()
	if active == nil {
		return fmt.Sprintf("MCP server %q is not connected", server)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := active.client.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: tool, Arguments: args},
	})
	if err != nil {
		return fmt.Sprintf("MCP call %s_%s failed: %v", server, tool, err)
	}
	var out strings.Builder
	for _, block := range result.Content {
		if t, ok := block.(mcp.TextContent); ok {
			out.WriteString(t.Text)
		} else {
			out.WriteString(fmt.Sprintf("[%s]", block))
		}
	}
	return strings.TrimSpace(out.String())
}

// Close shuts down all MCP client connections. Idempotent — safe to call
// multiple times (Core.Close may run on already-closed bridges).
func (b *MCPBridge) Close() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, s := range b.servers {
			s.client.Close()
		}
	})
}

// Reload re-scans mcp.d/ for new server configs and connects them.
// Already-connected servers are skipped — only new configs are picked up.
func (b *MCPBridge) Reload() {
	b.mu.Lock()
	defer b.mu.Unlock()

	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return
	}
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.dir, e.Name()))
		if err != nil {
			continue
		}
		var cfg mcpServerConfig
		if json.Unmarshal(data, &cfg) != nil {
			continue
		}
		if cfg.Command == "" && cfg.URL == "" {
			continue
		}
		if cfg.Name == "" {
			cfg.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		if _, ok := b.servers[cfg.Name]; ok {
			continue // already connected
		}
		b.connect(cfg)
		count++
	}
	if count > 0 {
		slog.Info("mcp reload added servers", "count", count)
	}
}

func toolSchema(schema mcp.ToolInputSchema) map[string]any {
	// mcp-go's ToolInputSchema wraps Properties + Required as raw maps.
	if schema.Properties == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := map[string]any{
		"type":       "object",
		"properties": schema.Properties,
	}
	if len(schema.Required) > 0 {
		out["required"] = schema.Required
	}
	return out
}
