package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mino.env")
	os.WriteFile(path, []byte("MINO_HOME=/home/mino\nCLOUDFLARE_API_TOKEN=keepme\nMINO_API_KEY=old\n"), 0600)
	// unrelated keys survive; empty values don't wipe existing keys
	err := mergeEnvFile(path, map[string]string{
		"MINO_API_KEY":       "new",
		"TELEGRAM_BOT_TOKEN": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "CLOUDFLARE_API_TOKEN=keepme\nMINO_API_KEY=new\nMINO_HOME=/home/mino\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}
