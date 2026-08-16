package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateConfigPerFile locks the per-file validation bars (parse check
// + minimal sanity, documented on config_heal.go): providers.json must
// yield at least one named provider (a config that bricks routing is bad),
// mino.env must match loadEnvFile's accept grammar (a line the loader would
// silently skip is a failure), cost-watch.json must be a JSON object (the
// extension silently defaults on anything else).
func TestValidateConfigPerFile(t *testing.T) {
	goodProviders := `{"providers":[
		{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"},
		{"name":"qwen","base_url":"https://api.qwen.example","api_key_env":"MINO_QWEN_KEY","model":"m2"}
	]}`
	bad := []struct {
		name    string
		content string
	}{
		{"providers.json", "not json"},
		{"providers.json", `{"providers":[]}`},
		{"providers.json", `{"providers":[{"base_url":"https://x"}]}`}, // no name
		{"providers.json", `{"providers":[{"name":""}]}`},
		{"mino.env", "NOEQUALS"},
		{"mino.env", "=value"},
		{"mino.env", "  KEY WITHOUT VALUE"},
		{"cost-watch.json", "not json"},
		{"cost-watch.json", "[1,2,3]"},
		{"cost-watch.json", `"just a string"`},
	}
	if err := ValidateConfig("providers.json", []byte(goodProviders)); err != nil {
		t.Fatalf("good providers.json rejected: %v", err)
	}
	for _, c := range bad {
		if err := ValidateConfig(c.name, []byte(c.content)); err == nil {
			t.Errorf("ValidateConfig(%s, %q) = nil, want error", c.name, c.content)
		}
	}
	goodEnv := "# comment\n\nMINO_LUNA_KEY=sk-abc\nTELEGRAM_BOT_TOKEN=123:token=with=equals\n"
	if err := ValidateConfig("mino.env", []byte(goodEnv)); err != nil {
		t.Fatalf("good mino.env rejected: %v", err)
	}
	goodCostWatch := `{"port":9300,"models":{"deepseek/deepseek-v4-flash-0731":{"url":"https://openrouter.ai/deepseek/deepseek-v4-flash-0731","expected":0.08,"threshold":2.0}}}`
	if err := ValidateConfig("cost-watch.json", []byte(goodCostWatch)); err != nil {
		t.Fatalf("good cost-watch.json rejected: %v", err)
	}
}

// The reload-time boundary: a genuinely bad providers.json (garbage on
// disk, as a manual/bash edit would leave it) drives the REAL revert
// decision through HealConfig — and the file on disk must actually be
// restored to the known-good content, with the edit op marked rolled_back.
func TestHealConfigRevertsBadProviders(t *testing.T) {
	home := testHome(t)
	j := NewOpJournal(Connect(home))
	path := filepath.Join(home, "providers.json")
	v1 := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"}]}`
	v2 := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"},{"name":"qwen","base_url":"https://api.qwen.example","api_key_env":"MINO_QWEN_KEY","model":"m2"}]}`
	bad := `{"providers":[` // truncated JSON — the bad-edit class

	if err := os.WriteFile(path, []byte(v1), 0600); err != nil {
		t.Fatal(err)
	}
	// A real journaled edit establishes the .prev baseline and the op.
	if _, err := applyConfigEdit(home, path, []byte(v2), false); err != nil {
		t.Fatalf("seed good config: %v", err)
	}
	if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}

	HealConfig(home, j)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != v1 {
		t.Fatalf("providers.json on disk = %q, want known-good content restored", got)
	}
	op, err := j.LastOp(path)
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if op.Status != OpStatusRolledBack {
		t.Fatalf("op status = %q, want rolled_back", op.Status)
	}
	// .prev survives the revert — a re-revert stays possible (deliberate
	// difference from RUN-004's consuming rename).
	if _, err := os.Stat(prevPath(path)); err != nil {
		t.Fatalf(".prev missing after revert: %v", err)
	}
}

