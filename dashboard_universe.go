package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UniverseSnapshot is the compact, read-only contract for the Living Field.
// Durable state is represented as nodes; runtime work stays ephemeral in Activity.
type UniverseSnapshot struct {
	GeneratedAt     string             `json:"generated_at"`
	Timezone        string             `json:"timezone"`
	NeedsOnboarding bool               `json:"needs_onboarding"`
	Nodes           []UniverseNode     `json:"nodes"`
	Edges           []UniverseEdge     `json:"edges"`
	History         []UniverseHistory  `json:"history"`
	Activity        []UniverseActivity `json:"activity"`
	Counts          UniverseCounts     `json:"counts"`
}

type UniverseCounts struct {
	Memories         int `json:"memories"`
	Semantic         int `json:"semantic"`
	Episodic         int `json:"episodic"`
	Relationships    int `json:"relationships"`
	Responsibilities int `json:"responsibilities"`
	Playbooks        int `json:"playbooks"`
	Schedules        int `json:"schedules"`
	Reminders        int `json:"reminders"`
	Conversations    int `json:"conversations"`
	Artifacts        int `json:"artifacts"`
	Files            int `json:"files"`
	Skills           int `json:"skills"`
	Tools            int `json:"tools"`
}

type UniverseNode struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Summary        string `json:"summary,omitempty"`
	Source         string `json:"source,omitempty"`
	State          string `json:"state,omitempty"`
	Region         string `json:"region"`
	At             string `json:"at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	Community      int    `json:"community"`
	CommunityLabel string `json:"community_label,omitempty"`
	Connections    int    `json:"connections,omitempty"`
	Attention      bool   `json:"attention,omitempty"`
}

type UniverseEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	Kind     string `json:"kind,omitempty"`
}

type UniverseHistory struct {
	ID       string `json:"id"`
	EntityID string `json:"entity_id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	State    string `json:"state,omitempty"`
	At       string `json:"at"`
}

type UniverseActivity struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Tool      string `json:"tool,omitempty"`
	StartedAt string `json:"started_at"`
}

// UniverseProjection is the bounded Galaxy transport. /api/universe remains
// unchanged for existing callers; Galaxy loads this additive read-only view.
type UniverseProjection struct {
	UniverseSnapshot
	Scope       string              `json:"scope"`
	Revision    string              `json:"revision"`
	HasMore     bool                `json:"has_more"`
	Communities []UniverseCommunity `json:"communities,omitempty"`
}

type UniverseCommunity struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Count     int    `json:"count"`
	Attention int    `json:"attention"`
}

var universeOverviewCache struct {
	sync.Mutex
	core     *Core
	expires  time.Time
	snapshot UniverseSnapshot
}

func handleUniverseAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if dashCore == nil || dashCore.Settings == nil || dashCore.DB == nil {
		http.Error(w, "dashboard unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot, err := buildUniverseSnapshot(dashCore, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(snapshot)
}

func handleUniverseProjectionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if dashCore == nil || dashCore.Settings == nil || dashCore.DB == nil {
		http.Error(w, "dashboard unavailable", http.StatusServiceUnavailable)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "overview"
	}
	level, _ := strconv.Atoi(r.URL.Query().Get("level"))
	projection, ok, err := universeProjection(dashCore, scope, r.URL.Query().Get("id"), level, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "projection not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projection)
}

func universeProjection(core *Core, scope, id string, level int, now time.Time) (UniverseProjection, bool, error) {
	if scope != "overview" {
		snapshot, err := buildUniverseSnapshot(core, now)
		if err != nil {
			return UniverseProjection{}, false, err
		}
		projection, ok := projectUniverseSnapshot(snapshot, scope, id)
		projection.Activity = universeProjectionActivity(core, 64)
		return projection, ok, nil
	}

	universeOverviewCache.Lock()
	defer universeOverviewCache.Unlock()
	if universeOverviewCache.core == core && now.Before(universeOverviewCache.expires) {
		projection, ok := projectUniverseSnapshotAtLevel(universeOverviewCache.snapshot, scope, id, level)
		if !ok {
			return UniverseProjection{}, false, nil
		}
		projection.GeneratedAt = now.Format(time.RFC3339)
		projection.Activity = universeProjectionActivity(core, 64)
		return projection, ok, nil
	}
	snapshot, err := buildUniverseSnapshot(core, now)
	if err != nil {
		return UniverseProjection{}, false, err
	}
	projection, ok := projectUniverseSnapshotAtLevel(snapshot, scope, id, level)
	if ok {
		projection.Activity = universeProjectionActivity(core, 64)
		universeOverviewCache.core = core
		universeOverviewCache.expires = now.Add(30 * time.Second)
		universeOverviewCache.snapshot = snapshot
	}
	return projection, ok, nil
}

func universeProjectionActivity(core *Core, budget int) []UniverseActivity {
	activity := make([]UniverseActivity, 0, min(budget, 8))
	core.snapshots.Range(func(key, value any) bool {
		sessionID, ok := key.(string)
		snapshot, valid := value.(*LoopSnapshot)
		if ok && valid {
			activity = append(activity, UniverseActivity{SessionID: sessionID, Status: snapshot.Status, Tool: snapshot.CurrentTool, StartedAt: snapshot.StartedAt.UTC().Format(time.RFC3339)})
		}
		return len(activity) < budget
	})
	sort.Slice(activity, func(i, j int) bool { return activity[i].SessionID < activity[j].SessionID })
	return activity
}

func projectUniverseSnapshot(snapshot UniverseSnapshot, scope, id string) (UniverseProjection, bool) {
	return projectUniverseSnapshotAtLevel(snapshot, scope, id, 0)
}

func projectUniverseSnapshotAtLevel(snapshot UniverseSnapshot, scope, id string, level int) (UniverseProjection, bool) {
	if scope == "" {
		scope = "overview"
	}
	projection := UniverseProjection{UniverseSnapshot: snapshot, Scope: scope, Revision: universeRevision(snapshot)}
	switch scope {
	case "overview":
		communities := universeCommunities(snapshot.Nodes)
		projection.Nodes = universeOverviewNodes(snapshot.Nodes, communities, universeOverviewBudget(len(snapshot.Nodes), level))
		projection.HasMore = len(projection.Nodes) < len(snapshot.Nodes)
		projection.Communities = communities[:min(256, len(communities))]
		projection.HasMore = projection.HasMore || len(projection.Communities) < len(communities)
	case "community":
		for _, node := range snapshot.Nodes {
			if universeCommunityID(node) == id {
				projection.Nodes = append(projection.Nodes, node)
				if len(projection.Nodes) == 240 {
					break
				}
			}
		}
		if len(projection.Nodes) == 0 {
			return UniverseProjection{}, false
		}
		projection.HasMore = len(projection.Nodes) < universeCommunityCount(snapshot.Nodes, id)
	case "entity":
		projection.Nodes = universeNeighborhood(snapshot.Nodes, snapshot.Edges, id, 241)
		if len(projection.Nodes) == 0 {
			return UniverseProjection{}, false
		}
		projection.HasMore = len(projection.Nodes) > 240
		projection.Nodes = projection.Nodes[:min(240, len(projection.Nodes))]
	default:
		return UniverseProjection{}, false
	}
	projection.Edges = universeProjectionEdges(snapshot.Edges, projection.Nodes, id, 1200)
	projection.History = universeProjectionHistory(snapshot.History, projection.Nodes, 500)
	return projection, true
}

func universeOverviewBudget(total, level int) int {
	level = max(0, min(level, 2))
	base := 5_000
	if total <= 2_000 {
		base = 420
	}
	return min(total, min(15_000, base*(level+1)))
}

func universeRevision(snapshot UniverseSnapshot) string {
	hash := sha256.New()
	for _, node := range snapshot.Nodes {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%d\x00%t\x00",
			node.ID, node.Kind, node.Label, node.Summary, node.Source, node.State, node.Region, node.At, node.UpdatedAt,
			node.Community, node.CommunityLabel, node.Connections, node.Attention)
	}
	for _, edge := range snapshot.Edges {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\x00", edge.Source, edge.Target, edge.Relation, edge.Kind)
	}
	for _, event := range snapshot.History {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00", event.ID, event.EntityID, event.Kind, event.Label, event.State, event.At)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

func universeCommunityID(node UniverseNode) string {
	if node.Kind == "memory" {
		return fmt.Sprintf("memory:%d", node.Community)
	}
	if node.Kind == "tool" {
		return "tools"
	}
	if node.Kind == "skill" || node.Region == "system" {
		return "system"
	}
	if node.Kind == "playbook" || node.Kind == "schedule" || node.Kind == "reminder" || node.Region == "routines" {
		return "routines"
	}
	return "work"
}

func universeCommunities(nodes []UniverseNode) []UniverseCommunity {
	byID := map[string]*UniverseCommunity{}
	for _, node := range nodes {
		id := universeCommunityID(node)
		community := byID[id]
		if community == nil {
			label := strings.TrimPrefix(id, "memory:")
			if node.CommunityLabel != "" {
				label = node.CommunityLabel
			} else if id == "tools" || id == "system" || id == "routines" || id == "work" {
				label = strings.ToUpper(id[:1]) + id[1:]
			} else {
				label = "Memory " + label
			}
			community = &UniverseCommunity{ID: id, Label: label}
			byID[id] = community
		}
		community.Count++
		if node.Attention {
			community.Attention++
		}
	}
	communities := make([]UniverseCommunity, 0, len(byID))
	for _, community := range byID {
		communities = append(communities, *community)
	}
	sort.Slice(communities, func(i, j int) bool {
		if communities[i].Count != communities[j].Count {
			return communities[i].Count > communities[j].Count
		}
		return communities[i].ID < communities[j].ID
	})
	return communities
}

func universeOverviewNodes(nodes []UniverseNode, communities []UniverseCommunity, budget int) []UniverseNode {
	groups := map[string][]UniverseNode{}
	for _, node := range nodes {
		id := universeCommunityID(node)
		groups[id] = append(groups[id], node)
	}
	for id := range groups {
		sort.Slice(groups[id], func(i, j int) bool {
			if groups[id][i].Attention != groups[id][j].Attention {
				return groups[id][i].Attention
			}
			if groups[id][i].Connections != groups[id][j].Connections {
				return groups[id][i].Connections > groups[id][j].Connections
			}
			return groups[id][i].ID < groups[id][j].ID
		})
	}
	selected := make([]UniverseNode, 0, min(budget, len(nodes)))
	for round := 0; len(selected) < budget; round++ {
		added := false
		for _, community := range communities {
			group := groups[community.ID]
			if round < len(group) {
				selected = append(selected, group[round])
				added = true
				if len(selected) == budget {
					break
				}
			}
		}
		if !added {
			break
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected
}

func universeCommunityCount(nodes []UniverseNode, id string) int {
	count := 0
	for _, node := range nodes {
		if universeCommunityID(node) == id {
			count++
		}
	}
	return count
}

func universeNeighborhood(nodes []UniverseNode, edges []UniverseEdge, id string, budget int) []UniverseNode {
	byID := make(map[string]UniverseNode, len(nodes))
	adjacent := make(map[string][]string)
	for _, node := range nodes {
		byID[node.ID] = node
	}
	if _, ok := byID[id]; !ok {
		return nil
	}
	for _, edge := range edges {
		if _, sourceOK := byID[edge.Source]; !sourceOK {
			continue
		}
		if _, targetOK := byID[edge.Target]; !targetOK {
			continue
		}
		adjacent[edge.Source] = append(adjacent[edge.Source], edge.Target)
		adjacent[edge.Target] = append(adjacent[edge.Target], edge.Source)
	}
	queue, seen := []string{id}, map[string]bool{id: true}
	selected := make([]UniverseNode, 0, min(budget, len(nodes)))
	for len(queue) > 0 && len(selected) < budget {
		current := queue[0]
		queue = queue[1:]
		selected = append(selected, byID[current])
		sort.Strings(adjacent[current])
		for _, next := range adjacent[current] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return selected
}

func universeProjectionEdges(edges []UniverseEdge, nodes []UniverseNode, focus string, budget int) []UniverseEdge {
	ids := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		ids[node.ID] = true
	}
	selected := make([]UniverseEdge, 0, min(budget, len(edges)))
	for pass := 0; pass < 2 && len(selected) < budget; pass++ {
		for _, edge := range edges {
			incident := edge.Source == focus || edge.Target == focus
			if (pass == 0) != incident || !ids[edge.Source] || !ids[edge.Target] {
				continue
			}
			selected = append(selected, edge)
			if len(selected) == budget {
				break
			}
		}
	}
	return selected
}

func universeProjectionHistory(history []UniverseHistory, nodes []UniverseNode, budget int) []UniverseHistory {
	ids := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		ids[node.ID] = true
	}
	selected := make([]UniverseHistory, 0)
	for _, event := range history {
		if ids[event.EntityID] {
			selected = append(selected, event)
		}
	}
	if len(selected) > budget {
		selected = selected[len(selected)-budget:]
	}
	return selected
}

func buildUniverseSnapshot(core *Core, now time.Time) (UniverseSnapshot, error) {
	snapshot := UniverseSnapshot{
		GeneratedAt: now.Format(time.RFC3339), Timezone: core.Settings.Timezone,
		NeedsOnboarding: needsOnboarding(core.Settings.Home),
		Nodes:           []UniverseNode{}, Edges: []UniverseEdge{}, History: []UniverseHistory{}, Activity: []UniverseActivity{},
	}
	responsibilityIDs := map[string]string{}
	playbookIDs := map[string]string{}

	if core.Memory != nil && core.Memory.graph != nil {
		graph := core.Memory.graph
		communities := graph.Communities()
		graph.mu.RLock()
		labels := make(map[string]string, len(graph.labels))
		for id, label := range graph.labels {
			labels[id] = label
		}
		graph.mu.RUnlock()
		for _, fact := range graph.Facts() {
			community := communities[fact.ID]
			node := UniverseNode{
				ID: "memory:" + fact.ID, Kind: "memory", Label: fact.Subject,
				Summary: truncate(strings.TrimSpace(fact.Body), 240), Source: fact.Source,
				State: fact.Type, Region: "memory", Community: community,
				CommunityLabel: labels[fmt.Sprintf("%d", community)], Connections: len(fact.Edges),
			}
			if !fact.At.IsZero() {
				node.At = fact.At.UTC().Format(time.RFC3339)
			}
			snapshot.Nodes = append(snapshot.Nodes, node)
			snapshot.Counts.Memories++
			if fact.Type == "episodic" {
				snapshot.Counts.Episodic++
			} else {
				snapshot.Counts.Semantic++
			}
			for _, edge := range fact.Edges {
				snapshot.Edges = append(snapshot.Edges, UniverseEdge{
					Source: node.ID, Target: "memory:" + edge.Target, Relation: edge.Rel, Kind: edge.Kind,
				})
				snapshot.Counts.Relationships++
			}
		}
	}

	if core.Responsibilities != nil {
		items, err := core.Responsibilities.List(ResponsibilityFilter{})
		if err != nil {
			return snapshot, err
		}
		for _, item := range items {
			id := "responsibility:" + item.ID
			responsibilityIDs[item.ID] = id
			responsibilityIDs[item.SourceRef] = id
			node := UniverseNode{
				ID: id, Kind: "responsibility", Label: item.Title, Summary: item.Outcome,
				Source: item.SourceKind, State: item.Status, Region: responsibilityRegion(item.Kind),
				At: item.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
				Attention: item.Status == "needs_you" || item.Status == "blocked" || item.Status == "working",
			}
			snapshot.Nodes = append(snapshot.Nodes, node)
			snapshot.Counts.Responsibilities++
			history, err := core.Responsibilities.History(item.ID)
			if err != nil {
				return snapshot, err
			}
			for index, event := range history {
				snapshot.History = append(snapshot.History, UniverseHistory{
					ID: fmt.Sprintf("responsibility-event:%s:%d", item.ID, index), EntityID: id,
					Kind: "responsibility", Label: event.Summary, State: event.Status,
					At: event.At.UTC().Format(time.RFC3339),
				})
			}
		}
	}

	for _, playbook := range playbookCatalog(core.Settings.Home) {
		name := universeString(playbook["name"])
		if name == "" {
			continue
		}
		id := "playbook:" + name
		playbookIDs[name] = id
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{
			ID: id, Kind: "playbook", Label: name, Summary: universeString(playbook["description"]),
			State: universeString(playbook["status"]), Region: "routines",
		})
		snapshot.Counts.Playbooks++
	}

	schedules, err := loadSchedules(core.Settings.Home)
	if err != nil {
		return snapshot, err
	}
	for index, schedule := range schedules {
		id := fmt.Sprintf("schedule:%d:%s", index, schedule.Name)
		state := "scheduled"
		if schedule.LastError != "" || schedule.MissedAt != "" {
			state = "attention"
		}
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{
			ID: id, Kind: "schedule", Label: schedule.Name, Summary: schedule.Time + " " + schedule.Timezone,
			State: state, Region: "routines", At: schedule.LastRun, Attention: state == "attention",
		})
		snapshot.Counts.Schedules++
		if target := responsibilityIDs["routine:"+schedule.Name]; target != "" {
			snapshot.Edges = append(snapshot.Edges, UniverseEdge{Source: id, Target: target, Relation: "triggers", Kind: "structural"})
		} else if target := responsibilityIDs[schedule.Name]; target != "" {
			snapshot.Edges = append(snapshot.Edges, UniverseEdge{Source: id, Target: target, Relation: "triggers", Kind: "structural"})
		}
		if target := playbookIDs[schedule.Name]; target != "" {
			snapshot.Edges = append(snapshot.Edges, UniverseEdge{Source: id, Target: target, Relation: "runs", Kind: "structural"})
		}
	}

	if err := appendUniverseRows(core, &snapshot, playbookIDs); err != nil {
		return snapshot, err
	}

	for _, skill := range skillCatalog(core.Settings.Home) {
		name := universeString(skill["name"])
		if name == "" {
			continue
		}
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{ID: "skill:" + name, Kind: "skill", Label: name, Summary: universeString(skill["description"]), Region: "system"})
		snapshot.Counts.Skills++
	}
	if core.Tools != nil {
		for _, tool := range core.Tools.Catalog() {
			snapshot.Nodes = append(snapshot.Nodes, UniverseNode{ID: "tool:" + tool.Name, Kind: "tool", Label: tool.Name, Summary: truncate(tool.Description, 180), Source: tool.Source, Region: "system"})
			snapshot.Counts.Tools++
		}
	}
	core.snapshots.Range(func(key, value any) bool {
		sessionID, ok := key.(string)
		snap, valid := value.(*LoopSnapshot)
		if ok && valid {
			snapshot.Activity = append(snapshot.Activity, UniverseActivity{SessionID: sessionID, Status: snap.Status, Tool: snap.CurrentTool, StartedAt: snap.StartedAt.UTC().Format(time.RFC3339)})
		}
		return true
	})

	sort.Slice(snapshot.Nodes, func(i, j int) bool { return snapshot.Nodes[i].ID < snapshot.Nodes[j].ID })
	sort.Slice(snapshot.Edges, func(i, j int) bool {
		return snapshot.Edges[i].Source+snapshot.Edges[i].Target < snapshot.Edges[j].Source+snapshot.Edges[j].Target
	})
	sort.Slice(snapshot.History, func(i, j int) bool { return snapshot.History[i].At < snapshot.History[j].At })
	sort.Slice(snapshot.Activity, func(i, j int) bool { return snapshot.Activity[i].SessionID < snapshot.Activity[j].SessionID })
	return snapshot, nil
}

