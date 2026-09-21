// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package techmap

import (
	"sort"
	"strings"
)

// detectClusters finds communities in the weighted shared-tag graph by greedy
// modularity maximization (Clauset–Newman–Moore): every technique starts in its
// own community, and the pair of communities whose merge most raises modularity
// is merged, until no merge helps. Deterministic — ties break by index — so the
// same library always clusters the same way.
//
// It mutates each clustered node's Cluster field and returns the clusters of two
// or more members (a lone technique is not an area of practice), each named from
// its members' dominant tags. Runs only up to a few hundred nodes; beyond that
// the map returns no clusters rather than spend the time.
func detectClusters(nodes []Node, edges []Edge) []Cluster {
	n := len(nodes)
	if n < 2 || n > 400 {
		return nil
	}

	// community weight between comms, community degree, and members.
	ew := make([]map[int]float64, n)
	deg := make([]float64, n)
	members := make([][]int, n)
	alive := make([]bool, n)
	var m float64
	for i := 0; i < n; i++ {
		ew[i] = map[int]float64{}
		members[i] = []int{i}
		alive[i] = true
	}
	for _, e := range edges {
		w := float64(e.W)
		ew[e.A][e.B] += w
		ew[e.B][e.A] += w
		deg[e.A] += w
		deg[e.B] += w
		m += w
	}
	if m == 0 {
		return nil // no shared tags -> no communities
	}
	twoM2 := (2 * m) * (2 * m)

	// Neighbours are visited in index order, not map order.
	//
	// Ties are ordinary here — two pairs of techniques sharing the same tags
	// score exactly the same ΔQ — and the first pair seen wins. Ranging over the
	// map made "first" a coin flip per process, so the same playbook grouped
	// itself differently across a restart: seven areas of practice one morning,
	// six the next, with techniques moving between them and nothing in the data
	// changed. A page that reports what the organization knows cannot answer
	// differently each time it is asked.
	nbrs := make([]int, 0, 16)
	for {
		bestA, bestB, bestDQ := -1, -1, 1e-12
		for a := 0; a < n; a++ {
			if !alive[a] {
				continue
			}
			nbrs = nbrs[:0]
			for b := range ew[a] {
				nbrs = append(nbrs, b)
			}
			sort.Ints(nbrs)
			for _, b := range nbrs {
				if b <= a || !alive[b] {
					continue
				}
				// ΔQ of merging a and b.
				dq := ew[a][b]/m - 2*deg[a]*deg[b]/twoM2
				if dq > bestDQ {
					bestDQ, bestA, bestB = dq, a, b
				}
			}
		}
		if bestA < 0 {
			break
		}
		// Merge bestB into bestA.
		for x, w := range ew[bestB] {
			if x == bestA {
				continue
			}
			ew[bestA][x] += w
			ew[x][bestA] += w
			delete(ew[x], bestB)
		}
		delete(ew[bestA], bestB)
		deg[bestA] += deg[bestB]
		members[bestA] = append(members[bestA], members[bestB]...)
		alive[bestB] = false
		ew[bestB] = nil
		members[bestB] = nil
	}

	// Emit clusters of >=2, largest first, and stamp each member's Cluster id.
	var comms [][]int
	for a := 0; a < n; a++ {
		if alive[a] && len(members[a]) >= 2 {
			ms := append([]int(nil), members[a]...)
			sort.Ints(ms)
			comms = append(comms, ms)
		}
	}
	sort.SliceStable(comms, func(i, j int) bool { return len(comms[i]) > len(comms[j]) })

	clusters := make([]Cluster, 0, len(comms))
	for id, ms := range comms {
		for _, i := range ms {
			nodes[i].Cluster = id
		}
		tags := dominantTags(nodes, ms)
		clusters = append(clusters, Cluster{
			ID: id, Members: ms, Tags: tags, Name: tagName(tags),
		})
	}
	return clusters
}

// dominantTags ranks a cluster's tags by how many of its members carry them,
// commonest first, ties broken alphabetically for stability.
func dominantTags(nodes []Node, members []int) []string {
	count := map[string]int{}
	for _, i := range members {
		for _, t := range nodes[i].Tags {
			count[t]++
		}
	}
	tags := make([]string, 0, len(count))
	for t := range count {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool {
		if count[tags[i]] != count[tags[j]] {
			return count[tags[i]] > count[tags[j]]
		}
		return tags[i] < tags[j]
	})
	if len(tags) > 6 {
		tags = tags[:6]
	}
	return tags
}

// tagName is the honest default label for a cluster: its dominant tag, plus a
// second when it is nearly as common (so a genuinely two-themed cluster reads as
// both). This is what shows until the LLM naming pass fills in something better.
func tagName(tags []string) string {
	if len(tags) == 0 {
		return "untagged"
	}
	return strings.Join(tags[:1], " ")
}
