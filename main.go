package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func main() {
	// Handle special commands that don't need the full core
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			printVersion()
			return
		case "update":
			if err := DoUpdate(); err != nil {
				fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "rollback":
			// RUN-004: owner-call revert — restores the previous binary kept
			// by the last update, records the ledger line, marks the swap
			// op rolled_back.
			if err := DoRollback(); err != nil {
				fmt.Fprintf(os.Stderr, "Rollback failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "config-rollback":
			// RUN-005: owner-call config revert — restores the .prev backup
			// of one config-set file (providers.json / mino.env /
			// cost-watch.json) and marks the last config.edit op rolled_back.
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: mino config-rollback <providers.json|mino.env|cost-watch.json>")
				os.Exit(2)
			}
			if err := DoConfigRollback(os.Args[2]); err != nil {
				fmt.Fprintf(os.Stderr, "Config rollback failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "audit-memory", "--audit-memory":
			s := LoadSettings()
			if err := AuditMemory(s); err != nil {
				fmt.Fprintf(os.Stderr, "memory audit failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "migrate-memories", "--migrate-memories":
			s := LoadSettings()
			MigrateMemories(s.Home, s.MemoriesDir)
			return
		case "rebuild-memory-edges", "--rebuild-memory-edges":
			s := LoadSettings()
			RebuildMemoryEdges(s)
			return
		case "maintain-memory", "--maintain-memory":
			s := LoadSettings()
			MaintainMemory(s)
			return
		case "clean-memory-edges", "--clean-memory-edges":
			s := LoadSettings()
			CleanMemoryEdges(s)
			return
		case "consolidate-memory", "--consolidate-memory":
			s := LoadSettings()
			ConsolidateMemory(s)
			return
		case "synthesize-memory", "--synthesize-memory":
			s := LoadSettings()
			SynthesizeMemory(s)
			return
		case "eval-memory", "--eval-memory":
			s := LoadSettings()
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: mino eval-memory <cases.json>")
				os.Exit(2)
			}
			os.Exit(RunMemoryEval(s.MemoriesDir, os.Args[2]))
		case "eval":
			s := LoadSettings()
			os.Exit(RunEval(s.Home))
		case "setup-privileges":
			// RUN-003: write the sudoers command whitelist (run as root).
			user, home := "mino", ""
			for i := 2; i < len(os.Args); i++ {
				switch os.Args[i] {
				case "--user":
					i++
					if i < len(os.Args) {
						user = os.Args[i]
					}
				case "--home":
					i++
					if i < len(os.Args) {
						home = os.Args[i]
					}
				default:
					fmt.Fprintf(os.Stderr, "unknown flag %q\n", os.Args[i])
					os.Exit(2)
				}
			}
			if err := SetupPrivileges(home, user); err != nil {
				fmt.Fprintf(os.Stderr, "setup-privileges failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("wrote %s — the mino user can now run only the whitelisted commands as root.\n", sudoersPath())
			return
		}
	}

	// Register the HUP channel FIRST — before NewCore's slow init. A
	// cost-watch pin arriving during boot queues in the buffer instead of
	// terminating the process (default SIGHUP disposition); the goroutine
	// below drains it once the provider manager exists.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)

	// #310: on termination (systemd stop / timer bounce / manual restart),
	// cancel all in-flight playbook runs so each marks itself interrupted
	// cleanly instead of being killed mid-write (the 2026-08-20 franken-run
	// class). Bounded wait, then the default disposition proceeds.
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-sigterm
		fmt.Fprintln(os.Stderr, "\nmino: cancelling in-flight playbook runs before exit...")
		cancelAllRuns(3 * time.Second)
		os.Exit(0)
	}()

	// Check for updates early (before full init, so it works even without API key).
	s := LoadSettings()
	if latest := CheckForUpdate(s.Home); latest != "" {
		fmt.Fprintf(os.Stderr, "\n⚠ Mino %s is available (you have %s). Run 'mino update' to upgrade.\n\n", latest, Version)
	}

	// Default to dashboard on port 7779 (set before NewCore so onboarding works).
	// Explicit CLI or Telegram modes skip this.
	if len(os.Args) <= 1 || (len(os.Args) > 1 && os.Args[1] != "cli") {
		if os.Getenv("MINO_DASHBOARD_PORT") == "" && os.Getenv("TELEGRAM_BOT_TOKEN") == "" {
			os.Setenv("MINO_DASHBOARD_PORT", "7779")
		}
	}

	w := NewCore()
	defer w.Close()

	// CTX-022 A: external read surface — `mino remember "query"` prints the
	// exact in-loop retrieval output, no LLM call. Routed before the Telegram
	// branch so it works on a telegram-configured host.
	if len(os.Args) > 1 && os.Args[1] == "remember" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: mino remember \"query\"")
			os.Exit(2)
		}
		if w.Memory == nil || w.Memory.graph == nil {
			fmt.Fprintln(os.Stderr, "memory unavailable")
			os.Exit(1)
		}
		fmt.Println(w.Memory.graph.Remember(strings.Join(os.Args[2:], " "), ""))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "memory" && len(os.Args) > 2 && os.Args[2] == "path" {
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "usage: mino memory path <from> <to>")
			os.Exit(2)
		}
		if w.Memory == nil || w.Memory.graph == nil {
			fmt.Fprintln(os.Stderr, "memory unavailable")
			os.Exit(1)
		}
		fmt.Println(w.Memory.graph.RememberPath(os.Args[3], os.Args[4]))
		return
	}

	// cost-watch's autonomous pinning rewrites providers.json and signals mino;
	// hot-reload the routing list instead of waiting for a restart. The heal
	// runs first (RUN-005): validate the config set, revert anything bad from
	// its .prev backup — a bad SIGHUP reload must revert, not just log.
	journal := NewOpJournal(w.DB)
	go reloadOnHUP(func() error {
		HealConfig(w.Settings.Home, journal)
		return w.Client.ReloadProviders(w.Settings.Home)
	}, hup)

	// Auto-open browser on first run (onboarding §16)
	if needsOnboarding(w.Settings.Home) {
		go autoOpenBrowser(w.Settings.DashboardPort())
	}

	// Telegram runs alone unless a dashboard port is configured too.
	if w.Settings.Telegram != "" {
		if telegramDashboardEnabled() {
			go RunTelegram(w)
			RunDashboard(w)
			return
		}
		RunTelegram(w)
		return
	}

	// CLI mode when explicitly requested
	if len(os.Args) > 1 && os.Args[1] == "cli" {
		runCLI(w)
		return
	}

	// Default: dashboard
	RunDashboard(w)
}

