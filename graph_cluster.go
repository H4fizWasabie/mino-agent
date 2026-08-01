package main

// graph_cluster — Louvain community detection and god nodes for the memory
// graph. Deterministic, stdlib-only, runs automatically after edge passes.

import "sort"

// ClusterGraph runs Louvain (modularity-optimizing) on the fact graph and
// returns per-fact community IDs plus god nodes (top degree centrality).
func ClusterGraph(facts []Fact) (map[string]int, []string) {
	// Adjacency from explicit + inferred edges (undirected for clustering).
	adj := make(map[string]map[string]bool)
	degree := make(map[string]int)
	for _, f := range facts {
		adj[f.ID] = make(map[string]bool)
		degree[f.ID] = 0
	}
	for _, f := range facts {
		for _, e := range f.Edges {
			if e.Target == f.ID {
				continue
			}
			if _, ok := adj[e.Target]; !ok {
				continue
			}
			if !adj[f.ID][e.Target] {
				adj[f.ID][e.Target] = true
				adj[e.Target][f.ID] = true
				degree[f.ID]++
				degree[e.Target]++
			}
		}
	}
	m := louvain(adj)
	// God nodes: top 10% (min 1) by degree, ties broken by ID.
	var ids []string
	for id := range degree {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if degree[ids[i]] != degree[ids[j]] {
			return degree[ids[i]] > degree[ids[j]]
		}
		return ids[i] < ids[j]
	})
	nGods := len(ids) / 10
	if nGods < 1 {
		nGods = 1
	}
	return m, ids[:nGods]
}

func louvain(adj map[string]map[string]bool) map[string]int {
	community := make(map[string]int, len(adj))
	deg := make(map[string]int, len(adj))
	tot := make(map[int]int) // community → sum of member degrees
	m := 0
	for i, id := range sortedKeys(adj) {
		community[id] = i
		deg[id] = len(adj[id])
		tot[i] = deg[id]
		m += deg[id]
	}
	m /= 2 // edge count
	// Greedy local moves with the exact modularity delta for moving node i
	// from community D to C: ΔQ = (k_in(C) − k_in(D))/m + k_i·(s_D − s_C −
	// k_i)/(2m²). Only positive-gain moves are accepted, so modularity
	// strictly increases and the loop terminates; nodes and ties are visited
	// in sorted order, so results are deterministic. Passes are capped so a
	// pathological float edge case can never hang the 6h goroutine.
	// ponytail: single-level Louvain, no aggregation hierarchy — graphs here
	// are <10k nodes; add phase 2 if community quality ever matters.
	for pass := 0; pass < 100; pass++ {
		moved := false
		for _, id := range sortedKeys(adj) {
			neighborComms := make(map[int]int) // community → edge count
			for nb := range adj[id] {
				neighborComms[community[nb]]++
			}
			// Deterministic tie-break: lowest community ID wins.
			var comms []int
			for c := range neighborComms {
				comms = append(comms, c)
			}
			sort.Ints(comms)
			best, bestGain := community[id], 0.0
			for _, c := range comms {
				gain := float64(neighborComms[c]-neighborComms[community[id]])/float64(m) +
					float64(deg[id])*float64(tot[community[id]]-tot[c]-deg[id])/(2*float64(m)*float64(m))
				if gain > bestGain {
					best, bestGain = c, gain
				}
			}
			if bestGain > 0 {
				tot[community[id]] -= deg[id]
				community[id] = best
				tot[best] += deg[id]
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	// Renumber 0..n-1 for stable storage
	seen := make(map[int]int)
	next := 0
	for _, id := range sortedKeys(adj) {
		c := community[id]
		if _, ok := seen[c]; !ok {
			seen[c] = next
			next++
		}
		community[id] = seen[c]
	}
	return community
}

func sortedKeys(adj map[string]map[string]bool) []string {
	keys := make([]string, 0, len(adj))
	for k := range adj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
