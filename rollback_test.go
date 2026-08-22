package main

// rollback_test.go — RUN-004 (GitHub #218): binary self-rollback.
//
// The health check is tested against the real boundary: the candidate is
// spawned as a black box and must boot the REAL production binary (built
// here, like ext_supervisor_test builds fakeext) and answer /api/universe —
// or fail honestly (exit-1 script = boot failure, sleep = timeout). The
// applyUpdate tests exercise the actual revert decision logic with a failing
// boot, not a self-consistent round-trip (the RUN-003 review lesson).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildRealMino compiles the actual production binary so the health check
// tests the real boot path. t.TempDir() handles cleanup; the go build cache
// makes the second build cheap.
func buildRealMino(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mino")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mino: %v\n%s", err, out)
	}
	return bin
}

// testHome makes a home with a real state.db (what Connect creates at boot).
func testHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	Connect(home)
	return home
}

// writeExec writes an executable file with the given content.
func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

// The real binary boots against a staged copy of a real home and answers
// /api/universe — the happy path, through the real boot code.
func TestVerifyNewBinaryHealthy(t *testing.T) {
	bin := buildRealMino(t)
	home := testHome(t)

	if err := verifyNewBinary(bin, home, 30*time.Second); err != nil {
		t.Fatalf("verifyNewBinary: %v", err)
	}
}

// Boot failure: the candidate dies before ever answering — the decision
// logic must report it, not hang or pass.
func TestVerifyNewBinaryBootFailure(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "badbin")
	writeExec(t, bad, "#!/bin/sh\nexit 1\n")
	home := testHome(t)

	err := verifyNewBinary(bad, home, 30*time.Second)
	if err == nil {
		t.Fatal("verifyNewBinary = nil, want boot-failure error")
	}
	if !strings.Contains(err.Error(), "exited before readiness") {
		t.Fatalf("error = %q, want exited-before-readiness", err)
	}
}

// The candidate hangs: no /api/universe answer within the window — timeout
// branch, with a short window so the test stays fast.
func TestVerifyNewBinaryTimeout(t *testing.T) {
	hang := filepath.Join(t.TempDir(), "hangbin")
	writeExec(t, hang, "#!/bin/sh\nsleep 60\n")
	home := testHome(t)

	err := verifyNewBinary(hang, home, time.Second)
	if err == nil {
		t.Fatal("verifyNewBinary = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("error = %q, want did-not-answer", err)
	}
}

// The full update path: swap, journal entry, ledger line — happy path with
// the real binary as the incoming release.
func TestApplyUpdateHappyPathSwapsJournalsAndPassesHealthCheck(t *testing.T) {
	bin := buildRealMino(t)
	home := testHome(t)
	sum, err := sha256File(bin)
	if err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "OLD-BINARY")
	oldSum, _ := sha256File(exe)

	updateHealthTimeout = 30 * time.Second
	if err := applyUpdate(exe, home, "v9.9.9", sum, bin); err != nil {
		t.Fatalf("applyUpdate: %v", err)
	}

	got, _ := os.ReadFile(exe)
	if string(got) == "OLD-BINARY" {
		t.Fatal("exe was not swapped")
	}
	prev, err := os.ReadFile(exe + ".prev")
	if err != nil || string(prev) != "OLD-BINARY" {
		t.Fatalf("exe.prev = %q, %v — want the previous binary kept", prev, err)
	}

	op, err := NewOpJournal(Connect(home)).LastOp(exe)
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if op.OpType != "binary.swap" || op.Status != OpStatusOK {
		t.Fatalf("op = %+v, want binary.swap/ok", op)
	}
	if !strings.Contains(op.BeforeState, oldSum) || !strings.Contains(op.AfterState, sum) {
		t.Fatalf("states lack shas: before=%q after=%q", op.BeforeState, op.AfterState)
	}

	ledger, _ := os.ReadFile(filepath.Join(home, "deployments.log"))
	if !strings.Contains(string(ledger), "update=v9.9.9") {
		t.Fatalf("ledger missing update line: %s", ledger)
	}
}

