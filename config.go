package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Settings — matches Core's config.py exactly. Every knob is an env var.
type Settings struct {
	Provider         string
	APIKey           string
	BaseURL          string
	Model            string
	SmallModel       string
	Home             string
	Workspace        string
	MaxIter          int
	MaxTokens        int
	TopK             int
	ConsolidateEvery int
	MinSimilarity    float64
	ContextChars     int
	MaxHistoryTurns    int // keep only last N turns (0 = unlimited, default 5)
	MaxToolDescChars   int // trim tool descriptions exceeding this (0 = no limit)
	MaxReadOnlyStreak  int // max consecutive read-only tool calls before nudge (default 5)
	BashTimeout      time.Duration
	CodingTimeout    time.Duration
	SyncTimeout      time.Duration
	ConsolidateLimit int
	Telegram         string
	TelegramChatID   int64  // only this Telegram chat may control Mino or receive notifications
	Timezone         string // IANA timezone used for user-facing time and schedules
}

func LoadSettings() *Settings {
	home := os.Getenv("MINO_HOME")
	if home == "" {
		hd, err := os.UserHomeDir()
		if err != nil {
			home = ".mino"
		} else {
			home = filepath.Join(hd, ".mino")
		}
	}
	// load mino.env into process env (systemd-style, picks up dashboard-saved keys)
	loadEnvFile(filepath.Join(home, "mino.env"))
	return &Settings{
		Provider:         envOr("MINO_PROVIDER", "openai"),
		APIKey:           os.Getenv("MINO_API_KEY"),
		BaseURL:          os.Getenv("MINO_BASE_URL"),
		Model:            envOr("MINO_MODEL", "deepseek-v4-flash-free"),
		SmallModel:       envOr("MINO_SMALL_MODEL", "deepseek-v4-flash-free"),
		Home:             home,
		Workspace:        envOr("MINO_WORKSPACE", defaultWorkspace()),
		MaxIter:          envInt("MINO_MAX_ITERATIONS", 25),
		MaxTokens:        envInt("MINO_MAX_TOKENS", 16384),
		TopK:             envInt("MINO_RETRIEVAL_TOP_K", 4),
		ConsolidateEvery: envInt("MINO_CONSOLIDATE_EVERY", 6),
		MinSimilarity:    envFloat("MINO_MIN_SIMILARITY", 0.45),
		ContextChars:     envInt("MINO_CONTEXT_CHARS", 100000),
		MaxHistoryTurns:  envInt("MINO_MAX_HISTORY_TURNS", 5),
		MaxToolDescChars: envInt("MINO_MAX_TOOL_DESC_CHARS", 0),
		BashTimeout:      envDuration("MINO_BASH_TIMEOUT", 2*time.Minute),
		CodingTimeout:    envDuration("MINO_CODING_TIMEOUT", 2*time.Minute),
		SyncTimeout:      envDuration("MINO_SYNC_TIMEOUT", 5*time.Minute),
		ConsolidateLimit: envInt("MINO_CONSOLIDATE_LIMIT", 2),
		Telegram:         os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   envInt64("MINO_TELEGRAM_CHAT_ID", 0),
		Timezone:          envOr("MINO_TIMEZONE", "Asia/Kuala_Lumpur"),
		MaxReadOnlyStreak: envInt("MINO_MAX_READ_ONLY_STREAK", 5),
	}
}

func defaultWorkspace() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func (s *Settings) Location() *time.Location {
	if s != nil && s.Timezone != "" {
		if loc, err := time.LoadLocation(s.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

func (s *Settings) EnsureHome() string {
	os.MkdirAll(s.Home, 0700)
	os.MkdirAll(filepath.Join(s.Home, "traces"), 0700)
	os.MkdirAll(filepath.Join(s.Home, "outbox"), 0700)
	return s.Home
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// loadEnvFile reads KEY=VALUE lines from mino.env and sets them in the process
// environment if not already set. Lets dashboard-saved keys survive restarts.
func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
