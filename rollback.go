package main

// rollback.go — binary self-rollback (RUN-004, GitHub #218): `mino update`
// keeps the running binary at exe+".prev" before the swap, then health-checks
// the new binary against a staged copy of the live state (the stage-smoke
// shape: boot against a copy, not the live state). If the new binary does not
// boot and answer /api/universe, the swap is reverted with the same atomic
// rename the updater uses, the deployments.log gets a rollback line, and the
// swap's journal entry is marked rolled_back (the RUN-001/002 status seam's
// intended consumer). The owner call (`mino rollback`) uses the same revert.
//
// Journal discipline follows host_tools.go: the mutation happens first, then
// journal.Run commits the record; on journal failure the op is torn back
// down. The swap itself is a filesystem rename, not a DB mutation, so the
// entry is recorded with mutate=nil — the before/after states carry the
// evidence.
//
// Detection vocabulary (ticket): boot failure (candidate exits before
// readiness), health-check failure (no /api/universe answer within the
// window), owner call (mino rollback).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// updateHealthTimeout is the readiness window for the post-update health
// check (stage-smoke waits 45s; a binary boot on the VPS takes seconds).
// Package var like updateClient, so tests can shorten it.
var updateHealthTimeout = 30 * time.Second

// updateBinaryPath resolves the binary this updater manages. MINO_UPDATE_BINARY
// is a test seam (env-driven, same family as MINO_HOME/MINO_SERVICE).
func updateBinaryPath() string {
	if p := os.Getenv("MINO_UPDATE_BINARY"); p != "" {
		return p
	}
	return currentExe()
}

// binaryState is the before/after payload of a binary.swap journal entry.
type binaryState struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

func binaryStateJSON(path, version, sum string) string {
	b, _ := json.Marshal(binaryState{Path: path, Version: version, SHA256: sum})
	return string(b)
}

func binaryStateFromJSON(s string) *binaryState {
	var st binaryState
	if err := json.Unmarshal([]byte(s), &st); err != nil {
		return nil
	}
	return &st
}

// applyUpdate swaps the verified new binary in, journals the swap, and
// health-checks it — reverting on journal failure or a bad boot. DoUpdate's
// download/verify half ends here; everything after the verified download is
// this function so the swap/revert decision logic is testable end to end.
func applyUpdate(exe, home, tag, sum, newPath string) error {
	oldSum, err := sha256File(exe)
	if err != nil {
		return fmt.Errorf("checksum current binary: %w", err)
	}

	// Keep the running binary BEFORE the swap — the revert source.
	if err := keepPreviousBinary(exe); err != nil {
		return err
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("replace binary: %w — try running with sudo", err)
	}

	// Journal the swap (RUN-002 discipline): mutation first, then the record;
	// a journal failure tears the swap back down.
	j, jerr := openJournal(home)
	if jerr != nil {
		rerr := revertBinary(exe)
		if rerr != nil {
			return fmt.Errorf("journal open: %v AND revert failed: %v — manual intervention required", jerr, rerr)
		}
		return fmt.Errorf("journal open: %v — swap undone", jerr)
	}
	opID, jerr := j.Run(&OpEntry{
		OpType:      "binary.swap",
		Entity:      exe,
		BeforeState: binaryStateJSON(exe, Version, oldSum),
		AfterState:  binaryStateJSON(exe, tag, sum),
	}, nil)
	if jerr != nil {
		rerr := revertBinary(exe)
		if rerr != nil {
			return fmt.Errorf("journal swap: %v AND revert failed: %v — manual intervention required", jerr, rerr)
		}
		return fmt.Errorf("journal swap: %v — swap undone", jerr)
	}

	recordDeployment(home, "update", tag, sum, exe)

	if err := verifyNewBinary(exe, home, updateHealthTimeout); err != nil {
		rerr := revertBinary(exe)
		if rerr != nil {
			return fmt.Errorf("health check failed (%v) AND revert failed: %v — manual intervention required", err, rerr)
		}
		recordDeployment(home, "rollback", tag, oldSum, exe)
		if serr := j.SetStatus(opID, OpStatusRolledBack); serr != nil {
			slog.Error("mark swap rolled_back failed", "op", opID, "error", serr)
		}
		return fmt.Errorf("new binary failed health check (%v) — reverted to the previous binary; run 'mino rollback' to redo", err)
	}

	fmt.Printf("Updated to %s (verified %s, health check passed). Restart Mino to use the new version.\n", tag, sum)
	return nil
}