// The RUN-003-review lesson test: a failing boot must drive the REAL revert
// decision — swap back, ledger rollback line, journal marked rolled_back.
func TestApplyUpdateHealthFailureRevertsAndMarksRolledBack(t *testing.T) {
	home := testHome(t)
	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "OLD-BINARY")
	oldSum, _ := sha256File(exe)

	bad := filepath.Join(t.TempDir(), "bad-release")
	writeExec(t, bad, "#!/bin/sh\nexit 1\n")
	badSum, _ := sha256File(bad)

	if err := applyUpdate(exe, home, "v9.9.9", badSum, bad); err == nil {
		t.Fatal("applyUpdate = nil, want health-check failure")
	}

	got, _ := os.ReadFile(exe)
	if string(got) != "OLD-BINARY" {
		t.Fatalf("exe = %q, want the previous binary restored", got)
	}
	if _, err := os.Stat(exe + ".prev"); !os.IsNotExist(err) {
		t.Fatalf("exe.prev still exists after revert (err=%v)", err)
	}

	op, err := NewOpJournal(Connect(home)).LastOp(exe)
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if op.Status != OpStatusRolledBack {
		t.Fatalf("op status = %q, want rolled_back", op.Status)
	}

	ledger, _ := os.ReadFile(filepath.Join(home, "deployments.log"))
	l := string(ledger)
	if !strings.Contains(l, "update=v9.9.9") || !strings.Contains(l, "rollback=v9.9.9") || !strings.Contains(l, oldSum) {
		t.Fatalf("ledger missing update/rollback lines: %s", l)
	}
}

// Journal discipline (host_tools.go): the swap must not stand without its
// record — a journal failure tears the swap back down.
func TestApplyUpdateJournalFailureRevertsSwap(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "OLD-BINARY")
	newBin := filepath.Join(t.TempDir(), "new-bin")
	writeExec(t, newBin, "NEW-BINARY")

	// home is a FILE, so Connect/state.db cannot be created — journal fails.
	home := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(home, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := applyUpdate(exe, home, "v9.9.9", "deadbeef", newBin); err == nil {
		t.Fatal("applyUpdate = nil, want journal failure")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD-BINARY" {
		t.Fatalf("exe = %q, want swap torn down", got)
	}
}

