package main

import "testing"

func TestEmbeddingQueryCacheReusesTrimmedQuery(t *testing.T) {
	es := &EmbeddingStore{
		model:      "embedding-model",
		queryCache: map[string][]float32{"embedding-model\x00same query": {1, 2, 3}},
	}
	got, err := es.cachedEmbed("  same query  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("cached embedding = %#v", got)
	}
}
