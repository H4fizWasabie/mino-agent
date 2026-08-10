package main

// Embedding recall merge gate (issue #141): measured natural-paraphrase
// similarity is ~0.5 (text-embedding-3-large), oblique ~0.27 — the gate at
// 0.4 lets close phrasings merge into recall while filtering noise. The
// merge is pure (no API) so the semantics are locked by table.

import (
	"strings"
	"testing"
)

func TestMergeEmbeddingHits(t *testing.T) {
	facts := map[string]*Fact{
		"qwen": {Subject: "Mino runs on qwen/qwen3.7-flash via OpenRouter", Body: "with DeepSeek as small model."},
		"skip": {Subject: "unrelated", Body: "fact"},
	}
	paraphrase := scoredDoc{doc: embeddedDoc{Source: "fact:qwen", Content: "x"}, score: 0.498} // measured close paraphrase
	oblique := scoredDoc{doc: embeddedDoc{Source: "fact:qwen", Content: "x"}, score: 0.269}   // measured oblique query
	stale := scoredDoc{doc: embeddedDoc{Source: "fact:deleted", Content: "x"}, score: 0.9}    // vector without a fact
	legacy := scoredDoc{doc: embeddedDoc{Source: "fact", Content: "Mino runs on qwen/qwen3.7-flash via OpenRouter: with DeepSeek as small model."}, score: 0.6}

	cases := []struct {
		name  string
		ranked []rankedFact
		hits  []scoredDoc
		wantIDs []string
		check  func(t *testing.T, got []rankedFact)
	}{
		{
			"close paraphrase merges with similarity signal",
			nil, []scoredDoc{paraphrase},
			[]string{"qwen"},
			func(t *testing.T, got []rankedFact) {
				if !hasSignal(got, "qwen", "similarity: 0.50") {
					t.Fatalf("expected similarity signal, got %+v", got)
				}
			},
		},
		{
			"oblique query stays filtered",
			nil, []scoredDoc{oblique},
			nil, nil,
		},
		{
			"stale vector (no fact) dropped",
			nil, []scoredDoc{stale},
			nil, nil,
		},
		{
			"existing ranked fact boosted, not duplicated",
			[]rankedFact{{id: "qwen", score: 5, signals: []string{"your words: model"}}},
			[]scoredDoc{paraphrase},
			[]string{"qwen"},
			func(t *testing.T, got []rankedFact) {
				// int(20*0.498) truncates to 9; 5+9 = 14.
				if len(got) != 1 || got[0].score != 14 {
					t.Fatalf("expected boost to 14, got %+v", got)
				}
				if len(got[0].signals) != 2 {
					t.Fatalf("expected keyword + similarity signals, got %+v", got[0].signals)
				}
			},
		},
		{
			"legacy fact source mapped by content",
			nil, []scoredDoc{legacy},
			[]string{"qwen"}, nil,
		},
		{
			"ranking sorted by score desc",
			[]rankedFact{{id: "low", score: 1}},
			[]scoredDoc{paraphrase},
			[]string{"qwen", "low"},
			func(t *testing.T, got []rankedFact) {
				if got[0].id != "qwen" {
					t.Fatalf("expected qwen first, got %+v", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeEmbeddingHits(tc.ranked, tc.hits, facts)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("got %d results %+v, want %d (%v)", len(got), got, len(tc.wantIDs), tc.wantIDs)
			}
			for i, id := range tc.wantIDs {
				if got[i].id != id {
					t.Fatalf("result %d = %q, want %q (all: %+v)", i, got[i].id, id, got)
				}
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

func hasSignal(ranked []rankedFact, id, signal string) bool {
	for _, r := range ranked {
		if r.id != id {
			continue
		}
		for _, s := range r.signals {
			if strings.Contains(s, signal) {
				return true
			}
		}
	}
	return false
}
