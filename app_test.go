package main
import "testing"

// CTX-022 C (round 3): the harness signs contradictions between the draft
// reply and owner-established facts. Parse layer locks the verdict handling.
func TestParseVerifyResponse(t *testing.T) {
	cases := []struct {
		text string
		ok   bool
	}{
		{`{"ok": true}`, true},
		{`{"ok": false, "reason": "reply says alive but owner deleted it"}`, false},
		{`Sure thing! {"ok": false, "reason": "contradicts deletion"} trailing`, false},
		{`no json here`, true},
	}
	for _, c := range cases {
		ok, reason := parseVerifyResponse(c.text)
		if ok != c.ok {
			t.Fatalf("parseVerifyResponse(%q) ok=%v want %v", c.text, ok, c.ok)
		}
		if !c.ok && reason == "" {
			t.Fatalf("non-ok verdict missing reason: %q", c.text)
		}
	}
}