// reloadOnHUP reloads provider config on SIGHUP (the cost-watch pinning
// signal). Runs until sig closes; a failed reload is logged and the loop
// keeps listening — one bad reload must not wedge the watcher loop. The
// config self-heal (HealConfig) runs inside the reload closure BEFORE the
// provider reload, so a bad config is reverted before anything reads it.
func reloadOnHUP(reload func() error, sig <-chan os.Signal) {
	for range sig {
		if err := reload(); err != nil {
			slog.Error("provider reload on SIGHUP failed", "error", err)
			continue
		}
		slog.Info("providers reloaded on SIGHUP")
	}
}

func runCLI(w *Core) {
	fmt.Println("Mino ready. Type /exit to quit.")
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "/exit" {
			fmt.Println("bye!")
			break
		}

		// RUN-006: approve/deny replies resolve pending approvals before the loop.
		if w.approvals != nil {
			if reply, handled := w.approvals.ResolveReply(input); handled {
				fmt.Printf("\n%s\n", reply)
				continue
			}
		}

		result := w.Respond(input, "cli", nil)
		fmt.Printf("\n%s\n", result.Reply)
	}
}

// autoOpenBrowser opens the dashboard URL in the default browser.
// Runs in a goroutine with a short delay to let the HTTP server start.
func autoOpenBrowser(port string) {
	time.Sleep(500 * time.Millisecond)
	url := "http://localhost:" + port
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		return // unsupported host: skip
	}
	cmd.Start()
}
