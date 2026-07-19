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

	w := NewCore()
	defer w.Close()
	w.Scheduler.Start()
	defer w.Scheduler.Stop()

	// Telegram always takes priority (VPS/headless mode)
	if w.Settings.Telegram != "" {
		RunTelegram(w)
		return
	}

	// CLI mode when explicitly requested
	if len(os.Args) > 1 && os.Args[1] == "cli" {
		runCLI(w)
		return
	}

	// Default: dashboard on MINO_DASHBOARD_PORT or 7779
	dashPort := os.Getenv("MINO_DASHBOARD_PORT")
	if dashPort == "" {
		dashPort = "7779"
		os.Setenv("MINO_DASHBOARD_PORT", dashPort)
	}
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
