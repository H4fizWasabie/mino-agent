package main

// privilege.go — RUN-003 (GitHub #217): the privilege bridge.
//
// Transport: a sudoers command whitelist — the mino user may run EXACT
// binaries as root (apt-get, systemctl, install, rm), never a shell, never
// ALL. Interface: harness-native tools (host_tools.go: install_package,
// write_unit, restart_service) — the model never calls sudo itself.
//
// Whitelist entries are exact fixed-prefix commands, one entry per command
// shape (decided on #213/#217, 2026-08-16): membership = args prefix-match.
// `*` in an arg field matches any single argument (in sudoers, wildcards in
// command ARGS match across slashes — the no-slash rule applies only to the
// command name), and a trailing `/` in a field is a directory-prefix
// constraint. The install entry is deliberately tight: the fixed flags
// `-o root -g root -m 0644` with the source pinned under <home>/tmp and the
// target pinned under /etc/systemd/system — escalating means bypassing the
// harness's fixed-prefix enforcement, not just sudoers.
//
// The sudoers file itself is the autonomous/approval boundary RUN-006 reads
// later: the harness reads /etc/sudoers.d/mino (MINO_SUDOERS_FILE override)
// when present and falls back to the canonical defaults below when it is
// not (a dev machine where sudo itself will refuse anyway). The setup path
// (`mino setup-privileges`, run as root) writes the file from the same
// canonical entries, so the two never drift.

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// sudoersPath is where the whitelist lives on the host. The mino user must
// be able to read it (the setup step chmods it 0444 — sudoers only forbids
// group/world WRITE, read is fine).
func sudoersPath() string {
	if p := os.Getenv("MINO_SUDOERS_FILE"); p != "" {
		return p
	}
	return "/etc/sudoers.d/mino"
}

// unitNameRe — systemd unit file names: lower-case alnum with . _ - and a
// known type suffix. write_unit/restart_service share it; the suffix set
// covers what Mino manages (its own service, timers like cost-watch's).
var unitNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*\.(service|timer|socket|path)$`)

// pkgNameRe — Debian package names (lower-case, digits, + . -).
var pkgNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)

// Whitelist is the parsed privilege whitelist: a list of command shapes,
// each a fixed field prefix. Allows reports whether argv (the full command
// WITHOUT sudo) is a member.
type Whitelist struct {
	entries [][]string
}

// DefaultWhitelist returns the canonical whitelist for a Mino home — the
// same entries `mino setup-privileges` writes to sudoers.
func DefaultWhitelist(home string) *Whitelist {
	return &Whitelist{entries: [][]string{
		{"/usr/bin/apt-get", "install", "-y"},
		{"/usr/bin/apt-get", "remove", "-y"},
		{"/usr/bin/systemctl", "restart"},
		{"/usr/bin/systemctl", "daemon-reload"},
		{"/usr/bin/install", "-o", "root", "-g", "root", "-m", "0644", filepath.Join(home, "tmp") + "/", "/etc/systemd/system/"},
		{"/bin/rm", "-f", "/etc/systemd/system/"},
	}}
}

// LoadWhitelist parses the generated sudoers file. Only lines of our own
// shape are accepted (`<user> ALL=(root) NOPASSWD: <command>`); anything
// else is ignored — the parser is conservative, unknown lines are refused.
// An unreadable file is an error (the caller decides the fallback).
func LoadWhitelist(path string) (*Whitelist, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	w := &Whitelist{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// `\` escapes in sudoers (e.g. `\*`) are not produced by the setup
		// step; lines containing them are refused conservatively.
		if strings.Contains(line, `\`) {
			continue
		}
		i := strings.Index(line, "NOPASSWD:")
		if i < 0 {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line[i+len("NOPASSWD:"):]))
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
			continue
		}
		w.entries = append(w.entries, fields)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return w, nil
}

// Allows reports whether argv (the command to run, without sudo) is a
// whitelist member. Entry fields must match argv fields positionally; the
// entry's final fixed fields are a prefix check (apt-get install -y matches
// apt-get install -y curl); `*` matches any single argument; a trailing `/`
// is a directory-prefix constraint.
func (w *Whitelist) Allows(argv []string) bool {
	for _, e := range w.entries {
		if len(argv) < len(e) {
			continue
		}
		ok := true
		for i, ef := range e {
			if !fieldMatches(ef, argv[i]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func fieldMatches(entry, arg string) bool {
	if entry == "*" {
		return true
	}
	if strings.HasSuffix(entry, "*") {
		return strings.HasPrefix(arg, strings.TrimSuffix(entry, "*"))
	}
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(arg, entry)
	}
	return entry == arg
}

// SudoersLines renders the canonical entries as sudoers.d lines for the
// given user — what `mino setup-privileges` writes (directory-prefix fields
// become `*` args; sudoers wildcards in args match slashes).
func (w *Whitelist) SudoersLines(user string) []string {
	lines := make([]string, 0, len(w.entries))
	for _, e := range w.entries {
		fields := make([]string, len(e))
		for i, f := range e {
			if strings.HasSuffix(f, "/") {
				fields[i] = f + "*"
			} else {
				fields[i] = f
			}
		}
		lines = append(lines, user+" ALL=(root) NOPASSWD: "+strings.Join(fields, " "))
	}
	return lines
}

// SetupPrivileges writes the canonical whitelist to the sudoers file and
// validates it with visudo. Run as root: `mino setup-privileges --user mino
// --home /home/mino/.mino`. The file is chmod 0444 — sudo only forbids
// group/world WRITE on sudoers files, and the mino user must be able to
// READ its own boundary (the harness checks membership against the file).
func SetupPrivileges(home, user string) error {
	if user == "" {
		user = "mino"
	}
	// MINO_HOME is honored first: `sudo MINO_HOME=/home/mino/.mino mino
	// setup-privileges` must pin the mino user's home, not root's.
	if home == "" {
		home = os.Getenv("MINO_HOME")
	}
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".mino")
	}
	w := DefaultWhitelist(home)
	data := []byte(strings.Join(w.SudoersLines(user), "\n") + "\n")
	path := sudoersPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0444); err != nil {
		return fmt.Errorf("write %s: %w (run as root)", path, err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		return err
	}
	if out, err := exec.Command("visudo", "-c", "-f", path).CombinedOutput(); err != nil {
		return fmt.Errorf("visudo rejected %s: %v: %s", path, err, out)
	}
	return nil
}

