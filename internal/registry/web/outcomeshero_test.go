// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/insights"
)

// The empty hero uses the SAME two columns the populated one does: the figure's
// column carries the orientation until there is a figure to put there.
//
// It used to bail out of the grid entirely and return one sentence, so a new
// registry's first screen had no picture — and the orientation ended up stacked
// in a panel of its own above the funnel, three sentences of vertical space in
// front of the one drawing that would have explained them.
func TestTheEmptyHeroPutsItsOrientationInTheFigureColumn(t *testing.T) {
	html := outcomesHero(insights.Overview{}, insights.Window{Key: "30d"})

	for _, want := range []string{"hero-grid", "hero-flow", "hero-fig", "flow-empty"} {
		if !strings.Contains(html, want) {
			t.Errorf("the empty hero is missing %s", want)
		}
	}
	if !strings.Contains(html, "No techniques have been shown yet") {
		t.Error("the empty hero does not orient the reader")
	}
	// The same title the populated hero carries: this is one panel in two
	// states, not two panels.
	if !strings.Contains(html, "How ") {
		t.Error("the empty hero lost its title")
	}
	// And no headline figure, because there is nothing to headline.
	if strings.Contains(html, "hero-value") {
		t.Error("the empty hero prints a headline figure it has not measured")
	}
}

func TestAPopulatedHeroKeepsItsTwoColumns(t *testing.T) {
	o := insights.Overview{Funnel: insights.Funnel{Shown: 120, Adopted: 40, Helped: 25}}
	html := outcomesHero(o, insights.Window{Key: "30d"})

	for _, want := range []string{"hero-grid", "hero-flow", "hero-fig"} {
		if !strings.Contains(html, want) {
			t.Errorf("a populated hero is missing %s", want)
		}
	}
}
