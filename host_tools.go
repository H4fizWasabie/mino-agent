package main

// host_tools.go — RUN-003 (GitHub #217): harness-native host tools over the
// privilege bridge (privilege.go). install_package, write_unit, and
// restart_service are the ONLY way Mino touches host state: every call is
// arg-validated, whitelist-checked (membership = the autonomous/approval
// boundary RUN-006 reads later), and journaled through OpJournal (RUN-002)
// with before/after state. The model never calls sudo itself — the bash
// tool refuses it outright.
//
// Journal discipline: the host mutation happens first, then journal.Run
// commits the record; on journal failure the op is torn back down (the
// same pattern as RUN-001's install teardown) — no op without an entry.
// restart_service is the exception, and deliberately inverted: the intent
// is journaled BEFORE asking systemd, because a successful restart kills
// this process — the entry is the record boot reconciliation finds.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// HostTools carries the privilege bridge's seams. The exported fields are
// the test injection points; NewHostTools wires the real implementations.
type HostTools struct {
	home     string
	journal  *OpJournal
	stageDir string // unit staging dir (default <home>/tmp — pinned by the whitelist)
	unitDir  string // unit install dir (default /etc/systemd/system)

	sudo     func(ctx context.Context, argv []string) (string, error)             // privileged runner
	plain    func(ctx context.Context, argv []string) (string, error)             // unprivileged runner (probes)
	probe    func(ctx context.Context, pkg string) bool                           // package installed?
	resolve  func(ctx context.Context, name string) (id, state string, err error) // systemctl show
	check    func(argv []string) bool                                             // whitelist membership
	platform hostPlatform
	expected string // MINO_SERVICE — the only unit restart_service will touch
}

const (
	hostOpTimeout = 5 * time.Minute // apt-get installs can be slow
	unitMaxBytes  = 1 << 20         // unit files are small; cap the write
)

func NewHostTools(home string, j *OpJournal) *HostTools {
	platform := currentHostPlatform()
	expected := envOr("MINO_SERVICE", "mino.service")
	if runtime.GOOS != "linux" {
		expected = nativeServiceName(expected)
	}
	return &HostTools{
		home:     home,
		journal:  j,
		stageDir: filepath.Join(home, "tmp"),
		unitDir:  "/etc/systemd/system",
		sudo:     platform.sudo,
		plain:    runPlain,
		probe:    platform.probe,
		resolve:  platform.resolve,
		check:    func(argv []string) bool { return platform.allow(home, argv) },
		platform: platform,
		expected: expected,
	}
}

func (h *HostTools) packageInstallArgv(pkg string) []string {
	if h.platform.install != nil {
		return h.platform.install(pkg)
	}
	return []string{"/usr/bin/apt-get", "install", "-y", pkg}
}

func (h *HostTools) packageRemoveArgv(pkg string) []string {
	if h.platform.remove != nil {
		return h.platform.remove(pkg)
	}
	return []string{"/usr/bin/apt-get", "remove", "-y", pkg}
}

func (h *HostTools) restartArgv(name string) []string {
	if h.platform.restart != nil {
		return h.platform.restart(name)
	}
	return []string{"/usr/bin/systemctl", "restart", name}
}

func (h *HostTools) activeArgv(name string) []string {
	if h.platform.active != nil {
		return h.platform.active(name)
	}
	return []string{"/usr/bin/systemctl", "is-active", name}
}

// whitelisted reports whether the harness may invoke argv via sudo.
func (h *HostTools) whitelisted(argv []string) bool { return h.check(argv) }

// notWhitelisted is the refusal every host tool shares — membership is the
// boundary; outside it the op waits for the owner (RUN-006's approval tier).
const notWhitelisted = " not in the privilege whitelist (the sudoers whitelist is the autonomous/approval boundary — approval never grants root) — the owner must extend the whitelist for privileged ops; risky unprivileged ops go through request_approval"

func (h *HostTools) sessionID(ctx context.Context) string {
	if v := ctx.Value(sessionIDKey{}); v != nil {
		if sid, ok := v.(string); ok {
			return sid
		}
	}
	return ""
}

// --- install_package ---