// Owner call: `mino rollback` restores exe.prev, records the ledger line,
// and marks the last swap op rolled_back (the RUN-001/002 vocabulary).
func TestDoRollbackRestoresAndMarksRolledBack(t *testing.T) {
	home := testHome(t)
	t.Setenv("MINO_HOME", home)
	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "NEW-BINARY")
	writeExec(t, exe+".prev", "OLD-BINARY")
	oldSum, _ := sha256File(exe + ".prev")
	t.Setenv("MINO_UPDATE_BINARY", exe)

	j := NewOpJournal(Connect(home))
	opID, err := j.Run(&OpEntry{
		OpType:      "binary.swap",
		Entity:      exe,
		BeforeState: binaryStateJSON(exe, "v9.8.0", oldSum),
		AfterState:  binaryStateJSON(exe, "v9.9.9", "deadbeef"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := DoRollback(); err != nil {
		t.Fatalf("DoRollback: %v", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD-BINARY" {
		t.Fatalf("exe = %q, want previous binary", got)
	}
	op, err := j.Get(opID)
	if err != nil || op.Status != OpStatusRolledBack {
		t.Fatalf("op status = %+v, %v — want rolled_back", op, err)
	}
	ledger, _ := os.ReadFile(filepath.Join(home, "deployments.log"))
	l := string(ledger)
	if !strings.Contains(l, "rollback=v9.9.9") || !strings.Contains(l, oldSum) {
		t.Fatalf("ledger missing rollback line: %s", l)
	}
}

func TestDoRollbackNothingToRollBack(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "CURRENT")
	t.Setenv("MINO_UPDATE_BINARY", exe)
	t.Setenv("MINO_HOME", t.TempDir())

	if err := DoRollback(); err == nil {
		t.Fatal("DoRollback = nil, want nothing-to-roll-back error")
	}
}

// The staged copy carries stage-smoke's side-effect safety: no schedules
// (cannot fire playbooks), no token (cannot poll live Telegram), no
// extensions (no duplicate processes), exclusions respected, everything
// else present.
func TestStageStateSafetyList(t *testing.T) {
	home := t.TempDir()
	stage := filepath.Join(t.TempDir(), "stage")
	for _, name := range []string{"keepme.txt", "traces", "audit.jsonl", "outbox", "extensions", "extensions.json", "old.bak", "old.bak2"} {
		p := filepath.Join(home, name)
		if name == "traces" || name == "outbox" || name == "extensions" {
			os.MkdirAll(p, 0700)
			os.WriteFile(filepath.Join(p, "f"), []byte("x"), 0600)
		} else {
			os.WriteFile(p, []byte("x"), 0600)
		}
	}
	os.WriteFile(filepath.Join(home, "schedules.json"), []byte("[{}]"), 0600)
	os.WriteFile(filepath.Join(home, "mino.env"),
		[]byte("MINO_API_KEY=keep\nTELEGRAM_BOT_TOKEN=stripme\n"), 0600)
	os.MkdirAll(filepath.Join(home, "memories"), 0700)
	os.WriteFile(filepath.Join(home, "memories", "f.md"), []byte("fact"), 0600)

	if err := stageState(home, stage); err != nil {
		t.Fatal(err)
	}

	for _, absent := range []string{"schedules.json", "traces", "audit.jsonl", "outbox", "extensions", "extensions.json", "old.bak", "old.bak2"} {
		if _, err := os.Stat(filepath.Join(stage, absent)); !os.IsNotExist(err) {
			t.Errorf("staged %s exists, want excluded", absent)
		}
	}
	for _, present := range []string{"keepme.txt", "memories/f.md"} {
		if _, err := os.Stat(filepath.Join(stage, present)); err != nil {
			t.Errorf("staged %s missing: %v", present, err)
		}
	}
	env, err := os.ReadFile(filepath.Join(stage, "mino.env"))
	if err != nil {
		t.Fatal(err)
	}
	e := string(env)
	if strings.Contains(e, "TELEGRAM_BOT_TOKEN") {
		t.Errorf("staged mino.env still carries the token: %s", e)
	}
	if !strings.Contains(e, "MINO_API_KEY=keep") {
		t.Errorf("staged mino.env lost a normal key: %s", e)
	}
}

func TestWriteDeployedVersion(t *testing.T) {
	home := t.TempDir()
	if err := writeDeployedVersion(home, "v9.9.9"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, "deployed-version"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "v9.9.9" {
		t.Fatalf("marker = %q, want v9.9.9", got)
	}
}

// --- maybeRestartService (issue #331 #6): the post-update rolling restart ---

// stubSystemd replaces runPlain/sudoRun for maybeRestartService tests and
// returns a restore func. plain dispatches on argv[1]: "show" answers the
// resolveUnit probe, "is-active" answers the liveness probe.
func stubSystemd(t *testing.T, showOut string, showErr error, isActive string, isActiveErr error, sudoCalls *[][]string) func() {
	t.Helper()
	prevPlain, prevSudo := runPlain, sudoRun
	runPlain = func(ctx context.Context, argv []string) (string, error) {
		switch argv[1] {
		case "show":
			return showOut, showErr
		case "is-active":
			return isActive, isActiveErr
		}
		return "", fmt.Errorf("unexpected probe %v", argv)
	}
	sudoRun = func(ctx context.Context, argv []string) (string, error) {
		if sudoCalls != nil {
			*sudoCalls = append(*sudoCalls, argv)
		}
		return "", nil
	}
	return func() { runPlain, sudoRun = prevPlain, prevSudo }
}

// captureStdout collects everything a fn prints, so tests can assert the
// owner-facing message (the manual-restart fallback wording is contract).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = prev
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

const minoUnitShow = "Id=mino.service\nUnitFileState=enabled\n"

// Happy path: unit resolves to itself, is active, whitelisted — journals the
// restart intent FIRST (Run commits before the kill), then issues exactly
// one systemctl restart.
func TestMaybeRestartServiceJournalsThenRestarts(t *testing.T) {
	home := testHome(t)
	t.Setenv("MINO_SERVICE", "mino.service")
	var calls [][]string
	restore := stubSystemd(t, minoUnitShow, nil, "active", nil, &calls)
	defer restore()

	out := captureStdout(t, func() { maybeRestartService(home) })

	if len(calls) != 1 || strings.Join(calls[0], " ") != "/usr/bin/systemctl restart mino.service" {
		t.Fatalf("restart calls = %v, want one /usr/bin/systemctl restart mino.service", calls)
	}
	j, err := openJournal(home)
	if err != nil {
		t.Fatal(err)
	}
	op, err := j.LastOp("mino.service")
	if err != nil || op.OpType != "service.restart" {
		t.Fatalf("journal op = %+v, err = %v — restart ran without an entry", op, err)
	}
	// Print ordering: the drop notice must precede the restart in output,
	// because nothing after a successful restart call gets printed.
	if !strings.Contains(out, "Restarting mino.service") {
		t.Fatalf("output missing restart notice: %q", out)
	}
}

// The non-systemd path (Windows asset, dev box): resolveUnit fails → self-skip
// with today's manual message. Named test so this cannot regress silently —
// mino-windows-amd64.exe ships in every release.
func TestMaybeRestartServiceNonSystemdSelfSkips(t *testing.T) {
	home := testHome(t)
	t.Setenv("MINO_SERVICE", "mino.service")
	var calls [][]string
	restore := stubSystemd(t, "", fmt.Errorf("exec: not found"), "", nil, &calls)
	defer restore()

	out := captureStdout(t, func() { maybeRestartService(home) })

	if len(calls) != 0 {
		t.Fatalf("restart attempted on non-systemd host: %v", calls)
	}
	if !strings.Contains(out, "Restart Mino to use the new version") {
		t.Fatalf("manual fallback message missing: %q", out)
	}
}

// A unit name that resolves to something else is never touched (the
// restart_service identity rule).
func TestMaybeRestartServiceRefusesForeignUnit(t *testing.T) {
	home := testHome(t)
	t.Setenv("MINO_SERVICE", "not-mino.service")
	var calls [][]string
	restore := stubSystemd(t, "Id=other.service\nUnitFileState=enabled\n", nil, "active", nil, &calls)
	defer restore()

	out := captureStdout(t, func() { maybeRestartService(home) })

	if len(calls) != 0 {
		t.Fatalf("restart attempted on foreign unit: %v", calls)
	}
	if !strings.Contains(out, "refusing") {
		t.Fatalf("refusal message missing: %q", out)
	}
}

// Outside the sudoers whitelist → manual fallback, no privileged call.
func TestMaybeRestartServiceWhitelistRefusalSkips(t *testing.T) {
	home := testHome(t)
	t.Setenv("MINO_SERVICE", "mino.service")
	sudoers := filepath.Join(t.TempDir(), "mino")
	if err := os.WriteFile(sudoers, []byte("root ALL=(root) NOPASSWD: /usr/bin/apt-get install -y *\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MINO_SUDOERS_FILE", sudoers)
	var calls [][]string
	restore := stubSystemd(t, minoUnitShow, nil, "active", nil, &calls)
	defer restore()

	out := captureStdout(t, func() { maybeRestartService(home) })

	if len(calls) != 0 {
		t.Fatalf("restart attempted outside whitelist: %v", calls)
	}
	if !strings.Contains(out, "Restart Mino to use the new version") {
		t.Fatalf("manual fallback message missing: %q", out)
	}
}
