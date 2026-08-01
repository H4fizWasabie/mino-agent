package main

import "testing"

func TestLouvainClustersTightGroups(t *testing.T) {
	// Two cliques connected by one bridge: {a,b,c} and {d,e}
	facts := []Fact{
		{ID: "a", Edges: []Edge{{Target: "b", Rel: "x", Kind: "explicit"}, {Target: "c", Rel: "x", Kind: "explicit"}, {Target: "d", Rel: "x", Kind: "explicit"}}},
		{ID: "b", Edges: []Edge{{Target: "a", Rel: "x", Kind: "explicit"}, {Target: "c", Rel: "x", Kind: "explicit"}}},
		{ID: "c", Edges: []Edge{{Target: "a", Rel: "x", Kind: "explicit"}, {Target: "b", Rel: "x", Kind: "explicit"}}},
		{ID: "d", Edges: []Edge{{Target: "e", Rel: "x", Kind: "explicit"}}},
		{ID: "e", Edges: []Edge{{Target: "d", Rel: "x", Kind: "explicit"}}},
	}
	communities, _ := ClusterGraph(facts)
	if communities["a"] != communities["b"] || communities["b"] != communities["c"] {
		t.Fatalf("clique {a,b,c} split across communities: %v", communities)
	}
	if communities["d"] != communities["e"] {
		t.Fatalf("clique {d,e} split: %v", communities)
	}
	if communities["a"] == communities["d"] {
		t.Fatalf("bridge must not merge cliques: %v", communities)
	}
}

func TestGodNodesTopCentrality(t *testing.T) {
	facts := []Fact{
		{ID: "hub", Edges: []Edge{{Target: "x", Rel: "r"}, {Target: "y", Rel: "r"}, {Target: "z", Rel: "r"}}},
		{ID: "x", Edges: []Edge{{Target: "hub", Rel: "r"}}},
		{ID: "y", Edges: []Edge{{Target: "hub", Rel: "r"}}},
		{ID: "z", Edges: []Edge{{Target: "hub", Rel: "r"}}},
	}
	_, gods := ClusterGraph(facts)
	if len(gods) == 0 || gods[0] != "hub" {
		t.Fatalf("gods = %v, want hub first", gods)
	}
}