// allowSudo reports whether the harness may invoke argv via sudo. The file
// is the boundary when present; the canonical defaults (documented shape)
// cover a host where the bridge is not installed — sudo itself refuses
// there, so the fallback can only err toward a noisy refusal, never a grant.
func allowSudo(home string, argv []string) bool {
	if w, err := LoadWhitelist(sudoersPath()); err == nil {
		return w.Allows(argv)
	} else if !os.IsNotExist(err) {
		slog.Warn("privilege whitelist unreadable; using canonical defaults", "path", sudoersPath(), "error", err)
	}
	return DefaultWhitelist(home).Allows(argv)
}

// runSudo runs argv as root via `sudo -n` (no shell, no password prompt —
// NOPASSWD whitelist entries only). Output is combined so error messages
// carry the command's own words.
func runSudo(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, argv...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2000 {
			msg = msg[:2000] + "..."
		}
		if msg != "" {
			return "", fmt.Errorf("%v: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runPlain runs argv WITHOUT privilege (read-only probes: dpkg-query,
// systemctl show). Tests inject their own runner.
var runPlain = func(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// pkgInstalled probes whether a Debian package is currently installed.
func pkgInstalled(ctx context.Context, pkg string) bool {
	_, err := runPlain(ctx, []string{"dpkg-query", "-W", "-f=${Status}", pkg})
	if err != nil {
		return false
	}
	return true
}

// resolveUnit maps a unit name to its canonical systemd identity:
// `systemctl show -p Id -p UnitFileState <name>`. Read-only, no privilege.
// Returns the canonical unit name (e.g. "mino.service" for "mino") or an
// error when the unit does not exist.
func resolveUnit(ctx context.Context, name string) (id, state string, err error) {
	out, err := runPlain(ctx, []string{"systemctl", "show", "-p", "Id", "-p", "UnitFileState", name})
	if err != nil {
		return "", "", fmt.Errorf("resolve unit %q: %w", name, err)
	}
	id, state = parseUnitShow(out)
	if id == "" {
		return "", "", fmt.Errorf("resolve unit %q: no such unit", name)
	}
	return id, state, nil
}

// parseUnitShow extracts Id and UnitFileState from `systemctl show` output.
func parseUnitShow(out string) (id, state string) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "Id="):
			id = strings.TrimPrefix(line, "Id=")
		case strings.HasPrefix(line, "UnitFileState="):
			state = strings.TrimPrefix(line, "UnitFileState=")
		}
	}
	return id, state
}

// containsSudoInvocation is the bash-tool tripwire: the model must never
// call sudo itself — privileged operations go through the harness tools.
// The regex is intentionally conservative (false-positives like `echo sudo`
// are harmless refusals); the real enforcement is that the harness is the
// only thing that ever invokes sudo, and the sudoers whitelist is the only
// thing sudo grants.
var sudoInvocationRe = regexp.MustCompile(`(?:^|[;&|(]|\s)sudo(?:\s|$)`)

func containsSudoInvocation(command string) bool {
	return sudoInvocationRe.MatchString(command)
}
