package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// exec.go — `mino exec <tool> [args-json]` (SCR-001, #272): the stub layer
// through which playbook scripts call tools. One binary front door — the
// registry, DB, MCP bridge and extensions boot (the NewCore path, same as
// `mino remember`), one tool executes, and the call lands in tool_calls +
// audit.jsonl like any loop call. No loop, no Telegram, no scheduler: the
// process exits after the call, so scheduled dispatch never runs inside an
// exec subprocess.
//
// Exit contract (binary, never-silent): 0 when the tool ran clean, 1 when
// the result starts with "Error:" or the invocation itself failed. Scripts
// read one bit: `if ! mino exec post "..."; then exit 1; fi`.

// runExecTool executes one tool call and returns the process exit code.
func runExecTool(w *Core, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: mino exec <tool> [args-json]")
		return 1
	}
	argv := map[string]any{}
	if len(args) > 1 {
		if err := json.Unmarshal([]byte(args[1]), &argv); err != nil {
			fmt.Fprintf(os.Stderr, "mino exec: args must be a JSON object: %v\n", err)
			return 1
		}
	}
	ctx := context.WithValue(context.Background(), sessionIDKey{}, execSession())
	out := w.Tools.ExecuteContext(ctx, args[0], argv)
	fmt.Println(out)
	if execFailed(out) {
		return 1
	}
	return 0
}

// execSession attributes the call to its script's run when the scheduler
// exported MINO_EXEC_SESSION; interactive calls land under cli-exec.
func execSession() string {
	if s := os.Getenv("MINO_EXEC_SESSION"); s != "" {
		return s
	}
	return "cli-exec"
}

// execFailed is the tool-result contract: an "Error:" prefix means the call
// failed. Binary by design — scripts branch on one bit and never parse
// message text.
func execFailed(out string) bool { return strings.HasPrefix(out, "Error:") }
