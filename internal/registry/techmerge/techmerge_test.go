// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package techmerge

import (
	"math"
	"testing"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
)

// geometry places each recipe exactly where a test says, so the grouping is
// tested on stated angles. Which angles a real model gives real techniques is a
// question about a corpus, measured against one.
type geometry map[string]float64

func (g geometry) Embed(texts []string) []embed.Vector {
	out := make([]embed.Vector, len(texts))
	for i, t := range texts {
		cos, ok := g[t]
		if !ok {
			panic("the test did not place: " + t)
		}
		out[i] = embed.Vector{float32(cos), float32(math.Sqrt(1 - cos*cos))}
	}
	return out
}
func (g geometry) ModelID() string { return "geometry" }
func (g geometry) Dim() int        { return 2 }

func tech(id, status, recipe, created string) models.Technique {
	return models.Technique{ID: id, Name: id, Status: status, Recipe: recipe, CreatedAt: created}
}

func rate(r float64) *float64 { return &r }

// The pass keeps the technique the evidence favours and offers the rest for
// retirement — which is the whole point of doing this on outcomes rather than
// on whichever copy happened to arrive first.
func TestTheBestMeasuredTechniqueIsTheOneKept(t *testing.T) {
	group := []models.Technique{
		tech("arrived-first", "stable", "run the suite", "2026-01-01"),
		tech("helped-most", "stable", "run the suite", "2026-06-01"),
		tech("also-ran", "stable", "run the suite", "2026-07-01"),
	}
	outcomes := map[string]models.Outcome{
		"arrived-first": {Shown: 400, Adopted: 40, Helped: 12, HelpedRate: rate(0.30)},
		"helped-most":   {Shown: 100, Adopted: 20, Helped: 17, HelpedRate: rate(0.85)},
		"also-ran":      {Shown: 90, Adopted: 15, Helped: 3, HelpedRate: rate(0.20)},
	}
	groups := Propose(group, outcomes, geometry{"run the suite": 1.0}, PlaybookLane)
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	if got := groups[0].Keep.Technique.ID; got != "helped-most" {
		t.Errorf("kept %q; the evidence favours helped-most", got)
	}
	if len(groups[0].Retire) != 2 {
		t.Errorf("offered %d for retirement, expected 2", len(groups[0].Retire))
	}
	// The reason has to be auditable: it is asking to retire what members use.
	for _, want := range []string{"85%", "20 adoptions", "say the same thing"} {
		if !contains(groups[0].Why, want) {
			t.Errorf("the reason does not say %q: %s", want, groups[0].Why)
		}
	}
}

// A lucky one-of-one is not evidence of being the best of a group, so it does
// not beat a technique with a real sample behind it.
func TestALuckySampleDoesNotWinTheGroup(t *testing.T) {
	group := []models.Technique{
		tech("well-measured", "stable", "run the suite", "2026-01-01"),
		tech("lucky", "stable", "run the suite", "2026-06-01"),
	}
	outcomes := map[string]models.Outcome{
		"well-measured": {Shown: 300, Adopted: 40, Helped: 24, HelpedRate: rate(0.60)},
		"lucky":         {Shown: 3, Adopted: 1, Helped: 1, HelpedRate: rate(1.0)},
	}
	groups := Propose(group, outcomes, geometry{"run the suite": 1.0}, PlaybookLane)
	if got := groups[0].Keep.Technique.ID; got != "well-measured" {
		t.Errorf("kept %q on a sample of one", got)
	}
}

// A shadow technique has never been shown to anybody, so it has no adoption
// evidence to beat a serving one with. Retiring what members use in favour of
// what they have never seen would be deciding on an absence.
func TestAServingTechniqueOutranksOneNobodyHasSeen(t *testing.T) {
	group := []models.Technique{
		tech("serving", "stable", "run the suite", "2026-01-01"),
		tech("in-shadow", "shadow", "run the suite", "2026-02-01"),
	}
	outcomes := map[string]models.Outcome{"serving": {Shown: 50, Adopted: 2, Helped: 0, HelpedRate: rate(0.0)}}
	groups := Propose(group, outcomes, geometry{"run the suite": 1.0}, QueueLane)
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	if got := groups[0].Keep.Technique.ID; got != "serving" {
		t.Errorf("kept %q over the technique members actually use", got)
	}
	if len(groups[0].Retire) != 1 || groups[0].Retire[0].Technique.ID != "in-shadow" {
		t.Errorf("the queue lane offered %+v; only the candidate is its to drop", groups[0].Retire)
	}
}

