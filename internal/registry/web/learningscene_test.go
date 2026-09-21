// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"strings"
	"testing"
)

// Every number on this page is a gate — the evidence clears a floor or it does
// not — and which ones are open was a comparison of a have against a need across
// six table rows. Barred until the evidence arrives; open once it has.
func TestLearningSceneBarsTheCapabilitiesThatCannotRunYet(t *testing.T) {
	s := learningScene(400, []learningGate{
		{Name: "Findings and detectors", Needs: []learningNeed{{What: "days of accrued facts", Have: 3, Need: 14}}},
		{Name: "Experiments", Needs: []learningNeed{{What: "helped events (28d)", Have: 40, Need: 20}}},
		{Name: "Dynamic cohorts", Needs: []learningNeed{{What: "distinct cohorts", Have: 1, Need: 3}}},
	})
	if n := strings.Count(s, "dgm-bars"); n != 2 {
		t.Errorf("%d barred gates; two of these three are short of their evidence", n)
	}
	// What the evidence proves, and not a word past it: the floors are cleared.
	// "running" was a claim about a worker whose state this page cannot read.
	if !strings.Contains(s, "Requirements met") {
		t.Error("the capability whose requirements are met does not say so")
	}
	if strings.Contains(s, "running") {
		t.Error("the picture calls a cleared floor a running detector")
	}
	// The shortfall is what an operator can act on. "not met" is not.
	if !strings.Contains(s, "3 of 14 days of accrued facts") ||
		!strings.Contains(s, "1 of 3 distinct cohorts") {
		t.Error("a shut gate does not say how far short it is, or in what")
	}
}

// A COMPOUND TRIGGER IS COMPOUND. Both of the plan's later capabilities want two
// things at once, and the gate used to test whichever single pair the caller
// passed — so twenty helped events from one technique read as a running
// Experiments detector, and forty facts across three cohorts opened a gate that
// wants five hundred. The untested half could not fail.
func TestLearningGatesTestEveryRequirement(t *testing.T) {
	for _, c := range []struct {
		name string
		rd   readiness
		open map[string]bool
	}{{
		name: "helped events all from one technique",
		rd:   readiness{DaysOfFacts: 20, Helped28d: 20, HelpedTechniques: 1, FactsTotal: 900, Cohorts: 4},
		open: map[string]bool{"Findings and detectors": true, "Experiments": false, "Dynamic cohorts": true},
	}, {
		name: "cohorts without the corpus behind them",
		rd:   readiness{DaysOfFacts: 20, Helped28d: 40, HelpedTechniques: 5, FactsTotal: 40, Cohorts: 3},
		open: map[string]bool{"Findings and detectors": true, "Experiments": true, "Dynamic cohorts": false},
	}, {
		name: "every floor cleared",
		rd:   readiness{DaysOfFacts: 14, Helped28d: 20, HelpedTechniques: 3, FactsTotal: 500, Cohorts: 3},
		open: map[string]bool{"Findings and detectors": true, "Experiments": true, "Dynamic cohorts": true},
	}, {
		name: "a registry stood up this morning",
		rd:   readiness{},
		open: map[string]bool{"Findings and detectors": false, "Experiments": false, "Dynamic cohorts": false},
	}} {
		for _, g := range learningGates(c.rd) {
			want, named := c.open[g.Name]
			if !named {
				t.Fatalf("%s: no expectation for the %s gate", c.name, g.Name)
			}
			if g.Open() != want {
				t.Errorf("%s: %s gate open=%v, want %v — its requirements are %v",
					c.name, g.Name, g.Open(), want, g.Needs)
			}
		}
	}
}

// And a shut gate names EVERY requirement it is short of. An operator shown only
// the nearest one works on a number that opens nothing.
func TestLearningGateNamesEveryOutstandingRequirement(t *testing.T) {
	gates := learningGates(readiness{DaysOfFacts: 20, Helped28d: 4, HelpedTechniques: 1,
		FactsTotal: 40, Cohorts: 1})
	lines := map[string][]string{}
	for _, g := range gates {
		lines[g.Name] = gateStateLines(g)
	}
	if got := lines["Experiments"]; len(got) != 2 {
		t.Errorf("Experiments is short of two requirements and reports %v", got)
	}
	if got := lines["Dynamic cohorts"]; len(got) != 2 {
		t.Errorf("Dynamic cohorts is short of two requirements and reports %v", got)
	}
	if got := lines["Findings and detectors"]; len(got) != 1 || got[0] != "Requirements met" {
		t.Errorf("Findings and detectors has its days of facts and reports %v", got)
	}
}