// DoRollback is the owner-call detection path (RUN-004): restores exe.prev
// over exe with the same atomic rename, records the ledger line, and marks
// the last binary.swap op rolled_back. systemd stays the only thing that
// applies it — the running process keeps its inode until restart.
func DoRollback() error {
	home := homeDir()
	exe := updateBinaryPath()
	prev := exe + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary at %s — nothing to roll back to", prev)
	}
	sum, err := sha256File(prev)
	if err != nil {
		return fmt.Errorf("checksum previous binary: %w", err)
	}
	if err := revertBinary(exe); err != nil {
		return err
	}

	tag := "unknown"
	j, err := openJournal(home)
	if err != nil {
		slog.Warn("rollback journal unavailable — ledger line still written", "error", err)
	} else {
		if op, err := j.LastOp(exe); err == nil && op.OpType == "binary.swap" {
			if st := binaryStateFromJSON(op.AfterState); st != nil && st.Version != "" {
				tag = st.Version
			}
			if err := j.SetStatus(op.ID, OpStatusRolledBack); err != nil {
				slog.Error("mark swap rolled_back failed", "op", op.ID, "error", err)
			}
		} else {
			slog.Warn("no binary.swap journal entry for " + exe)
		}
	}
	recordDeployment(home, "rollback", tag, sum, exe)
	fmt.Printf("Rolled back to the previous binary (sha256 %s). Restart Mino to use it.\n", sum)
	return nil
}

// --- revert machinery ---

// keepPreviousBinary preserves the running binary at exe+".prev" so a bad
// update can be reverted. The previous .prev is overwritten — the running
// binary is the true "previous" at every swap.
func keepPreviousBinary(exe string) error {
	data, err := os.ReadFile(exe)
	if err != nil {
		return fmt.Errorf("read current binary: %w", err)
	}
	if err := os.WriteFile(exe+".prev", data, 0755); err != nil {
		return fmt.Errorf("preserve previous binary: %w", err)
	}
	return nil
}

// revertBinary restores exe.prev over exe with the same atomic rename the
// updater and the emergency lane use (docs/emergency-deploy.md: .new + mv —
// renaming over a running binary is safe on Linux; ETXTBSY only hits open()).
func revertBinary(exe string) error {
	if _, err := os.Stat(exe + ".prev"); err != nil {
		return fmt.Errorf("no previous binary at %s.prev — nothing to revert to", exe)
	}
	if err := os.Rename(exe+".prev", exe); err != nil {
		return fmt.Errorf("restore previous binary: %w", err)
	}
	return nil
}

// --- health check ---

// verifyNewBinary boots the candidate against a staged copy of the live
// state and requires /api/universe to answer before timeout. The candidate
// is a black box: it runs the real boot path (no self-check flag — a flag
// would be a second boot path that could drift from production), and the
// readiness decision lives here, not in the candidate.
func verifyNewBinary(exe, home string, timeout time.Duration) error {
	stage, err := os.MkdirTemp("", "mino-update-health-")
	if err != nil {
		return fmt.Errorf("stage dir: %w", err)
	}
	defer os.RemoveAll(stage)

	if err := stageState(home, stage); err != nil {
		return fmt.Errorf("stage state copy: %w", err)
	}

	port, err := freePort()
	if err != nil {
		return fmt.Errorf("pick health-check port: %w", err)
	}

	cmd := exec.Command(exe)
	cmd.Env = healthCheckEnv(stage, port)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start candidate: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	url := fmt.Sprintf("http://127.0.0.1:%d/api/universe", port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case werr := <-done:
			// Boot failure: the candidate died before ever answering.
			return fmt.Errorf("candidate exited before readiness (%v): %s", werr, tail(out.String()))
		case <-tick.C:
		}
		if time.Now().After(deadline) {
			stopCandidate(cmd, done)
			return fmt.Errorf("candidate did not answer %s within %s: %s", url, timeout, tail(out.String()))
		}
		resp, err := client.Get(url)
		if err != nil {
			continue // not ready yet
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			stopCandidate(cmd, done)
			return nil
		}
	}
}

