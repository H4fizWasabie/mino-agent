package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner   = "H4fizWasabie"
	repoName    = "mino-agent"
	releasesURL = "https://api.github.com/repos/" + repoOwner + "/" + repoName + "/releases/latest"
)

// ponytail: hardcoded HTTP timeout, upgrade if GitHub gets slow
var updateClient = &http.Client{Timeout: 30 * time.Second}

// The release binary download gets its own, longer window (issue #177): the
// 30s check timeout also capped the 22MB body read, so `mino update` hit
// "download: context deadline exceeded" on slow links. Client.Timeout covers
// the whole exchange including the io.Copy below.
var downloadClient = &http.Client{Timeout: 5 * time.Minute}

// fetchLatestAsset and fetchReleaseChecksum are package vars (the same
// test-seam family as updateClient/downloadClient) so update tests can stub
// the GitHub half and exercise the same-version decision end to end.
var fetchLatestAsset = fetchLatestAssetReal
var fetchReleaseChecksum = fetchReleaseChecksumReal

// updateCache is the on-disk cache for update checks (rate-limit friendly).
type updateCache struct {
	LastCheck time.Time `json:"last_check"`
	Latest    string    `json:"latest"`
}

// CheckForUpdate checks the GitHub releases API for a newer version.
// Caches the result for 24 hours to avoid rate limiting.
// Returns the latest version string if newer than current, empty string otherwise.
func CheckForUpdate(home string) string {
	if Version == "dev" {
		return "" // development builds don't check
	}

	cachePath := filepath.Join(home, "update-check")
	cache := loadUpdateCache(cachePath)

	// Use cache if checked within 24h
	if time.Since(cache.LastCheck) < 24*time.Hour {
		if cache.Latest != "" && isNewer(cache.Latest, Version) {
			return cache.Latest
		}
		return ""
	}

	// Fetch latest release
	latest, err := fetchLatestRelease()
	if err != nil {
		slog.Debug("update check failed", "error", err)
		return ""
	}

	// Persist cache
	cache.LastCheck = time.Now()
	cache.Latest = latest
	saveUpdateCache(cachePath, cache)

	if isNewer(latest, Version) {
		return latest
	}
	return ""
}

// DoUpdate downloads the latest release binary and replaces the current one.
// Atomic: writes to .new, then renames.
func DoUpdate() error {
	if Version == "dev" {
		return fmt.Errorf("development build — update not supported (build from source)")
	}

	fmt.Printf("mino %s → checking for updates...\n", Version)

	tag, assetURL, err := fetchLatestAsset()
	if err != nil {
		return fmt.Errorf("check release: %w", err)
	}

	assetName := fmt.Sprintf("mino-%s-%s", runtime.GOOS, runtime.GOARCH)

	if !isNewer(tag, Version) {
		if isNewer(Version, tag) {
			fmt.Printf("Already up to date (%s).\n", Version)
			return nil // release is older — the updater never downgrades
		}
		// Issue #231: two builds can share a version string while differing in
		// content (live hit 2026-08-16: a stale pre-RUN-map v2.11.0 build on
		// the VPS made `mino update` skip the fresh release). Distinguish
		// rebuilds by content: only a POSITIVE sha256 identity match skips;
		// any uncertainty (unreadable running binary, missing/unreachable
		// checksum file) proceeds — the download path re-verifies the release
		// checksum and health-checks the candidate anyway.
		if sameBuildAsRelease(updateBinaryPath(), tag, assetName) {
			fmt.Printf("Already up to date (%s).\n", Version)
			return nil
		}
		fmt.Printf("%s is current, but the running binary differs from the release build — reinstalling.\n", Version)
	}

	fmt.Printf("Downloading %s...\n", tag)
	resp, err := downloadClient.Get(assetURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	exe := updateBinaryPath()
	newPath := exe + ".new"

	f, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("write new binary: %w — try running with sudo", err)
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 100<<20)) // 100 MiB max
	f.Close()
	if err != nil {
		os.Remove(newPath)
		return fmt.Errorf("download: %w", err)
	}
	if n < 100_000 {
		os.Remove(newPath)
		return fmt.Errorf("downloaded file too small (%d bytes) — likely not a binary", n)
	}

	// REL-05a: "verified" only becomes true when the code verifies. The
	// release ships SHA256SUMS.txt; a binary that does not match its platform
	// checksum (or a release without a checksum) is refused — the self-updater
	// is the only production path, so it must be the trusted one.
	sum, err := sha256File(newPath)
	if err != nil {
		os.Remove(newPath)
		return fmt.Errorf("checksum local file: %w", err)
	}
	want, ok, err := fetchReleaseChecksum(tag, assetName)
	if err != nil {
		os.Remove(newPath)
		return fmt.Errorf("verify release checksum: %w", err)
	}
	if !ok {
		os.Remove(newPath)
		return fmt.Errorf("release %s has no checksum for %s — refusing to install unverified binary", tag, assetName)
	}
	if !strings.EqualFold(sum, want) {
		os.Remove(newPath)
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s — refusing to install", assetName, sum, want)
	}

	// RUN-004: the swap, the journal entry, and the post-update health check
	// (with revert on failure) live in applyUpdate — the download/verify half
	// ends here so the swap/revert decision logic is testable end to end.
	if err := applyUpdate(exe, homeDir(), tag, sum, newPath); err != nil {
		return err
	}
	return nil
}

