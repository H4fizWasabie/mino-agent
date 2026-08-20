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
	// ARCH-001 (#290): the stage tool boundary. A playbook script runs with
	// MINO_EXEC_ALLOWED_TOOLS set to its stage's whitelist; mino exec refuses
	// anything outside it (never silent — the model sees the reason and
	// rewrites). Unset/empty = unrestricted (chat turns, interactive exec).
	if allow := os.Getenv("MINO_EXEC_ALLOWED_TOOLS"); allow != "" {
		if !execToolAllowed(args[0], allow) {
			fmt.Fprintf(os.Stderr, "Error: tool %q is not allowed in this stage (whitelist: %s)", args[0], allow)
			return 1
		}
	}
	ctx := context.WithValue(context.Background(), sessionIDKey{}, execSession())
	out := w.Tools.ExecuteContext(ctx, args[0], argv)
	out = maybeConvertVision(ctx, out, argv, w.Client, execSession())
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

// execToolAllowed is the ARCH-001 stage boundary: a tool name passes only if
// the whitelist (comma list from MINO_EXEC_ALLOWED_TOOLS) contains it.
// Pure function so the boundary is unit-testable without a subprocess.
func execToolAllowed(tool, whitelist string) bool {
	for _, name := range strings.Split(whitelist, ",") {
		if name == tool {
			return true
		}
	}
	return false
}

// maybeConvertVision (T8, map #88): the main brain never carries image
// bytes. With code mode (#271) the loop's per-call conversion moved here —
// a script's `mino exec view_image` would otherwise print the raw data URL
// into context. Convert to vision-model text before stdout.
func maybeConvertVision(ctx context.Context, out string, argv map[string]any, client LLMClient, sessionID string) string {
	if !strings.HasPrefix(out, "data:image/") || client == nil {
		return out
	}
	task, _ := argv["task"].(string)
	desc, err := describeImage(ctx, client, sessionID, out, task, 600)
	if err == nil {
		return "[view_image: " + desc + "]"
	}
	return "Error: vision analysis failed: " + err.Error()
}
