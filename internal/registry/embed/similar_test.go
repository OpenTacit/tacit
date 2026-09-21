// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"math"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
)

// geometry is a stand-in embedder that returns whatever vector the test asks
// for. The Matcher's job is to decide from angles, so the tests state angles.
// Which angles a real model produces for real techniques is a question about a
// corpus and is answered by measuring one, not by inventing fixtures here.
type geometry func(text string) Vector

func (g geometry) Embed(texts []string) []Vector {
	out := make([]Vector, len(texts))
	for i, t := range texts {
		out[i] = g(t)
	}
	return out
}
func (g geometry) ModelID() string { return "geometry" }
func (g geometry) Dim() int        { return 2 }

// at returns a unit vector cos away from {1,0}.
func at(cos float64) Vector {
	return Vector{float32(cos), float32(math.Sqrt(1 - cos*cos))}
}

func technique(id, name, applies, recipe string) models.Technique {
	return models.Technique{ID: id, Name: name, AppliesWhen: applies, Recipe: recipe}
}

// place builds an embedder that puts each technique's two projections where the
// test says, and returns the held technique with its stored vector already set.
func place(held models.Technique, storedHeld, moveHeld float64,
	proposal models.Technique, storedProposal, moveProposal float64) (Embedder, models.Technique) {
	g := geometry(func(text string) Vector {
		switch text {
		case TechniqueText(held):
			return at(storedHeld)
		case MoveText(held):
			return at(moveHeld)
		case TechniqueText(proposal):
			return at(storedProposal)
		case MoveText(proposal):
			return at(moveProposal)
		}
		panic("the test did not place: " + text)
	})
	held.Embedding = g.Embed([]string{TechniqueText(held)})[0]
	return g, held
}

// The point of the whole design: sameness is decided on the recipe. Two
// techniques framed differently enough that the stored vector calls them
// distinct are still one technique when they tell you to do the same thing.
func TestSamenessIsDecidedOnTheMoveNotTheFraming(t *testing.T) {
	held := technique("test-before-commit", "Test Before Commit",
		"about to commit", "run the suite and fix what fails before committing")
	proposal := technique("test-before-done", "Test-Before-Done Checkpoint",
		"wrapping up a piece of work", "run the suite and fix what fails before committing")

	// Framing 0.60 apart — well under DupThreshold, which is why today's check
	// lets this through. Move 0.95 apart, which is what makes it a duplicate.
	e, held := place(held, 1.0, 1.0, proposal, 0.60, 0.95)
	vec := e.Embed([]string{TechniqueText(proposal)})[0]

	match, sim, dup := NewMatcher(e).Match(proposal, vec, []models.Technique{held})
	if !dup {
		t.Fatalf("a restatement of the same move was not caught (similarity %.3f)", sim)
	}
	if match.ID != held.ID {
		t.Errorf("matched %q, expected %q", match.ID, held.ID)
	}
	if sim < config.DupThreshold {
		t.Errorf("reported a duplicate on %.3f, below the threshold of %.2f", sim, config.DupThreshold)
	}
}

// The converse, which is what stops this being a blunt instrument. Two
// techniques that apply in the same situation and tell you to do different
// things are two techniques, however alike their framing reads.
func TestTheSameSituationWithADifferentMoveIsNotADuplicate(t *testing.T) {
	held := technique("test-before-commit", "Test Before Commit",
		"about to commit", "run the suite and fix what fails")
	proposal := technique("bisect-it", "Bisect it",
		"about to commit", "git bisect between the last good commit and HEAD")

	// Framing almost identical (0.98); move unrelated (0.20).
	e, held := place(held, 1.0, 1.0, proposal, 0.98, 0.20)
	vec := e.Embed([]string{TechniqueText(proposal)})[0]

	if match, sim, dup := NewMatcher(e).Match(proposal, vec, []models.Technique{held}); dup {
		t.Errorf("a different move was called a duplicate of %q at %.3f", match.ID, sim)
	}
}

