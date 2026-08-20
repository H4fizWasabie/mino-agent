//go:build windows

package main

// takeRunLock is a no-op on windows: the external updater that reads
// run-locks is Linux-only (the VPS), so windows builds need no lock
// semantics (#309). The run proceeds without a lock.
func takeRunLock(home, name string) (func(), error) {
	return func() {}, nil
}
