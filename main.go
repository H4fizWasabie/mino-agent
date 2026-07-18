package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
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