// The shortlist is an optimisation, and this is the case it gets wrong: a
// duplicate whose framing is so unlike the original that the stored vector
// never offers it for comparison. The floor is set far below where duplicates
// have been observed so that this stays hypothetical, and the test states the
// limitation rather than pretending it away.
func TestADuplicateBelowTheShortlistFloorIsMissed(t *testing.T) {
	held := technique("held", "Held", "some situation", "do the very same thing")
	proposal := technique("proposal", "Proposal", "nothing alike", "do the very same thing")

	e, held := place(held, 1.0, 1.0, proposal, config.DupBlockFloor-0.05, 1.0)
	vec := e.Embed([]string{TechniqueText(proposal)})[0]

	if _, _, dup := NewMatcher(e).Match(proposal, vec, []models.Technique{held}); dup {
		t.Error("the shortlist floor no longer bounds what is compared, " +
			"which means every proposal now embeds the whole corpus")
	}
}

// The two constants have to keep their relationship, or the shortlist stops
// narrowing the question and starts answering it.
func TestTheShortlistFloorLeavesRoomBelowTheDuplicateThreshold(t *testing.T) {
	if config.DupBlockFloor >= config.DupThreshold {
		t.Fatalf("the shortlist floor (%.2f) is not below the duplicate threshold (%.2f), "+
			"so shortlisting decides the answer instead of narrowing it",
			config.DupBlockFloor, config.DupThreshold)
	}
	// Measured over a 174-technique corpus: the weakest pair the recipe called a
	// duplicate still scored 0.615 on the stored vector. A floor above that
	// drops real duplicates before their recipes are ever compared.
	if config.DupBlockFloor > 0.60 {
		t.Errorf("the shortlist floor is %.2f, and duplicates have been observed at 0.615 "+
			"on the stored vector", config.DupBlockFloor)
	}
}

// Nothing to compare against is not a duplicate. The first technique a registry
// ever receives takes this path, and so does one filed while the backfill job
// still owes a candidate its vector.
func TestNothingToCompareAgainstIsNotADuplicate(t *testing.T) {
	p := technique("first", "First", "any time", "do the thing")
	g := geometry(func(string) Vector { return at(1.0) })
	vec := g.Embed([]string{TechniqueText(p)})[0]

	if _, _, dup := NewMatcher(g).Match(p, vec, nil); dup {
		t.Error("a proposal duplicated something in an empty corpus")
	}
	unembedded := technique("waiting", "Waiting on the backfill", "x", "do the thing")
	if _, _, dup := NewMatcher(g).Match(p, vec, []models.Technique{unembedded}); dup {
		t.Error("a technique with no stored vector was matched anyway")
	}
}

// The memo exists so a batch does not re-embed the same candidates for every
// proposal in it. It must not change what the answer is.
func TestTheMemoDoesNotChangeTheAnswer(t *testing.T) {
	held := technique("held", "Held", "when X", "do the very same thing")
	proposal := technique("p", "Proposal", "when Y", "do the very same thing")
	e, held := place(held, 1.0, 1.0, proposal, 0.70, 0.99)
	vec := e.Embed([]string{TechniqueText(proposal)})[0]

	m := NewMatcher(e)
	firstID, firstSim, firstDup := idOf(m.Match(proposal, vec, []models.Technique{held}))
	secondID, secondSim, secondDup := idOf(m.Match(proposal, vec, []models.Technique{held}))
	if firstID != secondID || firstSim != secondSim || firstDup != secondDup {
		t.Errorf("the memo changed the answer: (%q %.3f %v) then (%q %.3f %v)",
			firstID, firstSim, firstDup, secondID, secondSim, secondDup)
	}
}

func idOf(t models.Technique, sim float64, dup bool) (string, float64, bool) {
	return t.ID, sim, dup
}

// A technique with no recipe says nothing about what to do, so it is not a
// duplicate of anything and nothing is a duplicate of it. Comparing empty
// strings would otherwise make every such technique match every other.
func TestATechniqueWithNoRecipeMatchesNothing(t *testing.T) {
	g := geometry(func(string) Vector { return at(1.0) })
	withRecipe := technique("has", "Has one", "when X", "do the thing")
	withRecipe.Embedding = g.Embed([]string{TechniqueText(withRecipe)})[0]
	noRecipe := technique("none", "Has none", "when X", "")
	noRecipe.Embedding = g.Embed([]string{TechniqueText(noRecipe)})[0]

	m := NewMatcher(g)
	vec := g.Embed([]string{TechniqueText(noRecipe)})[0]
	if _, _, dup := m.Match(noRecipe, vec, []models.Technique{withRecipe}); dup {
		t.Error("a proposal with no recipe was called a duplicate")
	}
	if _, _, dup := m.Match(withRecipe, vec, []models.Technique{noRecipe}); dup {
		t.Error("a technique with no recipe was matched against")
	}
}
