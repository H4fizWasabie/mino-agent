package main

// ext_supervisor.go — RUN-001 (GitHub #215): in-process extension lifecycle.
// Mino clones, builds, registers, and supervises its own extensions: child
// processes serving the §3 HTTP protocol (GET /tools, POST /execute) on
// localhost. The supervisor spawns them, health-checks (/tools reachable),
// restarts on crash (with backoff), and kills them on shutdown — lifecycle
// tracks Mino's, so boot reconciliation re-spawns everything in
// extensions.json. systemd-per-extension is a LATER opt-in tier (needs
// RUN-003's privilege bridge) and is deliberately not built here.
//
// Install path: clone (git) → build (go build) → register. Registration
// reuses the existing discovery/proxy machinery in extensions.go; the
// extensions.json write is journaled through OpJournal (RUN-002), and
// uninstall marks the install op rolled_back — the undo half.
//
// Conventions (decided here, recorded on #213): extensions are Go modules
// building with `go build -o <name> .` — matches every in-repo extension
// (minowrap has go.mod at root; cost-watch ships a prebuilt binary). The
// child receives its listen port as the PORT env var (minowrap's
// MINOWRAP_PORT is honored via the config's per-extension env override).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	extHealthTimeout = 20 * time.Second // max wait for /tools after spawn
	extKillGrace     = 3 * time.Second  // SIGTERM → SIGKILL
	extMaxBackoff    = 30 * time.Second // crash-restart backoff ceiling
)

var extNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// managedExt is one supervised extension's runtime state. The supervisor's
// runLoop goroutine owns cmd; kill() signals it through stop.
type managedExt struct {
	cfg         ExtensionConfig
	stop        chan struct{} // closed → never restart, exit the loop
	done        chan struct{} // closed by runLoop on exit
	cmd         *exec.Cmd     // current child (guarded by supervisor.mu)
	firstHealth chan error    // first spawn/register outcome, read by Install
}

// ExtensionSupervisor owns the extension child processes: one runLoop
// goroutine per supervised extension, install/uninstall serialized through
// opMu, all shared state guarded by mu.
type ExtensionSupervisor struct {
	home     string
	registry *Registry
	journal  *OpJournal
	mu       sync.Mutex
	opMu     sync.Mutex // serializes Install/Uninstall end-to-end (config read→write)
	managed  map[string]*managedExt
	tools    map[string][]string // extension name → registered tool names
}

func NewExtensionSupervisor(home string, r *Registry, j *OpJournal) *ExtensionSupervisor {
	return &ExtensionSupervisor{
		home:     home,
		registry: r,
		journal:  j,
		managed:  map[string]*managedExt{},
		tools:    map[string][]string{},
	}
}

// configs reads extensions.json (nil when absent or unparsable).
func (s *ExtensionSupervisor) configs() []ExtensionConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(s.home, "extensions.json"))
	if err != nil {
		return nil
	}
	var configs []ExtensionConfig
	if json.Unmarshal(data, &configs) != nil {
		return nil
	}
	return configs
}

// writeConfigs persists extensions.json. The journal's Run passes it as the
// mutate — the file write and the journal entry commit together as far as
// the journal contract reaches.
func (s *ExtensionSupervisor) writeConfigs(configs []ExtensionConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.home, "extensions.json"), data, 0600)
}

func (s *ExtensionSupervisor) configJSON(configs []ExtensionConfig) string {
	data, _ := json.Marshal(configs)
	return string(data)
}

// Start — boot reconciliation: spawn every supervised entry (Repo set) in
// extensions.json, asynchronously. URL-only entries stay manual and are
// discovered by LoadExtensions as before.
func (s *ExtensionSupervisor) Start() {
	for _, c := range s.configs() {
		if c.Repo == "" {
			continue
		}
		s.start(c)
	}
}

// start launches one runLoop goroutine for cfg.
func (s *ExtensionSupervisor) start(cfg ExtensionConfig) *managedExt {
	m := &managedExt{
		cfg:         cfg,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		firstHealth: make(chan error, 1),
	}
	s.mu.Lock()
	s.managed[cfg.Name] = m
	s.mu.Unlock()
	go s.runLoop(m)
	return m
}

