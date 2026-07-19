package main

// MCP bridge (Model Context Protocol, DECISIONS.md §8).
// Pure Go — loads server configs from ~/.mino/mcp.d/, connects via stdio,
// discovers tools, and registers them prefixed as MCP_<server>_<tool>.

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
	"github.com/mark3labs/mcp-go/mcp"
)

type mcpServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

type mcpActive struct {
	cfg    mcpServerConfig
	client *client.Client
}

// MCPBridge loads server configs from mcp.d/ on boot and connects them.
// Each server's tools are registered on the ToolRegistry so the agent
// and dashboard treat them like any other tool.
type MCPBridge struct {
	dir      string
	registry *Registry
	servers  map[string]*mcpActive
	mu       sync.Mutex
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
		if json.Unmarshal(data, &cfg) != nil || cfg.Command == "" {
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

	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}

	c, err := client.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
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
	}
	slog.Info("mcp tools registered", "server", cfg.Name, "tools", count)
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

func (b *MCPBridge) Close() {
	for _, s := range b.servers {
		s.client.Close()
	}
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
		if json.Unmarshal(data, &cfg) != nil || cfg.Command == "" {
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

// Servers returns the list of configured MCP server names.
func (b *MCPBridge) Servers() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.servers))
	for n := range b.servers {
		names = append(names, n)
	}
	return names
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