func makeInstallPackageTool(h *HostTools) *Tool {
	return &Tool{
		Name:        "install_package",
		Description: "Install a Debian/Ubuntu package via apt-get as root (through Mino's sudoers command whitelist — the package name is validated and the exact command is journaled with before/after state). Only whitelisted operations are autonomous; anything else is refused for the owner to approve. Prefer this over bash + sudo (which is refused).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"package": map[string]any{"type": "string", "description": "Debian package name to install (e.g. 'jq')."},
			},
			"required": []string{"package"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			pkg, _ := args["package"].(string)
			if !pkgNameRe.MatchString(pkg) {
				return "Error: invalid package name " + fmt.Sprintf("%q", pkg) + " (lower-case letters, digits, + . - only)"
			}
			argv := h.packageInstallArgv(pkg)
			if !h.whitelisted(argv) {
				return "Error: apt-get install of " + pkg + notWhitelisted
			}
			wasInstalled := h.probe(ctx, pkg)
			ctx, cancel := context.WithTimeout(ctx, hostOpTimeout)
			defer cancel()
			out, err := h.sudo(ctx, argv)
			if err != nil {
				h.journal.Run(&OpEntry{
					OpType:      "package.install",
					Entity:      pkg,
					BeforeState: installStateJSON(wasInstalled),
					AfterState:  installStateJSON(wasInstalled),
					Status:      OpStatusFailed,
					SessionID:   h.sessionID(ctx),
				}, nil)
				return fmt.Sprintf("Error: install %s failed: %v", pkg, err)
			}
			installed := h.probe(ctx, pkg)
			entry := &OpEntry{
				OpType:      "package.install",
				Entity:      pkg,
				BeforeState: installStateJSON(wasInstalled),
				AfterState:  installStateJSON(installed),
				SessionID:   h.sessionID(ctx),
			}
			if _, err := h.journal.Run(entry, nil); err != nil {
				// Journal is the record of truth: tear the op back down.
				if !wasInstalled {
					teardown := h.packageRemoveArgv(pkg)
					if h.whitelisted(teardown) {
						if _, terr := h.sudo(ctx, teardown); terr != nil {
							slog.Error("install_package teardown failed", "package", pkg, "error", terr)
						}
					} else {
						slog.Error("install_package teardown impossible: apt-get remove not whitelisted", "package", pkg)
					}
				}
				return fmt.Sprintf("Error: journal install of %s: %v (operation rolled back)", pkg, err)
			}
			return fmt.Sprintf("Installed %s via apt-get. %s", pkg, out)
		},
	}
}

func installStateJSON(installed bool) string {
	b, _ := json.Marshal(map[string]bool{"installed": installed})
	return string(b)
}

// --- write_unit ---

func makeWriteUnitTool(h *HostTools) *Tool {
	return &Tool{
		Name:        "write_unit",
		Description: "Write or replace a native service definition through Mino's fixed host-operation boundary. Provide a service name, executable, optional arguments/environment/working directory, and restart policy; Mino renders systemd, launchd, or Windows Service configuration for the host. The operation is journaled and restored on journal failure. Use with restart_service to apply. Raw systemd content remains accepted only for backward compatibility on Linux.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":              map[string]any{"type": "string", "description": "Service name, e.g. 'mino'."},
				"executable":        map[string]any{"type": "string", "description": "Absolute or host-resolvable executable path."},
				"args":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"environment":       map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"working_directory": map[string]any{"type": "string"},
				"restart":           map[string]any{"type": "string", "enum": []string{"always", "on-failure", "never"}},
				"content":           map[string]any{"type": "string", "description": "Legacy raw systemd content; Linux only."},
			},
			"required": []string{"name"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			name, _ := args["name"].(string)
			content, _ := args["content"].(string)
			executable, _ := args["executable"].(string)
			if runtime.GOOS != "linux" {
				return writeNativeService(ctx, h, args)
			}
			if executable != "" {
				d, err := parseServiceDefinition(args)
				if err != nil {
					return "Error: " + err.Error()
				}
				filename, rendered := renderServiceDefinition(runtime.GOOS, d)
				name, content = filename, rendered
			} else if !unitNameRe.MatchString(name) {
				return "Error: invalid unit name " + fmt.Sprintf("%q", name) + " (lower-case letters, digits, . _ - and a .service/.timer/.socket/.path suffix)"
			}
			if content == "" {
				return "Error: unit content cannot be empty"
			}
			if len(content) > unitMaxBytes {
				return fmt.Sprintf("Error: unit content too large (%d bytes, max %d)", len(content), unitMaxBytes)
			}
			if strings.ContainsRune(content, '\x00') {
				return "Error: unit content must not contain NUL bytes"
			}
			unitPath := filepath.Join(h.unitDir, name)
			old, oldErr := os.ReadFile(unitPath)
			oldContent := "" // unreadable counts as absent — the teardown edge is a documented loud error
			if oldErr == nil {
				oldContent = string(old)
			}
			if err := os.MkdirAll(h.stageDir, 0700); err != nil {
				return "Error: stage unit: " + err.Error()
			}
			staged := filepath.Join(h.stageDir, name)
			if err := os.WriteFile(staged, []byte(content), 0600); err != nil {
				return "Error: stage unit: " + err.Error()
			}
			removeStaged := func() { os.Remove(staged) }
			// restore puts the previous unit back (or removes the new one
			// when nothing existed before); it is the shared teardown.
			restore := func() error {
				if oldErr == nil {
					if werr := os.WriteFile(staged, old, 0600); werr != nil {
						return werr
					}
					if _, serr := h.sudo(ctx, installArgv(h, staged, name)); serr != nil {
						return serr
					}
				} else {
					rm := []string{"/bin/rm", "-f", unitPath}
					if !h.whitelisted(rm) {
						return fmt.Errorf("cannot remove %s: /bin/rm not whitelisted — owner must remove it manually", unitPath)
					}
					if _, serr := h.sudo(ctx, rm); serr != nil {
						return serr
					}
				}
				_, derr := h.sudo(ctx, []string{"/usr/bin/systemctl", "daemon-reload"})
				return derr
			}

			argv := installArgv(h, staged, name)
			if !h.whitelisted(argv) {
				removeStaged()
				return "Error: install of " + name + notWhitelisted
			}
			ctx, cancel := context.WithTimeout(ctx, hostOpTimeout)
			defer cancel()
			out, err := h.sudo(ctx, argv)
			if err != nil {
				removeStaged()
				return fmt.Sprintf("Error: install %s failed: %v", name, err)
			}
			if _, err := h.sudo(ctx, []string{"/usr/bin/systemctl", "daemon-reload"}); err != nil {
				terr := restore()
				removeStaged()
				if terr != nil {
					slog.Error("write_unit teardown failed", "unit", name, "error", terr)
				}
				return fmt.Sprintf("Error: daemon-reload after %s failed: %v (unit restore attempted)", name, err)
			}
			entry := &OpEntry{
				OpType:      "unit.write",
				Entity:      name,
				BeforeState: unitStateJSON(oldContent, oldErr == nil),
				AfterState:  unitStateJSON(content, true),
				SessionID:   h.sessionID(ctx),
			}
			if _, err := h.journal.Run(entry, nil); err != nil {
				terr := restore()
				removeStaged()
				if terr != nil {
					slog.Error("write_unit journal-failure restore failed", "unit", name, "error", terr)
				}
				return fmt.Sprintf("Error: journal write of %s: %v (unit restored)", name, err)
			}
			removeStaged()
			return fmt.Sprintf("Wrote %s to %s and reloaded systemd. %s", name, unitPath, out)
		},
	}
}

