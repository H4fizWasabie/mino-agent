package main

import (
	"os"
	"path/filepath"
	"strconv"
)

// Settings — matches Core's config.py exactly. Every knob is an env var.
type Settings struct {
	Provider         string
	APIKey           string
	BaseURL          string
	Model            string
	SmallModel       string
	Home             string
	MaxIter          int
	MaxTokens        int
	TopK             int
	ConsolidateEvery int
	MinSimilarity    float64
	ContextChars     int
	Telegram         string
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
	return &Settings{
		Provider:         envOr("MINO_PROVIDER", "openai"),
		APIKey:           os.Getenv("MINO_API_KEY"),
		BaseURL:          os.Getenv("MINO_BASE_URL"),
		Model:            envOr("MINO_MODEL", "deepseek-v4-flash-free"),
		SmallModel:       envOr("MINO_SMALL_MODEL", "deepseek-v4-flash-free"),
		Home:             home,
		MaxIter:          envInt("MINO_MAX_ITERATIONS", 10),
		MaxTokens:        envInt("MINO_MAX_TOKENS", 2048),
		TopK:             envInt("MINO_RETRIEVAL_TOP_K", 4),
		ConsolidateEvery: envInt("MINO_CONSOLIDATE_EVERY", 6),
		MinSimilarity:    envFloat("MINO_MIN_SIMILARITY", 0.45),
		ContextChars:     envInt("MINO_CONTEXT_CHARS", 100000),
		Telegram:         os.Getenv("TELEGRAM_BOT_TOKEN"),
	}
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

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return fallback
}
