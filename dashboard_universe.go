package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
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

	if err := appendUniverseRows(core, &snapshot); err != nil {
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

func appendUniverseRows(core *Core, snapshot *UniverseSnapshot) error {
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
	for rows.Next() {
		var path, sessionID, label, createdAt string
		var size int64
		if err := rows.Scan(&path, &sessionID, &label, &size, &createdAt); err != nil {
			rows.Close()
			return err
		}
		snapshot.Nodes = append(snapshot.Nodes, UniverseNode{ID: "artifact:" + path, Kind: "artifact", Label: label, Summary: fmt.Sprintf("%s · %d bytes", path, size), Source: sessionID, Region: "work", At: createdAt})
		snapshot.Counts.Artifacts++
	}
	if err := rows.Close(); err != nil {
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
