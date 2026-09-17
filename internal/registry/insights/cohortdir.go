// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package insights

// The cohort directory answers the question a joining member has and nothing
// else in the product answered: "what do my colleagues call themselves?"
//
// A cohort value is free text typed once, on the member's own machine, at the
// moment they know least about the registry. Get it wrong and nothing breaks
// loudly — the member is simply filed under a cohort of one, their outcomes
// never reach the n=30 that makes a cohort-specific stat serve, their team's
// spread reads high and thin, and the k-anonymity floor on observed-technique
// discovery (config.ObserveMinCohorts) counts one person's two spellings as
// two people. So the fix is not validation, which would reject the first
// member of every new team. It is showing them what already exists.
//
// Aggregate only, like every other cohort surface here: dimension, value, and
// how much the value is used. Never who.

import (
	"sort"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
)

// CohortUse is one cohort value in use, with the two counts that tell a
// joining member whether it is the name their colleagues actually use or
// somebody's one-off. Sessions is distinct pseudonymous sessions tagged with
// the value; Shown is suggestions delivered to it.
type CohortUse struct {
	Value    string `json:"value"`
	Sessions int    `json:"sessions"`
	Shown    int    `json:"shown"`
}

// CohortDimensionUse is one dimension's values, most-used first.
type CohortDimensionUse struct {
	Dimension string      `json:"dimension"`
	Values    []CohortUse `json:"values"`
	// Derived is true for a dimension the session fills in by itself
	// (harness, surface). A member never types these, so a client offering a
	// cohort to pick should show them as context and not as a choice.
	Derived bool `json:"derived,omitempty"`
}

// CohortDirectory lists every cohort value the registry has seen, by
// dimension, most-used first.
//
// It reads both logs because they go quiet at different times: audit facts
// record a member's sessions from their first connected turn, while feedback
// events need a technique to have been shown. A member who joined this morning is
// in the first and not yet the second — and they are exactly the colleague the
// next joiner needs to find.
//
// All time, deliberately. A window would hide the team that shipped last
// quarter and is quiet this one, and a name you cannot see is a name you
// retype differently.
func CohortDirectory(events []models.FeedbackEvent, facts []models.AuditFact) []CohortDimensionUse {
	type tally struct {
		sessions map[string]bool
		anon     int // facts carrying no session hash: countable, not dedupable
		shown    int
	}
	byDim := map[string]map[string]*tally{}
	at := func(dim, val string) *tally {
		vals := byDim[dim]
		if vals == nil {
			vals = map[string]*tally{}
			byDim[dim] = vals
		}
		t := vals[val]
		if t == nil {
			t = &tally{sessions: map[string]bool{}}
			vals[val] = t
		}
		return t
	}

	for _, f := range facts {
		for dim, val := range f.Segment {
			if val == "" {
				continue
			}
			t := at(dim, val)
			if f.SessionHash == "" {
				t.anon++
			} else {
				t.sessions[f.SessionHash] = true
			}
		}
	}
	for _, e := range events {
		if e.Stage != "shown" {
			continue
		}
		for dim, val := range e.Segment {
			if val == "" {
				continue
			}
			at(dim, val).shown++
		}
	}

	derived := map[string]bool{}
	for _, d := range config.SegmentDimensions {
		derived[d] = true
	}
	for _, d := range config.MemberDimensions {
		derived[d] = false
	}

	emit := func(dim string) CohortDimensionUse {
		out := CohortDimensionUse{Dimension: dim, Derived: derived[dim]}
		for val, t := range byDim[dim] {
			out.Values = append(out.Values, CohortUse{
				Value: val, Sessions: len(t.sessions) + t.anon, Shown: t.shown,
			})
		}
		sort.Slice(out.Values, func(i, j int) bool {
			a, b := out.Values[i], out.Values[j]
			if a.Sessions != b.Sessions {
				return a.Sessions > b.Sessions
			}
			if a.Shown != b.Shown {
				return a.Shown > b.Shown
			}
			return a.Value < b.Value
		})
		return out
	}

	// Canonical order first (team before role before the derived pair), then
	// anything a client invented, alphabetically — the same contract
	// cohortDimOrder holds for the breakdown page.
	var dims []CohortDimensionUse
	seen := map[string]bool{}
	for _, d := range cohortDimOrder {
		seen[d] = true
		if len(byDim[d]) > 0 {
			dims = append(dims, emit(d))
		}
	}
	var rest []string
	for d := range byDim {
		if !seen[d] {
			rest = append(rest, d)
		}
	}
	sort.Strings(rest)
	for _, d := range rest {
		dims = append(dims, emit(d))
	}
	return dims
}
