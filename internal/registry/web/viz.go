// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Chart builders for the Outcomes dashboard.
//
// Everything renders server-side as inline SVG / HTML against the CSS design
// tokens in shell.go, so both color modes come for free and there is no build
// step. Mark specs follow the dataviz method: 2px round-cap lines, ≥8px end
// dots with 2px surface rings, bars ≤24px with 4px rounded data-ends (square
// at the baseline), 10% area washes, hairline solid gridlines, selective
// direct labels in text tokens (never series-colored text), a legend whenever
// two or more series share a plot, and hover targets that span whole buckets.
package web

import (
	"fmt"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/ui"
)

// VizSeries is one line on a chart; Key names the CSS series class (s1, s2…).
type VizSeries struct {
	Name   string
	Key    string
	Values []int
}

// niceAxis and fmtCount are the shared implementations under the names this
// package's ~60 call sites already use. The bodies moved to internal/ui when the
// ingress console needed the same charts; keeping the local names is what stopped
// that move from touching every chart in the dashboard.
func niceAxis(dataMax int) (yMax, step int) { return ui.NiceAxis(dataMax) }

func fmtCount(v int) string { return ui.FmtCount(v) }

// timeAxis is the x axis for a window's buckets. Each bucket start is a real
// instant, so it travels with the label and the browser re-dates it into the
// reader's own zone (ui.LocalTimeScript); the server-rendered label is the UTC
// fallback. Both charts build their axis here so the two cannot drift.
//
// Worth knowing what the label does and does not claim: a window is a rolling
// span from now, so a "daily" bucket is the 24 hours from the current
// time-of-day, not from anyone's midnight. The label says which day the bucket
// STARTS in for the reader looking at it — which is the honest reading in any
// zone, and the only one that stays stable as the page is opened from
// elsewhere.
func timeAxis(buckets []insights.Bucket, bucket time.Duration) ui.Axis {
	a := ui.Axis{
		Labels: make([]string, len(buckets)),
		ISO:    make([]string, len(buckets)),
		Kind:   ui.LTDate,
	}
	if bucket >= 7*24*time.Hour {
		a.Kind = ui.LTWeek
	}
	for i, bk := range buckets {
		a.Labels[i] = bucketLabel(bk.Start, bucket)
		a.ISO[i] = bk.Start.UTC().Format(time.RFC3339)
	}
	return a
}

// bucketLabel is the no-JS fallback text, in UTC. The live page shows the
// reader's zone instead (timeAxis).
func bucketLabel(t time.Time, bucket time.Duration) string {
	t = t.UTC()
	if bucket >= 7*24*time.Hour {
		return "wk " + t.Format("Jan 2")
	}
	return t.Format("Jan 2")
}

// LineChart renders a multi-series line chart with a crosshair hover layer.
// Buckets provide the x axis; each series must have len(Values)==len(buckets).
//
// Like the stacked bar chart, the plot (gridlines, area wash, lines, crosshair,
// hit targets) is a text-free SVG that stretches to fill the panel height
// (preserveAspectRatio="none"); the axis ticks, the endpoint dots and the direct
// end labels live in HTML gutters around it, so a non-uniform stretch grows the
// line into the space without smearing any text or squashing the dots into
// ellipses. Where the panel isn't stretched the box keeps the viewBox aspect.
func LineChart(buckets []insights.Bucket, bucket time.Duration, series []VizSeries, height int) template.HTML {
	shared := make([]ui.Series, len(series))
	for i, s := range series {
		shared[i] = ui.Series{Name: s.Name, Key: s.Key, Values: s.Values}
	}
	return ui.LineChart(timeAxis(buckets, bucket), shared, height)
}

// bucketNoun is "day" or "week" for the window's bucket size.
func bucketNoun(bucket time.Duration) string {
	if bucket >= 7*24*time.Hour {
		return "week"
	}
	return "day"
}