// --- helpers ---

// homeDir resolves the Mino home the same way LoadSettings does, so the
// deployments.log lands next to the other state even when `mino update` runs
// standalone (no Settings loaded).
func homeDir() string {
	if home := os.Getenv("MINO_HOME"); home != "" {
		return home
	}
	hd, err := os.UserHomeDir()
	if err != nil {
		return ".mino"
	}
	return filepath.Join(hd, ".mino")
}

// sha256File returns the lowercase hex SHA-256 of a file.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// releaseChecksumURL is the download URL of a release's checksum file (the
// download domain, not the API — no rate limits).
func releaseChecksumURL(tag string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/SHA256SUMS.txt", repoOwner, repoName, tag)
}

// fetchReleaseChecksum fetches SHA256SUMS.txt for the release and returns the
// checksum line for the named asset. Each line is "<hex>  <name>".
func fetchReleaseChecksumReal(tag, assetName string) (string, bool, error) {
	resp, err := updateClient.Get(releaseChecksumURL(tag))
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", false, fmt.Errorf("fetch %s: HTTP %d", releaseChecksumURL(tag), resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == assetName {
			return fields[0], true, nil
		}
	}
	return "", false, nil
}

// sameBuildAsRelease reports whether the running binary is byte-identical to
// the release asset of the SAME version (issue #231). Any failure to confirm
// identity returns false — the caller proceeds with the update, where the
// existing download path re-verifies the release checksum and health-checks
// the candidate, so a false "proceed" is safe and a false "skip" is not.
func sameBuildAsRelease(exe, tag, assetName string) bool {
	sum, err := sha256File(exe)
	if err != nil {
		return false
	}
	want, ok, err := fetchReleaseChecksum(tag, assetName)
	if err != nil || !ok {
		return false
	}
	return strings.EqualFold(sum, want)
}

// appendDeploymentLog records one line per successful update — timestamp,
// version, verified checksum, binary path. Append-only, 0600, never rotated
// by the updater (the owner can prune it). Rollback lines share the shape
// via recordDeployment (rollback.go, RUN-004).
func appendDeploymentLog(home, tag, exe, sum string) {
	recordDeployment(home, "update", tag, sum, exe)
}

func fetchLatestRelease() (string, error) {
	req, _ := http.NewRequest("GET", releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mino-agent/"+Version)
	resp, err := updateClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatestAssetReal() (string, string, error) {
	req, _ := http.NewRequest("GET", releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mino-agent/"+Version)
	resp, err := updateClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var release struct {
		TagName string         `json:"tag_name"`
		Assets  []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}
	assetURL := findAsset(release.Assets)
	if assetURL == "" {
		return "", "", fmt.Errorf("no binary for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
	}
	return release.TagName, assetURL, nil
}

func findAsset(assets []releaseAsset) string {
	want := fmt.Sprintf("mino-%s-%s", runtime.GOOS, runtime.GOARCH)
	for _, a := range assets {
		if strings.Contains(a.Name, want) {
			return a.BrowserDownloadURL
		}
	}
	// fallback: first asset containing "mino"
	for _, a := range assets {
		if strings.Contains(a.Name, "mino") {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func loadUpdateCache(path string) updateCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return updateCache{}
	}
	var c updateCache
	json.Unmarshal(data, &c)
	return c
}

func saveUpdateCache(path string, c updateCache) {
	data, _ := json.Marshal(c)
	os.WriteFile(path, data, 0644)
}

// isNewer does semver comparison with prerelease handling. Numeric parts first;
// at the same numeric version, a release (no suffix) is newer than a
// prerelease (rc/beta/alpha) — so v2.8.11 promotes over v2.8.11-rc4 (previously
// the rc suffix was dropped by Sscanf and the two compared equal, leaving the
// updater stuck on the rc). Tags must be "vMAJOR.MINOR.PATCH" with an optional
// -prerelease suffix.
func isNewer(a, b string) bool {
	av := parseSemver(a)
	bv := parseSemver(b)
	for i := 0; i < 3; i++ {
		if av.nums[i] > bv.nums[i] {
			return true
		}
		if av.nums[i] < bv.nums[i] {
			return false
		}
	}
	// Same numeric part: a release beats a prerelease; two prereleases are
	// treated as equal (rc-vs-rc promotion is not used here).
	if av.pre == "" && bv.pre != "" {
		return true
	}
	return false
}

func parseSemver(v string) semver {
	v = strings.TrimPrefix(v, "v")
	var s semver
	if i := strings.IndexByte(v, '-'); i >= 0 {
		s.pre = v[i:]
		v = v[:i]
	}
	fmt.Sscanf(v, "%d.%d.%d", &s.nums[0], &s.nums[1], &s.nums[2])
	return s
}

type semver struct {
	nums [3]int
	pre  string // prerelease suffix ("-rc4", "-beta1", "" = release)
}
