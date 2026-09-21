// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
)

// Matcher answers one question: is this proposal a technique the registry
// already has? It is the novelty gate every lane that files a technique goes
// through — the two machine ones and the member's own contribution.
//
// Two projections, because two different questions are being asked of the same
// text. The STORED vector indexes what a technique applies TO, since that is
// what retrieval matches against a member's situation; it leaves the recipe out
// on purpose. Sameness is a question about the move, so it is asked of the
// recipe (MoveText) — and asking it of the applicability text instead is how a
// registry accumulates fifty differently-worded copies of one instruction.
//
// No recipe vector is stored. Computing one for the whole corpus on every
// proposal would be the corpus per proposal, so the stored vector shortlists
// first and only the shortlist gets a recipe embedded. That is safe rather than
// merely cheap: measured over a 174-technique corpus, of the 360 pairs the
// recipe calls duplicates, the weakest still scores 0.615 on the stored vector,
// well clear of config.DupBlockFloor.
type Matcher struct {
	embedder Embedder
	// move memoises recipe vectors by technique id. A batch shortlists the same
	// techniques over and over, so the second proposal of a pass pays nothing.
	// The memo lives as long as the Matcher, which is one pass or one request —
	// short enough that an edited recipe cannot go stale inside it.
	move map[string]Vector
}

func NewMatcher(embedder Embedder) *Matcher {
	return &Matcher{embedder: embedder, move: map[string]Vector{}}
}

// Match reports the technique this proposal duplicates, and how sure that is.
// vec is the proposal's stored-projection vector, which every caller has
// already computed to store.
//
// A candidate with no stored vector is skipped rather than embedded: it is
// waiting on the backfill job, and embedding the unembedded here would make the
// cost this design avoids reappear one technique at a time.
func (m *Matcher) Match(proposal models.Technique, vec Vector, existing []models.Technique) (models.Technique, float64, bool) {
	// No recipe, nothing to compare. Sameness is a question about the move, and
	// a technique that does not say what to do cannot answer it — comparing the
	// empty string would make every such technique a duplicate of every other.
	if len(vec) == 0 || m.embedder == nil || MoveText(proposal) == "" {
		return models.Technique{}, 0, false
	}
	var shortlist []models.Technique
	for _, c := range existing {
		if len(c.Embedding) != len(vec) || MoveText(c) == "" {
			continue
		}
		if Dot(vec, c.Embedding) >= config.DupBlockFloor {
			shortlist = append(shortlist, c)
		}
	}
	if len(shortlist) == 0 {
		return models.Technique{}, 0, false
	}

	proposalMove := m.embedder.Embed([]string{MoveText(proposal)})[0]
	best, bestSim := models.Technique{}, 0.0
	for _, c := range m.shortlistVectors(shortlist) {
		sim := Dot(proposalMove, c.vec)
		if sim > bestSim {
			best, bestSim = c.technique, sim
		}
	}
	if bestSim < config.DupThreshold {
		return models.Technique{}, bestSim, false
	}
	return best, bestSim, true
}

type candidate struct {
	technique models.Technique
	vec       Vector
}

// shortlistVectors embeds the recipes the memo does not already hold, in one
// batch — an embedder call per candidate would turn a cheap shortlist into an
// expensive one.
func (m *Matcher) shortlistVectors(shortlist []models.Technique) []candidate {
	var missing []string
	var missingIDs []string
	for _, c := range shortlist {
		if _, ok := m.move[c.ID]; !ok {
			missing = append(missing, MoveText(c))
			missingIDs = append(missingIDs, c.ID)
		}
	}
	if len(missing) > 0 {
		for i, vec := range m.embedder.Embed(missing) {
			m.move[missingIDs[i]] = vec
		}
	}
	out := make([]candidate, 0, len(shortlist))
	for _, c := range shortlist {
		if vec, ok := m.move[c.ID]; ok && len(vec) > 0 {
			out = append(out, candidate{c, vec})
		}
	}
	return out
}
