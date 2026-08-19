package main

import (
	"context"
	"strconv"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// script_mode.go — code mode (#271, CDE-001): the model's only action is
// writing bash scripts. The registry renders itself as a compact stub
// module (replacing the JSON tools array), the loop extracts [script]..
// [/script] markers from the model's text, a denylist gate scans the script
// BEFORE execution, and the harness runs it in a subprocess with visible
// stdout/stderr — failures are debuggable by construction (the CTX-025
// degeneration class: malformed JSON tool calls, empty args, raw payloads —
// cannot exist when there is no serialization step at all).

// loopScriptTimeout bounds one model-written script (interactive loop).
// Shorter than the scheduled-playbook 30min: the loop must converge per
// iteration budget. A var so tests can shorten it.
var loopScriptTimeout = 5 * time.Minute

// maxScriptOutput caps what comes back into context per script run — the
// loop's own context-bloat guard (CTX-025).
const maxScriptOutput = 8000

// ---------------------------------------------------------------------------
// Stub module

// StubModule renders the registry as the code-mode stub: a compact header
// (the operating contract) + one line per tool. The loop appends it to the
// system prompt — byte-stable per boot, so the provider prefix cache stays
// warm. Stage runs pass a filtered registry (Tools.Only), so the stub is
// stage-scoped automatically; chat runs get the full registry.
func (r *Registry) StubModule() string {
	r.toolsMu.RLock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	r.toolsMu.RUnlock()
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(scriptModeHeader)
	if len(names) > 0 {
		b.WriteString("\n\n## Stub module (tools callable from scripts)\n")
		b.WriteString("Call pattern: `mino exec <tool> '<json-args>'` — the tool runs inside the binary and its result is printed to stdout. Exit code 0 = clean, 1 = failure (result starts with Error:).\n")
		r.toolsMu.RLock()
		defer r.toolsMu.RUnlock()
		for _, name := range names {
			t := r.tools[name]
			desc := strings.TrimSpace(t.Description)
			if i := strings.Index(desc, "."); i > 0 {
				desc = desc[:i]
			}
			if len(desc) > 90 {
				desc = desc[:90] + "…"
			}
			line := "- `" + name + "` — " + desc
			if t.Schema != nil {
				if props, ok := t.Schema["properties"].(map[string]any); ok {
					keys := make([]string, 0, len(props))
					for k := range props {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					line += " (args: " + strings.Join(keys, ", ") + ")"
				}
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// scriptModeHeader is the code-mode operating contract the model sees in
// every system prompt (chat + playbook runs).
const scriptModeHeader = `## CODE MODE (absolute)
You act by writing bash scripts. To perform work, emit ONE script between [script] and [/script] markers — the harness executes it (bounded timeout) and shows you the stdout/stderr. Inside a script, call tools via ` + "`mino exec <tool> '<json-args>'`" + ` (listed in the stub module below). Rules:
- Every number and fact in your reply must trace to script output you actually saw — never invent results.
- A script that fails (non-zero exit) shows its stderr: fix the script, do not repeat it unchanged.
- Do NOT emit [tool_call: ...] markers or JSON tool calls — they are no longer parsed.
- When the work is done, reply in plain text with no script marker.`

// ---------------------------------------------------------------------------
// Script marker extraction

var scriptMarkerRe = regexp.MustCompile(`(?s)\[script\](.*?)\[/script\]`)

// extractScriptMarkers returns the scripts between [script]..[/script]
// markers. found reports whether any marker appeared; malformed reports a
// marker that produced an empty script (the model's call was dropped, not
// absent — the parse-failure machinery treats it like a broken tool call);
// legacy reports the retired [tool_call: ...] protocol, which gets a
// corrective push instead of a silent no-op.
func extractScriptMarkers(text string) (scripts []string, found, malformed, legacy bool) {
	legacy = strings.Contains(text, "[tool_call:")
	matches := scriptMarkerRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil, false, false, legacy
	}
	for _, m := range matches {
		body := strings.TrimSpace(m[1])
		if body == "" {
			malformed = true
			continue
		}
		scripts = append(scripts, body)
	}
	return scripts, true, malformed, legacy
}

// ---------------------------------------------------------------------------
// Classification gate (CDE-001): a static denylist scan BEFORE execution.
// Flagged scripts never run — the model gets the reason and rewrites. This
// is a mechanical backstop UNDER the review floor, not a whitelist: scripts
// legitimately do complex things.

var gatePatterns = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`\brm\s+(-[a-z]*r)`), "recursive rm (-r)"},
	{regexp.MustCompile(`\brm\s+(-[a-z]*f)`), "forced rm (-f)"},
	{regexp.MustCompile(`:>\s*/`), "truncate to absolute path"},
	{regexp.MustCompile(`>>?\s*/(etc|dev|boot|proc|sys|usr|var|home|root)(\s|$|/)`), "redirect into a system directory"},
	{regexp.MustCompile(`\bdd\s+if=[^\s]+\s+of=`), "dd device write"},
	{regexp.MustCompile(`\bmkfs\b`), "mkfs"},
	{regexp.MustCompile(`\b(shutdown|reboot|poweroff|halt)\b`), "system shutdown"},
	{regexp.MustCompile(`\bcurl[^\n]*\|\s*(ba)?sh\b`), "curl-pipe-to-shell"},
	{regexp.MustCompile(`\bchmod\s+-R\b|\bchown\s+-R\b`), "recursive chmod/chown"},
	{regexp.MustCompile(`\bmv\s+[^\n]*(mino\.env|providers\.json|state\.db|schedules\.json)\b`), "moving config/secrets"},
	{regexp.MustCompile(`\b(rm|mv|cp)\s+[^\n]*\.mino\b`), "touching the .mino home"},
}

// gateScript returns "" when the script is allowed to run, else the reason.
func gateScript(script string) string {
	for _, g := range gatePatterns {
		if g.re.MatchString(script) {
			return g.what
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Loop script execution

// runLoopScript executes one model-written script with the minimal env
// (secrets never reach scripts — systemd's EnvironmentFile=mino.env puts
// every token in the process env; only the essentials ride along). Returns
// the combined output (bounded) and the exit code.
func runLoopScript(ctx context.Context, script, sessionID string) (string, int) {
	ctx, cancel := context.WithTimeout(ctx, loopScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TZ=" + os.Getenv("TZ"),
		"LANG=" + os.Getenv("LANG"),
		"MINO_EXEC_SESSION=" + sessionID,
	}
	out, err := cmd.CombinedOutput()
	output := string(out)
	if len(output) > maxScriptOutput {
		output = output[:maxScriptOutput] + "\n…(output truncated)"
	}
	if ctx.Err() != nil {
		return output + "\nError: script timed out after " + loopScriptTimeout.String(), 1
	}
	if err == nil {
		return output, 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return output, ee.ExitCode()
	}
	return output + "\nError: " + err.Error(), 1
}

// LogScriptRun records one loop script execution in tool_calls under the
// session (the observational probe: self-correction rate = script errors
// followed by fixed scripts within the iteration cap, read from traces +
// this table). Name "script" keeps the dashboard/alerts machinery uniform.
func (r *Registry) LogScriptRun(ctx context.Context, sessionID, script, output string, status string) {
	if r.logDB == nil {
		return
	}
	summary := output
	if len(summary) > 200 {
		summary = summary[:200]
	}
	argsJSON := `{"head":` + strconv.Quote(firstScriptLine(script)) + `}`
	iter := 0
	if v := ctx.Value(iterationKey{}); v != nil {
		iter, _ = v.(int)
	}
	r.logDB.Exec("INSERT INTO tool_calls (session_id, tool_name, args, output_summary, status, iteration) VALUES (?,?,?,?,?,?)",
		sessionID, "script", argsJSON, summary, status, iter)
}

func firstScriptLine(script string) string {
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			if len(t) > 60 {
				t = t[:60]
			}
			return t
		}
	}
	return ""
}
