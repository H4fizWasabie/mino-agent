package main

// config_heal.go — config self-heal (RUN-005, GitHub #219): Mino backs up,
// validates, and reverts its own config on a bad edit. Today a bad SIGHUP
// reload only logs; this adds the revert path for the config set in
// docs/config.md (providers.json, mino.env, cost-watch.json) — the files
// the model can write itself (write_file) or that manual/cost-watch edits
// can break.
//
// Three triggers, one mechanism:
//   - write-time (write_file/edit_file): the new content is validated BEFORE
//     the write lands — an invalid model edit is refused and never mutates
//     disk; a valid edit is backed up (.prev) and journaled (config.edit).
//   - reload-time (SIGHUP): HealConfig validates the whole set before
//     ReloadProviders runs; a file that fails is restored from its .prev
//     backup and the last config.edit op on it is marked rolled_back.
//   - owner call: `mino config-rollback <name>` restores the .prev backup
//     of one config file (the RUN-004 `mino rollback` shape).
//
// Backup shape: sibling `<file>.prev` files, the RUN-004 exe.prev shape
// (the /etc fallback was already retired to `.bak`; .prev is the live
// revert source). Revert COPIES .prev back over the file and keeps it —
// deliberately not the RUN-004 rename, so a revert is idempotent and a
// re-revert stays possible (the RUN-004 "run rollback to redo" message
// landed on an already-consumed .prev; this shape cannot).
//
// Journal payload: before/after = {path, sha256} (hash + path, as the
// ticket allows) — the journal is the record, not the backup, and
// mino.env holds secrets that must not be duplicated into state.db.
//
// Validation per file (the ticket's "parse check + minimal sanity",
// deliberately not a heavyweight validator):
//   - providers.json: valid JSON, at least one provider, every provider has
//     a non-empty name — "the file must still yield at least one usable
//     provider (don't accept a config that bricks routing)". Same bar as
//     loadProviders' "invalid providers.json" plus the name guard.
//   - mino.env: every non-blank, non-comment line is KEY=VALUE with a
//     non-empty key — loadEnvFile's exact accept grammar. A line the loader
//     silently skips is a validation failure (silent degradation is the
//     failure class this ticket exists to catch).
//   - cost-watch.json: valid JSON object. Field-type semantics stay with
//     cost-watch's own loader, which silently falls back to defaults on any
//     unmarshal failure — mino catches the "not even JSON" class; the
//     extension owns its schema.
//
// Journal discipline follows host_tools.go / rollback.go: the mutation
// happens first, then OpJournal.Run records it; on journal failure the op
// is torn back down (the .prev restore), with loud logs when teardown
// fails.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// configSet is the self-healed config set (docs/config.md), in stable order
// for deterministic logs and tests.
var configSet = []string{"providers.json", "mino.env", "cost-watch.json"}

func isConfigFile(name string) bool {
	for _, n := range configSet {
		if n == name {
			return true
		}
	}
	return false
}

// ValidateConfig checks one config-set file's content. A nil error means the
// content is acceptable; anything else names the failure. The per-file bars
// are documented on the file header — parse check + minimal sanity only.
func ValidateConfig(name string, content []byte) error {
	switch name {
	case "providers.json":
		var file providerFile
		if err := json.Unmarshal(content, &file); err != nil {
			return fmt.Errorf("invalid JSON: %v", err)
		}
		if len(file.Providers) == 0 {
			return fmt.Errorf("no providers — routing would be bricked")
		}
		for _, p := range file.Providers {
			if strings.TrimSpace(p.Name) == "" {
				return fmt.Errorf("provider without a name")
			}
		}
		return nil
	case "mino.env":
		for i, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, _, ok := strings.Cut(line, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return fmt.Errorf("line %d is not KEY=VALUE: %q", i+1, line)
			}
		}
		return nil
	case "cost-watch.json":
		var obj map[string]any
		if err := json.Unmarshal(content, &obj); err != nil {
			return fmt.Errorf("invalid JSON: %v", err)
		}
		return nil
	}
	return nil
}

// configState is the before/after payload of a config.edit journal entry.
type configState struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func configStateJSON(path, sum string) string {
	b, _ := json.Marshal(configState{Path: path, SHA256: sum})
	return string(b)
}

func sha256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// prevPath is the .prev backup of a config file — the live revert source.
func prevPath(path string) string { return path + ".prev" }

// keepPrev refreshes the known-good baseline AFTER a file validated — a
// valid config becomes the next revert point. 0600 always: backups of
// config (mino.env holds secrets) have no reason to be world-readable.
func keepPrev(path string, content []byte) error {
	return os.WriteFile(prevPath(path), content, 0600)
}

// restorePrev copies the .prev backup back over the file and KEEPS it
// (idempotent revert, re-revert stays possible). Returns an error when no
// backup exists or the copy fails.
func restorePrev(path string) error {
	data, err := os.ReadFile(prevPath(path))
	if err != nil {
		return fmt.Errorf("no known-good backup at %s", prevPath(path))
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("restore %s from backup: %v", path, err)
	}
	return nil
}

// markRolledBack flags the last config.edit op on path as rolled_back —
// the RUN-001/002 status seam, carried forward from RUN-004. Failure to
// mark is logged loudly, never fatal: the file restore is the heal, the
// status is the record.
func markRolledBack(j *OpJournal, path string) {
	if j == nil {
		return
	}
	op, err := j.LastOp(path)
	if err != nil {
		slog.Warn("config self-heal: no config.edit journal entry for " + path)
		return
	}
	if err := j.SetStatus(op.ID, OpStatusRolledBack); err != nil {
		slog.Error("config self-heal: mark op rolled_back failed", "op", op.ID, "error", err)
	}
}

