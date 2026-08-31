package main

import (
	"strings"
	"sync"
	"time"
)

// playbook_nav.go — chat-navigation run pointer (issues #450/#451/#452) and
// the run-scoped read tracker it enables (#453).
//
// run_playbook no longer drives a stage's LLM turn itself for chat-triggered
// or scheduled runs (navigatePlaybookRun in playbook_workspace.go); Mino acts
// on the workspace with its own read_file/write_file calls across many
// ordinary turns instead of one dedicated stage loop. Something still has to
// remember which run a session is currently authorized to touch between
// those calls — the old stageCtx-injected traceTagKey did this per attempt,
// but there is no per-attempt wrapper left to inject it. This is that seam:
// a lightweight per-session pointer, the same sync.Mutex-guarded map pattern
// run_registry.go already uses for run cancellation.

var (
	navMu  sync.Mutex
	navPtr = map[string]playbookNavPointer{}
)

type playbookNavPointer struct {
	Playbook string
	RunID    string
}

// setSessionNav records the run a session is currently navigating.
func setSessionNav(sessionID, playbook, runID string) {
	navMu.Lock()
	navPtr[sessionID] = playbookNavPointer{Playbook: playbook, RunID: runID}
	navMu.Unlock()
}

// clearSessionNav drops a session's navigation pointer once its run finishes
// (complete, failed, or interrupted) so a stale pointer never authorizes
// writes into a run that is no longer in progress. It also drops that run's
// read tracker (#453) — a finished run has nothing left to avoid re-reading.
func clearSessionNav(sessionID string) {
	navMu.Lock()
	p, ok := navPtr[sessionID]
	delete(navPtr, sessionID)
	navMu.Unlock()
	if ok {
		clearNavReads(p.RunID)
	}
}

// sessionNav reports the run a session is currently authorized to touch, if any.
func sessionNav(sessionID string) (playbookNavPointer, bool) {
	navMu.Lock()
	defer navMu.Unlock()
	p, ok := navPtr[sessionID]
	return p, ok
}

// navReads tracks, per run, which paths have already been read and when
// (#453: token-efficiency discipline for workspace navigation). A playbook
// navigation spans many turns — unlike the per-turn knownArtifactsKey — so
// nothing else remembers this across the run's whole lifetime. Read-only
// nudge, never enforcement: read_file always returns real content, this
// only lets the harness tell the model when a path is unchanged since its
// last read this run.
var (
	navReadsMu sync.Mutex
	navReads   = map[string]map[string]navRead{} // runID -> path -> last read
)

type navRead struct {
	At    time.Time
	ModAt time.Time
}

// noteNavRead records path as read now for runID and returns the previous
// read, if any, so the caller can compare it against the file's current mtime.
func noteNavRead(runID, path string, modAt time.Time) (navRead, bool) {
	navReadsMu.Lock()
	defer navReadsMu.Unlock()
	paths, ok := navReads[runID]
	if !ok {
		paths = map[string]navRead{}
		navReads[runID] = paths
	}
	prev, seen := paths[path]
	paths[path] = navRead{At: time.Now(), ModAt: modAt}
	return prev, seen
}

func clearNavReads(runID string) {
	navReadsMu.Lock()
	delete(navReads, runID)
	navReadsMu.Unlock()
}

// scheduledSessionPrefix is the deterministic session ID prefix fireSchedule
// (playbook.go) uses for every scheduled fire: "scheduled-" + playbook name.
const scheduledSessionPrefix = "scheduled-"

// navigationPlaybookForTurn reports the playbook name a turn is already
// known, at its start, to be navigating (#477 ICM-scoped context
// restoration) — a scheduled fire's entire purpose is always exactly one
// playbook (derivable from its deterministic session ID, no need to wait for
// a run_playbook call to find out), and a chat turn continuing a run an
// earlier message already started is exactly what sessionNav tracks. A chat
// turn that will only decide to call run_playbook partway through its own
// tool-calling loop isn't covered — the system prompt is fixed for the whole
// turn (cache stability), so there's nothing to detect yet at this point.
func navigationPlaybookForTurn(source, sessionID string) (string, bool) {
	if source == "schedule" {
		if name := strings.TrimPrefix(sessionID, scheduledSessionPrefix); name != "" && name != sessionID {
			return name, true
		}
	}
	if p, navigating := sessionNav(sessionID); navigating {
		return p.Playbook, true
	}
	return "", false
}

// activeStageToolNames predicts the declared tools of the stage a navigating
// turn will reach, so they can be force-included in this turn's tool schema
// selection before the turn starts (#481) — the same guarantee #449's
// additive-tools design intended stage declarations to have, which was lost
// when #450/#451/#452 replaced the per-stage loop that used to wire
// stageToolNamesKey (loop.go) per attempt. There is no per-stage loop left to
// set it fresh each time, so this predicts it once, at the point the whole
// turn's schema selection is fixed.
//
// For a chat turn continuing an already-active navigation, sessionNav names
// the exact run, so this is exact, not a guess. For a scheduled fire's first
// call (no sessionNav yet), it is best-effort: the newest run's current
// stage, or the playbook's first stage if no run exists yet. A wrong guess
// is harmless — the mechanism is additive, so at worst a declared tool isn't
// force-included this one call and falls back to today's uncertain
// sliding-window selection, exactly the status quo this is improving on.
func activeStageToolNames(home, source, sessionID string) []string {
	var playbook, runID string
	if p, navigating := sessionNav(sessionID); navigating {
		playbook, runID = p.Playbook, p.RunID
	} else if source == "schedule" {
		playbook = strings.TrimPrefix(sessionID, scheduledSessionPrefix)
	}
	if playbook == "" {
		return nil
	}
	pb, err := loadPlaybookWorkspace(home, playbook)
	if err != nil || len(pb.Stages) == 0 {
		return nil
	}
	var run *PlaybookRun
	if runID != "" {
		run, _ = loadPlaybookRunByID(pb, runID)
	} else {
		run, _ = latestPlaybookRun(pb)
	}
	if run == nil {
		return pb.Stages[0].Tools
	}
	state := nextPlaybookStage(run)
	if state == nil {
		return nil
	}
	stage, ok := workspaceStageFor(pb, state.Number, state.Name)
	if !ok {
		return nil
	}
	return stage.Tools
}
