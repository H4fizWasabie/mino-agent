package main

// approval_test.go — RUN-006 (GitHub #220). The tests drive the REAL
// decision chain with the REAL whitelist (DefaultWhitelist), a REAL outbox
// (temp home, no Telegram transport — the fake transport), a REAL journal
// (state.db in the temp home), and a REAL executor (plain exec of the staged
// command against real files). The trap these tests exist to catch: a test
// that mocks its own decision logic would pass while the boundary drifts.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestApprovalGate builds the real gate over a temp home: real outbox
// pager (queueOutbox + stat), real executor (runPlain), real journal.
func newTestApprovalGate(t *testing.T) (*ApprovalGate, string) {
	t.Helper()
	home := t.TempDir()
	g := NewApprovalGate(home, NewOpJournal(Connect(home)))
	return g, home
}

// stageForApproval stages the given command against a real target file and
// returns the request id. The whitelist-miss is asserted with the REAL
// whitelist — the classifier is the boundary, not the test.
func stageForApproval(t *testing.T, g *ApprovalGate, ctx context.Context, command, target string) int64 {
	t.Helper()
	argv := strings.Fields(command)
	if DefaultWhitelist(g.home).Allows(argv) {
		t.Fatalf("test command %q must be a whitelist MISS for the boundary to be real", command)
	}
	out := g.Stage(ctx, command, target, "test reason")
	var id int64
	if !strings.Contains(out, "Approval request ") {
		t.Fatalf("Stage output = %q, want staged request", out)
	}
	idx := strings.Index(out, "request ")
	var err error
	id, err = parseID(t, out[idx+len("request "):])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func parseID(t *testing.T, s string) (int64, error) {
	t.Helper()
	var id int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		id = id*10 + int64(s[i]-'0')
	}
	if id == 0 && !strings.HasPrefix(s, "0") {
		return 0, errors.New("no id in " + s)
	}
	return id, nil
}

func TestApprovalStagePagesAndExecutesOnApprove(t *testing.T) {
	g, home := newTestApprovalGate(t)
	ctx := testCtx()

	// Real state: a file the approved op will delete. The command is a
	// genuine whitelist-miss (rm -rf of a home path is not the pinned
	// /bin/rm -f /etc/systemd/system shape).
	target := filepath.Join(home, "cache")
	if err := os.WriteFile(target, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	command := "rm -rf " + target

	id := stageForApproval(t, g, ctx, command, target)

	// The real outbox carried the page with the exact op and the reply format.
	page, err := os.ReadFile(filepath.Join(home, "outbox", "msg_owner.txt"))
	if err != nil {
		t.Fatalf("outbox page missing: %v", err)
	}
	for _, want := range []string{command, "Target: " + target, "approve " + itoa(id), "deny " + itoa(id)} {
		if !strings.Contains(string(page), want) {
			t.Fatalf("page = %q, want it to contain %q", page, want)
		}
	}

	// Pending: nothing executed yet.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target gone before approval: %v", err)
	}
	req, err := g.journal.Get(id)
	if err != nil || req.Status != OpStatusOK {
		t.Fatalf("request op = %+v, err %v — want staged (ok)", req, err)
	}
	var bs map[string]any
	json.Unmarshal([]byte(req.BeforeState), &bs)
	if bs["exists"] != true {
		t.Fatalf("request before-state = %v, want exists:true snapshot", bs)
	}

	// Approve: the owner's reply resolves BEFORE any loop involvement.
	reply, handled := g.ResolveReply("approve " + itoa(id))
	if !handled {
		t.Fatal("approve reply not handled")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists after approval: %v", err)
	}
	if !strings.Contains(reply, "executed") {
		t.Fatalf("approve reply = %q, want execution result", reply)
	}

	// Journal: the execution is its own op, undo_of = the request, with
	// real before/after evidence.
	exec, err := g.journal.LastOp(command)
	if err != nil || exec.OpType != "approval.exec" || exec.UndoOf != id || exec.Status != OpStatusOK {
		t.Fatalf("exec op = %+v, err %v — want approval.exec ok undo_of=%d", exec, err, id)
	}
	json.Unmarshal([]byte(exec.BeforeState), &bs)
	if bs["exists"] != true {
		t.Fatalf("exec before-state = %v, want exists:true", bs)
	}
	json.Unmarshal([]byte(exec.AfterState), &bs)
	if bs["exists"] != false {
		t.Fatalf("exec after-state = %v, want exists:false", bs)
	}

	// Double-approve: the request is consumed.
	if reply, handled := g.ResolveReply("approve " + itoa(id)); !handled || !strings.Contains(reply, "No pending") {
		t.Fatalf("second approve = %q handled=%v, want consumed", reply, handled)
	}
}