// Install clones, builds, spawns, registers, and journal-commits cfg.
// The config write happens only after the extension is healthy, so a failed
// install never leaves a dead entry in extensions.json; the failed attempt
// is journaled with status=failed (before == after). On journal failure the
// child is killed, the clone removed, and the config restored — no op
// without an entry.
func (s *ExtensionSupervisor) Install(cfg ExtensionConfig, sessionID string) error {
	if cfg.Name == "" || cfg.Repo == "" {
		return errors.New("name and repo are required")
	}
	if !extNameRe.MatchString(cfg.Name) {
		return fmt.Errorf("invalid name %q (letters, digits, - and _ only)", cfg.Name)
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	for _, c := range s.configs() {
		if c.Name == cfg.Name {
			return fmt.Errorf("extension %q is already configured", cfg.Name)
		}
	}
	if cfg.Port == 0 {
		port, err := freePort()
		if err != nil {
			return fmt.Errorf("pick free port: %w", err)
		}
		cfg.Port = port
	}
	dir := filepath.Join(s.home, "extensions", cfg.Name)
	os.RemoveAll(dir) // stale clone from a crashed install must not block re-install
	if err := runCmd("", "git", "clone", "--depth", "1", cfg.Repo, dir); err != nil {
		return fmt.Errorf("clone %s: %w", cfg.Repo, err)
	}
	if err := runCmd(dir, "go", "build", "-o", cfg.Name, "."); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("build failed (extensions must be Go modules building with `go build -o <name> .`): %w", err)
	}

	m := s.start(cfg)
	select {
	case err := <-m.firstHealth:
		if err != nil {
			// Spawned but never served /tools: record the failed op, then
			// tear child and clone down — the caller retries.
			s.recordFailedOp(cfg, sessionID)
			s.kill(cfg.Name)
			os.RemoveAll(dir)
			return fmt.Errorf("extension never became healthy: %w", err)
		}
	case <-time.After(extHealthTimeout):
		s.recordFailedOp(cfg, sessionID)
		s.kill(cfg.Name)
		os.RemoveAll(dir)
		return fmt.Errorf("extension not healthy within %s", extHealthTimeout)
	}

	configs := s.configs()
	entry := &OpEntry{
		OpType:      "extension.install",
		Entity:      cfg.Name,
		BeforeState: s.configJSON(configs),
		AfterState:  s.configJSON(append(configs, cfg)),
		SessionID:   sessionID,
	}
	if _, err := s.journal.Run(entry, func(tx *sql.Tx) error { return s.writeConfigs(append(configs, cfg)) }); err != nil {
		// Journal is the record of truth: tear the op back down.
		s.writeConfigs(configs)
		s.kill(cfg.Name)
		s.unregisterTools(cfg.Name) // no proxy closures pointing at a dead child
		os.RemoveAll(dir)
		return fmt.Errorf("journal install: %w", err)
	}
	slog.Info("extension installed", "name", cfg.Name, "repo", cfg.Repo, "port", cfg.Port)
	return nil
}

// recordFailedOp journals an install that never became healthy (before ==
// after — the config never changed).
func (s *ExtensionSupervisor) recordFailedOp(cfg ExtensionConfig, sessionID string) {
	configs := s.configs()
	s.journal.Run(&OpEntry{
		OpType:      "extension.install",
		Entity:      cfg.Name,
		BeforeState: s.configJSON(configs),
		AfterState:  s.configJSON(configs),
		Status:      OpStatusFailed,
		SessionID:   sessionID,
	}, func(tx *sql.Tx) error { return s.writeConfigs(configs) })
}

// Uninstall kills the child, removes the config, unregisters the tools,
// journal-commits the removal, and marks the original install op
// rolled_back — the undo half (carry-forward from RUN-002 review).
func (s *ExtensionSupervisor) Uninstall(name, sessionID string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	configs := s.configs()
	idx := -1
	for i, c := range configs {
		if c.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("extension %q is not configured", name)
	}
	after := append(append([]ExtensionConfig{}, configs[:idx]...), configs[idx+1:]...)

	entry := &OpEntry{
		OpType:      "extension.uninstall",
		Entity:      name,
		BeforeState: s.configJSON(configs),
		AfterState:  s.configJSON(after),
		SessionID:   sessionID,
	}
	if last, err := s.journal.LastOp(name); err == nil && last.OpType == "extension.install" && last.Status == OpStatusOK {
		entry.UndoOf = last.ID
	}
	if _, err := s.journal.Run(entry, func(tx *sql.Tx) error { return s.writeConfigs(after) }); err != nil {
		s.writeConfigs(configs) // restore — no op without an entry
		return fmt.Errorf("journal uninstall: %w", err)
	}
	s.kill(name)
	s.unregisterTools(name)
	os.RemoveAll(filepath.Join(s.home, "extensions", name))
	if entry.UndoOf != 0 {
		s.journal.SetStatus(entry.UndoOf, OpStatusRolledBack)
	}
	slog.Info("extension uninstalled", "name", name)
	return nil
}