// installArgv is the exact install command the whitelist pins: fixed flags,
// source under the staging dir, target under the unit dir.
func installArgv(h *HostTools, staged, name string) []string {
	return []string{"/usr/bin/install", "-o", "root", "-g", "root", "-m", "0644", staged, filepath.Join(h.unitDir, name)}
}

func unitStateJSON(content string, present bool) string {
	b, _ := json.Marshal(map[string]any{"present": present, "content": content})
	return string(b)
}

// --- restart_service ---

func makeRestartServiceTool(h *HostTools) *Tool {
	return &Tool{
		Name:        "restart_service",
		Description: fmt.Sprintf("Restart Mino's own native service (the service %q, MINO_SERVICE). The service identity is resolved through the host's native service manager first — a name that resolves to any other service is refused. The intent is journaled BEFORE the restart (a successful restart terminates this process; the journal entry is what boot reconciliation finds).", envOr("MINO_SERVICE", "mino.service")),
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": fmt.Sprintf("Service name to restart — must resolve to Mino's own unit (%s).", envOr("MINO_SERVICE", "mino.service"))},
			},
			"required": []string{"service"},
		},
		ContextFn: func(ctx context.Context, args map[string]any) string {
			name, _ := args["service"].(string)
			if !unitNameRe.MatchString(name) {
				return "Error: invalid service name " + fmt.Sprintf("%q", name)
			}
			id, state, err := h.resolve(ctx, name)
			if err != nil {
				return "Error: " + err.Error()
			}
			if id != h.expected {
				return fmt.Sprintf("Error: refusing to restart %s — Mino's own service is %q (MINO_SERVICE); other units are outside the whitelist boundary", id, h.expected)
			}
			argv := h.restartArgv(id)
			if !h.whitelisted(argv) {
				return "Error: systemctl restart of " + id + notWhitelisted
			}
			active, _ := h.plain(ctx, h.activeArgv(id)) // read-only probe, no privilege needed
			before, _ := json.Marshal(map[string]string{"active": strings.TrimSpace(active), "unit_file_state": state})
			entry := &OpEntry{
				OpType:      "service.restart",
				Entity:      id,
				BeforeState: string(before),
				AfterState:  `{"requested": true}`,
				SessionID:   h.sessionID(ctx),
			}
			if _, err := h.journal.Run(entry, nil); err != nil {
				// Intent-first: a restart we cannot record must not happen.
				return fmt.Sprintf("Error: journal restart intent: %v — restart aborted", err)
			}
			ctx, cancel := context.WithTimeout(ctx, hostOpTimeout)
			defer cancel()
			if _, err := h.sudo(ctx, argv); err != nil {
				h.journal.SetStatus(entry.ID, OpStatusFailed)
				return fmt.Sprintf("Error: restart %s failed: %v", id, err)
			}
			return fmt.Sprintf("Restart of %s requested via systemd — this process will terminate; the intent is journaled and boot reconciliation picks up the pieces.", id)
		},
	}
}
