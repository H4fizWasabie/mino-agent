package main

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
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