// Shutdown kills every supervised child — called from Core.Close. The
// runLoop goroutines exit via their stop channels.
func (s *ExtensionSupervisor) Shutdown() {
	s.mu.Lock()
	names := make([]string, 0, len(s.managed))
	for n := range s.managed {
		names = append(names, n)
	}
	s.mu.Unlock()
	for _, n := range names {
		s.kill(n)
	}
}

// kill stops one extension for good: close its stop channel, SIGTERM the
// child, SIGKILL after the grace period if it ignores the request. No-op
// for unknown names.
func (s *ExtensionSupervisor) kill(name string) {
	s.mu.Lock()
	m, ok := s.managed[name]
	if ok {
		delete(s.managed, name)
	}
	var cmd *exec.Cmd // m.cmd is set under mu in runLoop
	if ok {
		cmd = m.cmd
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	close(m.stop)
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-m.done:
	case <-time.After(extKillGrace):
		if cmd != nil && cmd.Process != nil {
			cmd.Process.Kill() // no-op error if already exited
		}
		<-m.done
	}
}

// runLoop is one supervised extension's lifetime: spawn → health-check →
// register tools → wait for exit → backoff → respawn, until stop closes.
// Restarts do NOT re-register tools: the proxy closures capture the URL,
// which is unchanged, so registrations survive the process swap.
func (s *ExtensionSupervisor) runLoop(m *managedExt) {
	defer close(m.done)
	backoff := time.Second
	first := true
	for {
		cmd, err := s.spawnAndRegister(m)
		if err != nil {
			if first {
				m.firstHealth <- err
				first = false
			}
			slog.Warn("extension spawn failed", "name", m.cfg.Name, "error", err, "retry_in", backoff)
			if !s.waitOrStop(m, backoff) {
				return
			}
			backoff = min(backoff*2, extMaxBackoff)
			continue
		}
		if first {
			m.firstHealth <- nil
			first = false
		}
		start := time.Now()
		err = cmd.Wait()
		select {
		case <-m.stop:
			return
		default:
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // was stable → restart promptly, not on the old backoff
		}
		slog.Warn("extension exited; restarting", "name", m.cfg.Name, "error", err, "retry_in", backoff)
		if !s.waitOrStop(m, backoff) {
			return
		}
		backoff = min(backoff*2, extMaxBackoff) // crash-loop ramp, same as the spawn-failure branch
	}
}

func (s *ExtensionSupervisor) waitOrStop(m *managedExt, d time.Duration) bool {
	select {
	case <-m.stop:
		return false
	case <-time.After(d):
		return true
	}
}

// spawnAndRegister starts the child and waits for /tools to answer (the
// health check), then registers its tools. On any failure the child is
// killed and reaped before returning.
func (s *ExtensionSupervisor) spawnAndRegister(m *managedExt) (*exec.Cmd, error) {
	cfg := m.cfg
	cmd := exec.Command(filepath.Join(s.home, "extensions", cfg.Name, cfg.Name))
	cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(cfg.Port))
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = os.Stderr // child logs land in Mino's journald stream
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	killAndWait := func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
	var tools []ExtensionTool
	deadline := time.Now().Add(extHealthTimeout)
	for {
		var err error
		if tools, err = discoverTools(url); err == nil {
			break
		}
		select {
		case <-m.stop:
			killAndWait()
			return nil, errors.New("stopped during health check")
		case <-time.After(300 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			killAndWait()
			return nil, fmt.Errorf("no /tools response within %s", extHealthTimeout)
		}
	}
	s.registerTools(cfg.Name, url, tools)
	s.mu.Lock()
	m.cmd = cmd
	s.mu.Unlock()
	return cmd, nil
}

