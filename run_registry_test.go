package main

import (
	"context"
	"testing"
	"time"
)

func TestRegisterAndCancelRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dereg := registerRun("run-1", cancel)
	defer dereg()

	if !cancelRun("run-1") {
		t.Fatal("cancelRun on a live run returned false")
	}
	if ctx.Err() == nil {
		t.Fatal("run context not cancelled")
	}
	// Once deregistered, cancelRun reports no live run.
	dereg()
	if cancelRun("run-1") {
		t.Fatal("cancelRun on a deregistered run returned true")
	}
}

func TestCancelAllRunsReturns(t *testing.T) {
	for i := 0; i < 3; i++ {
		_, cancel := context.WithCancel(context.Background())
		id := "bulk-" + string(rune('a'+i))
		registerRun(id, cancel)
	}
	done := make(chan struct{})
	go func() {
		cancelAllRuns(2 * time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelAllRuns did not return within grace")
	}
}

func TestStageContractHashChangesWithContract(t *testing.T) {
	pb := &PlaybookWorkspace{Stages: []WorkspaceStage{
		{Number: 1, Name: "judgment", Tools: []string{"search_web", "write_file"}},
	}}
	run := &PlaybookRun{Stages: []PlaybookRunStage{{Number: 1, Name: "judgment"}}}
	h1 := stageContractHash(pb, run)
	pb.Stages[0].Tools = []string{"search_web"}
	h2 := stageContractHash(pb, run)
	if h1 == h2 {
		t.Fatal("contract hash did not change when tools changed")
	}
	if h1 == "" || h2 == "" {
		t.Fatal("empty hash")
	}
	// A run referencing a stage that no longer exists on disk must hash
	// differently (the franken class: run references 1-news-report, disk has
	// 1-judgment).
	pb2 := &PlaybookWorkspace{Stages: []WorkspaceStage{{Number: 1, Name: "news-report"}}}
	run2 := &PlaybookRun{Stages: []PlaybookRunStage{{Number: 1, Name: "judgment"}}}
	if stageContractHash(pb2, run2) == stageContractHash(pb2, &PlaybookRun{Stages: []PlaybookRunStage{{Number: 1, Name: "news-report"}}}) {
		t.Fatal("hash must differ when the run references a missing stage")
	}
}
