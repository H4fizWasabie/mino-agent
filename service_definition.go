package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var serviceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

type serviceDefinition struct {
	Name             string
	Executable       string
	Args             []string
	Environment      map[string]string
	WorkingDirectory string
	Restart          string
}

func parseServiceDefinition(args map[string]any) (serviceDefinition, error) {
	name, _ := args["name"].(string)
	executable, _ := args["executable"].(string)
	if !serviceNameRe.MatchString(name) {
		return serviceDefinition{}, fmt.Errorf("invalid service name %q", name)
	}
	if executable == "" || strings.ContainsAny(executable, "\x00\r\n") {
		return serviceDefinition{}, fmt.Errorf("executable must be a non-empty path without control characters")
	}
	d := serviceDefinition{Name: name, Executable: executable, Restart: "on-failure"}
	if raw, ok := args["args"].([]any); ok {
		for _, value := range raw {
			arg, ok := value.(string)
			if !ok || strings.ContainsRune(arg, '\x00') {
				return serviceDefinition{}, fmt.Errorf("args must contain only strings without NUL bytes")
			}
			d.Args = append(d.Args, arg)
		}
	}
	if raw, ok := args["environment"].(map[string]any); ok {
		d.Environment = make(map[string]string, len(raw))
		for key, value := range raw {
			text, ok := value.(string)
			if !ok || !envKeyRe.MatchString(key) || strings.ContainsRune(text, '\x00') {
				return serviceDefinition{}, fmt.Errorf("environment must use valid keys and string values without NUL bytes")
			}
			d.Environment[key] = text
		}
	}
	d.WorkingDirectory, _ = args["working_directory"].(string)
	if strings.ContainsAny(d.WorkingDirectory, "\x00\r\n") {
		return serviceDefinition{}, fmt.Errorf("working_directory contains control characters")
	}
	if restart, ok := args["restart"].(string); ok && restart != "" {
		switch restart {
		case "always", "on-failure", "never":
			d.Restart = restart
		default:
			return serviceDefinition{}, fmt.Errorf("restart must be always, on-failure, or never")
		}
	}
	return d, nil
}

func renderServiceDefinition(goos string, d serviceDefinition) (filename string, content string) {
	switch goos {
	case "darwin":
		return d.Name + ".plist", renderLaunchd(d)
	case "windows":
		return d.Name + ".service.txt", renderWindowsService(d)
	default:
		return d.Name + ".service", renderSystemd(d)
	}
}

func renderSystemd(d serviceDefinition) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=Mino managed service %s\n\n[Service]\nExecStart=%s", d.Name, shellQuoteArgs(append([]string{d.Executable}, d.Args...)))
	if d.WorkingDirectory != "" {
		fmt.Fprintf(&b, "\nWorkingDirectory=%s", d.WorkingDirectory)
	}
	for _, key := range sortedEnvKeys(d.Environment) {
		fmt.Fprintf(&b, "\nEnvironment=\"%s=%s\"", key, strings.ReplaceAll(d.Environment[key], "\"", "\\\""))
	}
	if d.Restart != "never" {
		fmt.Fprintf(&b, "\nRestart=%s", d.Restart)
	}
	return b.String() + "\n"
}

func renderLaunchd(d serviceDefinition) string {
	var program []string
	program = append(program, d.Executable)
	program = append(program, d.Args...)
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict>")
	fmt.Fprintf(&b, "<key>Label</key><string>%s</string><key>ProgramArguments</key><array>", xmlEscape(d.Name))
	for _, arg := range program {
		fmt.Fprintf(&b, "<string>%s</string>", xmlEscape(arg))
	}
	b.WriteString("</array>")
	if d.WorkingDirectory != "" {
		fmt.Fprintf(&b, "<key>WorkingDirectory</key><string>%s</string>", xmlEscape(d.WorkingDirectory))
	}
	if d.Restart != "never" {
		b.WriteString("<key>KeepAlive</key><true/>")
	}
	b.WriteString("</dict></plist>\n")
	return b.String()
}

func renderWindowsService(d serviceDefinition) string {
	return shellQuoteArgs(append([]string{d.Executable}, d.Args...))
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\"") {
			quoted[i] = `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

func sortedEnvKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func xmlEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(value)
}

func writeNativeService(ctx context.Context, h *HostTools, args map[string]any) string {
	d, err := parseServiceDefinition(args)
	if err != nil {
		return "Error: " + err.Error()
	}
	filename, content := renderServiceDefinition(runtime.GOOS, d)
	dir := filepath.Join(filepath.Dir(h.home), "Library", "LaunchAgents")
	if runtime.GOOS == "windows" {
		dir = filepath.Join(h.home, "services")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "Error: create service directory: " + err.Error()
	}
	target := filepath.Join(dir, filename)
	old, oldErr := os.ReadFile(target)
	if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		return "Error: write native service: " + err.Error()
	}
	rollbackFile := func() error {
		if oldErr == nil {
			return os.WriteFile(target, old, 0600)
		}
		return os.Remove(target)
	}
	cleanup := func() { _ = rollbackFile() }
	if runtime.GOOS == "darwin" {
		uid, uidErr := exec.CommandContext(ctx, "id", "-u").Output()
		if uidErr != nil {
			cleanup()
			return "Error: determine macOS service user: " + uidErr.Error()
		}
		domain := "user/" + strings.TrimSpace(string(uid))
		if _, err := h.plain(ctx, []string{"launchctl", "bootstrap", domain, target}); err != nil {
			cleanup()
			return "Error: load launchd service: " + err.Error()
		}
	} else {
		argv := []string{"sc.exe", "create", d.Name, "binPath=", content, "start=", "auto"}
		if d.Restart == "never" {
			argv[len(argv)-1] = "demand"
		}
		if !h.whitelisted(argv) {
			cleanup()
			return "Error: create service " + d.Name + notWhitelisted
		}
		if _, err := h.sudo(ctx, argv); err != nil {
			cleanup()
			return "Error: create Windows service: " + err.Error()
		}
	}
	entry := &OpEntry{OpType: "unit.write", Entity: d.Name, BeforeState: unitStateJSON(string(old), oldErr == nil), AfterState: unitStateJSON(content, true), SessionID: h.sessionID(ctx)}
	if _, err := h.journal.Run(entry, nil); err != nil {
		cleanup()
		if runtime.GOOS == "windows" {
			_, _ = h.sudo(ctx, []string{"sc.exe", "delete", d.Name})
		}
		return fmt.Sprintf("Error: journal write of %s: %v (service restored)", d.Name, err)
	}
	return fmt.Sprintf("Wrote native service %s to %s", d.Name, target)
}