func (s *ExtensionSupervisor) registerTools(name, url string, tools []ExtensionTool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[name] = nil
	for _, et := range tools {
		s.tools[name] = append(s.tools[name], et.Name)
		t := et
		s.registry.Register(&Tool{
			Name:        t.Name,
			Description: t.Description,
			Schema:      t.Schema,
			Fn: func(args map[string]any) string {
				return proxyExecute(url, t.Name, args)
			},
		})
		slog.Info("extension tool registered", "tool", t.Name, "extension", name)
	}
}

func (s *ExtensionSupervisor) unregisterTools(name string) {
	s.mu.Lock()
	names := s.tools[name]
	delete(s.tools, name)
	s.mu.Unlock()
	for _, n := range names {
		s.registry.Unregister(n)
	}
}

// freePort asks the kernel for a free localhost port.
// ponytail: TOCTOU window between close and child bind; the supervisor's
// restart loop absorbs a collision, revisit only if it ever bites.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func runCmd(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2000 {
			msg = msg[:2000] + "..."
		}
		if msg != "" {
			return fmt.Errorf("%s: %s", err, msg)
		}
		return err
	}
	return nil
}

// makeManageExtensionTool is the harness tool for RUN-001 — the LLM drives
// installs/uninstalls; the harness owns clone/build/spawn/register/journal.
func makeManageExtensionTool(sup *ExtensionSupervisor) *Tool {
	return &Tool{
		Name:        "manage_extension",
		Description: "Install or uninstall a Mino extension. Extensions are git repos cloned to ~/.mino/extensions/, built with `go build -o <name> .` (Go module at repo root), spawned as child processes on localhost, health-checked via GET /tools, restarted on crash, and killed on Mino shutdown. The extension must read its listen port from the PORT env var (or declare a per-extension env override). Install: action=install, repo=<git clone URL>, name=<optional, defaults to repo basename>, port=<optional, defaults to a free port>, env=<optional {VAR: value} map>. Uninstall: action=uninstall, name=<name> — kills the process, removes the config and clone, unregisters the tools, and marks the original install rolled back in the operation journal.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"install", "uninstall"}, "description": "install: clone+build+spawn+register; uninstall: kill+remove+unregister"},
				"name":   map[string]any{"type": "string", "description": "Extension name (config key, clone dir, binary name). Required for uninstall; defaults to the repo basename for install."},
				"repo":   map[string]any{"type": "string", "description": "Git clone URL of the extension. Required for install."},
				"port":   map[string]any{"type": "integer", "description": "Listen port on 127.0.0.1. Defaults to a free port. Passed to the child as PORT."},
				"env":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Extra environment variables for the child process (e.g. {\"MINOWRAP_PORT\": \"9876\"})."},
			},
			"required": []string{"action"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			action, _ := args["action"].(string)
			sessionID := ""
			if v := ctx.Value(sessionIDKey{}); v != nil {
				sessionID, _ = v.(string)
			}
			switch action {
			case "install":
				name, _ := args["name"].(string)
				repo, _ := args["repo"].(string)
				if repo == "" {
					return "Error: install requires a repo (git clone URL)."
				}
				if name == "" {
					name = strings.TrimSuffix(filepath.Base(repo), ".git")
					if name == "" || name == "." {
						return "Error: cannot derive an extension name from repo; pass name explicitly."
					}
				}
				cfg := ExtensionConfig{Name: name, Repo: repo}
				if p, ok := args["port"].(float64); ok && p > 0 {
					cfg.Port = int(p)
				}
				if env, ok := args["env"].(map[string]any); ok {
					cfg.Env = map[string]string{}
					for k, v := range env {
						cfg.Env[k] = fmt.Sprint(v)
					}
				}
				if err := sup.Install(cfg, sessionID); err != nil {
					return fmt.Sprintf("Extension install failed: %v", err)
				}
				return fmt.Sprintf("Extension %q installed and healthy — tools registered. It is supervised: restarted on crash, killed on Mino shutdown.", cfg.Name)
			case "uninstall":
				name, _ := args["name"].(string)
				if name == "" {
					return "Error: uninstall requires a name."
				}
				if err := sup.Uninstall(name, sessionID); err != nil {
					return fmt.Sprintf("Extension uninstall failed: %v", err)
				}
				return fmt.Sprintf("Extension %q uninstalled: process killed, config and clone removed, tools unregistered, install op marked rolled back in the journal.", name)
			default:
				return "Error: action must be \"install\" or \"uninstall\"."
			}
		},
	}
}