// StackedBarChart renders one vertical stacked bar per bucket over the window —
// each segment a series (shown/adopted/helped/dismissed). It reuses the shared
// hover layer: the <svg> carries data-series/data-keys and each bucket a full-
// height .hit rect with data-v, so the tooltip behaves exactly like the line
// charts. No crosshair — that's a line affordance, omitted for bars.
//
// The plot (bars + gridlines + hit targets) is a text-free SVG that stretches
// to whatever height its container gives it (preserveAspectRatio="none"), so
// the Activity panel can fill a grid cell sized by a taller neighbour instead
// of stranding the chart over dead space. The axis labels are HTML positioned
// by percentage OUTSIDE that SVG — a non-uniform stretch would smear SVG text,
// but HTML glyphs stay crisp at any size and the percentages keep them pinned
// to the gridlines they annotate. height is the design height that sets the
// viewBox aspect and the min-height floor; CSS may draw it taller.
func StackedBarChart(buckets []insights.Bucket, bucket time.Duration, series []VizSeries, height int) template.HTML {
	const w = 760
	// No margin reserved for axis text — the labels live in the HTML gutters
	// around the plot. A small top/bottom inset keeps the extreme gridline
	// strokes and the tallest bar from clipping at the SVG edges.
	top, bottom := 6.0, 6.0
	h := float64(height)
	plotW, plotH := float64(w), h-top-bottom
	n := len(buckets)
	if n == 0 || len(series) == 0 {
		return `<p class="empty">No activity in this window.</p>`
	}
	totals := make([]int, n)
	for _, s := range series {
		for i, v := range s.Values {
			totals[i] += v
		}
	}
	maxTotal := 0
	for _, t := range totals {
		if t > maxTotal {
			maxTotal = t
		}
	}
	yMax, yStep := niceAxis(maxTotal)
	yFrac := func(v float64) float64 { return (top + plotH*(1-v/float64(yMax))) / h }
	colW := plotW / float64(n)
	barW := colW * 0.66
	if barW > 30 {
		barW = 30
	}
	center := func(i int) float64 { return colW * (float64(i) + 0.5) }

	var b strings.Builder
	// legend (always: >= 2 series share the plot)
	b.WriteString(`<div class="legend">`)
	for _, s := range series {
		fmt.Fprintf(&b, `<span class="lg"><span class="lg-swatch %s"></span>%s</span>`, s.Key, html.EscapeString(s.Name))
	}
	b.WriteString(`</div>`)

	names, keys := make([]string, len(series)), make([]string, len(series))
	for i, s := range series {
		names[i], keys[i] = s.Name, s.Key
	}
	// The grid wrapper (shared with the line chart): a y-label gutter and the plot
	// on top, an x-label gutter beneath. It is the flex/grid child that grows
	// inside the panel; the bar chart leaves the end gutter empty.
	b.WriteString(`<div class="vchart">`)

	// y-axis labels, pinned to their gridlines by percentage.
	b.WriteString(`<div class="vchart-y" aria-hidden="true">`)
	for v := 0; v <= yMax; v += yStep {
		fmt.Fprintf(&b, `<span style="top:%.2f%%">%s</span>`, yFrac(float64(v))*100, fmtCount(v))
	}
	b.WriteString(`</div>`)

	b.WriteString(`<div class="vchart-plot">`)
	fmt.Fprintf(&b, `<svg class="viz" viewBox="0 0 %d %d" preserveAspectRatio="none" data-series="%s" data-keys="%s" role="img" aria-label="activity over time">`,
		w, int(h), html.EscapeString(strings.Join(names, "|")), strings.Join(keys, "|"))
	b.WriteString(vizBloomDefs)
	// gridlines (their numbers are the HTML labels above)
	for v := 0; v <= yMax; v += yStep {
		gy := yFrac(float64(v)) * h
		fmt.Fprintf(&b, `<line class="gl" x1="0" y1="%.1f" x2="%.1f" y2="%.1f"/>`, gy, plotW, gy)
	}
	// stacked bars, baseline up in series order
	base := top + plotH
	// --v is how hot a segment is, against the hottest single segment anywhere on
	// the chart. The stylesheet turns it into bloom: a quiet day stays matte, a
	// peak burns out. Normalising against the largest SEGMENT rather than the
	// largest stack is deliberate — against a stack total, a chart whose columns
	// are built from many small segments would never bloom at all.
	segMax := 0
	for _, s := range series {
		for _, v := range s.Values {
			if v > segMax {
				segMax = v
			}
		}
	}
	for i := 0; i < n; i++ {
		x := center(i) - barW/2
		yc := base
		for _, s := range series {
			if s.Values[i] <= 0 {
				continue
			}
			segH := plotH * float64(s.Values[i]) / float64(yMax)
			yc -= segH
			v := 0.0
			if segMax > 0 {
				v = float64(s.Values[i]) / float64(segMax)
			}
			fmt.Fprintf(&b, `<rect class="vbar %s%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f"/>`, s.Key, bloomStep(v), x, yc, barW, segH)
		}
	}
	// hover hit columns: the whole bucket is the target
	axis := timeAxis(buckets, bucket)
	for i := 0; i < n; i++ {
		vals := make([]string, len(series))
		for si, s := range series {
			vals[si] = fmt.Sprintf("%d", s.Values[i])
		}
		fmt.Fprintf(&b, `<rect class="hit" x="%.1f" y="%.1f" width="%.1f" height="%.1f" data-x="%.1f" %s data-v="%s" tabindex="0"/>`,
			colW*float64(i), top, colW, plotH, center(i),
			axis.HoverAttrs(i), strings.Join(vals, "|"))
	}
	b.WriteString(`</svg></div>`) // .vchart-plot

	// x-axis labels: first / middle / last, pinned under their columns.
	b.WriteString(`<div class="vchart-x" aria-hidden="true">`)
	for _, i := range []int{0, n / 2, n - 1} {
		fmt.Fprintf(&b, `<span style="left:%.2f%%">%s</span>`,
			center(i)/plotW*100, axis.Tick(i))
	}
	b.WriteString(`</div></div>`)
	return template.HTML(b.String())
}

