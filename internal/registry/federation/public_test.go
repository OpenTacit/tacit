// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"fmt"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// outcomes builds an OutcomeFunc from weighted (helped, adopted) pairs.
func outcomes(m map[string][2]float64) OutcomeFunc {
	return func(id string) (models.Outcome, bool) {
		p, ok := m[id]
		if !ok {
			return models.Outcome{}, false
		}
		rate := 0.0
		if p[1] > 0 {
			rate = p[0] / p[1]
		}
		return models.Outcome{
			TechniqueID: id, SegmentKey: "__overall__",
			WeightedHelped: p[0], WeightedAdopted: p[1],
			Helped: int(p[0]), Adopted: int(p[1]),
			HelpedRate: &rate, SampleSize: int(p[1]),
		}, true
	}
}

func publicTechnique(id string) models.Technique {
	return models.Technique{
		ID: id, Name: "Name " + id, Description: "d", Recipe: "recipe " + id,
		Scope: "general", Status: "stable", Provenance: "curated", Version: 1,
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
}

func allApproved(string) bool { return true }

// The whole reason for the bound: a perfect record over one adoption must not
// outrank a strong record over many. Ranking a commons by the raw rate fills it
// with noise and buries the techniques the pool exists to move.
func TestWilsonPutsThinPerfectionBelowProvenStrength(t *testing.T) {
	thin := WilsonLowerBound(1, 1)     // 100%, n=1
	proven := WilsonLowerBound(40, 50) // 80%, n=50
	if thin >= proven {
		t.Errorf("1/1 bound %.3f >= 40/50 bound %.3f: the raw rate is still deciding", thin, proven)
	}
	// And it must stay a probability.
	for _, c := range [][2]float64{{0, 0}, {0, 10}, {10, 10}, {-1, 5}, {9, 3}} {
		if b := WilsonLowerBound(c[0], c[1]); b < 0 || b > 1 {
			t.Errorf("WilsonLowerBound(%v, %v) = %v, outside [0,1]", c[0], c[1], b)
		}
	}
	// More evidence at the same rate is worth more.
	if WilsonLowerBound(8, 10) >= WilsonLowerBound(80, 100) {
		t.Error("the bound does not reward a larger sample at the same rate")
	}
}

func TestRankPublicOrdersByBoundNotRate(t *testing.T) {
	techniques := []models.Technique{publicTechnique("thin"), publicTechnique("proven")}
	ranked, _ := RankPublic(techniques,
		outcomes(map[string][2]float64{"thin": {5, 5}, "proven": {40, 50}}),
		allApproved, PublicPolicy{})
	if len(ranked) != 2 {
		t.Fatalf("ranked %d techniques, want 2", len(ranked))
	}
	if ranked[0].TechniqueID != "proven" {
		t.Errorf("rank 1 is %q (helped %.2f, n=%d); the thin 100%% technique won",
			ranked[0].TechniqueID, ranked[0].Helped, ranked[0].N)
	}
	if ranked[0].Rank != 1 || ranked[1].Rank != 2 {
		t.Error("ranks are not 1-based and dense")
	}
}

// Each exclusion path, with the reason an operator will read. The scrub case is
// the one nobody would guess without being told.
func TestRankPublicExcludesWithReasons(t *testing.T) {
	org := publicTechnique("org-one")
	org.Scope = "org"

	imported := publicTechnique("ext/peer/thing")
	imported.Provenance = "federated"

	unapproved := publicTechnique("machine")
	unapproved.Provenance = "suggested"

	leaky := publicTechnique("leaky")
	leaky.Recipe = "1. Mail the results to ops@example.com when it finishes"

	thin := publicTechnique("thin")
	noEvidence := publicTechnique("unmeasured")
	draft := publicTechnique("a-draft")
	draft.Status = "draft"

	techniques := []models.Technique{org, imported, unapproved, leaky, thin, noEvidence, draft}
	approved := func(id string) bool { return id != "machine" }
	ranked, excluded := RankPublic(techniques,
		outcomes(map[string][2]float64{"thin": {1, 2}, "leaky": {5, 5}}),
		approved, PublicPolicy{})

	if len(ranked) != 0 {
		t.Errorf("ranked %v, want nothing eligible", ranked)
	}
	want := map[string]string{
		"org-one":        ReasonOrgScoped,
		"ext/peer/thing": ReasonImported,
		"machine":        ReasonUnapproved,
		"leaky":          ReasonSecrets,
		"thin":           ReasonThinEvidence,
		"unmeasured":     ReasonNoEvidence,
	}
	got := map[string]string{}
	for _, e := range excluded {
		got[e.TechniqueID] = e.Reason
	}
	for id, reason := range want {
		if got[id] != reason {
			t.Errorf("%s excluded as %q, want %q", id, got[id], reason)
		}
	}
	// A draft is not a near miss; listing every one would bury the actionable ones.
	if _, listed := got["a-draft"]; listed {
		t.Error("a draft was reported as an exclusion")
	}
	// The thin case says how far off it is.
	for _, e := range excluded {
		if e.TechniqueID == "thin" && e.N != 2 {
			t.Errorf("thin exclusion reports n=%d, want the sample size so far", e.N)
		}
	}
}

// The auto-promote path graduates a shadow technique to serving with no manual
// review, and auto-discover feeds it. Without the approval gate, that chain
// publishes machine-written techniques to the internet under this org's signing key
// with no human anywhere on it.
func TestUnapprovedMachineTechniquesNeverReachTheChannel(t *testing.T) {
	c := publicTechnique("auto-promoted")
	c.Provenance = "suggested"
	ranked, excluded := RankPublic([]models.Technique{c},
		outcomes(map[string][2]float64{"auto-promoted": {50, 50}}),
		func(string) bool { return false }, PublicPolicy{})
	if len(ranked) != 0 {
		t.Fatal("a technique no person approved is in the Public channel on evidence alone")
	}
	if len(excluded) != 1 || excluded[0].Reason != ReasonUnapproved {
		t.Errorf("excluded = %v, want the approval reason", excluded)
	}
}

// The floor is a knob, and raising it must actually bite.
func TestMinNFloorIsRespected(t *testing.T) {
	techniques := []models.Technique{publicTechnique("five")}
	o := outcomes(map[string][2]float64{"five": {4, 5}})
	if ranked, _ := RankPublic(techniques, o, allApproved, PublicPolicy{}); len(ranked) != 1 {
		t.Error("n=5 does not clear the default floor (AttestationMinN)")
	}
	if ranked, _ := RankPublic(techniques, o, allApproved, PublicPolicy{MinN: 20}); len(ranked) != 0 {
		t.Error("a raised floor did not exclude a technique below it")
	}
}

// ---- hysteresis ----

// rankedRun builds a ranking of n synthetic candidates, best first.
func rankedRun(ids ...string) []PublicCandidate {
	out := make([]PublicCandidate, 0, len(ids))
	for i, id := range ids {
		out = append(out, PublicCandidate{TechniqueID: id, Bound: 1 - float64(i)/1000, Rank: i + 1})
	}
	return out
}

func idsOf(ms []PublicMember) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.TechniqueID)
	}
	return out
}

