package main

import "testing"

func TestDetectLoop(t *testing.T) {
	tests := []struct {
		name     string
		history  []string
		wantLoop bool
		wantMsg  string
	}{
		{"empty", []string{}, false, ""},
		{"below threshold", []string{"a", "b", "a"}, false, ""},
		{"exact threshold", []string{"a", "a", "a"}, true, "Detected 3 repeated calls to a"},
		{"above threshold", []string{"a", "a", "a", "a"}, true, "Detected 4 repeated calls to a"},
		{"mixed then loop", []string{"b", "a", "a", "a"}, true, "Detected 3 repeated calls to a"},
		{"interleaved", []string{"a", "b", "a", "b"}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLoop, gotMsg := detectLoop(tt.history)
			if gotLoop != tt.wantLoop {
				t.Errorf("loop = %v, want %v", gotLoop, tt.wantLoop)
			}
			if gotMsg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", gotMsg, tt.wantMsg)
			}
		})
	}
}
