package main

import (
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

	if !isNewer(tag, Version) {
		fmt.Printf("Already up to date (%s).\n", Version)
		return nil
	}

	fmt.Printf("Downloading %s...\n", tag)
	resp, err := updateClient.Get(assetURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	exe := currentExe()
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

	if err := os.Rename(newPath, exe); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("replace binary: %w — try running with sudo", err)
	}

	fmt.Printf("Updated to %s. Restart Mino to use the new version.\n", tag)
	return nil
}

// --- helpers ---

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

func fetchLatestAsset() (string, string, error) {
	req, _ := http.NewRequest("GET", releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "mino-agent/"+Version)
	resp, err := updateClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
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

func findAsset(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}) string {
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

// isNewer does naive semver comparison. Tags must be "vMAJOR.MINOR.PATCH".
func isNewer(a, b string) bool {
	av := parseSemver(a)
	bv := parseSemver(b)
	for i := 0; i < 3; i++ {
		if av[i] > bv[i] {
			return true
		}
		if av[i] < bv[i] {
			return false
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	var parts [3]int
	fmt.Sscanf(v, "%d.%d.%d", &parts[0], &parts[1], &parts[2])
	return parts
}