// A technique that dips below the served window but stays inside the band keeps its
// membership and its arrival date, and produces no retraction. Without this, the
// commons emits a "flag your local copy for review" alarm every few hours to
// every subscriber, for techniques that are perfectly fine.
func TestMembershipSurvivesADipInsideTheBand(t *testing.T) {
	p := PublicPolicy{Size: 2, DelistRank: 4}

	first, _ := Reconcile(nil, rankedRun("a", "b"), "2026-01-01T00:00:00Z", p)
	if len(first) != 2 {
		t.Fatalf("first reconcile admitted %v", idsOf(first))
	}

	// "a" slides to rank 3: outside the served window, inside the band.
	held, delisted := Reconcile(first, rankedRun("b", "c", "a"), "2026-02-01T00:00:00Z", p)
	if len(delisted) != 0 {
		t.Errorf("de-listed %v on a dip inside the band", idsOf(delisted))
	}
	var a PublicMember
	for _, m := range held {
		if m.TechniqueID == "a" {
			a = m
		}
	}
	if a.TechniqueID == "" {
		t.Fatalf("a lost its membership on a dip; members are %v", idsOf(held))
	}
	if a.Served {
		t.Error("a is still served at rank 3 with Size 2")
	}
	if a.EnteredAt != "2026-01-01T00:00:00Z" {
		t.Errorf("a's EnteredAt is %q, want its original arrival", a.EnteredAt)
	}

	// It recovers: still the original date, no re-entry.
	back, _ := Reconcile(held, rankedRun("a", "b", "c"), "2026-03-01T00:00:00Z", p)
	for _, m := range back {
		if m.TechniqueID == "a" {
			if m.EnteredAt != "2026-01-01T00:00:00Z" {
				t.Errorf("after recovering, a's EnteredAt is %q — it was treated as new", m.EnteredAt)
			}
			if !m.Served {
				t.Error("a recovered to rank 1 and is not served")
			}
		}
	}
}