// The two lanes see different sets. The playbook's is the serving library, so a
// candidate under evaluation is not in it — dropping one is a decision taken on
// fit verdicts, which the playbook lane does not read.
func TestThePlaybookLaneDoesNotSeeCandidates(t *testing.T) {
	group := []models.Technique{
		tech("serving", "stable", "run the suite", "2026-01-01"),
		tech("candidate", "shadow", "run the suite", "2026-02-01"),
	}
	if groups := Propose(group, map[string]models.Outcome{}, geometry{"run the suite": 1.0}, PlaybookLane); len(groups) != 0 {
		t.Errorf("the playbook lane proposed something about a candidate: %+v", groups)
	}
}

// The queue lane reads the serving library too, because "this candidate
// restates something you already publish" is the most useful thing it can say.
// But a serving technique there is context, never a casualty: retiring it is
// the playbook's decision, on evidence this lane does not read.
func TestTheQueueLaneNeverOffersAServingTechnique(t *testing.T) {
	group := []models.Technique{
		tech("published-a", "stable", "run the suite", "2026-01-01"),
		tech("published-b", "stable", "run the suite", "2026-01-02"),
		tech("candidate", "shadow", "run the suite", "2026-02-01"),
	}
	outcomes := map[string]models.Outcome{
		"published-a": {Shown: 90, Adopted: 30, Helped: 24, HelpedRate: rate(0.8)},
		"published-b": {Shown: 40, Adopted: 12, Helped: 3, HelpedRate: rate(0.25)},
	}
	groups := Propose(group, outcomes, geometry{"run the suite": 1.0}, QueueLane)
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	g := groups[0]
	for _, m := range g.Retire {
		if m.Technique.Status == "stable" {
			t.Errorf("the queue lane offered %q, a serving technique, for retirement", m.Technique.ID)
		}
	}
	// The other serving technique is shown beside it, so the reviewer can see
	// what the candidate duplicates.
	if len(g.Alongside) != 1 || g.Alongside[0].Technique.ID != "published-b" {
		t.Errorf("the serving technique was not shown as context: %+v", g.Alongside)
	}
	if len(g.Retire) != 1 || g.Retire[0].Technique.ID != "candidate" {
		t.Errorf("the queue lane's casualties are %+v; only the candidate is one", g.Retire)
	}
}

// An all-serving group found while looking at the queue has nothing the queue
// may act on, so it is not proposed there — the playbook lane proposes it,
// where the decision belongs.
func TestTheQueueLaneSkipsAGroupItCannotActOn(t *testing.T) {
	group := []models.Technique{
		tech("published-a", "stable", "run the suite", "2026-01-01"),
		tech("published-b", "stable", "run the suite", "2026-01-02"),
	}
	if groups := Propose(group, map[string]models.Outcome{}, geometry{"run the suite": 1.0}, QueueLane); len(groups) != 0 {
		t.Errorf("the queue lane proposed a decision only the playbook can take: %+v", groups)
	}
	// And the playbook lane does propose it.
	if groups := Propose(group, map[string]models.Outcome{}, geometry{"run the suite": 1.0}, PlaybookLane); len(groups) != 1 {
		t.Errorf("the playbook lane did not propose its own duplicates: %d groups", len(groups))
	}
}

// Chains are what make a proposal untrustworthy: A close to B, B close to C, A
// and C nothing alike. Single-link clustering would put all three on a
// reviewer's screen as one move.
func TestAChainIsNotOneGroup(t *testing.T) {
	group := []models.Technique{
		tech("a", "stable", "recipe a", "2026-01-01"),
		tech("b", "stable", "recipe b", "2026-01-02"),
		tech("c", "stable", "recipe c", "2026-01-03"),
	}
	// b sits between a and c; a and c are far apart.
	g := geometry{"recipe a": 1.0, "recipe b": 0.95, "recipe c": 0.80}
	groups := Propose(group, map[string]models.Outcome{}, g, PlaybookLane)
	for _, grp := range groups {
		ids := []string{grp.Keep.Technique.ID}
		for _, m := range grp.Retire {
			ids = append(ids, m.Technique.ID)
		}
		if len(ids) == 3 {
			t.Errorf("a chain was swept into one group: %v", ids)
		}
	}
}

