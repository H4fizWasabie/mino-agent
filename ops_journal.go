package main

import (
	"database/sql"
	"fmt"
)

// ops_journal — the operation journal (RUN-002, GitHub #216): the single
// undo backbone for every privileged/self-modifying operation (extension
// install, config edit, package change, unit write, binary swap). One table,
// four consumers: RUN-001/RUN-003 undo, RUN-004 binary rollback, RUN-005
// config self-heal. The journal only records; undo execution lands with the
// consumers in later tickets — this API must not assume their semantics.
//
// Storage decision (issue #216 comment, 2026-08-16): SQLite in state.db, not
// JSONL — a transaction wraps journal-write + state-change together, so a
// crash can never leave an op without an entry or an entry without an op.
// No FTS index: journal queries are structured lookups ("last op on entity
// X"), not free text.

// OpStatus values for ops_journal.status.
const (
	OpStatusOK         = "ok"
	OpStatusFailed     = "failed"
	OpStatusRolledBack = "rolled_back"
)

// OpEntry is one journaled operation.
//
// OpType is the operation kind — the "command" in before-state/command/
// after-state (e.g. "config.edit", "extension.install", "binary.swap").
// Entity is what was acted on (path, unit name, extension name).
// BeforeState/AfterState are JSON snapshots captured by the caller around
// the mutation. UndoOf links this op to the op it reverts (0 = not an undo);
// the rollback chain is resolved via Get in later tickets.
type OpEntry struct {
	ID          int64
	Ts          string
	OpType      string
	Entity      string
	BeforeState string
	AfterState  string
	Status      string
	SessionID   string
	UndoOf      int64
}

// OpJournal is the ops_journal store. Run is the only write path.
type OpJournal struct{ db *sql.DB }

func NewOpJournal(db *sql.DB) *OpJournal { return &OpJournal{db: db} }

// Run is the journal seam: it executes mutate and inserts entry in ONE
// transaction, so the op and its record commit together — no op without an
// entry, no entry without an op. The mutation runs first so it can set
// entry.AfterState (snapshot the post-op state, then return nil); the insert
// picks it up. If mutate errors, the whole transaction rolls back and no
// entry exists. A caller that must journal an op whose mutation already
// happened outside the transaction passes Status=OpStatusFailed and a
// no-op mutate.
func (j *OpJournal) Run(entry *OpEntry, mutate func(tx *sql.Tx) error) (int64, error) {
	if entry.OpType == "" || entry.Entity == "" {
		return 0, fmt.Errorf("op_type and entity are required")
	}
	if entry.Status == "" {
		entry.Status = OpStatusOK
	}
	tx, err := j.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if mutate != nil {
		if err := mutate(tx); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`INSERT INTO ops_journal (op_type, entity, before_state, after_state, status, session_id, undo_of)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.OpType, entry.Entity, entry.BeforeState, entry.AfterState, entry.Status, entry.SessionID, entry.UndoOf)
	if err != nil {
		return 0, err
	}
	entry.ID, err = res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return entry.ID, nil
}

// LastOp returns the most recent journaled op on entity — the lookup the
// journal exists for. Returns sql.ErrNoRows when the entity was never
// journaled.
func (j *OpJournal) LastOp(entity string) (*OpEntry, error) {
	return j.get(`SELECT id, ts, op_type, entity, before_state, after_state, status, session_id, undo_of
		FROM ops_journal WHERE entity = ? ORDER BY id DESC LIMIT 1`, entity)
}

// Get returns the op with the given id — how a rollback chain resolves
// undo_of links.
func (j *OpJournal) Get(id int64) (*OpEntry, error) {
	return j.get(`SELECT id, ts, op_type, entity, before_state, after_state, status, session_id, undo_of
		FROM ops_journal WHERE id = ?`, id)
}

// SetStatus transitions an entry's lifecycle status — the write path the
// journal's status vocabulary exists for. Rollback consumers (RUN-001/003/
// 004/005) mark the entry they revert with OpStatusRolledBack; failed
// installs record OpStatusFailed at Run time.
func (j *OpJournal) SetStatus(id int64, status string) error {
	switch status {
	case OpStatusOK, OpStatusFailed, OpStatusRolledBack:
	default:
		return fmt.Errorf("unknown status %q", status)
	}
	res, err := j.db.Exec(`UPDATE ops_journal SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (j *OpJournal) get(query string, args ...any) (*OpEntry, error) {
	var e OpEntry
	err := j.db.QueryRow(query, args...).Scan(&e.ID, &e.Ts, &e.OpType, &e.Entity,
		&e.BeforeState, &e.AfterState, &e.Status, &e.SessionID, &e.UndoOf)
	if err != nil {
		return nil, err
	}
	return &e, nil
}
