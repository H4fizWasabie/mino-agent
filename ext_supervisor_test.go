package main

// RUN-001 (GitHub #215) supervision tests. The fake extension
// (testdata/fakeext) is built once in TestMain; tests place it at the
// supervisor's convention path (~/.mino/extensions/<name>/<name>) and drive
// the supervisor exactly as the VPS would — env-driven, MINO_HOME override,
// no hardcoded paths.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var fakeExtBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeext-build")
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeext build dir:", err)
		os.Exit(1)
	}
	fakeExtBin = filepath.Join(dir, "fakeext")
	cmd := exec.Command("go", "build", "-o", fakeExtBin, ".")
	cmd.Dir = "testdata/fakeext" // its own module — testdata is excluded from the main module
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build testdata/fakeext: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// placeFakeExt copies the built fake extension to the supervisor's
// convention path and returns its log file path.
func placeFakeExt(t *testing.T, home, name string) string {
	t.Helper()
	dir := filepath.Join(home, "extensions", name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fakeExtBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(t.TempDir(), "fakeext.log")
}

func registryHas(r *Registry, name string) bool {
	for _, ti := range r.Catalog() {
		if ti.Name == name {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func logStarts(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "start ")
}

func lastPid(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(lines[len(lines)-1], "start pid=%d", &pid); err != nil {
		return 0
	}
	return pid
}

// startTimes parses the fakeext startup log's timestamps (ts=RFC3339Nano).
func startTimes(t *testing.T, path string) []time.Time {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []time.Time
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, "ts=") {
				if ts, err := time.Parse(time.RFC3339Nano, f[3:]); err == nil {
					out = append(out, ts)
				}
			}
		}
	}
	return out
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// Boot reconciliation: a supervised entry in extensions.json is spawned at
// Start, its tools registered, and Shutdown kills the child.
func TestSupervisorBootSpawnsRegistersAndShutdownKills(t *testing.T) {
	home := t.TempDir()
	logPath := placeFakeExt(t, home, "fakeext")
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	cfg := ExtensionConfig{Name: "fakeext", Repo: "file:///unused", Port: port, Env: map[string]string{"FAKEEXT_LOG": logPath}}
	data, _ := json.Marshal([]ExtensionConfig{cfg})
	if err := os.WriteFile(filepath.Join(home, "extensions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	sup := NewExtensionSupervisor(home, r, NewOpJournal(Connect(home)))
	sup.Start()

	waitFor(t, 10*time.Second, func() bool {
		if !registryHas(r, "fake_echo") {
			return false
		}
		return pidAlive(lastPid(t, logPath))
	}, "extension spawned, healthy, and its tool registered")

	sup.Shutdown()
	dead := lastPid(t, logPath)
	waitFor(t, 10*time.Second, func() bool { return !pidAlive(dead) }, "child killed at shutdown")
}

// Crash recovery: a crashing extension is restarted with backoff and its
// tools stay registered across the process swap (no re-registration).
func TestSupervisorRestartsCrashedExtension(t *testing.T) {
	home := t.TempDir()
	logPath := placeFakeExt(t, home, "fakeext")
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	cfg := ExtensionConfig{
		Name: "fakeext", Repo: "file:///unused", Port: port,
		Env: map[string]string{"FAKEEXT_LOG": logPath, "FAKEEXT_CRASH_AFTER_MS": "1000"},
	}
	data, _ := json.Marshal([]ExtensionConfig{cfg})
	if err := os.WriteFile(filepath.Join(home, "extensions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	r := NewRegistry()
	sup := NewExtensionSupervisor(home, r, NewOpJournal(Connect(home)))
	sup.Start()
	defer sup.Shutdown()

	// ≥2 starts means the crashed child was respawned.
	waitFor(t, 15*time.Second, func() bool {
		return registryHas(r, "fake_echo") && logStarts(t, logPath) >= 2
	}, "extension restarted after crash")
}

// Full install path through the harness tool: clone → build → spawn →
// journal; then uninstall: kill → remove → unregister → rolled_back.
func TestManageExtensionInstallUninstallJournaled(t *testing.T) {
	home := t.TempDir()
	sup := NewExtensionSupervisor(home, NewRegistry(), NewOpJournal(Connect(home)))
	tool := makeManageExtensionTool(sup)
	ctx := context.WithValue(context.Background(), sessionIDKey{}, "test-session")

	// A local git repo carrying the fake extension source.
	repo := t.TempDir()
	for _, f := range []string{"main.go", "go.mod"} {
		data, err := os.ReadFile(filepath.Join("testdata", "fakeext", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	if out, err := exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "fakeext").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// Install — log file via env so the test can see child starts.
	logPath := filepath.Join(t.TempDir(), "fakeext.log")
	out := tool.ContextFn(ctx, map[string]any{
		"action": "install",
		"repo":   repo,
		"name":   "fakeext",
		"env":    map[string]any{"FAKEEXT_LOG": logPath},
	})
	if strings.HasPrefix(out, "Extension install failed") || strings.HasPrefix(out, "Error") {
		t.Fatalf("install failed: %s", out)
	}

	// Config registered.
	var configs []ExtensionConfig
	data, err := os.ReadFile(filepath.Join(home, "extensions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &configs); err != nil || len(configs) != 1 || configs[0].Name != "fakeext" || configs[0].Repo != repo {
		t.Fatalf("extensions.json after install: %s (err %v)", data, err)
	}
	// Tool registered and proxying.
	if !registryHas(sup.registry, "fake_echo") {
		t.Fatal("fake_echo not registered after install")
	}
	tt, ok := sup.registry.tools["fake_echo"]
	if !ok {
		t.Fatal("fake_echo missing from registry")
	}
	if got := tt.Fn(map[string]any{"message": "hi"}); !strings.Contains(got, "echo: hi") {
		t.Fatalf("proxy execute = %q", got)
	}
	// Journal: install op with before/after state.
	last, err := sup.journal.LastOp("fakeext")
	if err != nil {
		t.Fatal(err)
	}
	if last.OpType != "extension.install" || last.Status != OpStatusOK || last.SessionID != "test-session" ||
		last.BeforeState != "null" || !strings.Contains(last.AfterState, `"name":"fakeext"`) {
		t.Fatalf("install journal entry: %+v", last)
	}
	// Child running.
	waitFor(t, 10*time.Second, func() bool { return pidAlive(lastPid(t, logPath)) }, "child process alive")

	// Uninstall.
	out = tool.ContextFn(ctx, map[string]any{"action": "uninstall", "name": "fakeext"})
	if strings.HasPrefix(out, "Extension uninstall failed") || strings.HasPrefix(out, "Error") {
		t.Fatalf("uninstall failed: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "extensions.json")); err != nil || len(sup.configs()) != 0 {
		t.Fatal("config not removed after uninstall")
	}
	if registryHas(sup.registry, "fake_echo") {
		t.Fatal("tool still registered after uninstall")
	}
	if _, err := os.Stat(filepath.Join(home, "extensions", "fakeext")); !os.IsNotExist(err) {
		t.Fatal("clone dir not removed after uninstall")
	}
	un, err := sup.journal.LastOp("fakeext")
	if err != nil {
		t.Fatal(err)
	}
	if un.OpType != "extension.uninstall" || un.UndoOf != last.ID {
		t.Fatalf("uninstall journal entry: %+v", un)
	}
	install, err := sup.journal.Get(last.ID)
	if err != nil {
		t.Fatal(err)
	}
	if install.Status != OpStatusRolledBack {
		t.Fatalf("install op status = %q, want rolled_back", install.Status)
	}
	dead := lastPid(t, logPath)
	waitFor(t, 10*time.Second, func() bool { return !pidAlive(dead) }, "child killed at uninstall")
}

// Install of a repo that does not exist fails cleanly without journaling
// or leaving config behind.
func TestManageExtensionInstallBadRepoFailsClean(t *testing.T) {
	home := t.TempDir()
	sup := NewExtensionSupervisor(home, NewRegistry(), NewOpJournal(Connect(home)))
	tool := makeManageExtensionTool(sup)

	out := tool.ContextFn(context.Background(), map[string]any{
		"action": "install",
		"repo":   filepath.Join(t.TempDir(), "does-not-exist"),
		"name":   "badext",
	})
	if !strings.HasPrefix(out, "Extension install failed") {
		t.Fatalf("expected failure, got: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "extensions.json")); !os.IsNotExist(err) {
		t.Fatal("extensions.json must not be created by a failed install")
	}
	if _, err := sup.journal.LastOp("badext"); err == nil {
		t.Fatal("failed clone must not journal an op")
	}
	if _, err := os.Stat(filepath.Join(home, "extensions", "badext")); !os.IsNotExist(err) {
		t.Fatal("clone dir must be removed after failed install")
	}
}

// Arg validation lives in the harness tool, not the supervisor.
func TestManageExtensionToolValidation(t *testing.T) {
	sup := NewExtensionSupervisor(t.TempDir(), NewRegistry(), nil)
	tool := makeManageExtensionTool(sup)
	ctx := context.Background()
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "install"}, "requires a repo"},
		{map[string]any{"action": "uninstall"}, "requires a name"},
		{map[string]any{"action": "frobnicate"}, "install"},
	}
	for _, c := range cases {
		if out := tool.ContextFn(ctx, c.args); !strings.Contains(out, c.want) {
			t.Errorf("args %v: output %q does not mention %q", c.args, out, c.want)
		}
	}
	// Derive name from repo basename.
	if name := extNameFromRepo("https://example.com/some/threads.git"); name != "threads" {
		t.Errorf("extNameFromRepo = %q, want threads", name)
	}
}

func extNameFromRepo(repo string) string {
	return strings.TrimSuffix(filepath.Base(repo), ".git")
}

// F2 regression (RUN-001 review, PR #224): crash-loop backoff must ramp.
// A child that spawns fine but crashes every ~600ms restarts at ~1s, ~2s,
// ~4s… — never a flat 1s-forever (600ms is the shortest crash window that
// still lets the /tools health poll land). Fails without the crash-branch
// doubling: third interval ≈ first (margin −0.6s), passes with it (margin
// +0.55s).
func TestSupervisorBackoffRampsOnRepeatedCrashes(t *testing.T) {
	home := t.TempDir()
	logPath := placeFakeExt(t, home, "fakeext")
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	cfg := ExtensionConfig{
		Name: "fakeext", Repo: "file:///unused", Port: port,
		Env: map[string]string{"FAKEEXT_LOG": logPath, "FAKEEXT_CRASH_AFTER_MS": "600"},
	}
	data, _ := json.Marshal([]ExtensionConfig{cfg})
	if err := os.WriteFile(filepath.Join(home, "extensions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	sup := NewExtensionSupervisor(home, NewRegistry(), NewOpJournal(Connect(home)))
	sup.Start()
	defer sup.Shutdown()

	var ts []time.Time
	waitFor(t, 20*time.Second, func() bool {
		ts = startTimes(t, logPath)
		return len(ts) >= 4
	}, "four extension starts (crash loop)")
	// Restart intervals grow as backoff doubles: I1≈1.6s, I2≈2.6s, I3≈4.6s.
	// The third must exceed twice the first — impossible with a flat 1s.
	if third, first := ts[3].Sub(ts[2]), ts[1].Sub(ts[0]); third <= 2*first {
		t.Fatalf("backoff not ramping: third interval %.2fs <= 2× first %.2fs", third.Seconds(), first.Seconds())
	}
}
