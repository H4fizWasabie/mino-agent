package main

import (
	"testing"
	"time"
)

// The self-updater must promote a release over a same-numeric prerelease.
// Previously Sscanf dropped the "-rc4" suffix, so v2.8.11-rc4 compared equal
// to v2.8.11 and `mino update` said "Already up to date", requiring a manual
// binary swap (witnessed after the v2.8.11 release).
func TestIsNewerRcToReleasePromotion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool // is a newer than b?
	}{
		{"v2.8.11", "v2.8.11-rc4", true},  // release promotes over rc
		{"v2.8.11-rc4", "v2.8.11", false}, // rc does not beat release
		{"v2.8.12", "v2.8.11-rc4", true},  // higher minor beats rc
		{"v2.8.11", "v2.8.10", true},      // plain bump still works
		{"v2.8.10", "v2.8.11", false},
		{"v2.8.11", "v2.8.11", false}, // equal
		{"v2.9.0", "v2.8.99", true},   // major-ish (minor) bump
	}
	for _, c := range cases {
		if got := isNewer(c.a, c.b); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// issue #177: the 22MB binary download must not share the 30s API-check
// timeout — `mino update` hit "download: context deadline exceeded" on slow
// links. The download client needs a materially longer window, and must
// always outlast the check client.
func TestDownloadTimeoutExceedsCheckTimeout(t *testing.T) {
	if downloadClient.Timeout <= updateClient.Timeout {
		t.Errorf("download client timeout %v must exceed check client timeout %v",
			downloadClient.Timeout, updateClient.Timeout)
	}
	if downloadClient.Timeout < 5*time.Minute {
		t.Errorf("download client timeout %v too short for the release binary", downloadClient.Timeout)
	}
}