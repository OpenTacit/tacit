// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import "github.com/opentacit/tacit/internal/registry/storage"

// mergedInto maps a consolidated technique's id to the one that survived
// (registry/techmerge, contracts.Technique.MergedInto).
//
// Every fold over the event log goes through it, so the survivor of a
// consolidation is judged on what the whole group learned rather than starting
// from whatever it happened to have accumulated on its own. That is the point
// of merging: a group of twenty-six splits its evidence twenty-six ways, and
// keeping one of them without its group's evidence keeps the fragment.
//
// The redirect is applied in memory on the way into a fold, the way
// RecomputeOutcomes already recovers a missing model cohort. The event log is
// append-only and stays untouched, and re-running a fold changes nothing.
//
// Chains resolve: B merged into A, then C merged into B, gives C -> A. A cycle
// cannot be built by the review lane — a technique is retired the moment it is
// merged, and a retired one is never offered as a survivor — but the walk is
// bounded anyway, because a corrupted table should degrade to the un-redirected
// answer rather than hang.
func mergedInto(st storage.Store) (map[string]string, error) {
	techniques, err := st.ListTechniques(nil, 0)
	if err != nil {
		return nil, err
	}
	direct := map[string]string{}
	for _, t := range techniques {
		if t.MergedInto != "" && t.MergedInto != t.ID {
			direct[t.ID] = t.MergedInto
		}
	}
	if len(direct) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(direct))
	for id := range direct {
		to, seen := id, 0
		for seen < len(direct)+1 {
			next, ok := direct[to]
			if !ok {
				break
			}
			to, seen = next, seen+1
		}
		if to != id {
			out[id] = to
		}
	}
	return out, nil
}

// redirect is the id a fold should count an event under.
func redirect(into map[string]string, id string) string {
	if to, ok := into[id]; ok {
		return to
	}
	return id
}
