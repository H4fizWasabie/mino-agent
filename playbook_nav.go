package main

import "sync"

// playbook_nav.go — chat-navigation run pointer (issues #450/#451).
//
// run_playbook no longer drives a stage's LLM turn itself for chat-triggered
// runs (navigatePlaybookRun in playbook_workspace.go); Mino acts on the
// workspace with its own read_file/write_file calls across many ordinary
// turns instead of one dedicated stage loop. Something still has to
// remember which run a session is currently authorized to touch between
// those calls — the old stageCtx-injected traceTagKey did this per attempt,
// but there is no per-attempt wrapper left to inject it. This is that seam:
// a lightweight per-session pointer, the same sync.Mutex-guarded map pattern
// run_registry.go already uses for run cancellation.
//
// The scheduler's dedicated stage loop (runWorkspacePlaybook, still used by
// schedule_playbook until #452) keeps setting traceTagKey per attempt as
// before and never touches this map.

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
// writes into a run that is no longer in progress.
func clearSessionNav(sessionID string) {
	navMu.Lock()
	delete(navPtr, sessionID)
	navMu.Unlock()
}

// sessionNav reports the run a session is currently authorized to touch, if any.
func sessionNav(sessionID string) (playbookNavPointer, bool) {
	navMu.Lock()
	defer navMu.Unlock()
	p, ok := navPtr[sessionID]
	return p, ok
}