// Nothing alike proposes nothing. A pass that always finds something to merge is
// one a reviewer learns to ignore.
func TestADistinctPlaybookProposesNothing(t *testing.T) {
	group := []models.Technique{
		tech("a", "stable", "recipe a", "2026-01-01"),
		tech("b", "stable", "recipe b", "2026-01-02"),
	}
	g := geometry{"recipe a": 1.0, "recipe b": 0.30}
	if groups := Propose(group, map[string]models.Outcome{}, g, PlaybookLane); len(groups) != 0 {
		t.Errorf("proposed %d groups over techniques with nothing in common", len(groups))
	}
}

// Drafts are the drafts lane's business, and the novelty gate now stops the
// duplicates that used to arrive as drafts. Consolidation is about what serves.
func TestDraftsAreLeftToTheDraftsLane(t *testing.T) {
	group := []models.Technique{
		tech("serving", "stable", "run the suite", "2026-01-01"),
		tech("waiting", "draft", "run the suite", "2026-02-01"),
	}
	if groups := Propose(group, map[string]models.Outcome{}, geometry{"run the suite": 1.0}, QueueLane); len(groups) != 0 {
		t.Errorf("a draft was pulled into a consolidation proposal: %+v", groups)
	}
}

// Same corpus, same proposal. A pass whose output moves between runs cannot be
// reviewed — a reviewer would be deciding about a different set each time.
func TestTheProposalIsStable(t *testing.T) {
	group := []models.Technique{
		tech("c", "stable", "run the suite", "2026-01-03"),
		tech("a", "stable", "run the suite", "2026-01-01"),
		tech("b", "stable", "run the suite", "2026-01-02"),
	}
	g := geometry{"run the suite": 1.0}
	first := Propose(group, map[string]models.Outcome{}, g, PlaybookLane)
	second := Propose(group, map[string]models.Outcome{}, g, PlaybookLane)
	if len(first) != len(second) || first[0].Keep.Technique.ID != second[0].Keep.Technique.ID {
		t.Fatal("two runs over one corpus proposed different things")
	}
	// With no evidence at all, the earliest is kept: it is the id most likely to
	// be written down elsewhere already.
	if first[0].Keep.Technique.ID != "a" {
		t.Errorf("kept %q; with nothing to measure the earliest should win", first[0].Keep.Technique.ID)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// The stored helped rate is weighted-helped over weighted-adopted, so it can
// exceed 1 and it is not a ratio of the raw counts. Reporting it beside Adopted
// pairs a number with an n it was never taken from — and on the real corpus that
// printed "200% helped of 1 adopted".
func TestTheRateIsReportedAgainstTheSampleItWasTakenFrom(t *testing.T) {
	m := Member{Outcome: models.Outcome{
		Shown: 90, Adopted: 1, Helped: 2,
		WeightedAdopted: 14, WeightedHelped: 7, HelpedRate: rate(0.5),
	}}
	if got := m.Sample(); got != 14 {
		t.Errorf("Sample() = %v, want the weighted adoptions the rate came from", got)
	}
	if r, ok := m.Measured(); !ok || r != 0.5 {
		t.Errorf("Measured() = %v %v; 14 weighted adoptions clears the floor", r, ok)
	}

	// Under the floor there is no rate to report, whatever the rollup stored.
	thin := Member{Outcome: models.Outcome{Adopted: 1, WeightedAdopted: 1, HelpedRate: rate(2.0)}}
	if _, ok := thin.Measured(); ok {
		t.Error("a rate off a single adoption was offered as measured")
	}

	// A rollup written before weights existed still ranks on its raw counts.
	old := Member{Outcome: models.Outcome{Adopted: 30, Helped: 15, HelpedRate: rate(0.5)}}
	if got := old.Sample(); got != 30 {
		t.Errorf("Sample() = %v for an unweighted rollup, want the raw adoptions", got)
	}
	if _, ok := old.Measured(); !ok {
		t.Error("an unweighted rollup with 30 adoptions was treated as unmeasured")
	}
}

// The count in the sentence is the whole group, so it decides the noun.
func TestTheReasonCountsTheWholeGroup(t *testing.T) {
	group := []models.Technique{
		tech("a", "stable", "run the suite", "2026-01-01"),
		tech("b", "stable", "run the suite", "2026-01-02"),
	}
	groups := Propose(group, map[string]models.Outcome{}, geometry{"run the suite": 1.0}, PlaybookLane)
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	if contains(groups[0].Why, "2 technique say") {
		t.Errorf("the reason miscounts its own noun: %s", groups[0].Why)
	}
	if !contains(groups[0].Why, "2 techniques say") {
		t.Errorf("the reason does not count the whole group: %s", groups[0].Why)
	}
}
