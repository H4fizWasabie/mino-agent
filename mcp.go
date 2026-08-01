package main

// MCP bridge (Model Context Protocol, DECISIONS.md §8).
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
		// connect must run WITHOUT b.mu held: registerFlattenedTools performs
		// live MCP calls (via direct client) and registering tools takes the
		// registry lock. Holding b.mu here deadlocked startup.
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
	var flatTools []flatToolDef
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
		// Wrapper tools (executor + slugs, e.g. Composio's MULTI_EXECUTE)
		// expose inner tools only as nested args. Many models fail to build
		// that nesting (observed with gpt-5.6-luna emitting arguments:{}).
		// Collect them for flat re-exposure below.
		if schemaHasToolsArray(t.InputSchema) {
			flatTools = append(flatTools, flatToolDef{server: cfg.Name, wrapper: toolName, wrapperDesc: t.Description})
		}
	}
	b.registerFlattenedTools(cfg.Name, c, flatTools)
	slog.Info("mcp tools registered", "server", cfg.Name, "tools", count)
}

// flatToolDef records a wrapper-shaped MCP tool whose inner tools (slugs) can
// be re-exposed with flat top-level args — the shape every model handles.
type flatToolDef struct {
	server     string
	wrapper    string
	wrapperDesc string
}

// schemaHasToolsArray reports whether the tool schema contains a "tools" array
// whose items carry tool_slug + arguments — the nested-executor pattern used by
// Composio's COMPOSIO_MULTI_EXECUTE_TOOL and similar servers.
func schemaHasToolsArray(schema mcp.ToolInputSchema) bool {
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

// registerFlattenedTools re-exposes inner toolkit tools with flat schemas. It
// discovers connected toolkits from the server's own response
// (toolkit_connection_statuses) rather than any hardcoded list, then queries
// each connected toolkit to surface its schemas. Each inner tool is registered
// as MCP_<server>_<SLUG> and, when called, re-wrapped into the executor's
// nested args. One generic mechanism — no per-toolkit code, no hardcoded names.
func (b *MCPBridge) registerFlattenedTools(serverName string, c *client.Client, wrappers []flatToolDef) {
	if len(wrappers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Step 1: discover which toolkits are connected. The search response reports
	// toolkit_connection_statuses — the server's own list, not ours.
	discovery := mcpCallDirect(c, "COMPOSIO_SEARCH_TOOLS", map[string]any{
		"queries": []any{map[string]any{"use_case": "list available tools and toolkits"}},
	})
	var discResp struct {
		Data struct {
			ToolkitConnectionStatuses []struct {
				Toolkit            string `json:"toolkit"`
				HasActiveConnection bool   `json:"has_active_connection"`
			} `json:"toolkit_connection_statuses"`
		} `json:"data"`
	}
	var toolkits []string
	if json.Unmarshal([]byte(discovery), &discResp) == nil {
		for _, s := range discResp.Data.ToolkitConnectionStatuses {
			if s.Toolkit != "" && s.HasActiveConnection {
				toolkits = append(toolkits, s.Toolkit)
			}
		}
	}

	// Step 2: query each connected toolkit for its tool schemas.
	schemas := make(map[string]map[string]any)
	if len(toolkits) == 0 {
		// Fallback: the broad query may itself carry inline schemas.
		schemas = extractSearchSchemas(discovery)
	}
	for _, toolkit := range toolkits {
		out := mcpCallDirect(c, "COMPOSIO_SEARCH_TOOLS", map[string]any{
			"queries": []any{map[string]any{"use_case": "list " + toolkit + " tools"}},
		})
		for slug, s := range extractSearchSchemas(out) {
			schemas[slug] = s
		}
	}
	if len(schemas) == 0 {
		slog.Warn("mcp flatten: no tool schemas returned", "server", serverName)
		return
	}

	registered := 0
	for slug, s := range schemas {
		if strings.HasPrefix(strings.ToLower(descOf(s)), "deprecated") {
			continue // don't offer deprecated tools alongside their replacements
		}
		fullName := fmt.Sprintf("MCP_%s_%s", serverName, slug)
		if _, ok := b.registry.tools[fullName]; ok {
			continue
		}
		wrapper := wrappers[0].wrapper // all wrappers share the same executor shape
		innerSlug := slug
		inputSchema := map[string]any{"type": "object", "properties": map[string]any{}}
		if props, ok := s["input_schema"].(map[string]any); ok {
			inputSchema = props
		}
		b.registry.Register(&Tool{
			Name:        fullName,
			Description: fmt.Sprintf("[MCP:%s flattened] %s", serverName, descOf(s)),
			Schema:      inputSchema,
			Fn: func(args map[string]any) string {
				return b.call(serverName, wrapper, map[string]any{
					"tools": []any{map[string]any{
						"tool_slug": innerSlug,
						"arguments": args,
					}},
				})
			},
		})
		registered++
	}
	slog.Info("mcp flattened tools registered", "server", serverName, "tools", registered)
	_ = ctx
}

// extractSearchSchemas pulls inline tool_schemas out of a COMPOSIO_SEARCH_TOOLS
// response. Schemas may live at data.tool_schemas or inside each result.
func extractSearchSchemas(out string) map[string]map[string]any {
	var resp struct {
		Data struct {
			Results []struct {
				ToolSchemas map[string]map[string]any `json:"tool_schemas"`
			} `json:"results"`
			ToolSchemas map[string]map[string]any `json:"tool_schemas"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(out), &resp) != nil {
		return nil
	}
	schemas := make(map[string]map[string]any)
	for slug, s := range resp.Data.ToolSchemas {
		schemas[slug] = s
	}
	for _, r := range resp.Data.Results {
		for slug, s := range r.ToolSchemas {
			schemas[slug] = s
		}
	}
	return schemas
}

func descOf(s map[string]any) string {
	if d, ok := s["description"].(string); ok {
		return d
	}
	return ""
}

func (b *MCPBridge) call(server, tool string, args map[string]any) string {
	b.mu.Lock()
	active := b.servers[server]
	b.mu.Unlock()
	if active == nil {
		return fmt.Sprintf("MCP server %q is not connected", server)
	}
	return mcpCallDirect(active.client, tool, args)
}

// mcpCallDirect performs an MCP tool call on an already-connected client
// without touching the bridge mutex. Used both by b.call (runtime) and by
// registerFlattenedTools (startup, where b.mu must not be held).
func mcpCallDirect(c *client.Client, tool string, args map[string]any) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: tool, Arguments: args},
	})
	if err != nil {
		return fmt.Sprintf("MCP call %s failed: %v", tool, err)
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
		b.mu.Lock()
		_, connected := b.servers[cfg.Name]
		b.mu.Unlock()
		if connected {
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
