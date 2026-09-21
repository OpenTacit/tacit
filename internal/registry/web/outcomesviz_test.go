// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/insights"
)

func TestFunnelFlowRendersBothShapes(t *testing.T) {
	f := insights.Funnel{Shown: 100, Adopted: 60, Helped: 30}
	prev := insights.Funnel{Shown: 40, Adopted: 20, Helped: 10}
	out := string(FunnelFlow(f, prev, true))

	// The desktop SVG and the phone rendering — the MCP app's vertical
	// funnel, sans footnote — both ship; CSS picks.
	for _, want := range []string{
		`class="flow-svg"`, `class="flow-phone"`, `class="funnel-svg"`,
		"60% adopted", "40 not adopted", "50% helped", "30 not helped",
		`class="flow-delta up"`, // +60 shown vs prev
		"SHOWN", "ADOPTED", "HELPED",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FunnelFlow missing %q", want)
		}
	}
	if strings.Contains(out, "funnel-note") {
		t.Fatal("phone funnel repeats the throughput note the hero already states")
	}

	// Without a previous window there is nothing honest to compare against.
	if noPrev := string(FunnelFlow(f, insights.Funnel{}, false)); strings.Contains(noPrev, "flow-delta") {
		t.Fatal("FunnelFlow renders deltas with no previous window")
	}
	// Nothing shown: the SAME flow, drawn empty. It used to return one sentence,
	// which is why a new registry's first screen had no picture on it and grew a
	// bespoke one instead — a second drawing of one pipeline, for the readers
	// least able to afford a second visual grammar.
	empty := string(FunnelFlow(insights.Funnel{}, insights.Funnel{}, false))
	for _, want := range []string{"flow-empty", "SHOWN", "ADOPTED", "HELPED", "flow-meaning"} {
		if !strings.Contains(empty, want) {
			t.Errorf("the empty flow is missing %q", want)
		}
	}
	// Equal bars, in outline. A funnel's heights are proportional to what is in
	// it; with nothing in it, a narrowing silhouette would draw a drop-off
	// nobody has measured.
	// The same elements with the same classes: the empty state is a register,
	// not a second drawing. A parallel set of *-empty classes would mean the
	// geometry had been copied again.
	if !strings.Contains(empty, `class="flow-bar `) || !strings.Contains(empty, `class="flow-band `) {
		t.Error("the empty flow does not use the funnel's own bars and bands")
	}
	if strings.Contains(empty, "-empty\" x=") || strings.Contains(empty, "flow-bar-empty") {
		t.Error("the empty flow has its own element classes; it is a copy, not a state")
	}
	// EVERY PART THAT WILL CARRY WORDS LATER CARRIES WORDS NOW. The two bands
	// between the three stages hold a rate and the loss it implies once there
	// is something to divide; with nothing measured they held nothing, and two
	// bare bands between three labelled stages read as a drawing that failed to
	// finish rather than as a funnel at rest. They name the stage that has not
	// happened instead — a rate here would be 0/0.
	for _, want := range []string{"none shown yet", "0 adopted", "none helped yet", "0 helped"} {
		if !strings.Contains(empty, want) {
			t.Errorf("the empty flow leaves a band bare; missing %q", want)
		}
	}
	if n := strings.Count(empty, `class="flow-conv"`); n != 2 {
		t.Errorf("%d conversion labels on the empty flow, want one per band", n)
	}
	if strings.Contains(empty, "NaN") {
		t.Error("the empty flow divided by a denominator it does not have")
	}
	// Each stage carries its count as 0, not as a dash. Nothing shown means
	// nothing adopted and nothing helped, and all three are measured; a dash
	// would claim the number is unavailable.
	if n := strings.Count(empty, `class="flow-count flow-count-none"`); n != 3 {
		t.Errorf("%d quiet counts on the empty flow, want one per stage", n)
	}
	if strings.Contains(empty, "&#8212;") {
		t.Error("the empty flow shows a dash where the count is a known 0")
	}
	if n := strings.Count(empty, `text-anchor="start">0</text>`) +
		strings.Count(empty, `text-anchor="middle">0</text>`) +
		strings.Count(empty, `text-anchor="end">0</text>`); n != 3 {
		t.Errorf("%d zero counts on the empty flow, want three", n)
	}
	// And a populated flow is untouched by any of that.
	full := string(FunnelFlow(insights.Funnel{Shown: 100, Adopted: 40, Helped: 10}, insights.Funnel{}, false))
	for _, want := range []string{"40% adopted", "60 not adopted", "25% helped", "30 not helped"} {
		if !strings.Contains(full, want) {
			t.Errorf("the measured flow lost %q", want)
		}
	}
	if strings.Contains(full, "none shown yet") {
		t.Error("a measured flow is using the empty state's words")
	}
	// Equal bars: with nothing measured there is no proportion to draw.
	heights := regexp.MustCompile(`class="flow-bar [^"]*" x="[^"]*" y="[^"]*" width="[^"]*" height="([0-9.]+)"`).FindAllStringSubmatch(empty, -1)
	if len(heights) != 3 {
		t.Fatalf("expected three bars, found %d", len(heights))
	}
	for _, hgt := range heights[1:] {
		if hgt[1] != heights[0][1] {
			t.Errorf("the empty flow narrows: %s vs %s — a drop-off nobody measured", hgt[1], heights[0][1])
		}
	}
	// And no delta: "±0 from the previous window" is a comparison between two
	// nothings, which is the fake zero the house rule forbids — and it printed
	// three times, in green, above an empty funnel.
	if strings.Contains(empty, "flow-delta") {
		t.Error("the empty flow compares itself to a previous window")
	}
	// NO RATE anywhere: a stage that never happened has no rate to convert FROM.
	// The bands still carry words — see above — they just carry no arithmetic.
	// (Not a bare "%" check: the shared bloom filter defs are full of
	// percentages, and so are SVG gradient offsets.)
	for _, m := range regexp.MustCompile(`class="flow-(?:conv|drop)"[^>]*>([^<]*)<`).FindAllStringSubmatch(empty, -1) {
		if strings.Contains(m[1], "%") {
			t.Errorf("the empty flow prints the rate %q; it has measured nothing to convert", m[1])
		}
	}
	// The phone rendering says nothing at all in these places: its bands are
	// too short to hold a line, which is why it has none to begin with.
	for _, rate := range []string{"fn-conv", "fn-drop"} {
		if strings.Contains(empty, rate) {
			t.Errorf("the empty flow prints %s; it has measured nothing to convert", rate)
		}
	}
	// The counts read 0 in the quiet register — same face and size as a
	// measured count, only the ink drops back.
	if !strings.Contains(empty, "flow-count-none") {
		t.Error("the empty flow does not mark its counts as unmeasured")
	}
}

