package main

import (
	"database/sql"
	"errors"
	"testing"
)

func newTestOpJournal(t *testing.T) *OpJournal {
	t.Helper()
	return NewOpJournal(Connect(t.TempDir()))
}

// The atomicity contract (RUN-002 core requirement): op and entry commit
// together — the state mutation and the journal insert share one transaction.
func TestOpJournalRunCommitsEntryAndMutation(t *testing.T) {
	j := newTestOpJournal(t)
	entry := &OpEntry{
		OpType:      "config.edit",
		Entity:      "providers.json",
		BeforeState: `{"main":"luna"}`,
		SessionID:   "test-session",
	}
	id, err := j.Run(entry, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO session_notes (session_id, note) VALUES (?, ?)", "s1", "note"); err != nil {
			return err
		}
		entry.AfterState = `{"main":"qwen"}`
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 || entry.ID != id {
		t.Fatalf("Run id = %d, entry.ID = %d", id, entry.ID)
	}
	got, err := j.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.OpType != "config.edit" || got.Entity != "providers.json" ||
		got.BeforeState != `{"main":"luna"}` || got.AfterState != `{"main":"qwen"}` ||
		got.SessionID != "test-session" || got.Status != "ok" {
		t.Fatalf("entry round-trip mismatch: %+v", got)
	}
	if got.Ts == "" {
		t.Fatal("ts not set")
	}
	// The state mutation committed in the same transaction as the entry.
	var n int
	if err := j.db.QueryRow("SELECT COUNT(*) FROM session_notes WHERE session_id = 's1'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("mutation did not commit with entry: n=%d err=%v", n, err)
	}
}

// A failed mutation rolls back the entry too: no entry without an op.
func TestOpJournalRunRollsBackOnMutationError(t *testing.T) {
	j := newTestOpJournal(t)
	_, err := j.Run(&OpEntry{OpType: "config.edit", Entity: "mino.env"}, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO session_notes (session_id, note) VALUES (?, ?)", "s1", "note"); err != nil {
			return err
		}
		return errors.New("apply failed")
	})
	if err == nil {
		t.Fatal("expected mutation error")
	}
	var n int
	if err := j.db.QueryRow("SELECT COUNT(*) FROM ops_journal").Scan(&n); err != nil || n != 0 {
		t.Fatalf("journal not empty after failed mutation: n=%d err=%v", n, err)
	}
	// And the partial mutation is gone with it.
	if err := j.db.QueryRow("SELECT COUNT(*) FROM session_notes WHERE session_id = 's1'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("mutation survived rollback: n=%d err=%v", n, err)
	}
}

// "Last op on entity X" — the structured lookup the journal exists for.
func TestOpJournalLastOpIsNewestPerEntity(t *testing.T) {
	j := newTestOpJournal(t)
	for i := 0; i < 3; i++ {
		if _, err := j.Run(&OpEntry{OpType: "binary.swap", Entity: "/usr/local/bin/mino", BeforeState: "v1"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := j.Run(&OpEntry{OpType: "config.edit", Entity: "mino.env"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := j.LastOp("/usr/local/bin/mino")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 3 {
		t.Fatalf("LastOp id = %d, want 3 (newest)", got.ID)
	}
	if _, err := j.LastOp("never-touched"); err != sql.ErrNoRows {
		t.Fatalf("LastOp on untouched entity = %v, want sql.ErrNoRows", err)
	}
}

// The rollback chain column: an undo op records undo_of, Get resolves it.
func TestOpJournalUndoChain(t *testing.T) {
	j := newTestOpJournal(t)
	orig, err := j.Run(&OpEntry{OpType: "config.edit", Entity: "providers.json", BeforeState: `{"main":"luna"}`, AfterState: `{"main":"qwen"}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	undo, err := j.Run(&OpEntry{
		OpType:      "config.edit",
		Entity:      "providers.json",
		BeforeState: `{"main":"qwen"}`,
		AfterState:  `{"main":"luna"}`,
		Status:      OpStatusRolledBack,
		UndoOf:      orig,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := j.Get(undo)
	if err != nil {
		t.Fatal(err)
	}
	if got.UndoOf != orig || got.Status != OpStatusRolledBack {
		t.Fatalf("undo chain broken: %+v (orig=%d)", got, orig)
	}
	// The reverted op still resolves to its original state.
	origGot, err := j.Get(orig)
	if err != nil {
		t.Fatal(err)
	}
	if origGot.AfterState != `{"main":"qwen"}` {
		t.Fatalf("original op after_state lost: %+v", origGot)
	}
}

// Validation: an entry without op_type or entity never reaches the table.
func TestOpJournalRunValidates(t *testing.T) {
	j := newTestOpJournal(t)
	for _, e := range []*OpEntry{{Entity: "x"}, {OpType: "y"}, {}} {
		if _, err := j.Run(e, nil); err == nil {
			t.Fatalf("expected validation error for %+v", e)
		}
	}
	var n int
	if err := j.db.QueryRow("SELECT COUNT(*) FROM ops_journal").Scan(&n); err != nil || n != 0 {
		t.Fatalf("validation failure wrote a row: n=%d err=%v", n, err)
	}
}

// SetStatus is the status-transition seam the journal vocabulary exists for
// (carry-forward from RUN-002 review, picked up by RUN-001): rollback
// consumers mark the entry they revert as rolled_back.
func TestOpJournalSetStatus(t *testing.T) {
	j := newTestOpJournal(t)
	id, err := j.Run(&OpEntry{OpType: "extension.install", Entity: "threads"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.SetStatus(id, OpStatusRolledBack); err != nil {
		t.Fatal(err)
	}
	got, err := j.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OpStatusRolledBack {
		t.Fatalf("status = %q, want %q", got.Status, OpStatusRolledBack)
	}
	if err := j.SetStatus(id, "bogus"); err == nil {
		t.Fatal("SetStatus must reject unknown status vocabulary")
	}
	if err := j.SetStatus(999999, OpStatusRolledBack); err != sql.ErrNoRows {
		t.Fatalf("SetStatus on missing id = %v, want sql.ErrNoRows", err)
	}
}