// HealConfig is the SIGHUP hook: validate the whole config set and revert
// any file that fails, BEFORE the caller reloads providers. A file that
// validates becomes the new known-good baseline (.prev); a file that fails
// is restored from its baseline, its last edit op is marked rolled_back,
// and the event is logged loudly — a bad edit must never be a quiet log
// line. Missing files are skipped (nothing to heal); a missing .prev on a
// bad file is logged as unrecoverable, not panicked on.
func HealConfig(home string, j *OpJournal) {
	for _, name := range configSet {
		path := filepath.Join(home, name)
		content, err := os.ReadFile(path)
		if err != nil {
			continue // file absent — nothing to heal
		}
		verr := ValidateConfig(name, content)
		if verr == nil {
			if kerr := keepPrev(path, content); kerr != nil {
				slog.Error("config self-heal: keep backup failed", "file", path, "error", kerr)
			}
			continue
		}
		slog.Error("config self-heal: bad config detected, reverting",
			"file", path, "reason", verr)
		if rerr := restorePrev(path); rerr != nil {
			slog.Error("config self-heal: REVERT FAILED — manual fix required", "file", path, "error", rerr)
			continue
		}
		markRolledBack(j, path)
		slog.Warn("config self-heal: reverted " + path + " from its known-good backup")
	}
}

// applyConfigEdit is the write-time guard for write_file/edit_file: it
// validates the final content, refuses invalid edits before they land,
// keeps the .prev backup, performs the write, and journals the config.edit
// op (hash + path). On journal failure the write is torn back down from
// .prev with a loud log — RUN-002 discipline, the mutation must not stand
// without its record. Returns handled=false for files outside the config
// set (the caller's plain write path handles them).
func applyConfigEdit(home, path string, final []byte, appendMode bool) (bool, error) {
	clean := filepath.Clean(path)
	if filepath.Dir(clean) != filepath.Clean(home) || !isConfigFile(filepath.Base(clean)) {
		return false, nil
	}
	name := filepath.Base(clean)

	old, err := os.ReadFile(clean)
	if err != nil && !os.IsNotExist(err) {
		return true, fmt.Errorf("read %s: %v", clean, err)
	}
	if appendMode {
		final = append(append([]byte{}, old...), final...)
	}

	if err := ValidateConfig(name, final); err != nil {
		// Refusal before the write — nothing mutated, nothing to undo.
		return true, fmt.Errorf("%s rejected: %v — file unchanged", name, err)
	}
	// The .prev backup exists only when a known-good content did (RUN-003's
	// write_unit pattern: teardown deletes what never existed before).
	hadPrev := old != nil
	if hadPrev {
		if err := keepPrev(clean, old); err != nil {
			return true, fmt.Errorf("backup %s: %v", clean, err)
		}
	}
	if err := os.WriteFile(clean, final, 0600); err != nil {
		return true, fmt.Errorf("write %s: %v", clean, err)
	}

	// Teardown: restore the known-good content (or remove a file that never
	// existed before) so the mutation never stands without its record.
	teardown := func() error {
		if hadPrev {
			return restorePrev(clean)
		}
		if err := os.Remove(clean); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	j, jerr := openJournal(home)
	if jerr != nil {
		if rerr := teardown(); rerr != nil {
			slog.Error("config edit: journal unavailable AND revert failed — manual fix required",
				"file", clean, "journal", jerr, "revert", rerr)
			return true, fmt.Errorf("journal unavailable (%v) AND revert failed (%v) — manual fix required", jerr, rerr)
		}
		slog.Error("config edit rolled back: journal unavailable", "file", clean, "error", jerr)
		return true, fmt.Errorf("config edit rolled back: journal unavailable (%v)", jerr)
	}
	_, jerr = j.Run(&OpEntry{
		OpType:      "config.edit",
		Entity:      clean,
		BeforeState: configStateJSON(clean, sha256Bytes(old)),
		AfterState:  configStateJSON(clean, sha256Bytes(final)),
	}, nil)
	if jerr != nil {
		if rerr := teardown(); rerr != nil {
			slog.Error("config edit: journal failed AND revert failed — manual fix required",
				"file", clean, "journal", jerr, "revert", rerr)
			return true, fmt.Errorf("journal failed (%v) AND revert failed (%v) — manual fix required", jerr, rerr)
		}
		slog.Error("config edit rolled back: journal failed", "file", clean, "error", jerr)
		return true, fmt.Errorf("config edit rolled back: journal failed (%v)", jerr)
	}
	slog.Info("config edit journaled", "file", clean, "bytes", len(final))
	return true, nil
}

// DoConfigRollback is the owner-call trigger (`mino config-rollback <name>`):
// restores the .prev backup of one config-set file (copy, backup kept), and
// marks the last config.edit op on it rolled_back. Mirrors DoRollback's
// shape; the same restore path as the SIGHUP heal.
func DoConfigRollback(name string) error {
	if !isConfigFile(name) {
		return fmt.Errorf("unknown config file %q — must be one of %s", name, strings.Join(configSet, ", "))
	}
	home := homeDir()
	path := filepath.Join(home, name)
	if _, err := os.Stat(prevPath(path)); err != nil {
		return fmt.Errorf("no backup at %s — nothing to roll back to", prevPath(path))
	}
	sum, err := sha256File(prevPath(path))
	if err != nil {
		return fmt.Errorf("checksum backup: %w", err)
	}
	if err := restorePrev(path); err != nil {
		return err
	}
	j, err := openJournal(home)
	if err != nil {
		slog.Warn("config rollback journal unavailable — file still restored", "error", err)
	} else {
		markRolledBack(j, path)
	}
	fmt.Printf("Restored %s from backup (sha256 %s).\n", path, sum)
	return nil
}
