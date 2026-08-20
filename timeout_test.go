package main

import (
	"os"
	"testing"
	"time"
)

func TestNewClientTimeoutFromEnv(t *testing.T) {
	os.Setenv("MINO_LLM_TIMEOUT", "7m")
	defer os.Unsetenv("MINO_LLM_TIMEOUT")
	c := NewClient("k", "https://x")
	if got := c.client.Timeout; got != 7*time.Minute {
		t.Fatalf("timeout = %v, want 7m", got)
	}
}

func TestNewClientDefaultTimeout(t *testing.T) {
	os.Unsetenv("MINO_LLM_TIMEOUT")
	c := NewClient("k", "https://x")
	if got := c.client.Timeout; got != 5*time.Minute {
		t.Fatalf("timeout = %v, want default 5m", got)
	}
}