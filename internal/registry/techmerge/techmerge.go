// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package techmerge proposes consolidations of the playbook itself.
//
// The novelty gate (registry/embed.Matcher) stops a restatement at the door.
// It cannot touch what is already inside: a corpus accumulates near-identical
// techniques faster than anyone merges them, and every check in the lifecycle —
// decay, shadow retirement, auto-promotion — judges one technique against a bar
// rather than against another technique. Twenty-eight ways of saying "run the
// checks" are twenty-eight independent evaluations, and nothing notices they are
// one move.
//
// That is worse than untidy. Retrieval shows one member of a group at a time, so
// a group of twenty-eight splits its shown/adopted/helped twenty-eight ways, and
// config.MinRankSample means a technique under ten adoptions cannot have measured
// impact reorder it at all. A move that would clear the bar as one technique may
// never clear it as any of its fragments. The group stays unmeasured because it
// is a group.
//
// So this package PROPOSES and never applies, the same bargain package tagmerge
// strikes for the tag vocabulary. Retiring a serving technique is a decision
// about what an organization is told, and a similarity score is not entitled to
// take it. What is different here is that no language model is asked: sameness
// is decided on the recipe, which is measurable, and which of a group to keep is
// decided on the outcomes, which are measured. A model would add an opinion where
// there are already numbers.
package techmerge

import (
	"fmt"
	"sort"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
)

// Lane is which consolidation this is. The two are separate because the
// decisions are: retiring a stable technique takes something members use out of
// retrieval and is answered by adoption evidence, while retiring a shadow one
// drops a candidate nobody has ever been shown and is answered by fit verdicts.
// One panel mixing them made every group ask the harder question.
type Lane int

const (
	// PlaybookLane consolidates the serving library against itself.
	PlaybookLane Lane = iota
	// QueueLane consolidates what is under evaluation. It matches shadow
	// techniques against the serving library as well as against each other,
	// because "this candidate restates something you already publish" is the
	// most useful thing it can find — but only the shadow ones are ever offered
	// for retirement.
	QueueLane
)

// Member is one technique in a group, with the evidence that ranked it.
type Member struct {
	Technique  models.Technique
	Outcome    models.Outcome
	Similarity float64 // to the technique the group would keep; 1 for the keeper
}

// Adopted and Shown read through to the rollup, so the panel does not have to
// know the shape of an Outcome.
func (m Member) Adopted() int { return m.Outcome.Adopted }
func (m Member) Shown() int   { return m.Outcome.Shown }

// Sample is the n the helped rate was computed over: the confidence-WEIGHTED
// adoptions, falling back to the raw count for a rollup written before weights
// existed. It is not Adopted — the stored rate is weighted-helped over
// weighted-adopted, so pairing it with the raw count reports a ratio beside a
// number it was not taken from. Ranking reads the same weighted funnel
// (retrieval.weightedFunnel), and consolidation has to agree with ranking or it
// keeps a technique ranking would not have shown.
func (m Member) Sample() float64 {
	if m.Outcome.WeightedAdopted > 0 || m.Outcome.WeightedHelped > 0 {
		return m.Outcome.WeightedAdopted
	}
	return float64(m.Outcome.Adopted)
}

// HelpedRate is the measured rate, and whether there is one. A technique nobody
// has adopted has no rate — not a rate of zero, which is a claim about it.
func (m Member) HelpedRate() (float64, bool) {
	if m.Outcome.HelpedRate == nil {
		return 0, false
	}
	return *m.Outcome.HelpedRate, true
}

// Measured is the helped rate where the sample is big enough for ranking to use
// it, and it is the only rate anything here reports. A lucky one-of-one is not
// evidence that a technique is the best of its group, and printing it as a
// percentage invites a reviewer to read it as though it were.
func (m Member) Measured() (float64, bool) { return m.measuredRate() }

