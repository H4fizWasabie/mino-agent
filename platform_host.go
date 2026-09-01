package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// hostPlatform is the small command seam for host tools. Tool policy and
// journaling stay shared; only native package/service commands vary.
type hostPlatform struct {
	install func(string) []string
	remove  func(string) []string
	restart func(string) []string
	probe   func(context.Context, string) bool
	active  func(string) []string
	resolve func(context.Context, string) (string, string, error)
	sudo    func(context.Context, []string) (string, error)
	allow   func(string, []string) bool
}

func hostPlatformFor(goos string) hostPlatform {
	switch goos {
	case "darwin":
		return hostPlatform{
			install: func(pkg string) []string { return []string{"brew", "install", pkg} },
			remove:  func(pkg string) []string { return []string{"brew", "uninstall", pkg} },
			restart: func(name string) []string { return []string{"launchctl", "kickstart", "-k", "system/" + name} },
			probe: func(ctx context.Context, pkg string) bool {
				_, err := runPlain(ctx, []string{"brew", "list", "--formula", pkg})
				return err == nil
			},
			active: func(name string) []string {
				return []string{"sh", "-c", "launchctl print system/" + name + " >/dev/null && printf active"}
			},
			resolve: func(ctx context.Context, name string) (string, string, error) {
				_, err := runPlain(ctx, []string{"launchctl", "print", "system/" + name})
				if err != nil {
					return "", "", fmt.Errorf("resolve service %q: %w", name, err)
				}
				return name, "loaded", nil
			},
			sudo:  runSudo,
			allow: allowNativeHostCommand,
		}
	case "windows":
		return hostPlatform{
			install: func(pkg string) []string {
				return []string{"winget.exe", "install", "--accept-source-agreements", "--accept-package-agreements", pkg}
			},
			remove: func(pkg string) []string { return []string{"winget.exe", "uninstall", pkg} },
			restart: func(name string) []string {
				return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Restart-Service -Name '" + name + "' -Force"}
			},
			probe: func(ctx context.Context, pkg string) bool {
				_, err := runPlain(ctx, []string{"winget.exe", "list", "--id", pkg, "--exact"})
				return err == nil
			},
			active: func(name string) []string {
				return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "if ((Get-Service -Name '" + name + "' -ErrorAction SilentlyContinue).Status -eq 'Running') {'active'} else {'inactive'}"}
			},
			resolve: func(ctx context.Context, name string) (string, string, error) {
				_, err := runPlain(ctx, []string{"sc.exe", "query", name})
				if err != nil {
					return "", "", fmt.Errorf("resolve service %q: %w", name, err)
				}
				return name, "installed", nil
			},
			sudo:  runWindowsElevated,
			allow: allowNativeHostCommand,
		}
	default:
		return hostPlatform{
			install: func(pkg string) []string { return []string{"/usr/bin/apt-get", "install", "-y", pkg} },
			remove:  func(pkg string) []string { return []string{"/usr/bin/apt-get", "remove", "-y", pkg} },
			restart: func(name string) []string { return []string{"/usr/bin/systemctl", "restart", name} },
			probe:   pkgInstalled,
			active:  func(name string) []string { return []string{"/usr/bin/systemctl", "is-active", name} },
			resolve: resolveUnit,
			sudo:    runSudo,
			allow:   allowSudo,
		}
	}
}

func currentHostPlatform() hostPlatform { return hostPlatformFor(runtime.GOOS) }

func hostServiceHealthCommands(goos string) (service, recentErrors []string) {
	switch goos {
	case "darwin":
		return []string{"launchctl", "print", "system/mino"}, []string{"log", "show", "--last", "1h", "--style", "compact", "--predicate", "process == 'mino'"}
	case "windows":
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "(Get-Service -Name 'mino' -ErrorAction SilentlyContinue).Status"}, nil
	default:
		return []string{"systemctl", "is-active", "mino"}, []string{"journalctl", "-u", "mino", "-p", "err", "--since", "1 hour ago", "-n", "10", "--no-pager"}
	}
}

func allowNativeHostCommand(_ string, argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	if argv[0] == "brew" || argv[0] == "winget.exe" {
		return len(argv) >= 3 && (argv[1] == "install" || argv[1] == "uninstall")
	}
	if argv[0] == "launchctl" {
		return len(argv) == 4 && argv[1] == "kickstart" && argv[2] == "-k" && strings.HasPrefix(argv[3], "system/")
	}
	return argv[0] == "powershell.exe" && strings.HasPrefix(strings.Join(argv[1:], " "), "-NoProfile -NonInteractive -Command Restart-Service -Name '")
}

func runWindowsElevated(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("empty elevated command")
	}
	args := make([]string, len(argv)-1)
	for i := range args {
		args[i] = "'" + strings.ReplaceAll(argv[i+1], "'", "''") + "'"
	}
	ps := "Start-Process -FilePath '" + strings.ReplaceAll(argv[0], "'", "''") + "' -ArgumentList @(" + strings.Join(args, ",") + ") -Verb RunAs -Wait -PassThru | Select-Object -ExpandProperty ExitCode"
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("elevated command: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
