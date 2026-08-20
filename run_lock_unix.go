//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// takeRunLock takes an exclusive flock on run-locks/<name>.lock, creating the
// dir and file as needed. The lock is held for the run's duration; the
// external updater's `flock -n` probe then fails and it defers the restart
// (#309). Returns a release func. Unix build (linux/darwin): real flock.
func takeRunLock(home, name string) (func(), error) {
	dir := filepath.Join(home, "run-locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("run-lock dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("run-lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("run-lock flock: %w", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
