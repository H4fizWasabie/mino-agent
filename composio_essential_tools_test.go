package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Tests for #481: composio's two dispatcher tools are essentials (never
// compete for a sliding-window slot), and a navigating turn's tool schema
// selection is force-fed the active stage's declared tools before the turn
// starts — restoring the guarantee #449's additive-tools design intended,
// lost when #450/#451/#452 removed the per-stage loop that used to wire it.

func TestComposioToolsAreEssential(t *testing.T) {
	if !floorToolNames["MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL"] {
		t.Fatal("expected the composio executor tool to be essential")
	}
	if !floorToolNames["MCP_composio_COMPOSIO_GET_TOOL_SCHEMAS"] {
		t.Fatal("expected the composio schema-lookup tool to be essential")
	}
}

func TestSchemasForContextAlwaysIncludesComposioEssentials(t *testing.T) {
	db := Connect(t.TempDir())
	defer db.Close()
	r := NewRegistry()
	r.SetSearchDB(db)
	for _, name := range floorNamesSorted {
		r.Register(&Tool{Name: name, Description: "core capability", Schema: map[string]any{"type": "object"}})
	}
	// A turn with context that has nothing to do with composio or Instagram —
	// the old sliding-window-only selection would have no reason to include
	// either composio tool.
	got := r.SchemasForContext("unrelated-session", "what's the weather like", "what's the weather like")
	names := make(map[string]bool, len(got))
	for _, schema := range got {
		names[schema.Name] = true
	}
	if !names["MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL"] || !names["MCP_composio_COMPOSIO_GET_TOOL_SCHEMAS"] {
		t.Fatalf("expected both composio tools present regardless of turn relevance, got %v", names)
	}
}

func TestActiveStageToolNamesExactForChatContinuation(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "ig", "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL")
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	pb, err := loadPlaybookWorkspace(home, "ig")
	if err != nil {
		t.Fatal(err)
	}
	run, err := loadOrCreatePlaybookRun(pb, registry, "go", "tg:1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	setSessionNav("tg:1", "ig", run.ID)
	defer clearSessionNav("tg:1")

	got := activeStageToolNames(home, "telegram", "tg:1")
	found := false
	for _, n := range got {
		if n == "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the active run's stage tools, got %v", got)
	}
}

func TestActiveStageToolNamesBestEffortForFreshScheduledFire(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "ig", "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL")

	// No run exists yet — this is a brand-new scheduled fire's first call.
	got := activeStageToolNames(home, "schedule", "scheduled-ig")
	found := false
	for _, n := range got {
		if n == "MCP_composio_COMPOSIO_MULTI_EXECUTE_TOOL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stage 1's declared tools as the best-effort guess, got %v", got)
	}
}

func TestActiveStageToolNamesNilOutsideNavigation(t *testing.T) {
	home := t.TempDir()
	if got := activeStageToolNames(home, "telegram", "tg:no-nav"); len(got) != 0 {
		t.Fatalf("expected no prediction outside navigation, got %v", got)
	}
}

// TestNavigatingTurnForceIncludesDeclaredStageTool uses a deliberately
// non-essential tool name (not composio) so this test exercises the #481
// stage-tools wiring specifically, independent of the composio-essentials
// fix — the two fixes must each hold on their own.
func TestNavigatingTurnForceIncludesDeclaredStageTool(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceStageTool(t, home, "ig", "stage_only_capability")
	var bodies []string
	core := capturingProviderCore(t, home, &bodies)
	db := Connect(t.TempDir())
	defer db.Close()
	core.Tools.SetSearchDB(db)
	for _, name := range floorNamesSorted {
		core.Tools.Register(&Tool{Name: name, Description: "core capability", Schema: map[string]any{"type": "object"}})
	}
	core.Tools.Register(&Tool{Name: "stage_only_capability", Description: "capability useful only for the active stage", Schema: map[string]any{"type": "object"}})

	core.RespondForContext(context.Background(), "scheduled-ig", "go", "schedule", nil, false)
	body := bodies[len(bodies)-1]
	if !strings.Contains(body, "stage_only_capability") {
		t.Fatalf("expected the declared stage tool force-included in the request payload, got %s", body)
	}
}
