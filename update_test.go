package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
// Issue #231: two builds with the same version string must be distinguished
// by content. Identical build (same version + same sha) stays "Already up to
// date" — the asset URL is a connection-refused tripwire, so any download
// attempt makes the test fail instead of silently passing.
func TestUpdateSameVersionSameSHASkips(t *testing.T) {
	oldVersion := Version
	Version = "v2.11.0"
	defer func() { Version = oldVersion }()

	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "STALE-BUILD-CONTENT")
	t.Setenv("MINO_UPDATE_BINARY", exe)

	oldAsset, oldSum := fetchLatestAsset, fetchReleaseChecksum
	defer func() { fetchLatestAsset, fetchReleaseChecksum = oldAsset, oldSum }()
	fetchLatestAsset = func() (string, string, error) {
		return "v2.11.0", "http://127.0.0.1:1/asset", nil // port 1: refused if downloaded
	}
	sum, _ := sha256File(exe)
	fetchReleaseChecksum = func(tag, assetName string) (string, bool, error) {
		return sum, true, nil
	}

	if err := DoUpdate(); err != nil {
		t.Fatalf("DoUpdate = %v, want skip with nil", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "STALE-BUILD-CONTENT" {
		t.Fatal("exe was modified — identical build must be left untouched")
	}
}

// Issue #231: same version string but different content must proceed through
// the full update path — download, release-checksum verify, applyUpdate swap
// with journal + ledger + health check. The release serves a real built
// binary (like TestApplyUpdateHappyPath) over httptest.
func TestUpdateSameVersionDifferentSHAProceeds(t *testing.T) {
	oldVersion := Version
	Version = "v2.11.0"
	defer func() { Version = oldVersion }()

	bin := buildRealMino(t)
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	home := testHome(t)
	exe := filepath.Join(t.TempDir(), "mino-bin")
	writeExec(t, exe, "OLD-BINARY")
	t.Setenv("MINO_UPDATE_BINARY", exe)
	t.Setenv("MINO_HOME", home)

	oldAsset, oldSum := fetchLatestAsset, fetchReleaseChecksum
	defer func() { fetchLatestAsset, fetchReleaseChecksum = oldAsset, oldSum }()
	fetchLatestAsset = func() (string, string, error) {
		return "v2.11.0", srv.URL, nil
	}
	newSum, _ := sha256File(bin)
	fetchReleaseChecksum = func(tag, assetName string) (string, bool, error) {
		return newSum, true, nil
	}

	updateHealthTimeout = 30 * time.Second
	if err := DoUpdate(); err != nil {
		t.Fatalf("DoUpdate = %v, want same-version reinstall to proceed", err)
	}

	got, _ := os.ReadFile(exe)
	if string(got) == "OLD-BINARY" {
		t.Fatal("exe was not swapped — same version + different sha must update")
	}
	prev, err := os.ReadFile(exe + ".prev")
	if err != nil || string(prev) != "OLD-BINARY" {
		t.Fatalf("exe.prev = %q, %v — want the previous binary kept", prev, err)
	}

	op, err := NewOpJournal(Connect(home)).LastOp(exe)
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if op.OpType != "binary.swap" || op.Status != OpStatusOK {
		t.Fatalf("op = %+v, want binary.swap/ok", op)
	}

	ledger, _ := os.ReadFile(filepath.Join(home, "deployments.log"))
	if !strings.Contains(string(ledger), "update=v2.11.0") {
		t.Fatalf("ledger missing update line: %s", ledger)
	}
}

func TestDownloadTimeoutExceedsCheckTimeout(t *testing.T) {
	if downloadClient.Timeout <= updateClient.Timeout {
		t.Errorf("download client timeout %v must exceed check client timeout %v",
			downloadClient.Timeout, updateClient.Timeout)
	}
	if downloadClient.Timeout < 5*time.Minute {
		t.Errorf("download client timeout %v too short for the release binary", downloadClient.Timeout)
	}
}