// stageState copies home into stage with stage-smoke's side-effect safety:
// traces / audit.jsonl / outbox / *.bak* excluded, schedules.json removed so
// the staged instance cannot fire playbooks, TELEGRAM_BOT_TOKEN stripped so
// it can never poll the live agent's Telegram. RUN-004 additions: extensions
// excluded — the staged boot validates the mino binary itself, and must not
// spawn duplicate extension processes.
func stageState(home, stage string) error {
	if err := os.MkdirAll(stage, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if skipStaged(name) {
			continue
		}
		src := filepath.Join(home, name)
		dst := filepath.Join(stage, name)
		if e.IsDir() {
			if err := copyTree(src, dst); err != nil {
				return err
			}
		} else {
			info, ierr := e.Info()
			if ierr != nil {
				return ierr
			}
			if err := copyFile(src, dst, info.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	os.Remove(filepath.Join(stage, "schedules.json"))
	stripToken(filepath.Join(stage, "mino.env"))
	return nil
}

func skipStaged(name string) bool {
	switch name {
	case "traces", "audit.jsonl", "outbox", "extensions", "extensions.json":
		return true
	}
	return strings.Contains(name, ".bak")
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	cerr := out.Close()
	if err != nil {
		return err
	}
	return cerr
}

// stripToken removes the TELEGRAM_BOT_TOKEN line from a staged mino.env so
// the health-check instance cannot poll the live agent's updates.
func stripToken(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // no env file — nothing to strip
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "TELEGRAM_BOT_TOKEN=") {
			continue
		}
		kept = append(kept, line)
	}
	os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0600)
}

// healthCheckEnv is the staged instance's environment: the real env minus
// TELEGRAM_BOT_TOKEN (belt-and-braces over the stripped mino.env — a
// systemd-started updater could otherwise inherit the token), plus the stage
// overrides.
func healthCheckEnv(stage string, port int) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TELEGRAM_BOT_TOKEN=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "MINO_HOME="+stage, fmt.Sprintf("MINO_DASHBOARD_PORT=%d", port))
}

// stopCandidate ends the staged instance the way stage-smoke does (kill the
// PID): SIGTERM with a short grace before SIGKILL, so a hung boot still gets
// cleaned up.
func stopCandidate(cmd *exec.Cmd, done <-chan error) {
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

// tail returns the last 400 chars of the candidate's output for error
// reporting — enough to name the failure without flooding the message.
func tail(s string) string {
	if len(s) > 400 {
		return "…" + s[len(s)-400:]
	}
	return s
}

// openJournal opens state.db through the normal Connect path but reports the
// panic as an error — `mino update` / `mino rollback` are standalone CLI
// commands where a panic would be an unhandled failure.
func openJournal(home string) (j *OpJournal, err error) {
	defer func() {
		if r := recover(); r != nil {
			j, err = nil, fmt.Errorf("%v", r)
		}
	}()
	return NewOpJournal(Connect(home)), nil
}

// recordDeployment appends one who/what/when line to deployments.log. The
// ledger is code-generated (REL-05) so it cannot rot; rollback lines use the
// same shape with action=rollback.
func recordDeployment(home, action, tag, sum, exe string) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, "deployments.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s=%s sha256=%s binary=%s\n", time.Now().UTC().Format(time.RFC3339), action, tag, sum, exe)
}
