// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package conventions

import "sort"

// DriftReport is what a member's projects look like side by side.
//
// The question it answers is the one nobody with a single repository ever asks:
// where do my own projects disagree with each other? A team's conventions
// converge because everyone edits the same file. One person's diverge, quietly,
// because every project was set up on a different afternoon.
type DriftReport struct {
	Repos     int
	Unmanaged []string // no managed block anywhere in the repository
	Stale     []string // a managed block that is not the current one
	Damaged   []string // a file carrying one marker and not the other
	Gaps      []Gap    // a convention file some projects have and others do not
}

// Gap is one convention file present in some projects and absent from others —
// a harness that is guided in one repository and flying blind in the next.
type Gap struct {
	Path    string
	Harness string
	Present []string
	Missing []string
}

// Drift compares the inspected repositories.
//
// A file absent everywhere is not a gap: nobody uses that harness, and naming
// it would be advice about a tool the member does not run. A file present
// everywhere is not a gap either. Only the split cases are reported, which is
// the difference between a report and a checklist.
func Drift(states []RepoState) DriftReport {
	rep := DriftReport{Repos: len(states)}
	presence := map[string]*Gap{}
	for _, st := range states {
		if !st.Managed() {
			rep.Unmanaged = append(rep.Unmanaged, st.Name)
		}
		if stale := st.Stale(); len(stale) > 0 {
			rep.Stale = append(rep.Stale, st.Name)
		}
		for _, f := range st.Files {
			if f.Damaged {
				rep.Damaged = append(rep.Damaged, st.Name+"/"+f.Path)
			}
			g := presence[f.Path]
			if g == nil {
				g = &Gap{Path: f.Path, Harness: f.Harness}
				presence[f.Path] = g
			}
			if f.Exists {
				g.Present = append(g.Present, st.Name)
			} else {
				g.Missing = append(g.Missing, st.Name)
			}
		}
	}
	for _, t := range Targets {
		g := presence[t.Path]
		if g == nil || len(g.Present) == 0 || len(g.Missing) == 0 {
			continue
		}
		sort.Strings(g.Present)
		sort.Strings(g.Missing)
		rep.Gaps = append(rep.Gaps, *g)
	}
	// Widest gap first: the file most projects are missing is the one worth
	// deciding about.
	sort.SliceStable(rep.Gaps, func(i, j int) bool {
		return len(rep.Gaps[i].Missing) > len(rep.Gaps[j].Missing)
	})
	return rep
}