func TestApprovalDenyLeavesStateUntouched(t *testing.T) {
	g, home := newTestApprovalGate(t)
	ctx := testCtx()
	target := filepath.Join(home, "cache")
	if err := os.WriteFile(target, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	id := stageForApproval(t, g, ctx, "rm -rf "+target, target)

	reply, handled := g.ResolveReply("deny " + itoa(id))
	if !handled || !strings.Contains(reply, "denied") {
		t.Fatalf("deny reply = %q handled=%v", reply, handled)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target affected by denial: %v", err)
	}
	req, err := g.journal.Get(id)
	if err != nil || req.Status != OpStatusRolledBack {
		t.Fatalf("request op = %+v, err %v — want rolled_back", req, err)
	}
	if op, err := g.journal.LastOp("rm -rf " + target); err != nil || op.OpType == "approval.exec" {
		t.Fatalf("exec op after denial: %+v err %v — want only the rolled_back request", op, err)
	}
}

func TestApprovalTimeoutDeniesAndLeavesStateUntouched(t *testing.T) {
	g, home := newTestApprovalGate(t)
	ctx := testCtx()
	fixed := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return fixed }
	t.Setenv("MINO_APPROVAL_TIMEOUT_MINUTES", "30")
	target := filepath.Join(home, "cache")
	if err := os.WriteFile(target, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	id := stageForApproval(t, g, ctx, "rm -rf "+target, target)

	// Not yet expired: sweep leaves it pending.
	g.sweep(fixed.Add(29 * time.Minute))
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target gone before timeout: %v", err)
	}

	// Past the timeout: the safe default fires — deny + journal.
	g.sweep(fixed.Add(31 * time.Minute))
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target affected by timeout: %v", err)
	}
	req, err := g.journal.Get(id)
	if err != nil || req.Status != OpStatusRolledBack {
		t.Fatalf("request op = %+v, err %v — want rolled_back after timeout", req, err)
	}
	if reply, handled := g.ResolveReply("approve " + itoa(id)); !handled || !strings.Contains(reply, "No pending") {
		t.Fatalf("approve after timeout = %q handled=%v, want consumed/no-pending", reply, handled)
	}
}

func TestApprovalPagerFailureStillDeniesOnTimeout(t *testing.T) {
	// The map precondition: the pager is fallible, the flow must never
	// deadlock. A failed page keeps the request pending (the model's own
	// turn carries the id) and the timeout denies it.
	g, _ := newTestApprovalGate(t)
	g.page = func(msg string) error { return os.ErrPermission }
	target := filepath.Join(t.TempDir(), "x")
	os.WriteFile(target, []byte("stale"), 0600)
	id := stageForApproval(t, g, testCtx(), "rm -rf "+target, target)

	g.sweep(time.Now().Add(24 * time.Hour))
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target affected despite failed pager: %v", err)
	}
	req, err := g.journal.Get(id)
	if err != nil || req.Status != OpStatusRolledBack {
		t.Fatalf("request op = %+v, err %v — want rolled_back", req, err)
	}
}

func TestApprovalStageRefusesInvalidCommands(t *testing.T) {
	g, home := newTestApprovalGate(t)
	ctx := testCtx()
	target := filepath.Join(home, "cache")
	os.WriteFile(target, []byte("stale"), 0600)

	cases := []struct{ cmd, want string }{
		{"sudo rm -rf " + target, "sudo is refused"},
		{"rm -rf " + target + " | tee /tmp/x", "shell operators"},
		{"rm -rf " + target + " > /dev/null", "shell operators"},
		{"echo `id`", "shell operators"},
		{"", "cannot be empty"},
	}
	for _, c := range cases {
		out := g.Stage(ctx, c.cmd, target, "reason")
		if !strings.Contains(out, c.want) {
			t.Fatalf("Stage(%q) = %q, want refusal containing %q", c.cmd, out, c.want)
		}
	}
	if out := g.Stage(ctx, "rm -rf "+target, "", "reason"); !strings.Contains(out, "target is required") {
		t.Fatalf("empty-target stage = %q, want refusal", out)
	}
	if out := g.Stage(ctx, "rm -rf "+target, target, "  "); !strings.Contains(out, "reason is required") {
		t.Fatalf("empty-reason stage = %q, want refusal", out)
	}
	// Refusals journal nothing — nothing was staged.
	if _, err := g.journal.LastOp("rm -rf " + target); err == nil {
		t.Fatal("refused stages must not journal")
	}
}

