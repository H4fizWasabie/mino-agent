package main

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseORImage(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	b64 := base64.StdEncoding.EncodeToString(png)
	cases := []struct {
		name    string
		body    string
		wantErr bool
		wantExt string
	}{
		{"data-uri image_url decodes", `{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64 + `"}}]}}]}`, false, ".png"},
		{"jpeg mime maps to .jpg", `{"choices":[{"message":{"content":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,` + b64 + `"}}]}}]}`, false, ".jpg"},
		{"b64_json decodes", `{"choices":[{"message":{"content":[{"type":"image_url","b64_json":"` + b64 + `"}]}}]}`, false, ".png"},
		{"message.images array decodes", `{"choices":[{"message":{"images":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,` + b64 + `"}}]}}]}`, false, ".jpg"},
		{"text-only content errors", `{"choices":[{"message":{"content":"sorry, no image"}}]}`, true, ""},
		{"bad json errors", `not json`, true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img, ext, err := parseORImage([]byte(c.body))
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(img, png) {
				t.Fatalf("decoded %v, want %v", img, png)
			}
			if ext != c.wantExt {
				t.Fatalf("ext %q, want %q", ext, c.wantExt)
			}
		})
	}
}

func TestFetchImage(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer srv.Close()
	img, ext, err := fetchImage(srv.URL + "/img")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(img, png) {
		t.Fatalf("decoded %v, want %v", img, png)
	}
	if ext != ".png" {
		t.Fatalf("ext %q, want .png", ext)
	}
}

func TestParseCFImage(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47} // fake image bytes
	b64 := base64.StdEncoding.EncodeToString(png)
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid image decodes", `{"success":true,"result":{"image":"` + b64 + `"}}`, false},
		{"valid images array decodes", `{"success":true,"result":{"images":["` + b64 + `"]}}`, false},
		{"success false errors", `{"success":false,"result":{}}`, true},
		{"empty image errors", `{"success":true,"result":{"image":""}}`, true},
		{"bad json errors", `not json`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img, err := parseCFImage([]byte(c.body))
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(img, png) {
				t.Fatalf("decoded %v, want %v", img, png)
			}
		})
	}
}

// Issue #16: a non-zero exit with stdout is a PARTIAL failure — the output
// often contains the answer (find exits 1 on unreadable dirs while still
// printing matches). Agents must see the output first, not "Error: exit 1".
func TestBashPartialFailureCarriesOutput(t *testing.T) {
	tool := makeBashToolFor("", 2*time.Minute)
	out := tool.Fn(map[string]any{"command": "echo /home/mino/.mino/mino.db; exit 1"})
	if !strings.Contains(out, "PARTIAL: command exited with error") {
		t.Fatalf("partial-failure marker missing: %q", out)
	}
	if !strings.Contains(out, "/home/mino/.mino/mino.db") {
		t.Fatalf("output not carried in result: %q", out)
	}
}

// MEM-07: save_note accepts the user's verbatim words as an optional why seed,
// stored on the fact and echoed back; the seed feeds the MEM-02 judgment pass.
func TestSaveNoteCapturesWhy(t *testing.T) {
	home := t.TempDir()
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: Connect(home), cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	defer mem.db.Close()
	tool := makeNotesTool(mem.db, mem)

	out := tool.Fn(map[string]any{
		"id":      "planet",
		"subject": "My planet is Mars",
		"content": "Red planet.",
		"why":     "because I live there",
	})
	if !strings.Contains(out, "why: because I live there") {
		t.Fatalf("why not echoed in result: %q", out)
	}
	fact, ok := mem.graph.FindFact("planet")
	if !ok {
		t.Fatal("fact not saved")
	}
	if fact.Why != "because I live there" {
		t.Fatalf("fact.Why = %q, want the user's verbatim words", fact.Why)
	}
	raw, err := os.ReadFile(filepath.Join(memories, "planet.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "why: because I live there") {
		t.Fatalf("why missing from front matter:\n%s", raw)
	}
}

// MEM-07: no why given -> fact saved with empty why (never invented).
func TestSaveNoteWithoutWhy(t *testing.T) {
	home := t.TempDir()
	memories := filepath.Join(home, "memories")
	mem := &Memory{db: Connect(home), cfg: &Settings{Home: home, MemoriesDir: memories}, graph: NewGraphMemory(memories, nil)}
	defer mem.db.Close()
	tool := makeNotesTool(mem.db, mem)

	out := tool.Fn(map[string]any{"id": "plain", "subject": "Plain fact"})
	if strings.Contains(out, "why:") {
		t.Fatalf("invented a why: %q", out)
	}
	fact, ok := mem.graph.FindFact("plain")
	if !ok {
		t.Fatal("fact not saved")
	}
	if fact.Why != "" {
		t.Fatalf("fact.Why = %q, want empty (no asking, no invention)", fact.Why)
	}
}

func TestBashHardFailureNoOutput(t *testing.T) {
	tool := makeBashToolFor("", 2*time.Minute)
	out := tool.Fn(map[string]any{"command": "exit 3"})
	if !strings.Contains(out, "Command failed:") || !strings.Contains(out, "no output") {
		t.Fatalf("hard-failure format wrong: %q", out)
	}
}
