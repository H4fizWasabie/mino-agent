package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestScreenshotHonestFailureWhenRendererMissing locks the #235 lesson at the
// real tool boundary: when the renderer is absent the tool says exactly why
// and names the requirement — never a phantom success, never an empty "ok".
// PATH is forced to nothing so the branch is deterministic on any host (with
// or without wkhtmltoimage installed).
func TestScreenshotHonestFailureWhenRendererMissing(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	home := t.TempDir()
	out := makeScreenshotTool(home).Fn(map[string]any{"url": "http://127.0.0.1:1/"})
	for _, want := range []string{"Screenshot failed", "wkhtmltoimage", "not installed", "headless browser", "install_package"} {
		if !strings.Contains(out, want) {
			t.Fatalf("honest failure result = %q, want it to contain %q", out, want)
		}
	}
	// An honest failure must not leave a phantom artifact behind.
	files := 0
	filepath.WalkDir(filepath.Join(home, "results"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return nil
	})
	if files != 0 {
		t.Fatalf("honest failure left %d artifact(s) in the spill store", files)
	}
}

// TestScreenshotRefusesBadInput locks the arg contract at the tool boundary:
// exactly one of url/path, http(s) scheme, readable local file.
func TestScreenshotRefusesBadInput(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	home := t.TempDir()
	tool := makeScreenshotTool(home)
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"no args", map[string]any{}, "exactly one"},
		{"both args", map[string]any{"url": "http://x", "path": "/tmp/x"}, "exactly one"},
		{"bad scheme", map[string]any{"url": "ftp://x"}, "http(s)"},
		{"missing file", map[string]any{"path": "/nonexistent/nope.html"}, "not readable"},
	}
	for _, c := range cases {
		if out := tool.Fn(c.args); !strings.Contains(out, c.want) {
			t.Errorf("%s: result = %q, want it to contain %q", c.name, out, c.want)
		}
	}
}

// TestScreenshotAdvertisedInRegistry enforces the advertise-all discipline
// (the cost-watch TestToolSchemasAdvertiseAllDispatchableTools lesson): a
// tool registered in BuildRegistry must be advertised in the schemas the
// model sees — dispatchable but unadvertised is unreachable.
func TestScreenshotAdvertisedInRegistry(t *testing.T) {
	home := t.TempDir()
	db := Connect(home)
	defer db.Close()
	r := BuildRegistry(db, home, "/", nil)
	for _, s := range r.Schemas() {
		if s.Name == "screenshot" {
			if s.Description == "" || len(s.Parameters) == 0 {
				t.Fatal("screenshot advertised without a description or input schema")
			}
			return
		}
	}
	t.Fatal("screenshot not advertised in registry schemas")
}

// TestScreenshotCapturesStaticURLAndFile is the acceptance test: a REAL
// capture of a static URL and a local file — a real PNG under the durable
// spill store (RUN-007, prunable by retention), with the path in the result
// and a path view_image can read. Requires the host wkhtmltoimage binary;
// skipped with a clear message when absent — the honest-result contract
// applies to the tests too: a capture is never mocked.
func TestScreenshotCapturesStaticURLAndFile(t *testing.T) {
	if _, err := exec.LookPath("wkhtmltoimage"); err != nil {
		t.Skip("wkhtmltoimage not installed in this sandbox — a real capture cannot run here. The honest-failure path is covered by TestScreenshotHonestFailureWhenRendererMissing; the VPS has the binary.")
	}
	home := t.TempDir()
	tool := makeScreenshotTool(home)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<!doctype html><html><body style="background:#fff"><h1>Static page</h1><p>rendered by wkhtmltoimage</p></body></html>`)
	}))
	defer server.Close()

	htmlFile := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(htmlFile, []byte(`<!doctype html><html><body style="background:#fff"><h1>Local file</h1><p>static</p></body></html>`), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"url", map[string]any{"url": server.URL}},
		{"local file", map[string]any{"path": htmlFile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := tool.Fn(tc.args)
			if strings.Contains(out, "Screenshot failed") || strings.Contains(out, "Error:") {
				t.Fatalf("capture failed: %s", out)
			}
			// The result states what was captured: path and dimensions.
			matches, err := filepath.Glob(filepath.Join(spillDir(home), "screenshots", "*.png"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("spill artifacts = %v (err %v), want exactly one png under %s", matches, err, spillDir(home))
			}
			path := matches[0]
			if !strings.Contains(out, path) {
				t.Fatalf("result = %q, want it to state the artifact path %s", out, path)
			}
			// A real PNG with parseable dimensions, reported in the result.
			data, err := os.ReadFile(path)
			if err != nil || len(data) < 24 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
				t.Fatalf("artifact is not a real PNG: %v", err)
			}
			w, h := int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24]))
			if w == 0 || h == 0 {
				t.Fatalf("artifact dimensions = %dx%d, want non-zero", w, h)
			}
			if !strings.Contains(out, fmt.Sprintf("%dx%d", w, h)) {
				t.Fatalf("result = %q, want it to state dimensions %dx%d", out, w, h)
			}
			// Round-trip: view_image must read the returned path.
			vi := makeViewImageTool().Fn(map[string]any{"path": path})
			if !strings.HasPrefix(vi, "data:image/png;base64,") {
				t.Fatalf("view_image cannot read the artifact: %.60s", vi)
			}
			// RUN-007: the artifact is prunable by spill retention.
			old := time.Now().Add(-spillRetention - time.Hour)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			pruneSpills(home)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("prune did not remove the aged artifact: %v", err)
			}
		})
	}
}