// A valid config on SIGHUP becomes the new known-good baseline.
func TestHealConfigRefreshesBaseline(t *testing.T) {
	home := testHome(t)
	path := filepath.Join(home, "providers.json")
	v1 := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"}]}`
	v2 := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"},{"name":"qwen","base_url":"https://api.qwen.example","api_key_env":"MINO_QWEN_KEY","model":"m2"}]}`
	if _, err := applyConfigEdit(home, path, []byte(v1), false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(v2), 0600); err != nil {
		t.Fatal(err)
	}

	HealConfig(home, NewOpJournal(Connect(home)))

	prev, err := os.ReadFile(prevPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(prev) != v2 {
		t.Fatalf(".prev = %q, want the new valid content as baseline", prev)
	}
}

// A bad file with no backup must fail loudly, not panic, and leave the bad
// content in place for manual diagnosis.
func TestHealConfigNoPrevLeavesFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "providers.json")
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	HealConfig(home, nil) // no journal either — must not crash
	got, _ := os.ReadFile(path)
	if string(got) != "{broken" {
		t.Fatalf("file changed without a backup: %q", got)
	}
}

// The write-time boundary: an invalid edit is refused before it lands — the
// file on disk is unchanged and no .prev or journal entry is created.
func TestApplyConfigEditRefusesInvalid(t *testing.T) {
	home := testHome(t)
	path := filepath.Join(home, "providers.json")
	good := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"}]}`
	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := applyConfigEdit(home, path, []byte("{oops"), false)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("applyConfigEdit = %v, want rejection naming the failure", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != good {
		t.Fatalf("file on disk = %q, want original content untouched", got)
	}
	if _, err := os.Stat(prevPath(path)); !os.IsNotExist(err) {
		t.Fatalf(".prev created for a refused edit (err=%v)", err)
	}
	if _, err := NewOpJournal(Connect(home)).LastOp(path); err == nil {
		t.Fatal("journal has an op for a refused edit — nothing mutated")
	}
}

// Happy write path: valid content lands, the old content is backed up to
// .prev, and the edit is journaled with before/after hashes (hash + path —
// mino.env secrets never enter the journal).
func TestApplyConfigEditJournalsAndBacksUp(t *testing.T) {
	home := testHome(t)
	j := NewOpJournal(Connect(home))
	path := filepath.Join(home, "mino.env")
	v1 := "MINO_LUNA_KEY=sk-old\n"
	v2 := "MINO_LUNA_KEY=sk-new\nTELEGRAM_BOT_TOKEN=tok\n"
	if err := os.WriteFile(path, []byte(v1), 0600); err != nil {
		t.Fatal(err)
	}

	handled, err := applyConfigEdit(home, path, []byte(v2), false)
	if err != nil || !handled {
		t.Fatalf("applyConfigEdit = %v, %v — want handled", handled, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != v2 {
		t.Fatalf("file = %q, want new content", got)
	}
	prev, _ := os.ReadFile(prevPath(path))
	if string(prev) != v1 {
		t.Fatalf(".prev = %q, want old content", prev)
	}
	op, err := j.LastOp(path)
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if op.OpType != "config.edit" || op.Status != OpStatusOK {
		t.Fatalf("op = %+v, want config.edit/ok", op)
	}
	if !strings.Contains(op.BeforeState, sha256Bytes([]byte(v1))) ||
		!strings.Contains(op.AfterState, sha256Bytes([]byte(v2))) {
		t.Fatalf("states lack hashes: before=%q after=%q", op.BeforeState, op.AfterState)
	}
}

// RUN-002 discipline: the mutation must not stand without its record — a
// journal failure tears the edit back down (restore for an existing file,
// remove for a brand-new one).
func TestApplyConfigEditJournalFailureReverts(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "providers.json")
	good := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"}]}`
	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	// state.db is a DIRECTORY — Connect cannot open it, so the journal is
	// unavailable (the rollback_test home-is-a-file trick, same boundary).
	if err := os.Mkdir(filepath.Join(home, "state.db"), 0700); err != nil {
		t.Fatal(err)
	}

	_, err := applyConfigEdit(home, path, []byte(`{"providers":[{"name":"qwen","base_url":"https://x","api_key_env":"K","model":"m"}]}`), false)
	if err == nil || !strings.Contains(err.Error(), "journal") {
		t.Fatalf("applyConfigEdit = %v, want journal-unavailable failure", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != good {
		t.Fatalf("file on disk = %q, want original content after teardown", got)
	}

	// Brand-new config file + journal failure = the file is removed (nothing
	// existed before — the RUN-003 write_unit teardown shape).
	path2 := filepath.Join(home, "cost-watch.json")
	if _, err := applyConfigEdit(home, path2, []byte(`{"port":9300}`), false); err == nil {
		t.Fatal("applyConfigEdit on new file = nil, want journal failure")
	}
	if _, err := os.Stat(path2); !os.IsNotExist(err) {
		t.Fatalf("new file survived a journal failure (err=%v)", err)
	}
}

// Non-config paths pass through untouched — the guard never intercepts the
// workspace write path.
func TestApplyConfigEditNonConfigUntouched(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "notes.md")
	handled, err := applyConfigEdit(home, path, []byte("hi"), false)
	if err != nil || handled {
		t.Fatalf("applyConfigEdit = %v, %v — want not-handled", handled, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("guard wrote a non-config file")
	}
}

// Append mode composes old+new and validates the composed result — appending
// a fragment to a JSON config is a refusal, appending a KEY=VALUE line to
// mino.env is fine.
func TestApplyConfigEditAppendComposes(t *testing.T) {
	home := testHome(t)
	path := filepath.Join(home, "mino.env")
	if err := os.WriteFile(path, []byte("MINO_LUNA_KEY=sk-old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := applyConfigEdit(home, path, []byte("MINO_QWEN_KEY=sk-new\n"), true); err != nil {
		t.Fatalf("append KEY=VALUE: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "MINO_LUNA_KEY=sk-old\nMINO_QWEN_KEY=sk-new\n" {
		t.Fatalf("appended file = %q", got)
	}

	jpath := filepath.Join(home, "providers.json")
	if err := os.WriteFile(jpath, []byte(`{"providers":[{"name":"luna","base_url":"https://x","api_key_env":"K","model":"m"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := applyConfigEdit(home, jpath, []byte(`,"providers":[`), true); err == nil {
		t.Fatal("append of a JSON fragment = nil, want refusal")
	}
}

// The tool-level boundary: the model's write_file drives the REAL guard —
// a bad providers.json edit is refused with the file untouched; a good edit
// lands backed up and journaled.
func TestWriteFileToolGuardsConfig(t *testing.T) {
	home := testHome(t)
	j := NewOpJournal(Connect(home))
	registry := NewRegistry()
	registry.Register(makeWriteTool(home, home))
	path := filepath.Join(home, "providers.json")
	good := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"}]}`
	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}

	out := registry.ExecuteContext(context.Background(), "write_file", map[string]any{
		"path": path, "content": "{broken json",
	})
	if !strings.Contains(out, "rejected") {
		t.Fatalf("write_file output = %q, want rejection", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != good {
		t.Fatalf("file on disk = %q, want original content", got)
	}

	out = registry.ExecuteContext(context.Background(), "write_file", map[string]any{
		"path": path, "content": good,
	})
	if !strings.Contains(out, "Wrote") {
		t.Fatalf("write_file output = %q, want success", out)
	}
	if _, err := j.LastOp(path); err != nil {
		t.Fatalf("journal missing the edit: %v", err)
	}
}

// Owner call: `mino config-rollback <name>` restores the .prev backup
// (kept, so it is idempotent) and marks the last edit op rolled_back.
func TestDoConfigRollbackRestoresAndMarksRolledBack(t *testing.T) {
	home := testHome(t)
	t.Setenv("MINO_HOME", home)
	j := NewOpJournal(Connect(home))
	path := filepath.Join(home, "providers.json")
	good := `{"providers":[{"name":"luna","base_url":"https://api.luna.example","api_key_env":"MINO_LUNA_KEY","model":"m1"}]}`
	bad := `{"providers":[` // genuinely bad — the class that slips past tool guards

	if err := os.WriteFile(path, []byte(good), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := applyConfigEdit(home, path, []byte(good), false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}

	if err := DoConfigRollback("providers.json"); err != nil {
		t.Fatalf("DoConfigRollback: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != good {
		t.Fatalf("file on disk = %q, want known-good content", got)
	}
	if _, err := os.Stat(prevPath(path)); err != nil {
		t.Fatalf(".prev consumed by the owner rollback (err=%v)", err)
	}
	op, err := j.LastOp(path)
	if err != nil || op.Status != OpStatusRolledBack {
		t.Fatalf("op = %+v, %v — want rolled_back", op, err)
	}
}

func TestDoConfigRollbackRejects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MINO_HOME", home)
	if err := DoConfigRollback("unknown.json"); err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("unknown name: %v, want refusal", err)
	}
	if err := DoConfigRollback("providers.json"); err == nil || !strings.Contains(err.Error(), "nothing to roll back") {
		t.Fatalf("no backup: %v, want nothing-to-roll-back error", err)
	}
}
