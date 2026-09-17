// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Charts, in the house grammar, for both consoles.
//
// Everything renders server-side as inline SVG / HTML against the design tokens
// in assets/app.css, so both color modes come for free and there is no build
// step. Mark specs follow the dataviz method: 2px round-cap lines, ≥8px end dots
// with 2px surface rings, 10% area washes, hairline solid gridlines, selective
// direct labels in text tokens (never series-colored text), a legend whenever two
// or more series share a plot, and hover targets that span whole buckets.
//
// This file holds what is not specific to either console's data — the geometry
// and the markup. The registry's viz.go builds the rest of its charts (bars,
// funnels, heatmaps) on the same helpers; the ingress draws its request-rate
// chart with LineChart directly. One implementation, so the two consoles cannot
// drift into two chart languages.
package ui

import (
	"fmt"
	"html"
	"html/template"
	"math"
	"strings"
)

// Series is one line on a chart; Key names the CSS series class (s1, s2…).
type Series struct {
	Name   string
	Key    string
	Values []int
}

// LineSeriesKeys is the fixed order a line chart assigns identity in, and it is
// three hues rather than eight on purpose.
//
// The eight-hue categorical palette was run through the dataviz validator for
// every pair, not merely adjacent ones — a chart whose series appear and vanish
// with the data makes any pair adjacent. Blue, green and pink pass every check
// in light and pass CVD separation in dark; every larger subset fails one, and
// not marginally: amber against green is ΔE 3.8 for a protan reader, orange
// against green 1.0, red against pink 12.5 for a reader with full colour vision.
//
// So the fourth series does not invent a fourth hue. It reuses the first with a
// dash pattern (app.css), which is a secondary encoding rather than a colour
// nobody can separate — every pair then differs in hue, in dash, or in both.
// Assignment is by entity, never by rank: a filter that drops a series must not
// repaint the ones that remain. A ninth entity folds into "other".
var LineSeriesKeys = []string{
	"s1", "s2", "s7",
	"s1 dash1", "s2 dash1", "s7 dash1",
	"s1 dash2", "s2 dash2",
}

func maxOfSeries(series []Series) int {
	max := 0
	for _, s := range series {
		for _, v := range s.Values {
			if v > max {
				max = v
			}
		}
	}
	return max
}

// NiceAxis picks a y-axis maximum and tick step for a chart whose data peaks at
// dataMax. It targets ~5 intervals with a round step (1 / 2 / 2.5 / 5 × 10^k),
// then sets yMax to the smallest multiple of that step at or above dataMax — so
// the plot fills vertically instead of stranding the curve below a coarse
// ceiling. (A 145 peak used to round up to 200, leaving the top quarter of the
// panel empty; it now lands on 150, ticks 0/50/100/150.) Callers draw a gridline
// and label at every multiple of step from 0 to yMax.
func NiceAxis(dataMax int) (yMax, step int) {
	if dataMax <= 4 {
		return 4, 1
	}
	raw := float64(dataMax) / 5
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	s := 10 * mag
	for _, m := range []float64{1, 2, 2.5, 5} {
		if m*mag >= raw {
			s = m * mag
			break
		}
	}
	step = int(math.Round(s))
	if step < 1 {
		step = 1
	}
	yMax = ((dataMax + step - 1) / step) * step
	return yMax, step
}