// The picture and the table are drawn from ONE set of gates, so the page cannot
// show a capability open above a row that says it is two numbers short.
func TestLearningPictureAndTableComeFromTheSameGates(t *testing.T) {
	page := signedInPage(t, "/learning")
	for _, g := range learningGates(readiness{}) {
		for _, n := range g.Needs {
			if !strings.Contains(page, ">"+n.What+"<") {
				t.Errorf("the requirements table has no row for %q, which the gates carry", n.What)
			}
		}
	}
	if strings.Contains(page, "<i>running</i>") {
		t.Error("the page still calls a cleared floor a running detector")
	}
}

// A registry with no facts is not feeding anything, and drawing traffic on those
// lines would be drawing traffic that does not exist. It is also the honest
// answer on a registry that has just been stood up.
func TestLearningSceneDrawsNoTrafficWhereThereIsNoEvidence(t *testing.T) {
	empty := learningScene(0, []learningGate{{Name: "A", Needs: []learningNeed{{What: "facts", Have: 0, Need: 5}}}})
	if !strings.Contains(empty, "dgm-beam-pending") {
		t.Error("no evidence has arrived, but the feed is drawn carrying some")
	}
	if !strings.Contains(empty, "No evidence yet") {
		t.Error("an empty corpus reports a count instead of saying so")
	}
	if strings.Contains(empty, "0 audit fact") {
		t.Error("the picture reports a zero where it has something truer to say")
	}
	// And no end dot: the gate is drawn at the end of every feed.
	if !strings.Contains(empty, "dgm-beam-bare dgm-beam-pending") {
		t.Error("a feed draws its own end marker inside the gate it lands on")
	}
	live := learningScene(400, []learningGate{{Name: "A", Needs: []learningNeed{{What: "facts", Have: 9, Need: 5}}}})
	if strings.Contains(live, "dgm-beam-pending") {
		t.Error("evidence is arriving and the feed is drawn inert")
	}
}

// The picture and the corpus it is drawn from sit side by side, and the tiles go
// two by two rather than four across under it — which put the gates and the
// figures that open them a scroll apart.
func TestLearningPictureSitsBesideItsFigures(t *testing.T) {
	if !strings.Contains(appCSS, ".lrn-tiles .tiles{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))") {
		t.Error("the tiles are not two by two beside the picture")
	}
	if !strings.Contains(appCSS, "@media (max-width:900px){.lrn-band{grid-template-columns:minmax(0,1fr)}}") {
		t.Error("the band does not stack on a narrow window")
	}
	page := signedInPage(t, "/learning")
	band := strings.Index(page, `class="lrn-band"`)
	if band < 0 {
		t.Fatal("the learning page is not banded")
	}
	scene, tiles := strings.Index(page, "lrn-stage"), strings.Index(page, `class="lrn-tiles"`)
	if scene < 0 || tiles < 0 || scene > tiles {
		t.Error("the picture must be the first half of the band")
	}
}

// A bare beam ends at the gate drawn on it, so a scene with fewer capabilities
// than the frame has room for must draw fewer beams: the extras end in mid-air
// and point at nothing.
func TestLearningSceneDrawsOneFeedPerGate(t *testing.T) {
	gate := func(name string) learningGate {
		return learningGate{Name: name, Needs: []learningNeed{{What: "facts", Have: 1, Need: 5}}}
	}
	for _, n := range []int{0, 1, 2, 3} {
		var gates []learningGate
		for i := 0; i < n; i++ {
			gates = append(gates, gate(fmt.Sprintf("G%d", i)))
		}
		scene := learningScene(400, gates)
		if got := strings.Count(scene, "lrn-feed"); got != n {
			t.Errorf("%d gates: %d feeds drawn, want %d", n, got, n)
		}
	}
}
