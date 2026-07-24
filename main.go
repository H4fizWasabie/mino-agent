package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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
	w.Scheduler.Start()
	defer w.Scheduler.Stop()

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