// FmtCount is the house's compact number: thousands separated, then K and M.
func FmtCount(v int) string {
	switch {
	case v >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(v)/1e6), ".0") + "M"
	case v >= 10_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(v)/1e3), ".0") + "K"
	case v >= 1_000:
		return fmt.Sprintf("%d,%03d", v/1000, v%1000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Axis is a chart's x axis: one label per bucket, and — when the buckets are
// real instants rather than pre-aggregated calendar keys — the instants
// themselves, so each label can be re-formatted into the viewer's timezone
// (LocalTimeScript). Labels alone still work: an axis with no ISO is rendered
// exactly as it was given.
//
// The distinction matters. A bucket that starts at a known moment can honestly
// be re-dated for a reader in another zone; a day key that was aggregated
// somewhere upstream cannot, because the bucket boundary is already fixed in
// whatever zone did the aggregating and moving only its label would be a lie
// about which rows are in it.
type Axis struct {
	Labels []string
	ISO    []string // optional; parallel to Labels when present
	Kind   string   // LTDate or LTWeek — how the browser should re-format
}

// Tick is the axis label for bucket i, as markup.
func (a Axis) Tick(i int) string {
	if i < len(a.ISO) && a.ISO[i] != "" {
		return LocalTimeISO(a.ISO[i], a.Kind)
	}
	return html.EscapeString(a.Labels[i])
}

// HoverAttrs is the label attributes for bucket i's hit target. The tooltip
// reads data-label at hover time, so the localiser rewrites that attribute
// rather than any text — hence the instant travelling beside it.
func (a Axis) HoverAttrs(i int) string {
	if i < len(a.ISO) && a.ISO[i] != "" {
		return fmt.Sprintf(`data-label="%s" data-label-iso="%s" data-lt="%s"`,
			html.EscapeString(a.Labels[i]), html.EscapeString(a.ISO[i]), a.Kind)
	}
	return fmt.Sprintf(`data-label="%s"`, html.EscapeString(a.Labels[i]))
}

// LineChart renders a multi-series line chart with a crosshair hover layer.
// The axis names the x positions (one label per bucket); each series must have
// len(Values)==len(axis.Labels).
//
// The plot (gridlines, area wash, lines, crosshair, hit targets) is a text-free
// SVG that stretches to fill the panel height (preserveAspectRatio="none"); the
// axis ticks, the endpoint dots and the direct end labels live in HTML gutters
// around it, so a non-uniform stretch grows the line into the space without
// smearing any text or squashing the dots into ellipses. Where the panel isn't
// stretched the box keeps the viewBox aspect.
func LineChart(axis Axis, series []Series, height int) template.HTML {
	const w = 760
	top, bottom := 6.0, 6.0
	h := float64(height)
	plotW, plotH := float64(w), h-top-bottom

	n := len(axis.Labels)
	if n == 0 || len(series) == 0 {
		return `<p class="empty">no activity in this window</p>`
	}
	yMax, yStep := NiceAxis(maxOfSeries(series))
	xf := func(i int) float64 {
		if n == 1 {
			return 0.5
		}
		return float64(i) / float64(n-1)
	}
	yf := func(v int) float64 { return (top + plotH*(1-float64(v)/float64(yMax))) / h }
	x := func(i int) float64 { return xf(i) * plotW }
	y := func(v int) float64 { return yf(v) * h }

	var b strings.Builder
	// legend (always present: >= 2 series)
	if len(series) > 1 {
		b.WriteString(`<div class="legend">`)
		for _, s := range series {
			fmt.Fprintf(&b, `<span class="lg"><span class="lg-line %s"></span>%s</span>`,
				s.Key, html.EscapeString(s.Name))
		}
		b.WriteString(`</div>`)
	}

	names, keys := make([]string, len(series)), make([]string, len(series))
	for i, s := range series {
		names[i], keys[i] = s.Name, s.Key
	}

	b.WriteString(`<div class="vchart">`)

	// y-axis labels, pinned to their gridlines by percentage.
	b.WriteString(`<div class="vchart-y" aria-hidden="true">`)
	for v := 0; v <= yMax; v += yStep {
		fmt.Fprintf(&b, `<span style="top:%.2f%%">%s</span>`, yf(v)*100, FmtCount(v))
	}
	b.WriteString(`</div>`)

	// The plot spans the full width; its endpoint dot and direct label overlay
	// it (below) instead of taking a right gutter.
	b.WriteString(`<div class="vchart-plot">`)
	// plot: gridlines, wash, lines, crosshair, hit targets — no text, no dots.
	fmt.Fprintf(&b, `<svg class="viz" viewBox="0 0 %d %d" preserveAspectRatio="none" data-series="%s" data-keys="%s" role="img" aria-label="activity over time">`,
		w, int(h), html.EscapeString(strings.Join(names, "|")), strings.Join(keys, "|"))
	b.WriteString(BloomDefs)
	for v := 0; v <= yMax; v += yStep {
		gy := yf(v) * h
		fmt.Fprintf(&b, `<line class="gl" x1="0" y1="%.1f" x2="%.1f" y2="%.1f"/>`, gy, plotW, gy)
	}
	// crosshair (moved by the hover layer; x is in viewBox units, which map
	// linearly to pixels even under the non-uniform stretch)
	fmt.Fprintf(&b, `<line class="cross" x1="0" x2="0" y1="%.1f" y2="%.1f" visibility="hidden"/>`, top, top+plotH)
	for _, s := range series {
		var pts strings.Builder
		for i, v := range s.Values {
			if i > 0 {
				pts.WriteByte(' ')
			}
			fmt.Fprintf(&pts, "%.1f,%.1f", x(i), y(v))
		}
		if len(series) == 1 {
			fmt.Fprintf(&b, `<polygon class="wash %s" points="%.1f,%.1f %s %.1f,%.1f"/>`,
				s.Key, x(0), top+plotH, pts.String(), x(n-1), top+plotH)
		}
		fmt.Fprintf(&b, `<polyline class="line %s" points="%s"/>`, s.Key, pts.String())
	}
	// hover hit columns: the whole bucket is the target, never the 2px line
	colW := plotW / float64(maxInt(n-1, 1))
	for i := 0; i < n; i++ {
		vals := make([]string, len(series))
		for si, s := range series {
			vals[si] = fmt.Sprintf("%d", s.Values[i])
		}
		fmt.Fprintf(&b, `<rect class="hit" x="%.1f" y="%.1f" width="%.1f" height="%.1f" data-x="%.1f" %s data-v="%s" tabindex="0"/>`,
			x(i)-colW/2, top, colW, plotH, x(i),
			axis.HoverAttrs(i), strings.Join(vals, "|"))
	}
	b.WriteString(`</svg>`)

	// Endpoint dot on the plot's right edge, marking each series' latest value.
	for _, s := range series {
		fmt.Fprintf(&b, `<span class="end-dot %s" style="top:%.2f%%"></span>`, s.Key, yf(s.Values[n-1])*100)
	}
	b.WriteString(`</div>`) // .vchart-plot

	// x-axis labels: first / middle / last, pinned under their columns.
	b.WriteString(`<div class="vchart-x" aria-hidden="true">`)
	for _, i := range []int{0, n / 2, n - 1} {
		fmt.Fprintf(&b, `<span style="left:%.2f%%">%s</span>`,
			xf(i)*100, axis.Tick(i))
	}
	b.WriteString(`</div></div>`)
	return template.HTML(b.String())
}

// Sparkline is a figure's recent shape, drawn small enough to sit inside a stat
// tile: no axes, no labels, no hover — the number beside it is the value, and
// this says which way it has been going.
//
// Fewer than two points is not a trend and draws nothing. A single sample would
// otherwise be a dot claiming a direction, and the arithmetic that places it
// divides by the number of gaps between points, of which there are none.
func Sparkline(values []int) template.HTML {
	const w, h = 120, 30
	if len(values) < 2 {
		return ""
	}
	max := 1
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	var pts strings.Builder
	for i, v := range values {
		if i > 0 {
			pts.WriteByte(' ')
		}
		fmt.Fprintf(&pts, "%.1f,%.1f", 4+float64(i)*(w-8)/float64(len(values)-1),
			float64(h-5)-float64(v)/float64(max)*float64(h-10))
	}
	lastX := 4 + float64(len(values)-1)*(w-8)/float64(len(values)-1)
	lastY := float64(h-5) - float64(values[len(values)-1])/float64(max)*float64(h-10)
	return template.HTML(fmt.Sprintf(
		`<svg class="spark" viewBox="0 0 %d %d" aria-hidden="true"><polyline class="spark-line" points="%s"/><circle class="spark-dot" cx="%.1f" cy="%.1f" r="3.5"/></svg>`,
		w, h, pts.String(), lastX, lastY))
}

// BloomDefs are the SVG filters the bloom classes reference. They are emitted
// into each chart rather than declared in CSS: WebKit does not apply a CSS
// filter to an SVG child element, so every drop-shadow aimed at a <rect> was
// doing nothing in Safari while working in Chromium.
const BloomDefs = `<defs>` +
	`<filter id="tb1" x="-150%" y="-150%" width="400%" height="400%">` +
	`<feGaussianBlur stdDeviation="0.7" result="b"/>` +
	`<feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>` +
	`<filter id="tb2" x="-150%" y="-150%" width="400%" height="400%">` +
	`<feGaussianBlur stdDeviation="1.3" result="b"/>` +
	`<feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>` +
	`<filter id="tb3" x="-150%" y="-150%" width="400%" height="400%">` +
	`<feGaussianBlur stdDeviation="2.2" result="b"/>` +
	`<feMerge><feMergeNode in="b"/><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>` +
	`<filter id="tb4" x="-150%" y="-150%" width="400%" height="400%">` +
	`<feGaussianBlur stdDeviation="3.2" result="b"/>` +
	`<feMerge><feMergeNode in="b"/><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>` +
	`<filter id="tbe" x="-150%" y="-150%" width="400%" height="400%">` +
	`<feGaussianBlur stdDeviation="1.8" result="b"/>` +
	`<feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge></filter>` +
	`</defs>`