// Group is a set of techniques that say the same thing, and the one this pass
// would keep.
type Group struct {
	Keep   Member
	Retire []Member
	// Alongside is the members this lane may not retire — a serving technique
	// that a shadow candidate restates. Shown so the reviewer can see what the
	// candidate duplicates, never offered as a casualty.
	Alongside []Member
	Lane      Lane
	Why       string
}

// Propose finds the groups and ranks them for one lane. It reads nothing and
// writes nothing: the caller supplies the techniques and the rollups, and gets
// back what it might put to a reviewer.
//
// A draft is in neither lane. It is not serving, the drafts lane already
// decides about it, and the novelty gate stops the duplicates that used to
// arrive as drafts.
func Propose(techniques []models.Technique, outcomes map[string]models.Outcome,
	embedder embed.Embedder, lane Lane) []Group {
	var live []models.Technique
	for _, t := range techniques {
		if embed.MoveText(t) == "" {
			continue
		}
		if t.Status == "stable" || (lane == QueueLane && t.Status == "shadow") {
			live = append(live, t)
		}
	}
	if len(live) < 2 || embedder == nil {
		return nil
	}
	// Deterministic input order, so the same corpus proposes the same groups.
	sort.Slice(live, func(i, j int) bool { return live[i].ID < live[j].ID })

	texts := make([]string, len(live))
	for i, t := range live {
		texts[i] = embed.MoveText(t)
	}
	vecs := embedder.Embed(texts)

	neighbours := make([][]int, len(live))
	sims := make([]map[int]float64, len(live))
	for i := range live {
		sims[i] = map[int]float64{}
		for j := range live {
			if i == j {
				continue
			}
			if d := embed.Dot(vecs[i], vecs[j]); d >= config.DupThreshold {
				neighbours[i] = append(neighbours[i], j)
				sims[i][j] = d
			}
		}
	}

	// Every pair in a group has to be a duplicate of every other — a clique, not
	// a chain and not a star. Near-duplicate similarity is not transitive: A can
	// say the same thing as B, and B as C, while A and C say different things,
	// and a hub like B would otherwise pull both ends onto one line of a
	// reviewer's screen. A proposal is worth having only if it can be read
	// quickly and trusted, so the grouping pays for that up front.
	taken := make([]bool, len(live))
	var groups []Group
	for {
		seed, best := -1, 0
		for i := range live {
			if taken[i] {
				continue
			}
			n := 0
			for _, j := range neighbours[i] {
				if !taken[j] {
					n++
				}
			}
			if n > best {
				seed, best = i, n
			}
		}
		if seed < 0 {
			break
		}
		// Candidates in descending closeness to the seed, so the group forms
		// around the strongest agreement and the order cannot vary between runs.
		candidates := append([]int(nil), neighbours[seed]...)
		sort.Slice(candidates, func(a, b int) bool {
			if sims[seed][candidates[a]] != sims[seed][candidates[b]] {
				return sims[seed][candidates[a]] > sims[seed][candidates[b]]
			}
			return live[candidates[a]].ID < live[candidates[b]].ID
		})
		members := []int{seed}
		for _, j := range candidates {
			if taken[j] {
				continue
			}
			clique := true
			for _, m := range members {
				if m != seed && sims[m][j] < config.DupThreshold {
					clique = false
					break
				}
			}
			if clique {
				members = append(members, j)
			}
		}
		if len(members) < 2 {
			// A hub whose neighbours disagree with each other proposes nothing.
			// It is still available to join somebody else's group.
			taken[seed] = true
			continue
		}
		for _, i := range members {
			taken[i] = true
		}
		g, ok := build(live, outcomes, sims[seed], members, seed, lane)
		if !ok {
			continue
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Retire) != len(groups[j].Retire) {
			return len(groups[i].Retire) > len(groups[j].Retire)
		}
		return groups[i].Keep.Technique.ID < groups[j].Keep.Technique.ID
	})
	return groups
}