// Sparkline is the 12-point stat-tile trend: de-emphasis stroke, accent dot on
// the current period.
func Sparkline(values []int) template.HTML { return ui.Sparkline(values) }

// BarRow is one row of a horizontal leaderboard.
type BarRow struct {
	Label string
	Value int
	Max   int
	Key   string // series/ordinal CSS class
	Sub   string // right-hand annotation (Δ, n=, age)
	Href  string
	Ref   float64 // 0–1 baseline marker on the track (0 = none) — e.g. the org average a rate is judged against
}

// HBars renders a leaderboard: label · track+fill · value at the tip · sub.
func HBars(rows []BarRow, empty string) template.HTML {
	if len(rows) == 0 {
		return template.HTML(`<p class="empty">` + html.EscapeString(empty) + `</p>`)
	}
	var b strings.Builder
	b.WriteString(`<div class="bars">`)
	for _, r := range rows {
		pct := 0.0
		if r.Max > 0 {
			pct = float64(r.Value) / float64(r.Max) * 100
		}
		tag, href := "div", ""
		if r.Href != "" {
			tag, href = "a", fmt.Sprintf(` href="%s"`, html.EscapeString(r.Href))
		}
		ref := ""
		if r.Ref > 0 && r.Ref <= 1 {
			ref = fmt.Sprintf(`<i class="bar-ref" style="left:%.1f%%"></i>`, r.Ref*100)
		}
		fmt.Fprintf(&b,
			`<%s class="bar-row"%s data-tip="%s"><span class="bar-label">%s</span><span class="bar-track"><span class="bar-fill %s%s" style="width:%.1f%%"></span>%s</span><span class="bar-val">%s</span><span class="bar-sub">%s</span></%s>`,
			tag, href,
			html.EscapeString(fmt.Sprintf("%s: %s %s", r.Label, fmtCount(r.Value), r.Sub)),
			html.EscapeString(r.Label), r.Key, bloomStep(pct/100), pct, ref, fmtCount(r.Value),
			html.EscapeString(r.Sub), tag)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// vizBloomDefs is the bloom, expressed as SVG filters rather than as a CSS one.
//
// WebKit does not apply a CSS filter to SVG child elements, so every drop-shadow
// aimed at a <rect> or <polyline> was silently doing nothing in Safari while
// working in Chromium — which is why the leaderboard bars (HTML spans) glowed
// there and the chart bars did not. HTML marks keep the CSS drop-shadow, which
// Safari honours; SVG marks reference these.
//
// feGaussianBlur on the SourceGraphic blurs the mark IN ITS OWN COLOURS, so one
// set of filters serves every series and nothing here needs to know a hue. The
// blurred result is merged under the original, twice for the stronger steps,
// which is what accumulates past the mark's colour toward white and gives the
// spread its spectral edge.
//
// The filter region has to be stated: the default is 10% of the bounding box,
// which would crop the blur to nothing on a bar a few pixels wide.
const vizBloomDefs = ui.BloomDefs

// vizGoBloomSource restates bloomStep's constants so a test can pin the
// JavaScript copy in usage.go to them. Keeping it beside the function is the
// point: change one and this is right there to change too.
const vizGoBloomSource = "b := v * (0.5 + 0.5*v); 0.72 0.44 0.20 0.06"

// bloomStep turns a 0..1 magnitude into one of five step classes. The stylesheet
// carries a static filter per step rather than computing one from a custom
// property: WebKit drops an entire filter declaration if any part of it fails to
// parse, and it has been unreliable with color-mix() and with calc()/var()
// lengths inside drop-shadow(). Discrete classes with literal values render the
// same everywhere. The curve is the same one the CSS used to apply.
func bloomStep(v float64) string {
	if v <= 0 {
		return ""
	}
	b := v * (0.5 + 0.5*v)
	switch {
	case b >= 0.72:
		return " bloom4"
	case b >= 0.44:
		return " bloom3"
	case b >= 0.20:
		return " bloom2"
	case b >= 0.06:
		return " bloom1"
	}
	return ""
}

// FunnelChart draws the shown→adopted→helped funnel as an actual funnel: three
// deepening bars whose widths are proportional to their counts, joined by faint
// tapering walls, with the step conversion rate and the drop-off named in each
// neck. Below it, one line states the end-to-end throughput (helped of shown).
// The shape carries the story; the walls make the narrowing — and the losses —
// legible at a glance.
func FunnelChart(f insights.Funnel) template.HTML {
	if f.Shown == 0 {
		return template.HTML(`<p class="empty">No suggestions appeared in this window yet.</p>`)
	}
	return template.HTML(string(funnelSVG(f)) + fmt.Sprintf(
		`<p class="funnel-note"><b>%.0f%%</b> of shown suggestions were measurably helped: %s of %s.</p>`,
		float64(f.Helped)/float64(f.Shown)*100, fmtCount(f.Helped), fmtCount(f.Shown)))
}

// funnelSVG is the funnel shape alone — FunnelChart's drawing without its
// throughput footnote, so the Outcomes hero (which already states the
// end-to-end rate) can reuse the shape as its phone rendering.
func funnelSVG(f insights.Funnel) template.HTML {
	stages := []struct{ name, tint string }{
		{"shown", "o1"}, {"adopted", "o2"}, {"helped", "o3"},
	}
	vals := []int{f.Shown, f.Adopted, f.Helped}
	drops := []string{"not adopted", "not helped"}

	const (
		w       = 520.0
		cx      = 250.0
		maxHalf = 116.0
		barH    = 46.0
		neckH   = 40.0
		pad     = 14.0
	)
	half := func(v int) float64 {
		if v <= 0 {
			return 0
		}
		if h := maxHalf * float64(v) / float64(f.Shown); h > 6 {
			return h
		}
		return 6
	}
	barY := func(i int) float64 { return pad + float64(i)*(barH+neckH) }
	height := pad*2 + 3*barH + 2*neckH

	var b strings.Builder
	aria := fmt.Sprintf("Funnel: %s shown; %s adopted; %s measurably helped.",
		fmtCount(f.Shown), fmtCount(f.Adopted), fmtCount(f.Helped))
	fmt.Fprintf(&b, `<svg class="funnel-svg" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`,
		w, height, html.EscapeString(aria))
	b.WriteString(vizBloomDefs)

	// Tapering walls first, so the rounded bars sit over them.
	for i := 0; i < len(vals)-1; i++ {
		hu, hl := half(vals[i]), half(vals[i+1])
		yb, yt := barY(i)+barH, barY(i+1)
		fmt.Fprintf(&b, `<polygon class="fn-wall %s" points="%.1f,%.1f %.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`,
			stages[i+1].tint, cx-hu, yb, cx+hu, yb, cx+hl, yt, cx-hl, yt)
	}
	// The bars.
	for i, s := range stages {
		h := half(vals[i])
		fmt.Fprintf(&b, `<rect class="fn-bar %s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="5"/>`,
			s.tint, cx-h, barY(i), 2*h, barH)
	}
	// Stage name (left) and count (right), in fixed columns aligned to the widest bar.
	nameX, valX := cx-maxHalf-12, cx+maxHalf+12
	for i, s := range stages {
		yc := barY(i) + barH/2
		fmt.Fprintf(&b, `<text class="fn-name" x="%.1f" y="%.1f" text-anchor="end" dominant-baseline="middle">%s</text>`, nameX, yc, s.name)
		fmt.Fprintf(&b, `<text class="fn-count" x="%.1f" y="%.1f" text-anchor="start" dominant-baseline="middle">%s</text>`, valX, yc, fmtCount(vals[i]))
	}
	// Step conversion + the loss it implies, centred in each neck. A stage that
	// never happened has no rate to convert FROM — the same guard the
	// horizontal flow carries, and without it an empty funnel printed NaN%.
	for i := 0; i < len(vals)-1; i++ {
		if vals[i] == 0 {
			continue
		}
		yc := barY(i) + barH + neckH/2
		fmt.Fprintf(&b, `<text class="fn-conv" x="%.1f" y="%.1f" text-anchor="middle">%.0f%% &#8595;</text>`,
			cx, yc-3, float64(vals[i+1])/float64(vals[i])*100)
		fmt.Fprintf(&b, `<text class="fn-drop" x="%.1f" y="%.1f" text-anchor="middle">%s %s</text>`,
			cx, yc+12, fmtCount(vals[i]-vals[i+1]), drops[i])
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// MixBar renders a single stacked distribution bar (2px surface gaps between
// segments) with its legend — no drill-down.
func MixBar(entries []insights.MixEntry, keys []string, empty string) template.HTML {
	return LinkedMixBar(entries, keys, empty, nil)
}

// LinkedMixBar is MixBar with drill-down: when hrefFor is non-nil and returns a
// non-empty URL for a segment's label, that segment and its legend entry become
// links to the breakdown behind it. Same markup and look as MixBar, so linked
// and static distributions read identically across the dashboard.
func LinkedMixBar(entries []insights.MixEntry, keys []string, empty string, hrefFor func(label string) string) template.HTML {
	total := 0
	for _, e := range entries {
		total += e.Count
	}
	if total == 0 {
		return template.HTML(`<p class="empty">` + html.EscapeString(empty) + `</p>`)
	}
	href := func(label string) string {
		if hrefFor == nil {
			return ""
		}
		return hrefFor(label)
	}
	segMax := 0
	for _, e := range entries {
		if e.Count > segMax {
			segMax = e.Count
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="mix">`)
	for i, e := range entries {
		key := keys[i%len(keys)]
		tip := html.EscapeString(fmt.Sprintf("%s: %s (%.0f%%)",
			e.Label, fmtCount(e.Count), float64(e.Count)/float64(total)*100))
		v := 0.0
		if segMax > 0 {
			v = float64(e.Count) / float64(segMax)
		}
		if h := href(e.Label); h != "" {
			fmt.Fprintf(&b, `<a class="mix-seg %s%s" style="flex-grow:%d" href="%s" data-tip="%s"></a>`,
				key, bloomStep(v), e.Count, html.EscapeString(h), tip)
		} else {
			fmt.Fprintf(&b, `<span class="mix-seg %s%s" style="flex-grow:%d" data-tip="%s"></span>`,
				key, bloomStep(v), e.Count, tip)
		}
	}
	b.WriteString(`</div><div class="legend">`)
	for i, e := range entries {
		inner := fmt.Sprintf(`<span class="lg-swatch %s"></span>%s <b>%s</b>`,
			keys[i%len(keys)], html.EscapeString(e.Label), fmtCount(e.Count))
		if h := href(e.Label); h != "" {
			fmt.Fprintf(&b, `<a class="lg" href="%s">%s</a>`, html.EscapeString(h), inner)
		} else {
			fmt.Fprintf(&b, `<span class="lg">%s</span>`, inner)
		}
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// TileLink wraps a StatTile so the whole tile navigates to href (the label
// grows an accent arrow via CSS to signal it's clickable).
func TileLink(href string, tile template.HTML) template.HTML {
	return template.HTML(`<a class="tile-link" href="` + html.EscapeString(href) + `">` + string(tile) + `</a>`)
}

// Tile is one KPI, declared rather than assembled. There were eight separate
// hand-built tile rows over the StatTile primitive — each re-deriving the
// wrapper div, the link wrapping, and the tiles-tight variant, and each free to
// drift. Callers now describe the tiles and TileRow renders them.
type Tile struct {
	Label, Value, Delta string
	Good                bool          // a positive delta is good news (colours it)
	Spark               template.HTML // optional sparkline
	Href                string        // optional: makes the whole tile a doorway
	Note                string        // optional plain-English tooltip (title=), for a metric a label alone can't fully carry
	Action              TileAction    // optional: a control ON the tile, instead of the whole tile being one
}

// TileAction is a tile's own control — the Apply button's look, on the floor of
// the tile beside the row's sparklines.
//
// It REPLACES Href rather than joining it. A button inside an anchor is not
// valid HTML and reads as two doorways to a keyboard, so a tile that carries an
// action is not itself a link, and loses the ↗ that marks one.
type TileAction struct{ Label, Href string }

// TileRow renders a row of KPI tiles. tight selects the in-panel variant, whose
// tiles flatten to plain stats — no tile inside a tile.
func TileRow(tight bool, tiles ...Tile) template.HTML {
	cls := "tiles"
	if tight {
		cls = "tiles tiles-tight"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="%s">`, cls)
	for _, t := range tiles {
		one := StatTile(t.Label, t.Value, t.Delta, t.Good, t.Spark)
		if t.Note != "" {
			// StatTile's output always opens `<div class="tile">`; hang the
			// tooltip there. Controlled strings on both sides, so the single
			// replace is exact.
			one = template.HTML(strings.Replace(string(one), `<div class="tile">`,
				`<div class="tile" title="`+html.EscapeString(t.Note)+`">`, 1))
		}
		if t.Action.Href != "" {
			// StatTile always closes with the tile's own </div>; the control
			// goes inside it, last, so margin-top:auto stands it on the floor.
			one = template.HTML(strings.TrimSuffix(string(one), "</div>") +
				`<a class="tile-act" href="` + html.EscapeString(t.Action.Href) + `">` +
				html.EscapeString(t.Action.Label) + `</a></div>`)
		} else if t.Href != "" {
			one = TileLink(t.Href, one)
		}
		b.WriteString(string(one))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// StatTile renders one KPI: label / value / optional delta vs the previous
// window / optional sparkline.
func StatTile(label, value, delta string, deltaGood bool, spark template.HTML) template.HTML {
	var b strings.Builder
	b.WriteString(`<div class="tile"><div class="tile-label">` + html.EscapeString(label) + `</div>`)
	b.WriteString(`<div class="tile-row"><span class="tile-value">` + html.EscapeString(value) + `</span>`)
	if delta != "" {
		cls := "down"
		if deltaGood {
			cls = "up"
		}
		b.WriteString(`<span class="tile-delta ` + cls + `">` + html.EscapeString(delta) + `</span>`)
	}
	b.WriteString(`</div>`)
	if spark != "" {
		b.WriteString(string(spark))
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}
