package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
		case "migrate-memories", "--migrate-memories":
			s := LoadSettings()
			MigrateMemories(s.Home, s.MemoriesDir)
			return
		case "eval":
			s := LoadSettings()
			os.Exit(RunEval(s.Home))
		}
	}

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

		result := w.Respond(input, "cli", nil, false)
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
	default:
		return // windows/other: skip
	}
	cmd.Start()
}