func build(live []models.Technique, outcomes map[string]models.Outcome,
	seedSims map[int]float64, members []int, seed int, lane Lane) (Group, bool) {
	all := make([]Member, 0, len(members))
	for _, i := range members {
		sim := 1.0
		if i != seed {
			sim = seedSims[i]
		}
		all = append(all, Member{Technique: live[i], Outcome: outcomes[live[i].ID], Similarity: sim})
	}
	sort.Slice(all, func(i, j int) bool { return better(all[i], all[j]) })

	g := Group{Keep: all[0], Lane: lane}
	for _, m := range all[1:] {
		// In the queue lane a stable technique is context, never a casualty: it
		// is there to say "you already publish this", and retiring it is the
		// playbook lane's decision, taken on evidence this lane does not read.
		if lane == QueueLane && m.Technique.Status == "stable" {
			g.Alongside = append(g.Alongside, m)
			continue
		}
		g.Retire = append(g.Retire, m)
	}
	if len(g.Retire) == 0 {
		// Nothing this lane may act on. An all-stable group found while looking
		// at the queue belongs to the playbook, which proposes it there.
		return Group{}, false
	}
	g.Why = why(g.Keep, len(g.Retire)+len(g.Alongside))
	return g, true
}

// better ranks the group: the technique at the front is the one to keep.
//
// Status leads, because a shadow technique has never been shown to anybody and
// therefore has no adoption evidence to beat a serving one with — retiring what
// members use in favour of what they have never seen would be deciding on an
// absence.
//
// Then the measured helped rate, but only where there is enough of it to mean
// something (config.MinRankSample, the same floor ranking uses). Raw adoption is
// the tie-break rather than the criterion: which duplicate members adopted was
// largely which one retrieval happened to show them, and the oldest has had the
// longest to accumulate, so ranking on the count alone mostly rewards seniority.
func better(a, b Member) bool {
	if ra, rb := statusRank(a), statusRank(b); ra != rb {
		return ra < rb
	}
	aRate, aOK := a.measuredRate()
	bRate, bOK := b.measuredRate()
	if aOK != bOK {
		return aOK
	}
	if aOK && aRate != bRate {
		return aRate > bRate
	}
	if a.Adopted() != b.Adopted() {
		return a.Adopted() > b.Adopted()
	}
	if a.Shown() != b.Shown() {
		return a.Shown() > b.Shown()
	}
	// Oldest wins what evidence cannot settle: it is the one whose id members
	// and other registries are most likely to have written down already.
	if a.Technique.CreatedAt != b.Technique.CreatedAt {
		return a.Technique.CreatedAt < b.Technique.CreatedAt
	}
	return a.Technique.ID < b.Technique.ID
}

func (m Member) measuredRate() (float64, bool) {
	rate, ok := m.HelpedRate()
	if !ok || m.Sample() < config.MinRankSample {
		return 0, false
	}
	return rate, true
}

func statusRank(m Member) int {
	if m.Technique.Status == "stable" {
		return 0
	}
	return 1
}

// why says what decided it, in the numbers the registry already has. A proposal
// a reviewer cannot audit is one they have to take on trust, and this one is
// asking to retire things members use.
func why(keep Member, retiring int) string {
	// The count in the sentence is the whole group, so it is what decides the
	// noun — "2 technique say the same thing" was pluralising on the wrong one.
	total := retiring + 1
	noun := "techniques"
	if total == 1 {
		noun = "technique"
	}
	if rate, ok := keep.measuredRate(); ok {
		return fmt.Sprintf("%d %s say the same thing; this one helped %.0f%% over %.0f adoptions",
			total, noun, rate*100, keep.Sample())
	}
	if keep.Adopted() > 0 {
		return fmt.Sprintf("%d %s say the same thing; this one has the most adoptions (%d), too few to rate",
			total, noun, keep.Adopted())
	}
	if keep.Shown() > 0 {
		return fmt.Sprintf("%d %s say the same thing; none has been adopted, so this one is kept for being shown most (%d)",
			total, noun, keep.Shown())
	}
	return fmt.Sprintf("%d %s say the same thing; none has been shown, so the earliest is kept", total, noun)
}
