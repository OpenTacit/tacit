// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package techmap

import (
	"fmt"
	"strings"
	"testing"
)

// The same playbook must group itself the same way every time it is asked.
//
// Ties are ordinary in this graph — several pairs of techniques share exactly
// the same tags, so their merges score exactly the same ΔQ — and the first pair
// seen wins. Neighbours used to be visited in map order, which Go randomises,
// so "first" was a coin flip: the Playbook page showed seven areas of practice
// on one run of the registry and six on the next, with techniques moving
// between them and nothing in the data changed. The doc comment claimed ties
// broke by index; this is what makes that true.
func TestTheSameGraphAlwaysClustersTheSameWay(t *testing.T) {
	// A ring of equal-weight edges: every merge scores the same, so the result
	// is decided entirely by the order neighbours are visited in.
	const n = 12
	nodes := make([]Node, n)
	var edges []Edge
	for i := range nodes {
		nodes[i] = Node{ID: fmt.Sprintf("t%02d", i), Name: fmt.Sprintf("technique %d", i)}
		edges = append(edges, Edge{A: i, B: (i + 1) % n, W: 1})
		edges = append(edges, Edge{A: i, B: (i + 3) % n, W: 1})
	}

	first := ""
	for run := 0; run < 50; run++ {
		fresh := make([]Node, len(nodes))
		copy(fresh, nodes)
		got := describe(detectClusters(fresh, edges))
		if run == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d grouped the same graph differently:\n  first %s\n  now   %s", run, first, got)
		}
	}
	if first == "" {
		t.Fatal("the ring produced no clusters at all; the test is not exercising the merge")
	}
}

func describe(cs []Cluster) string {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "[%v]", c.Members)
	}
	return b.String()
}
