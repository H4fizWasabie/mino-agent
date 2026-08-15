package main

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

// The cost-watch pinning signal: SIGHUP must reload providers, keep listening
// after a failed reload, and stop when the channel closes.
func TestReloadOnHUP(t *testing.T) {
	got := make(chan struct{}, 4)
	attempts := 0
	reload := func() error {
		attempts++
		if attempts == 1 {
			return fmt.Errorf("boom")
		}
		got <- struct{}{}
		return nil
	}
	sig := make(chan os.Signal, 2)
	done := make(chan struct{})
	go func() {
		reloadOnHUP(reload, sig)
		close(done)
	}()

	// First signal: reload fails — loop must survive and keep listening.
	sig <- syscall.SIGHUP
	// Second signal: reload succeeds — receipt must arrive.
	sig <- syscall.SIGHUP
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("reload not called after second signal")
	}
	if attempts != 2 {
		t.Fatalf("reload called %d times, want 2 (survived the failure)", attempts)
	}

	close(sig)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reloadOnHUP did not exit after channel close")
	}
}
