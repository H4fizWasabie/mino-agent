package main

import (
	"bytes"
	"encoding/base64"
	"testing"
)

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