func appendUniverseRows(core *Core, snapshot *UniverseSnapshot, playbookIDs map[string]string) error {
	rows, err := core.DB.Query(`SELECT id, message, remind_at, status, created_at FROM reminders ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var message, remindAt, status, createdAt string
		if err := rows.Scan(&id, &message, &remindAt, &status, &createdAt); err != nil {
			rows.Close()
			return err
		}
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{ID: fmt.Sprintf("reminder:%d", id), Kind: "reminder", Label: message, State: status, Region: "now", At: createdAt, UpdatedAt: remindAt, Attention: status == "pending"})
		snapshot.Counts.Reminders++
	}
	if err := rows.Close(); err != nil {
		return err
	}

	rows, err = core.DB.Query(`SELECT path, session_id, label, size, created_at FROM session_artifacts ORDER BY path`)
	if err != nil {
		return err
	}
	artifactPaths := map[string]bool{}
	for rows.Next() {
		var path, sessionID, label, createdAt string
		var size int64
		if err := rows.Scan(&path, &sessionID, &label, &size, &createdAt); err != nil {
			rows.Close()
			return err
		}
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{ID: "artifact:" + path, Kind: "artifact", Label: label, Summary: fmt.Sprintf("%s · %d bytes", path, size), Source: sessionID, Region: "work", At: createdAt})
		artifactPaths[universeFilePath(core.Settings.Home, path)] = true
		snapshot.Counts.Artifacts++
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := appendUniverseFiles(core.Settings.Home, snapshot, artifactPaths, playbookIDs); err != nil {
		return err
	}

	for _, session := range sessionList(core.DB) {
		id := universeString(session["id"])
		if id == "" {
			continue
		}
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{ID: "conversation:" + id, Kind: "conversation", Label: universeString(session["title"]), Summary: universeString(session["last"]), Region: "conversations", UpdatedAt: universeString(session["last_at"])})
		snapshot.Counts.Conversations++
	}
	return nil
}

func universeFilePath(home, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(home, path)
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

func appendUniverseFiles(home string, snapshot *UniverseSnapshot, artifacts map[string]bool, playbookIDs map[string]string) error {
	type root struct{ path, source, region string }
	roots := []root{
		{filepath.Join(home, "results"), "results", "work"},
		{filepath.Join(home, "outbox"), "outbox", "work"},
		{filepath.Join(home, "traces"), "traces", "system"},
		{filepath.Join(home, "playbooks"), "playbooks", "work"},
	}
	appendFile := func(path, source, region string, info fs.FileInfo) {
		path = universeFilePath(home, path)
		if artifacts[path] || !info.Mode().IsRegular() {
			return
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			rel = path
		}
		node := UniverseNode{ID: "file:" + path, Kind: "file", Label: filepath.Base(path), Summary: fmt.Sprintf("%s · %d bytes", rel, info.Size()), Source: source, State: strings.TrimPrefix(filepath.Ext(path), "."), Region: region, At: info.ModTime().UTC().Format(time.RFC3339)}
		snapshot.Nodes = append(snapshot.Nodes, node)
		snapshot.Counts.Files++
		if source == "playbooks" {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) > 1 && playbookIDs[parts[1]] != "" {
				snapshot.Edges = append(snapshot.Edges, UniverseEdge{Source: node.ID, Target: playbookIDs[parts[1]], Relation: "belongs to", Kind: "structural"})
			}
		}
	}
	for _, root := range roots {
		if _, err := os.Stat(root.path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err := filepath.WalkDir(root.path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err == nil {
				appendFile(path, root.source, root.region, info)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, name := range []string{"calendar.ics", "usage.jsonl"} {
		path := filepath.Join(home, name)
		if info, err := os.Stat(path); err == nil {
			appendFile(path, "runtime", "system", info)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func responsibilityRegion(kind string) string {
	if kind == "routine" || kind == "system" {
		return "routines"
	}
	return "work"
}

func universeString(value any) string {
	text, _ := value.(string)
	return text
}
