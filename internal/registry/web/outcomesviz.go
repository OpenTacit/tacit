// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Visualization components for Outcomes: the horizontal funnel flow that heads
// the page, the meter used for org-share ratios, and the dumbbell used for
// cohort adoption gaps. Same rules as viz.go — server-rendered SVG/HTML over
// the CSS tokens, no build step, both color modes for free.
package web

import (
	"fmt"
	"html"
	"html/template"
	"strings"

	"github.com/opentacit/tacit/internal/registry/insights"
)

// emptyMeaningRoom is the extra depth the viewBox takes when the flow is drawn
// empty, for the line under each stage saying what the stage means. Without it
// that line falls outside the box and is clipped away from the readers it is
// written for.
const emptyMeaningRoom = 6.0

// emptyDeltaRoom is the depth the top band gives back when the flow is empty.
// Nothing is compared against a previous window there, so the line that would
// have carried the comparison is not drawn and its room is not held.
const emptyDeltaRoom = 18.0

// stageMeaning is that line, in the order the stages are drawn.
var stageMeaning = []string{"offered mid-task", "the member used it", "the work went better"}

// FunnelFlow draws shown → adopted → helped as a left-to-right flow: three
// vertical stage bars whose heights are proportional to their counts, joined
// by smooth tapering bands, with each step's conversion rate carried in the
// band that performs it. It replaces the old top-to-bottom FunnelChart on the
// Outcomes page (the vertical funnel remains for the MCP insights app): a
// horizontal flow reads in the order the story happens and leaves room above
// each stage for its count and its movement vs the previous window.
func FunnelFlow(f, prev insights.Funnel, hasPrev bool) template.HTML {
	// Nothing shown yet is a STATE OF THIS DRAWING, not a different drawing.
	//
	// It returned one sentence, so a new registry's first screen had no picture
	// on it — and the fix, twice, was a second picture: first a bespoke scene,
	// then a second function in this file that copied these constants and
	// redrew this geometry beside it. Both were the same mistake. There is one
	// funnel. It has an empty state, and the four places that differ say so
	// below: the bar heights, the counts, the tints, and one extra line naming
	// what each stage means, which is worth its room only when there are no
	// numbers to read instead.
	empty := f.Shown == 0
	type stage struct {
		name, tint string
		val, prev  int
	}
	stages := []stage{
		{"Shown", "o1", f.Shown, prev.Shown},
		{"Adopted", "o2", f.Adopted, prev.Adopted},
		{"Helped", "o3", f.Helped, prev.Helped},
	}
	convVerb := []string{"adopted", "helped"}
	dropVerb := []string{"not adopted", "not helped"}
	// What the same two bands say before anything has been measured. Not a rate
	// and not a loss: there is no denominator yet, so each band names the stage
	// that has not happened and the count that follows from it.
	emptyConv := []string{"none shown yet", "none helped yet"}
	emptyDrop := []string{"0 adopted", "0 helped"}

	const (
		w      = 760.0
		barW   = 58.0
		maxH   = 132.0 // tallest bar (= shown)
		botPad = 12.0
	)
	topBand := 74.0 // name / count / delta above the flow
	if empty {
		// No delta line to leave room for, and one meaning line to add: the
		// band above the bars carries a name and a dash, and nothing else.
		topBand -= emptyDeltaRoom
	}
	h := topBand + maxH + botPad
	if empty {
		h += emptyMeaningRoom // the meaning line sits below the bars
	}
	height := func(v int) float64 {
		// A funnel's bars are proportional to what is in them. With nothing in
		// them there is no proportion, and a narrowing silhouette would draw a
		// drop-off nobody has measured — so they stand equal.
		if empty {
			return maxH * 0.62
		}
		if v <= 0 {
			return 0
		}
		if hh := maxH * float64(v) / float64(f.Shown); hh > 8 {
			return hh
		}
		return 8
	}
	// Bars are vertically centred on the flow's midline, so the bands taper
	// symmetrically — the silhouette narrows like the funnel it is.
	midY := topBand + maxH/2
	barX := []float64{10, (w - barW) / 2, w - 10 - barW}

	var b strings.Builder
	cls, aria := "flow-svg", fmt.Sprintf("Funnel: %s suggestions shown; %s adopted; %s measurably helped.",
		fmtCount(f.Shown), fmtCount(f.Adopted), fmtCount(f.Helped))
	if empty {
		cls, aria = "flow-svg flow-empty", "Funnel: shown, adopted and helped. Nothing has been measured yet."
	}
	fmt.Fprintf(&b, `<svg class="%s" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`,
		cls, w, h, html.EscapeString(aria))
	b.WriteString(vizBloomDefs)

	// Tapering bands first, so the bars sit over their ends. A cubic ease
	// between the two bar edges keeps the flow soft without pretending
	// precision the shape doesn't have.
	for i := 0; i < len(stages)-1; i++ {
		h1, h2 := height(stages[i].val), height(stages[i+1].val)
		x1, x2 := barX[i]+barW, barX[i+1]
		mx := (x1 + x2) / 2
		t1, b1 := midY-h1/2, midY+h1/2
		t2, b2 := midY-h2/2, midY+h2/2
		fmt.Fprintf(&b, `<path class="flow-band %s" d="M%.1f,%.1f C%.1f,%.1f %.1f,%.1f %.1f,%.1f L%.1f,%.1f C%.1f,%.1f %.1f,%.1f %.1f,%.1f Z"/>`,
			stages[i+1].tint,
			x1, t1, mx, t1, mx, t2, x2, t2,
			x2, b2, mx, b2, mx, b1, x1, b1)
	}
	// The stage bars.
	for i, s := range stages {
		hh := height(s.val)
		fmt.Fprintf(&b, `<rect class="flow-bar %s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="6"/>`,
			s.tint, barX[i], midY-hh/2, barW, hh)
	}
	// Name, count, and movement above each stage — anchored so the outer
	// stages hug their page edge and the middle one centres.
	anchors := []string{"start", "middle", "end"}
	textX := []float64{barX[0], w / 2, barX[2] + barW}
	for i, s := range stages {
		fmt.Fprintf(&b, `<text class="flow-name" x="%.1f" y="18" text-anchor="%s">%s</text>`,
			textX[i], anchors[i], strings.ToUpper(s.name))
		count, countCls := fmtCount(s.val), "flow-count"
		if empty {
			// Zero is the measurement, not the absence of one: nothing has
			// been shown, so nothing was adopted or helped, and all three
			// counts are known. A dash says the number is unavailable, which
			// is a different and untrue claim — and the phone rendering of
			// this same funnel prints the zeros. Only the register is quiet.
			count, countCls = "0", "flow-count flow-count-none"
		}
		fmt.Fprintf(&b, `<text class="%s" x="%.1f" y="44" text-anchor="%s">%s</text>`,
			countCls, textX[i], anchors[i], count)
		if empty {
			fmt.Fprintf(&b, `<text class="flow-meaning" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`,
				textX[i], midY+height(0)/2+22, anchors[i], html.EscapeString(stageMeaning[i]))
		}
		if hasPrev && !empty {
			d, good := delta(s.val, s.prev)
			cls := "down"
			if good {
				cls = "up"
			}
			fmt.Fprintf(&b, `<text class="flow-delta %s" x="%.1f" y="62" text-anchor="%s">%s</text>`,
				cls, textX[i], anchors[i], html.EscapeString(d))
		}
	}
	// Each band carries its own conversion — the percentage IS the band's
	// narrowing — with the loss it implies named beneath. A stage that never
	// happened (0 adopted) has no rate to convert FROM; its band stays silent
	// rather than printing 0/0.
	//
	// A registry that has measured NOTHING is the exception, and it says so in
	// those same two places. This is the first picture a new operator meets and
	// the one they keep, so every part of it that will carry words later should
	// carry words now — two bare bands between three labelled stages read as a
	// drawing that failed to finish rather than as a funnel at rest.
	for i := 0; i < len(stages)-1; i++ {
		var conv, drop string
		switch {
		case empty:
			conv, drop = emptyConv[i], emptyDrop[i]
		case stages[i].val == 0:
			continue
		default:
			conv = fmt.Sprintf("%.0f%% %s", float64(stages[i+1].val)/float64(stages[i].val)*100, convVerb[i])
			drop = fmt.Sprintf("%s %s", fmtCount(stages[i].val-stages[i+1].val), dropVerb[i])
		}
		mx := (barX[i] + barW + barX[i+1]) / 2
		fmt.Fprintf(&b, `<text class="flow-conv" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`,
			mx, midY-4, html.EscapeString(conv))
		fmt.Fprintf(&b, `<text class="flow-drop" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`,
			mx, midY+14, html.EscapeString(drop))
	}
	b.WriteString(`</svg>`)

	// The phone rendering of the same journey: the 760-unit viewBox shrunk to
	// a phone column halves every label past legibility, so below 640px CSS
	// swaps the horizontal flow for the vertical funnel — the same drawing the
	// MCP insights app uses (its 520-unit portrait shape was made for a narrow
	// column), minus the throughput footnote the hero already states. Each
	// rendering carries its own aria label; CSS keeps exactly one in the tree.
	b.WriteString(`<div class="flow-phone">`)
	b.WriteString(string(funnelSVG(f)))
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// Meter renders a labelled ratio bar: the fill in the accent, the unfilled
// track a lighter step of the same ramp, the value at the right of the label
// line. For the shares a bare percentage under-tells (an 89% org share reads
// stronger as a nearly-full bar than as two digits).
func Meter(label string, frac float64, sub string) template.HTML {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return template.HTML(fmt.Sprintf(
		`<div class="meter"><div class="meter-head"><span>%s</span><b>%.0f%%</b></div>`+
			`<div class="meter-track"><i style="width:%.1f%%"></i></div><div class="meter-sub">%s</div></div>`,
		html.EscapeString(label), frac*100, frac*100, html.EscapeString(sub)))
}

// GapRow is one cohort-vs-peers adoption gap, drawn as a dumbbell: both
// adoption rates on one 0–100% track, the span between them shaded. The old
// rendering said "7 of 12 suggestions adopted here; peers adopted 69 with 52
// helped" for every row — true, unreadable at volume, and the SIZE of each gap
// (the reason the list is ranked) was nowhere visible.
type GapRow struct {
	Label, LabelHref string  // the cohort
	Area, AreaHref   string  // the area of practice the gap is in
	Here, Peers      float64 // adoption rates, 0–1
	Tip              string  // full counts, for hover
}

// GapRows renders the dumbbell list; legend adds the two-dot key (once per
// panel — a disclosure's continuation list doesn't repeat it).
func GapRows(rows []GapRow, legend bool) template.HTML {
	var b strings.Builder
	if legend {
		b.WriteString(`<div class="gap-legend"><span><i class="gap-dot here"></i>this cohort</span><span><i class="gap-dot peer"></i>peers with helped outcomes</span></div>`)
	}
	clamp := func(v float64) float64 {
		if v < 0 || v != v { // negative or NaN
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}
	for _, r := range rows {
		r.Here, r.Peers = clamp(r.Here), clamp(r.Peers)
		lo, hi := r.Here, r.Peers
		if lo > hi {
			lo, hi = hi, lo
		}
		fmt.Fprintf(&b,
			`<div class="gap-row" data-tip="%s"><div class="gap-who"><a href="%s"><b>%s</b></a> <a class="gap-area" href="%s">%s</a></div>`+
				`<div class="gap-track"><i class="gap-span" style="left:%.1f%%;width:%.1f%%"></i>`+
				`<i class="gap-dot here" style="left:%.1f%%"></i><i class="gap-dot peer" style="left:%.1f%%"></i></div>`+
				`<div class="gap-nums"><b>%.0f%%</b> here · peers <b>%.0f%%</b></div></div>`,
			html.EscapeString(r.Tip), html.EscapeString(r.LabelHref), html.EscapeString(r.Label),
			html.EscapeString(r.AreaHref), html.EscapeString(r.Area),
			lo*100, (hi-lo)*100, r.Here*100, r.Peers*100, r.Here*100, r.Peers*100)
	}
	return template.HTML(b.String())
}
