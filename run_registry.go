package main

import (
	"context"
	"sync"
	"time"
)

// run_registry.go — live playbook run registry (issue #310).
//
// Every in-flight playbook run registers a cancel func here. Two
// consumers:
//   - the cancel_run harness tool: the owner (or any session) cancels a run
//     by id; the stage loop sees ctx.Done() and marks the run interrupted
//     cleanly instead of leaving it "running" forever.
//   - the shutdown hook (CancelAllRuns on daemon stop): a timer bounce / mino
//     update / manual restart turns from a run-killer into a clean
//     interrupter — bounded wait, then exit.

var (
	runRegMu     sync.Mutex
	runRegistry  = map[string]context.CancelFunc{}
	runShutdown  = make(chan struct{}) // closed once, at daemon shutdown
	runShutdownO sync.Once
)

// registerRun tracks a live run. Returns a deregister func.
func registerRun(id string, cancel context.CancelFunc) func() {
	runRegMu.Lock()
	runRegistry[id] = cancel
	runRegMu.Unlock()
	return func() {
		runRegMu.Lock()
		delete(runRegistry, id)
		runRegMu.Unlock()
	}
}

// cancelRun cancels one live run by id. Returns false when no such run is
// registered (or it already finished).
func cancelRun(id string) bool {
	runRegMu.Lock()
	cancel, ok := runRegistry[id]
	runRegMu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// setRunInterruptReason records the cancellation reason before cancelRun so
// the runner picks it up at its next boundary check (#310). Runs without a
// reason default to "cancelled".
var runInterruptReasons = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func setRunInterruptReason(id, reason string) {
	runInterruptReasons.Lock()
	runInterruptReasons.m[id] = reason
	runInterruptReasons.Unlock()
}

func takeRunInterruptReason(id string) string {
	runInterruptReasons.Lock()
	defer runInterruptReasons.Unlock()
	r := runInterruptReasons.m[id]
	delete(runInterruptReasons.m, id)
	return r
}

// cancelAllRuns cancels every live run and waits up to shutdownGrace for the
// runners to persist their interrupted state. Called once at daemon shutdown.
func cancelAllRuns(grace time.Duration) {
	runRegMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(runRegistry))
	for _, c := range runRegistry {
		cancels = append(cancels, c)
	}
	runRegMu.Unlock()
	runShutdownO.Do(func() { close(runShutdown) })
	for _, c := range cancels {
		c()
	}
	// Wait for deregistration (runners exit their loop and unregister).
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		runRegMu.Lock()
		n := len(runRegistry)
		runRegMu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
