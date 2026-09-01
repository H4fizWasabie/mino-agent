package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeRunner records every privileged/plain invocation and can fail or
// mutate simulated host state per command. It replaces HostTools.sudo/
// plain — the seam under test is the harness logic, not sudo itself.
type fakeRunner struct {
	calls [][]string
	errs  map[string]error
	// sideEffects runs after a call succeeds (key = joined argv); used to
	// simulate apt actually installing a package.
	sideEffects map[string]func()
}

func (f *fakeRunner) run(_ context.Context, argv []string) (string, error) {
	f.calls = append(f.calls, argv)
	key := strings.Join(argv, " ")
	if f.errs != nil {
		if err := f.errs[key]; err != nil {
			return "", err
		}
	}
	if f.sideEffects != nil {
		if fn := f.sideEffects[key]; fn != nil {
			fn()
		}
	}
	return "ok", nil
}

func (f *fakeRunner) called(argv ...string) bool {
	for _, c := range f.calls {
		if len(c) == len(argv) {
			match := true
			for i := range c {
				if c[i] != argv[i] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// newTestHost builds a HostTools over temp dirs: home/stageDir are real, the
// unit dir is a temp dir (never /etc/systemd/system), and every external
// interaction is a fake. The whitelist mirrors the canonical shape with the
// test's dirs substituted.
func newTestHost(t *testing.T) (*HostTools, *fakeRunner) {
	t.Helper()
	home := t.TempDir()
	unitDir := filepath.Join(home, "units")
	f := &fakeRunner{}
	installed := map[string]bool{}
	h := &HostTools{
		home:     home,
		journal:  NewOpJournal(Connect(home)),
		stageDir: filepath.Join(home, "tmp"),
		unitDir:  unitDir,
		sudo:     f.run,
		plain:    f.run,
		probe: func(_ context.Context, pkg string) bool {
			return installed[pkg]
		},
		resolve: func(_ context.Context, name string) (string, string, error) {
			switch name {
			case "mino", "mino.service":
				return "mino.service", "enabled", nil
			case "nginx.service":
				return "nginx.service", "enabled", nil
			}
			return "", "", errNoSuchUnit
		},
		check: func(argv []string) bool {
			w := &Whitelist{entries: []whitelistEntry{
				{fields: []string{"/usr/bin/apt-get", "install", "-y"}, trailingArg: true},
				{fields: []string{"/usr/bin/apt-get", "remove", "-y"}, trailingArg: true},
				{fields: []string{"/usr/bin/systemctl", "restart"}, trailingArg: true},
				{fields: []string{"/usr/bin/systemctl", "daemon-reload"}},
				{fields: []string{"/usr/bin/install", "-o", "root", "-g", "root", "-m", "0644", filepath.Join(home, "tmp") + "/", unitDir + "/"}},
				{fields: []string{"/bin/rm", "-f", unitDir + "/"}},
			}}
			return w.Allows(argv)
		},
		expected: "mino.service",
	}
	return h, f
}

func TestHostPlatformCommands(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		install []string
		remove  []string
		restart []string
	}{
		{name: "linux", goos: "linux", install: []string{"/usr/bin/apt-get", "install", "-y", "jq"}, remove: []string{"/usr/bin/apt-get", "remove", "-y", "jq"}, restart: []string{"/usr/bin/systemctl", "restart", "mino.service"}},
		{name: "macos", goos: "darwin", install: []string{"brew", "install", "jq"}, remove: []string{"brew", "uninstall", "jq"}},
		{name: "windows", goos: "windows", install: []string{"winget.exe", "install", "--accept-source-agreements", "--accept-package-agreements", "jq"}, remove: []string{"winget.exe", "uninstall", "jq"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := hostPlatformFor(tt.goos)
			if got := p.install(tt.install[len(tt.install)-1]); !reflect.DeepEqual(got, tt.install) {
				t.Fatalf("install = %v, want %v", got, tt.install)
			}
			if got := p.remove(tt.remove[len(tt.remove)-1]); !reflect.DeepEqual(got, tt.remove) {
				t.Fatalf("remove = %v, want %v", got, tt.remove)
			}
			wantRestart := tt.restart
			if tt.goos == "darwin" {
				wantRestart = []string{"launchctl", "kickstart", "-k", macServiceTarget("mino.service")}
			}
			if tt.goos == "windows" {
				wantRestart = []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Restart-Service -Name 'mino' -Force"}
			}
			if got := p.restart("mino.service"); !reflect.DeepEqual(got, wantRestart) {
				t.Fatalf("restart = %v, want %v", got, wantRestart)
			}
		})
	}
}

var errNoSuchUnit = errors.New("no such unit")

func testCtx() context.Context {
	return context.WithValue(context.Background(), sessionIDKey{}, "test-session")
}

func lastEntry(t *testing.T, h *HostTools, entity string) *OpEntry {
	t.Helper()
	e, err := h.journal.LastOp(entity)
	if err != nil {
		t.Fatalf("LastOp(%q): %v", entity, err)
	}
	return e
}

// stagedContent returns the staged file's bytes at call time (or "" when
// it no longer exists) — for asserting teardown/restore behavior.
func stagedContent(h *HostTools, name string) string {
	data, err := os.ReadFile(filepath.Join(h.stageDir, name))
	if err != nil {
		return ""
	}
	return string(data)
}

// --- install_package ---

func TestInstallPackageJournaledAndSudoExactCommand(t *testing.T) {
	h, f := newTestHost(t)
	installed := false
	f.sideEffects = map[string]func(){
		"/usr/bin/apt-get install -y jq": func() { installed = true },
	}
	h.probe = func(_ context.Context, pkg string) bool { return installed }

	got := makeInstallPackageTool(h).ContextFn(testCtx(), map[string]any{"package": "jq"})
	if !strings.Contains(got, "Installed jq") {
		t.Fatalf("unexpected result: %q", got)
	}
	if len(f.calls) != 1 || !f.called("/usr/bin/apt-get", "install", "-y", "jq") {
		t.Fatalf("sudo must run exactly the whitelisted command, got %v", f.calls)
	}
	e := lastEntry(t, h, "jq")
	if e.OpType != "package.install" || e.Status != OpStatusOK || e.SessionID != "test-session" {
		t.Fatalf("journal entry: %+v", e)
	}
	if !strings.Contains(e.BeforeState, `"installed":false`) || !strings.Contains(e.AfterState, `"installed":true`) {
		t.Fatalf("before/after state: %q -> %q", e.BeforeState, e.AfterState)
	}
}

func TestInstallPackageRefusedWhenNotWhitelisted(t *testing.T) {
	h, f := newTestHost(t)
	allow := h.check
	h.check = func(argv []string) bool { return allow(argv) && argv[1] != "install" }
	got := makeInstallPackageTool(h).ContextFn(testCtx(), map[string]any{"package": "jq"})
	if !strings.Contains(got, "whitelist") {
		t.Fatalf("refusal must name the whitelist boundary, got %q", got)
	}
	if len(f.calls) != 0 {
		t.Fatalf("no sudo call may happen for a refused op, got %v", f.calls)
	}
	if _, err := h.journal.LastOp("jq"); err == nil {
		t.Fatal("a refused op must not be journaled")
	}
}

func TestInstallPackageInvalidName(t *testing.T) {
	h, f := newTestHost(t)
	for _, bad := range []string{"rm -rf /", "-evil", "UPPER", "a b"} {
		got := makeInstallPackageTool(h).ContextFn(testCtx(), map[string]any{"package": bad})
		if !strings.Contains(got, "invalid package name") {
			t.Fatalf("%q: expected invalid-name error, got %q", bad, got)
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("no sudo call may happen for an invalid name")
	}
}

func TestInstallPackageSudoFailureJournaledFailed(t *testing.T) {
	h, f := newTestHost(t)
	f.errs = map[string]error{"/usr/bin/apt-get install -y jq": errBoom}
	got := makeInstallPackageTool(h).ContextFn(testCtx(), map[string]any{"package": "jq"})
	if !strings.Contains(got, "failed") {
		t.Fatalf("expected failure result, got %q", got)
	}
	e := lastEntry(t, h, "jq")
	if e.Status != OpStatusFailed {
		t.Fatalf("failed install must journal status=failed, got %+v", e)
	}
}

var errBoom = &boomErr{}

type boomErr struct{}

func (*boomErr) Error() string { return "boom" }

func TestInstallPackageJournalFailureRollsBack(t *testing.T) {
	h, f := newTestHost(t)
	db := h.journal.db
	db.Close() // journal Run now fails at Begin
	got := makeInstallPackageTool(h).ContextFn(testCtx(), map[string]any{"package": "jq"})
	if !strings.Contains(got, "rolled back") {
		t.Fatalf("expected rollback result, got %q", got)
	}
	if !f.called("/usr/bin/apt-get", "remove", "-y", "jq") {
		t.Fatalf("teardown must remove the package, got %v", f.calls)
	}
}

func TestInstallPackageJournalFailureKeepsPreInstalled(t *testing.T) {
	h, f := newTestHost(t)
	installed := true
	h.probe = func(_ context.Context, pkg string) bool { return installed }
	db := h.journal.db
	db.Close()
	got := makeInstallPackageTool(h).ContextFn(testCtx(), map[string]any{"package": "jq"})
	if !strings.Contains(got, "rolled back") {
		t.Fatalf("expected rollback result, got %q", got)
	}
	if f.called("/usr/bin/apt-get", "remove", "-y", "jq") {
		t.Fatalf("teardown must NOT remove a package that was already installed, got %v", f.calls)
	}
}

// --- write_unit ---

func TestWriteUnitRendersNeutralDefinition(t *testing.T) {
	h, f := newTestHost(t)
	got := makeWriteUnitTool(h).ContextFn(testCtx(), map[string]any{
		"name": "mino", "executable": "/opt/mino/mino", "args": []any{"--serve"}, "restart": "always",
	})
	if !strings.Contains(got, "Wrote mino.service") || len(f.calls) != 2 {
		t.Fatalf("unexpected neutral write result: %q calls=%v", got, f.calls)
	}
	e := lastEntry(t, h, "mino.service")
	if !strings.Contains(e.AfterState, "ExecStart=/opt/mino/mino --serve") {
		t.Fatalf("rendered systemd definition missing executable: %q", e.AfterState)
	}
}

func TestWriteUnitJournaledAndSudoExactCommand(t *testing.T) {
	h, f := newTestHost(t)
	content := "[Unit]\nDescription=test\n[Service]\nExecStart=/bin/true\n"
	got := makeWriteUnitTool(h).ContextFn(testCtx(), map[string]any{"name": "mino.service", "content": content})
	if !strings.Contains(got, "Wrote mino.service") {
		t.Fatalf("unexpected result: %q", got)
	}
	install := []string{"/usr/bin/install", "-o", "root", "-g", "root", "-m", "0644", filepath.Join(h.stageDir, "mino.service"), filepath.Join(h.unitDir, "mino.service")}
	if len(f.calls) != 2 || !f.called(install...) || !f.called("/usr/bin/systemctl", "daemon-reload") {
		t.Fatalf("expected install + daemon-reload, got %v", f.calls)
	}
	// staged file carries the content and is cleaned up after the journal commit
	if staged := stagedContent(h, "mino.service"); staged != "" {
		t.Fatalf("staged file must be removed after commit, still present with %d bytes", len(staged))
	}
	e := lastEntry(t, h, "mino.service")
	if e.OpType != "unit.write" {
		t.Fatalf("journal entry: %+v", e)
	}
	var before, after map[string]any
	json.Unmarshal([]byte(e.BeforeState), &before)
	json.Unmarshal([]byte(e.AfterState), &after)
	if before["present"] != false || after["present"] != true || after["content"] != content {
		t.Fatalf("before/after state: %q -> %q", e.BeforeState, e.AfterState)
	}
}

func TestWriteUnitReplacesExisting(t *testing.T) {
	h, _ := newTestHost(t)
	if err := os.MkdirAll(h.unitDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.unitDir, "mino.service"), []byte("OLD"), 0600); err != nil {
		t.Fatal(err)
	}
	got := makeWriteUnitTool(h).ContextFn(testCtx(), map[string]any{"name": "mino.service", "content": "NEW"})
	if !strings.Contains(got, "Wrote mino.service") {
		t.Fatalf("unexpected result: %q", got)
	}
	e := lastEntry(t, h, "mino.service")
	if !strings.Contains(e.BeforeState, "OLD") {
		t.Fatalf("before-state must carry the old content, got %q", e.BeforeState)
	}
}

func TestWriteUnitInvalidInput(t *testing.T) {
	h, f := newTestHost(t)
	cases := []struct {
		name, content, want string
	}{
		{"../evil.service", "x", "invalid unit name"},
		{"MINO.service", "x", "invalid unit name"},
		{"mino.txt", "x", "invalid unit name"},
		{"mino.service", "", "cannot be empty"},
		{"mino.service", "a\x00b", "NUL"},
		{"mino.service", strings.Repeat("x", unitMaxBytes+1), "too large"},
	}
	for _, tc := range cases {
		got := makeWriteUnitTool(h).ContextFn(testCtx(), map[string]any{"name": tc.name, "content": tc.content})
		if !strings.Contains(got, tc.want) {
			t.Fatalf("%q: expected %q in result, got %q", tc.name, tc.want, got)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("no sudo call may happen for invalid input, got %v", f.calls)
	}
}

func TestWriteUnitJournalFailureRestoresOld(t *testing.T) {
	h, f := newTestHost(t)
	if err := os.MkdirAll(h.unitDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.unitDir, "mino.service"), []byte("OLD"), 0600); err != nil {
		t.Fatal(err)
	}
	db := h.journal.db
	db.Close()
	// Capture the staged bytes at the moment the RESTORE install runs: the
	// write install fires first (staged=NEW), the restore second (staged=OLD).
	installKey := strings.Join(installArgv(h, filepath.Join(h.stageDir, "mino.service"), "mino.service"), " ")
	restoreContent := ""
	installCount := 0
	f.sideEffects = map[string]func(){
		installKey: func() {
			installCount++
			if installCount == 2 {
				restoreContent = stagedContent(h, "mino.service")
			}
		},
	}
	got := makeWriteUnitTool(h).ContextFn(testCtx(), map[string]any{"name": "mino.service", "content": "NEW"})
	if !strings.Contains(got, "restored") {
		t.Fatalf("expected restore result, got %q", got)
	}
	if installCount != 2 {
		t.Fatalf("expected install + restore install, got %v", f.calls)
	}
	if restoreContent != "OLD" {
		t.Fatalf("restore must restage the old content before installing, staged=%q", restoreContent)
	}
}

func TestWriteUnitJournalFailureRemovesNewUnit(t *testing.T) {
	h, f := newTestHost(t)
	db := h.journal.db
	db.Close()
	got := makeWriteUnitTool(h).ContextFn(testCtx(), map[string]any{"name": "mino.service", "content": "NEW"})
	if !strings.Contains(got, "restored") {
		t.Fatalf("expected restore result, got %q", got)
	}
	if !f.called("/bin/rm", "-f", filepath.Join(h.unitDir, "mino.service")) {
		t.Fatalf("teardown must remove the never-journaled unit, got %v", f.calls)
	}
}

// --- restart_service ---

func TestRestartServiceJournaledBeforeRestart(t *testing.T) {
	h, f := newTestHost(t)
	got := makeRestartServiceTool(h).ContextFn(testCtx(), map[string]any{"service": "mino.service"})
	if !strings.Contains(got, "will terminate") {
		t.Fatalf("unexpected result: %q", got)
	}
	// intent is in the journal BEFORE systemd was asked
	e := lastEntry(t, h, "mino.service")
	if e.OpType != "service.restart" || e.Status != OpStatusOK {
		t.Fatalf("journal entry: %+v", e)
	}
	if !f.called("/usr/bin/systemctl", "restart", "mino.service") {
		t.Fatalf("expected restart call, got %v", f.calls)
	}
}

func TestRestartServiceRefusesForeignUnit(t *testing.T) {
	h, f := newTestHost(t)
	got := makeRestartServiceTool(h).ContextFn(testCtx(), map[string]any{"service": "nginx.service"})
	if !strings.Contains(got, "refusing") || !strings.Contains(got, "mino.service") {
		t.Fatalf("expected refusal naming MINO_SERVICE, got %q", got)
	}
	if len(f.calls) != 0 {
		t.Fatalf("no sudo call may happen for a foreign unit, got %v", f.calls)
	}
	if _, err := h.journal.LastOp("nginx.service"); err == nil {
		t.Fatal("a refused restart must not be journaled")
	}
}

func TestRestartServiceJournalFailureAborts(t *testing.T) {
	h, f := newTestHost(t)
	db := h.journal.db
	db.Close()
	got := makeRestartServiceTool(h).ContextFn(testCtx(), map[string]any{"service": "mino.service"})
	if !strings.Contains(got, "aborted") {
		t.Fatalf("expected abort result, got %q", got)
	}
	if f.called("/usr/bin/systemctl", "restart", "mino.service") {
		t.Fatalf("a restart whose intent could not be journaled must never run, got %v", f.calls)
	}
}

func TestRestartServiceFailureMarksJournalFailed(t *testing.T) {
	h, f := newTestHost(t)
	f.errs = map[string]error{"/usr/bin/systemctl restart mino.service": errBoom}
	got := makeRestartServiceTool(h).ContextFn(testCtx(), map[string]any{"service": "mino.service"})
	if !strings.Contains(got, "failed") {
		t.Fatalf("expected failure result, got %q", got)
	}
	e := lastEntry(t, h, "mino.service")
	if e.Status != OpStatusFailed {
		t.Fatalf("failed restart must be journaled status=failed, got %+v", e)
	}
}

func TestRestartServiceNotWhitelisted(t *testing.T) {
	h, f := newTestHost(t)
	allow := h.check
	h.check = func(argv []string) bool { return allow(argv) && argv[1] != "restart" }
	got := makeRestartServiceTool(h).ContextFn(testCtx(), map[string]any{"service": "mino.service"})
	if !strings.Contains(got, "whitelist") {
		t.Fatalf("expected whitelist refusal, got %q", got)
	}
	if len(f.calls) != 0 {
		t.Fatalf("no sudo call may happen for a refused restart, got %v", f.calls)
	}
	if _, err := h.journal.LastOp("mino.service"); err == nil {
		t.Fatal("a refused restart must not be journaled")
	}
}

// --- bash guard ---

func TestBashRejectsSudo(t *testing.T) {
	tool := makeBashToolFor(t.TempDir(), 0)
	for _, cmd := range []string{
		"sudo apt-get install jq",
		"x=`sudo -n systemctl restart mino.service`", // backtick substitution must be caught too
		"x=$(sudo -n id)",
	} {
		got := tool.Fn(map[string]any{"command": cmd})
		if !strings.Contains(got, "refused") {
			t.Fatalf("bash must refuse %q, got %q", cmd, got)
		}
	}
}