func TestGapRowsDumbbell(t *testing.T) {
	rows := []GapRow{{
		Label: "growth", LabelHref: "/outcomes/cohorts/team:growth?w=30d",
		Area: "migration", AreaHref: "/outcomes/task-type/migration?w=30d",
		Here: 0.33, Peers: 0.68,
		Tip: "growth · migration: 1 of 3 suggestions adopted here",
	}}
	out := string(GapRows(rows, true))
	for _, want := range []string{
		`class="gap-legend"`, `class="gap-dot here"`, `class="gap-dot peer"`,
		`<b>33%</b> here`, `peers <b>68%</b>`,
		`data-tip="growth · migration: 1 of 3 suggestions adopted here"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("GapRows missing %q", want)
		}
	}
	// The span shades between the two rates regardless of which is larger.
	if !strings.Contains(out, `style="left:33.0%;width:35.0%"`) {
		t.Fatalf("gap span not spanning the rates: %s", out)
	}
	if legend := string(GapRows(rows, false)); strings.Contains(legend, "gap-legend") {
		t.Fatal("continuation list repeats the legend")
	}
}

func TestMeterClampsAndLabels(t *testing.T) {
	out := string(Meter("Org-scoped share", 0.89, "of 1810 shown"))
	for _, want := range []string{"Org-scoped share", "<b>89%</b>", `width:89.0%`, "of 1810 shown"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Meter missing %q", want)
		}
	}
	if over := string(Meter("x", 1.7, "")); !strings.Contains(over, `width:100.0%`) {
		t.Fatal("Meter does not clamp above 1")
	}
}

func TestMixOrLineCollapsesSingleSegment(t *testing.T) {
	one := mixOrLine([]insights.MixEntry{{Label: "contributed", Count: 1222}},
		mixKeys, "none", "adoptions", func(string) string { return "/outcomes/source/contributed" })
	if !strings.Contains(one, `class="mix-one"`) || !strings.Contains(one, "1,222") ||
		!strings.Contains(one, `href="/outcomes/source/contributed"`) {
		t.Fatalf("single-segment mix did not collapse to a sentence: %s", one)
	}
	two := mixOrLine([]insights.MixEntry{{Label: "a", Count: 2}, {Label: "b", Count: 1}},
		mixKeys, "none", "adoptions", func(string) string { return "" })
	if !strings.Contains(two, `class="mix"`) {
		t.Fatal("multi-segment mix lost its distribution bar")
	}
}