// Past the band, membership ends and the subscriber is told.
func TestFallingPastTheBandDeLists(t *testing.T) {
	p := PublicPolicy{Size: 2, DelistRank: 3}
	first, _ := Reconcile(nil, rankedRun("a", "b"), "2026-01-01T00:00:00Z", p)

	next, delisted := Reconcile(first, rankedRun("b", "c", "d", "a"), "2026-02-01T00:00:00Z", p)
	if len(delisted) != 1 || delisted[0].TechniqueID != "a" {
		t.Fatalf("de-listed %v, want just a", idsOf(delisted))
	}
	if delisted[0].EnteredAt != "2026-01-01T00:00:00Z" {
		t.Error("the de-listed record lost its history")
	}
	for _, m := range next {
		if m.TechniqueID == "a" {
			t.Error("a is still a member after falling past the band")
		}
	}
}

// Losing eligibility de-lists even from rank 1 — the technique is simply gone from
// the ranking, which is how a retired or re-scoped technique leaves.
func TestLosingEligibilityDeLists(t *testing.T) {
	p := PublicPolicy{Size: 2, DelistRank: 4}
	first, _ := Reconcile(nil, rankedRun("a", "b"), "2026-01-01T00:00:00Z", p)
	_, delisted := Reconcile(first, rankedRun("b"), "2026-02-01T00:00:00Z", p)
	if len(delisted) != 1 || delisted[0].TechniqueID != "a" {
		t.Errorf("de-listed %v, want a", idsOf(delisted))
	}
}

// The served set is one feed page and never more, however the band moves — the
// property that keeps the channel free of archive paging.
func TestServedNeverExceedsTheCap(t *testing.T) {
	p := PublicPolicy{Size: 3, DelistRank: 8}
	var members []PublicMember
	// Churn the ranking repeatedly with more candidates than the cap.
	for round := 0; round < 6; round++ {
		var ids []string
		for i := 0; i < 8; i++ {
			ids = append(ids, fmt.Sprintf("c%d", (i+round)%8))
		}
		members, _ = Reconcile(members, rankedRun(ids...), fmt.Sprintf("2026-0%d-01T00:00:00Z", round+1), p)
		if n := countServed(members); n > p.Size {
			t.Fatalf("round %d serves %d techniques, cap is %d", round, n, p.Size)
		}
		if len(members) > p.DelistRank {
			t.Fatalf("round %d holds %d members, band is %d", round, len(members), p.DelistRank)
		}
		if len(ServedIDs(members)) != countServed(members) {
			t.Fatal("ServedIDs disagrees with the Served flags")
		}
	}
}
