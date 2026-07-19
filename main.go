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
	// Load minimal settings just for the home path.
	s := LoadSettings()
	if latest := CheckForUpdate(s.Home); latest != "" {
		fmt.Fprintf(os.Stderr, "\n⚠ Mino %s is available (you have %s). Run 'mino update' to upgrade.\n\n", latest, Version)
	}

	w := NewCore()
	defer w.Close()
	w.Scheduler.Start()
	defer w.Scheduler.Stop()

	if len(os.Args) > 1 && os.Args[1] == "dashboard" {
		RunDashboard(w)
		return
	}

	// Dashboard alongside Telegram if port configured
	dashPort := os.Getenv("MINO_DASHBOARD_PORT")
	if dashPort != "" {
		go RunDashboard(w)
	}

	if w.Settings.Telegram != "" {
		RunTelegram(w)
		return
	}

	// CLI mode
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