func TestApprovalReplyUnknownAndOrdinaryMessages(t *testing.T) {
	g, _ := newTestApprovalGate(t)
	if reply, handled := g.ResolveReply("approve 999"); !handled || !strings.Contains(reply, "No pending approval request 999") {
		t.Fatalf("unknown approve = %q handled=%v", reply, handled)
	}
	if reply, handled := g.ResolveReply("deny 999"); !handled || !strings.Contains(reply, "No pending") {
		t.Fatalf("unknown deny = %q handled=%v", reply, handled)
	}
	// Ordinary messages (even approval-shaped words) are NOT consumed.
	for _, msg := range []string{"please approve the request", "approve it", "what is 3?", "Approve:"} {
		if _, handled := g.ResolveReply(msg); handled {
			t.Fatalf("ordinary message %q was consumed as an approval reply", msg)
		}
	}
}

func TestReconcileStaleApprovals(t *testing.T) {
	j := NewOpJournal(Connect(t.TempDir()))
	// Orphaned request: staged, never decided.
	orphan, err := j.Run(&OpEntry{OpType: "approval.request", Entity: "rm -rf /tmp/orphan", BeforeState: "{}", AfterState: `{"state":"pending"}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Resolved request: has an approval.exec child.
	done, err := j.Run(&OpEntry{OpType: "approval.request", Entity: "rm -rf /tmp/done", BeforeState: "{}", AfterState: `{"state":"pending"}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Run(&OpEntry{OpType: "approval.exec", Entity: "rm -rf /tmp/done", BeforeState: "{}", AfterState: "{}", UndoOf: done}, nil); err != nil {
		t.Fatal(err)
	}
	// Already denied: must stay rolled_back.
	denied, err := j.Run(&OpEntry{OpType: "approval.request", Entity: "rm -rf /tmp/denied", BeforeState: "{}", AfterState: `{"state":"pending"}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	j.SetStatus(denied, OpStatusRolledBack)

	if n := ReconcileStaleApprovals(j); n != 1 {
		t.Fatalf("reconciled %d, want 1", n)
	}
	for id, want := range map[int64]string{orphan: OpStatusRolledBack, done: OpStatusOK, denied: OpStatusRolledBack} {
		op, err := j.Get(id)
		if err != nil || op.Status != want {
			t.Fatalf("op %d = %+v (err %v), want status %s", id, op, err, want)
		}
	}
}

func TestRequestApprovalToolThroughRegistry(t *testing.T) {
	// The real tool boundary: the model's only path to the gate is the
	// registry — the tool must stage through it, not around it.
	g, home := newTestApprovalGate(t)
	registry := NewRegistry()
	registry.Register(behaves(makeRequestApprovalTool(g), BehaviorMutate))

	target := filepath.Join(home, "cache")
	if err := os.WriteFile(target, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	command := "rm -rf " + target
	out := registry.ExecuteContext(testCtx(), "request_approval", map[string]any{
		"command": command, "target": target, "reason": "clear stale cache",
	})
	if !strings.Contains(out, "Approval request ") {
		t.Fatalf("tool output = %q, want staged request", out)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target gone at stage time: %v", err)
	}

	// Resolve exactly as the message boundary would.
	reply, handled := g.ResolveReply("approve " + strings.Fields(out)[2])
	if !handled {
		t.Fatal("approve not handled")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists after approve: %v", err)
	}
	if !strings.Contains(reply, "executed") {
		t.Fatalf("reply = %q, want execution result", reply)
	}
}

func itoa(n int64) string {
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if len(b) == 0 {
		return "0"
	}
	return string(b)
}
