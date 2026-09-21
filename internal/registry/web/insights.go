// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Outcomes pages — overview and per-technique drill-down.
// Aggregate/cohort analytics only:
// the registry holds no user identity by design.
package web

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// mixKeys is the fixed categorical order for distribution segments.
var mixKeys = []string{"s1", "s2", "s3", "s5", "s7", "s8"}

// windowSelect renders the time-period dropdown that scopes the page. It's
// emitted in the page body but the shell script relocates it onto the
// breadcrumb line (right-aligned); with JS off it stays in the body, still
// functional. Each option's value is the page URL with ?w=<key>, so changing
// the selection navigates. The preset labels don't depend on earliest, so the
// options are stable across pages.
func windowSelect(basePath, active string, now time.Time, earliest time.Time) string {
	return windowSelectURLs(active, now, earliest, func(key string) string {
		return basePath + "?w=" + key
	})
}

// windowSelectURLs is the one copy of the picker markup. Pages differ only in
// the URL an option navigates to, so they hand in urlFor and share everything
// else: the option order, the labels, which one is selected, and the escaping.
func windowSelectURLs(active string, now, earliest time.Time, urlFor func(key string) string) string {
	var b strings.Builder
	b.WriteString(`<div class="window-nav"><select class="window-select" aria-label="Time period">`)
	for _, w := range insights.Windows(now, earliest) {
		sel := ""
		if w.Key == active {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			html.EscapeString(urlFor(w.Key)), sel, html.EscapeString(w.Label))
	}
	b.WriteString(`</select></div>`)
	return b.String()
}

func delta(cur, prev int) (string, bool) {
	d := cur - prev
	switch {
	case d > 0:
		return fmt.Sprintf("+%d from the previous window", d), true
	case d < 0:
		return fmt.Sprintf("%d from the previous window", d), false
	default:
		return "±0 from the previous window", true
	}
}

// activityChartPanel is the shared Activity panel: a stacked bar per bucket over
// the window — shown, adopted, helped, dismissed. Used on every insight view that
// can scope events to a slice (overview, technique, tag, source, cohort).
func activityChartPanel(bkts []insights.Bucket, bucket time.Duration) string {
	shown := make([]int, len(bkts))
	adopted := make([]int, len(bkts))
	helped := make([]int, len(bkts))
	dismissed := make([]int, len(bkts))
	for i, b := range bkts {
		shown[i], adopted[i], helped[i], dismissed[i] = b.Funnel.Shown, b.Funnel.Adopted, b.Funnel.Helped, b.Funnel.Dismissed
	}
	series := []VizSeries{
		{Name: "shown", Key: "s1", Values: shown},
		{Name: "adopted", Key: "s2", Values: adopted},
		{Name: "helped", Key: "s4", Values: helped},
		{Name: "dismissed", Key: "s6", Values: dismissed},
	}
	// The four series are named by the chart's own legend and the bar tooltips,
	// so the caption that used to list them here said nothing twice.
	return `<section class="panel chart-panel"><h2>Activity</h2>` +
		ui.Sub("", "events per "+bucketNoun(bucket)) +
		string(StackedBarChart(bkts, bucket, series, 230)) + `</section>`
}

// capSetMatch returns an event matcher accepting events for any technique in the
// given roll-up — used to scope the Activity panel on the tag and source views.
func capSetMatch(list []insights.TechniqueStat) func(models.FeedbackEvent) bool {
	set := make(map[string]bool, len(list))
	for _, c := range list {
		set[c.Technique.ID] = true
	}
	return func(e models.FeedbackEvent) bool { return set[e.TechniqueID] }
}

func drillHref(id, window string) string {
	return "/techniques/" + url.PathEscape(id) + "?w=" + url.QueryEscape(window)
}

// pageInsightCohorts is the cohort breakdown reached from the Outcomes view
// menu and the heatmap's "All cohorts →": an org baseline plus, per segment
// dimension, how each cohort moves through the funnel — so the reader sees
// which cohorts exist and how their OpenTacit usage differs. Aggregate cohorts
// only; never individuals.
func (s *Server) pageInsightCohorts(r *http.Request, user oidc.Claims) page {
	events, err := s.Store.AllEvents("")
	if err != nil {
		return storeUnavailablePage("outcomes", nil, err)
	}
	now := time.Now().UTC()
	w := insights.WindowByKey(r.URL.Query().Get("w"), now, insights.Earliest(events))
	rep := insights.ComputeCohorts(events, now, w)
	cohortCrumbs := []crumb{{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(w.Key)}, {label: "Cohorts", href: ""}}
	cohortViews := outcomesViews("cohorts", w.Key)

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Cohorts group activity by shared context. Each cohort is a <code>dimension:value</code> pair, such as team, role, or harness. One event can belong to several cohorts. A member joins one with <code>tacit cohorts</code>, which lists exactly the values below.</p></div>`)
	b.WriteString(windowSelect("/outcomes/cohorts", w.Key, now, insights.Earliest(events)))

	// Baseline — computed over cohort-tagged events only, so the per-cohort rates
	// below are compared against a like-for-like average (not the org-wide total,
	// which can include activity that never carried a segment).
	helpedRate, measured := rep.Tagged.HelpedRate()
	rateStr := "—"
	if measured {
		rateStr = fmt.Sprintf("%.0f%%", helpedRate*100)
	}
	adoptStr := "—"
	if rep.Tagged.Shown > 0 {
		adoptStr = fmt.Sprintf("%.0f%%", float64(rep.Tagged.Adopted)/float64(rep.Tagged.Shown)*100)
	}
	// The same tagged-event slice rep.Tagged aggregates, over time, for the tile
	// sparklines. (Active cohorts is a distinct-count, not an additive trend — no
	// spark.)
	taggedBkts := insights.ActivityBuckets(events, now, w, func(e models.FeedbackEvent) bool { return len(e.Segment) > 0 })
	b.WriteString(string(TileRow(false,
		Tile{Label: "Active cohorts", Value: fmtCount(rep.Distinct), Good: true,
			Delta: nIf(len(rep.Dimensions) > 0, fmt.Sprintf("%d dimension%s", len(rep.Dimensions), plural(len(rep.Dimensions))))},
		Tile{Label: "Suggestions shown", Value: fmtCount(rep.Tagged.Shown), Good: true, Spark: tileSpark(bucketCounts(taggedBkts, fShown))},
		Tile{Label: "Adoption rate", Value: adoptStr, Good: true,
			Delta: nIf(rep.Tagged.Shown > 0, fmt.Sprintf("n=%d", rep.Tagged.Shown)), Spark: tileSpark(cumulativeRate(taggedBkts, fAdopted, fShown))},
		Tile{Label: "Helped rate", Value: rateStr, Good: true,
			Delta: nIf(measured, fmt.Sprintf("n=%d", rep.Tagged.Adopted)), Spark: tileSpark(cumulativeRate(taggedBkts, fHelped, fAdopted))},
	)))

	if len(rep.Dimensions) == 0 {
		b.WriteString(`<p class="empty">No cohort-tagged activity in this window. Try a longer window—or, if no member set a cohort yet, members add one with <code>tacit cohorts</code> and then <code>tacit connect --segment team=…,role=…</code>.</p>`)
		return page{active: "outcomes", crumbs: cohortCrumbs, content: b.String(), viewMenu: cohortViews}
	}

	// Coverage: how much of the window's activity carried a cohort tag at all.
	if rep.Overall.Shown > rep.Tagged.Shown {
		fmt.Fprintf(&b, `<p class="hint">Cohort tags cover <b>%d of %d</b> suggestions shown this window (%.0f%%). The breakdown below includes tagged suggestions.</p>`,
			rep.Tagged.Shown, rep.Overall.Shown, float64(rep.Tagged.Shown)/float64(rep.Overall.Shown)*100)
	}
	b.WriteString(ui.Fine(`A rate in <b>bold</b> is above the tagged baseline and a muted one is below it.`))
	for _, dim := range rep.Dimensions {
		fmt.Fprintf(&b, `<section class="panel"><h2>By %s</h2>`+ui.Sub("", "%d cohort%s, most shown first"),
			html.EscapeString(dim.Name), len(dim.Cohorts), plural(len(dim.Cohorts)))
		b.WriteString(cohortTable(dim, rep.Tagged, w.Key))
		b.WriteString(`</section>`)
	}
	return page{active: "outcomes", crumbs: cohortCrumbs, content: b.String(), viewMenu: cohortViews}
}

// cohortTable renders one dimension's cohorts as a funnel table, with a
// dimension-total footer. Adoption/helped rates are emboldened when they beat
// the org baseline so differences between cohorts read at a glance.
func cohortTable(dim insights.CohortDimension, base insights.Funnel, windowKey string) string {
	baseAdopt, baseAdoptOK := rate(base.Adopted, base.Shown)
	baseHelped, baseHelpedOK := rate(base.Helped, base.Adopted)
	cells := func(label string, f insights.Funnel) []string {
		return []string{
			html.EscapeString(label), fmtCount(f.Shown), fmtCount(f.Adopted),
			rateCell(f.Adopted, f.Shown, baseAdopt, baseAdoptOK),
			fmtCount(f.Helped), rateCell(f.Helped, f.Adopted, baseHelped, baseHelpedOK),
		}
	}
	rows := make([]tableRow, 0, len(dim.Cohorts)+1)
	for _, c := range dim.Cohorts {
		rows = append(rows, tableRow{
			Cells: cells(c.Val, c.Funnel),
			Href:  "/outcomes/cohorts/" + url.PathEscape(dim.Name+":"+c.Val) + "?w=" + url.QueryEscape(windowKey),
		})
	}
	rows = append(rows, tableRow{Cells: cells("all "+dim.Name, dim.Funnel), Total: true})
	return dataTable(numCols(html.EscapeString(dim.Name), "shown", "adopted", "adopt&#8202;%", "helped", "helped&#8202;%"), rows)
}

// rate returns num/den as a fraction (ok=false when den is 0).
func rate(num, den int) (float64, bool) {
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

// rateCell formats a percentage, em-dashed when undefined, and emboldened when
// it beats the baseline (so a cohort that over-performs the org average stands
// out from one that trails it).
func rateCell(num, den int, base float64, baseOK bool) string {
	r, ok := rate(num, den)
	if !ok {
		return `<span class="muted">—</span>`
	}
	s := fmt.Sprintf("%.0f%%", r*100)
	if baseOK && r > base {
		return `<b>` + s + `</b>`
	}
	return `<span class="muted">` + s + `</span>`
}

// insightsInputs loads the shared inputs for the drill-down pages: all events,
// all techniques, and the window resolved from ?w=.
func (s *Server) insightsInputs(r *http.Request) (techniques []models.Technique, events []models.FeedbackEvent, w insights.Window, now time.Time, err error) {
	in, err := s.readAnalyticsInputs(false)
	if err != nil {
		return
	}
	events, techniques = in.events, in.techniques
	now = time.Now().UTC()
	w = insights.WindowByKey(r.URL.Query().Get("w"), now, insights.Earliest(events))
	return
}

func insightsStoreErr(err error) page {
	return storeUnavailablePage("outcomes", nil, err)
}

// capRow is one technique line in a breakdown table.
type capRow struct {
	id, name, scope string
	nums            []string // pre-formatted numeric cells (may contain HTML), aligned to the header
}

// capTable renders the shared technique-breakdown table used by every drill-down
// that lists techniques with numbers: a name column whose rows link to that
// technique's insights (window preserved), plus right-aligned numeric columns.
// One component so dismissals, retrieval misses, by-source and cohort detail all
// read and behave alike (click a row for the full picture).
func capTable(numHeads []string, rows []capRow, windowKey string) string {
	out := make([]tableRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, tableRow{
			Cells: append([]string{techniqueCell(r.scope, r.name)}, r.nums...),
			Href:  techniqueHref(r.id, windowKey),
		})
	}
	return dataTable(numCols("technique", numHeads...), out)
}

// funnelCols formats a technique's shown/adopted/adopt%/helped/helped% cells —
// the common numeric columns for the breakdown tables (baseOK false, so no
// beats-baseline emphasis on these single-population lists).
func funnelCols(f insights.Funnel) []string {
	return []string{
		fmtCount(f.Shown), fmtCount(f.Adopted), rateCell(f.Adopted, f.Shown, 0, false),
		fmtCount(f.Helped), rateCell(f.Helped, f.Adopted, 0, false),
	}
}

var funnelHeads = []string{"shown", "adopted", "adopt %", "helped", "helped %"}

// Funnel-field accessors for building tile sparklines from a bucket series.
var (
	fShown     = func(f insights.Funnel) int { return f.Shown }
	fAdopted   = func(f insights.Funnel) int { return f.Adopted }
	fHelped    = func(f insights.Funnel) int { return f.Helped }
	fDismissed = func(f insights.Funnel) int { return f.Dismissed }
)

// tileSpark renders a KPI-tile sparkline, or "" when there are too few points to
// draw a trend (Sparkline needs at least two).
func tileSpark(series []int) template.HTML {
	if len(series) < 2 {
		return ""
	}
	return Sparkline(series)
}

// bucketCounts pulls one funnel field per bucket into a plain series.
func bucketCounts(bkts []insights.Bucket, pick func(insights.Funnel) int) []int {
	out := make([]int, len(bkts))
	for i, bk := range bkts {
		out[i] = pick(bk.Funnel)
	}
	return out
}

// cumulativeRate is the CUMULATIVE num/den percent across buckets — the smooth
// trend a sparse rate needs (a quiet bucket carries the running rate forward
// instead of crashing to 0%). Matches the overview's helped/decline sparks.
func cumulativeRate(bkts []insights.Bucket, num, den func(insights.Funnel) int) []int {
	out := make([]int, len(bkts))
	cn, cd := 0, 0
	for i, bk := range bkts {
		cn += num(bk.Funnel)
		cd += den(bk.Funnel)
		switch {
		case cd > 0:
			out[i] = (cn*100 + cd/2) / cd
		case i > 0:
			out[i] = out[i-1]
		}
	}
	return out
}

// funnelTiles renders the standard shown/adoptions/adoption-rate/helped-rate KPI
// row for a funnel — the shared header for cohort, tag and other roll-up drills.
// bkts is the same activity series rendered in the panel below, reused here for
// per-tile sparklines (pass nil to omit them).
func funnelTiles(f insights.Funnel, bkts []insights.Bucket) string {
	adoptStr := "—"
	if f.Shown > 0 {
		adoptStr = fmt.Sprintf("%.0f%%", float64(f.Adopted)/float64(f.Shown)*100)
	}
	helpedStr, measured := "—", false
	if hr, ok := f.HelpedRate(); ok {
		helpedStr, measured = fmt.Sprintf("%.0f%%", hr*100), true
	}
	var b strings.Builder
	b.WriteString(string(TileRow(false,
		Tile{Label: "Suggestions shown", Value: fmtCount(f.Shown), Good: true, Spark: tileSpark(bucketCounts(bkts, fShown))},
		Tile{Label: "Adoptions", Value: fmtCount(f.Adopted), Good: true, Spark: tileSpark(bucketCounts(bkts, fAdopted))},
		Tile{Label: "Adoption rate", Value: adoptStr, Good: true,
			Delta: nIf(f.Shown > 0, fmt.Sprintf("n=%d", f.Shown)), Spark: tileSpark(cumulativeRate(bkts, fAdopted, fShown))},
		Tile{Label: "Helped rate", Value: helpedStr, Good: true,
			Delta: nIf(measured, fmt.Sprintf("n=%d", f.Adopted)), Spark: tileSpark(cumulativeRate(bkts, fHelped, fAdopted))},
	)))
	return b.String()
}

// drill is one technique-list drill-down: the parts that differ between the
// dismissals, source, tag, task-type and cohort views. Everything else about
// them is the same page, built by drillPage.
type drill struct {
	crumbs  []crumb
	base    string            // page path the window selector navigates within
	sub     string            // page-head sub-line, already escaped
	empty   string            // the whole empty-state line, in the page's own words
	funnel  *insights.Funnel  // KPI tiles; nil for a view with no funnel to show
	buckets []insights.Bucket // Activity panel and tile sparklines; nil for neither
	heads   []string          // numeric column heads for the technique table
	rows    []capRow
	panel   string // panel heading to wrap the table in; empty renders it bare

	w      insights.Window
	now    time.Time
	events []models.FeedbackEvent
}

// drillPage renders a technique-list drill-down. Every one of them is this page
// with different words in it, so the order is fixed here: page head, time
// period, KPI tiles, Activity panel, then the technique table — or the view's
// own empty line when there is nothing to list.
func drillPage(d drill) page {
	return page{active: "outcomes", crumbs: d.crumbs, content: insightDrillBody(d)}
}

// insightDrillBody is that page's body; drillPage wraps it in the shared chrome.
func insightDrillBody(d drill) string {
	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">` + d.sub + `</p></div>`)
	b.WriteString(windowSelect(d.base, d.w.Key, d.now, insights.Earliest(d.events)))
	if d.funnel != nil {
		b.WriteString(funnelTiles(*d.funnel, d.buckets))
	}
	if d.buckets != nil {
		b.WriteString(activityChartPanel(d.buckets, d.w.Bucket))
	}
	if len(d.rows) == 0 {
		b.WriteString(d.empty)
		return b.String()
	}
	if d.panel != "" {
		b.WriteString(`<section class="panel"><h2>` + d.panel + `</h2>`)
		b.WriteString(capTable(d.heads, d.rows, d.w.Key))
		b.WriteString(`</section>`)
		return b.String()
	}

	b.WriteString(capTable(d.heads, d.rows, d.w.Key))
	return b.String()
}

// capRows turns a technique list into the shared table's rows, funnel columns and
// all — the body of every drill-down table but the dismissal one.
func capRows(list []insights.TechniqueStat) []capRow {
	rows := make([]capRow, 0, len(list))
	for _, c := range list {
		rows = append(rows, capRow{c.Technique.ID, c.Technique.Name, c.Technique.Scope, funnelCols(c.Funnel)})
	}
	return rows
}

// rollFunnel sums a technique list's windowed funnels into the roll-up the KPI
// tiles report — shown, adopted and helped, the three stages those tiles read.
func rollFunnel(list []insights.TechniqueStat) insights.Funnel {
	var roll insights.Funnel
	for _, c := range list {
		roll.Shown += c.Funnel.Shown
		roll.Adopted += c.Funnel.Adopted
		roll.Helped += c.Funnel.Helped
	}
	return roll
}

// pageInsightDismissals lists the techniques members dismissed for one reason —
// reached from the overview's dismissal-reason breakdown. No tiles and no chart:
// a reason list is a count, not a funnel.
// outcomesLeafCrumbs is the trail above a per-item page: the section, the VIEW
// whose list the item was on — carrying that view's switcher, so the reader can
// move sideways without going home first — and the item itself.
//
// The view gets no caret of its own here: its children are one page per tag,
// per cohort, per reason, which is a list's worth of things and not a menu's
// (the rule in chrome.go). The list is on the page one step up.
func outcomesLeafCrumbs(view, windowKey string, rest ...crumb) []crumb {
	views := outcomesViews(view, windowKey)
	trail := []crumb{
		{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(windowKey)},
		{label: viewLabel(views), menu: views},
	}
	return append(trail, rest...)
}

func (s *Server) pageInsightDismissals(r *http.Request, user oidc.Claims) page {
	reason, _ := url.PathUnescape(r.PathValue("reason"))
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	names := map[string]models.Technique{}
	for _, c := range techniques {
		names[c.ID] = c
	}
	var rows []capRow
	for _, e := range insights.DismissalsFor(events, now, w, reason) {
		technique := names[e.Label]
		name := technique.Name
		if name == "" {
			name = e.Label // technique since deleted — show its id
		}
		rows = append(rows, capRow{e.Label, name, technique.Scope, []string{fmtCount(e.Count)}})
	}
	return drillPage(drill{
		w: w, now: now, events: events,
		crumbs: outcomesLeafCrumbs("signal-trust", w.Key, crumb{label: "Dismissed: " + reason}),
		base:   "/outcomes/dismissals/" + url.PathEscape(reason),
		sub: fmt.Sprintf(`Techniques members dismissed as <b>%s</b> this window. %s`,
			html.EscapeString(reason), dismissalReasonNote(reason)),
		empty: `<p class="empty">No dismissals for this reason in this window. Try a longer window.</p>`,
		heads: []string{"dismissals"}, rows: rows,
	})
}

// dismissalReasonNote explains what a dismissal reason implies for action.
func dismissalReasonNote(reason string) string {
	switch reason {
	case "not-relevant":
		return "Not-relevant feedback indicates a retrieval mismatch."
	case "didnt-work", "didn’t-work":
		return "Didn’t-work feedback contributes to technique decay."
	case "already-knew", "already-know":
		return "Already-knew means the technique repeats what the model already does."
	default:
		return ""
	}
}

// pageInsightSource lists the techniques of one provenance with their funnels —
// reached from the overview's by-source distribution bars.
func (s *Server) pageInsightSource(r *http.Request, user oidc.Claims) page {
	prov, _ := url.PathUnescape(r.PathValue("provenance"))
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	list := insights.SourceTechniques(techniques, events, now, w, prov)
	d := drill{
		w: w, now: now, events: events,
		crumbs: outcomesLeafCrumbs("overview", w.Key, crumb{label: "Source: " + prov}),
		base:   "/outcomes/source/" + url.PathEscape(prov),
		sub: fmt.Sprintf(`Reviewed techniques from <b>%s</b> and their outcomes in this window.`,
			html.EscapeString(prov)),
		empty: fmt.Sprintf(`<p class="empty">No reviewed techniques from this source. <a href="/outcomes?w=%s">← overview</a></p>`,
			url.QueryEscape(w.Key)),
		heads: funnelHeads, rows: capRows(list),
	}
	if len(list) > 0 {
		d.buckets = insights.ActivityBuckets(events, now, w, capSetMatch(list))
	}
	return drillPage(d)
}

// pageInsightCohortDetail is one cohort's (dimension:value) standing — reached
// by clicking a row of the cohorts table or a cohort-spread bar.
func (s *Server) pageInsightCohortDetail(r *http.Request, user oidc.Claims) page {
	key, _ := url.PathUnescape(r.PathValue("key"))
	dim, val, ok := strings.Cut(key, ":")
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	cohortsHref := "/outcomes/cohorts?w=" + url.QueryEscape(w.Key)
	if !ok || dim == "" || val == "" {
		return page{status: 404, active: "outcomes",
			crumbs:  outcomesLeafCrumbs("cohorts", w.Key, crumb{label: "?"}),
			content: `<p>Invalid cohort. Use <code>dimension:value</code>. <a href="` + cohortsHref + `">All cohorts.</a></p>`}
	}
	det := insights.ComputeCohortDetail(techniques, events, now, w, dim, val)
	return drillPage(drill{
		w: w, now: now, events: events,
		crumbs: outcomesLeafCrumbs("cohorts", w.Key, crumb{label: key}),
		base:   "/outcomes/cohorts/" + url.PathEscape(key),
		sub: fmt.Sprintf(`Funnel and technique activity for the <b>%s</b> cohort in this window.`,
			html.EscapeString(key)),
		empty:  `<p class="empty">No technique activity for this cohort in this window.</p>`,
		funnel: &det.Funnel,
		buckets: insights.ActivityBuckets(events, now, w,
			func(e models.FeedbackEvent) bool { return e.Segment[dim] == val }),
		heads: funnelHeads, rows: capRows(det.Techniques),
		panel: "Techniques this cohort engaged",
	})
}

// pageInsightTag is a tag's performance: a roll-up of every reviewed technique
// carrying it, reached from the tag-filter note on the techniques list.
func (s *Server) pageInsightTag(r *http.Request, user oidc.Claims) page {
	tag, _ := url.PathUnescape(r.PathValue("tag"))
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	list := insights.TagTechniques(techniques, events, now, w, tag)
	d := drill{
		w: w, now: now, events: events,
		crumbs: outcomesLeafCrumbs("overview", w.Key, crumb{label: "Tag: " + tag}),
		base:   "/outcomes/tag/" + url.PathEscape(tag),
		sub: fmt.Sprintf(`Outcomes for techniques tagged <b>%s</b> in this window.`,
			html.EscapeString(tag)),
		empty: fmt.Sprintf(`<p class="empty">No reviewed techniques tagged “%s”. <a href="/techniques?tag=%s">browse the list</a>.</p>`,
			html.EscapeString(tag), url.QueryEscape(tag)),
		heads: funnelHeads, rows: capRows(list),
	}
	if len(list) > 0 {
		roll := rollFunnel(list)
		d.funnel = &roll
		d.buckets = insights.ActivityBuckets(events, now, w, capSetMatch(list))
	}
	return drillPage(d)
}

// pageInsightTaskType is a task type's performance — the drill-down behind
// the Organization view's task-type areas, shaped exactly like the tag view.
func (s *Server) pageInsightTaskType(r *http.Request, user oidc.Claims) page {
	taskType, _ := url.PathUnescape(r.PathValue("type"))
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	list := insights.TaskTypeTechniques(techniques, events, now, w, taskType)
	d := drill{
		w: w, now: now, events: events,
		crumbs: outcomesLeafCrumbs("overview", w.Key, crumb{label: "Task type: " + taskType}),
		base:   "/outcomes/task-type/" + url.PathEscape(taskType),
		sub: fmt.Sprintf(`Outcomes for techniques used in <b>%s</b> tasks in this window.`,
			html.EscapeString(taskType)),
		empty: fmt.Sprintf(`<p class="empty">No reviewed techniques declare the task type “%s”. <a href="/techniques">browse the list</a>.</p>`,
			html.EscapeString(taskType)),
		heads: funnelHeads, rows: capRows(list),
	}
	if len(list) > 0 {
		roll := rollFunnel(list)
		d.funnel = &roll
		d.buckets = insights.ActivityBuckets(events, now, w, capSetMatch(list))
	}
	return drillPage(d)
}

// pageInsightHelpedRate ranks the measured techniques by how often adopting one went
// on to help — reached from the overview's "Helped rate" tile and the Outcomes
// view menu. (The decay queue it used to be lives on Review now; the health
// strip points there.)
func (s *Server) pageInsightHelpedRate(r *http.Request, user oidc.Claims) page {
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	list := insights.MeasuredTechniques(techniques, events, now, w)
	crumbs := []crumb{{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(w.Key)}, {label: "Helped rate", href: ""}}
	views := outcomesViews("helped-rate", w.Key)

	var b strings.Builder
	b.WriteString(`<div class="page-head"></div>`)
	b.WriteString(windowSelect("/outcomes/helped-rate", w.Key, now, insights.Earliest(events)))
	if len(list) == 0 {
		b.WriteString(`<p class="empty">No measured techniques in this window. Try a longer window.</p>`)
		return page{active: "outcomes", crumbs: crumbs, content: b.String(), viewMenu: views}
	}
	// Distribution across five helped-rate bands.
	labels := []string{"0–20%", "20–40%", "40–60%", "60–80%", "80–100%"}
	buckets := make([]int, 5)
	for _, c := range list {
		hr, _ := c.Funnel.HelpedRate()
		i := int(hr * 5)
		if i > 4 {
			i = 4
		}
		buckets[i]++
	}
	maxB := 1
	for _, n := range buckets {
		if n > maxB {
			maxB = n
		}
	}
	hrows := make([]BarRow, 0, 5)
	for i, lbl := range labels {
		hrows = append(hrows, BarRow{Label: lbl, Value: buckets[i], Max: maxB, Key: "s4",
			Sub: fmt.Sprintf("technique%s", plural(buckets[i]))})
	}
	b.WriteString(`<div class="grid two"><section class="panel fill-bars"><h2>Distribution</h2>` +
		ui.Sub("", "measured techniques by helped rate"))
	b.WriteString(`<div class="bars-tight-label">`)
	b.WriteString(string(HBars(hrows, "nothing measured")))
	b.WriteString(`</div>`)
	b.WriteString(`</section><section class="panel"><h2>Highest helped rate</h2>`)
	// The source list is worst-rate-first. Sort explicitly rather than reversing
	// it: reversing also inverts its larger-sample-first tie order, making this
	// leaderboard disagree with the table when helped % is sorted descending.
	top := append([]insights.TechniqueStat(nil), list...)
	sort.SliceStable(top, func(i, j int) bool {
		ri, _ := top[i].Funnel.HelpedRate()
		rj, _ := top[j].Funnel.HelpedRate()
		if ri != rj {
			return ri > rj
		}
		if top[i].Funnel.Adopted != top[j].Funnel.Adopted {
			return top[i].Funnel.Adopted > top[j].Funnel.Adopted
		}
		if top[i].Technique.Name != top[j].Technique.Name {
			return top[i].Technique.Name < top[j].Technique.Name
		}
		return top[i].Technique.ID < top[j].Technique.ID
	})
	if len(top) > 8 {
		top = top[:8]
	}
	lrows := make([]BarRow, 0, len(top))
	for _, c := range top {
		hr, _ := c.Funnel.HelpedRate()
		lrows = append(lrows, BarRow{Label: c.Technique.Name, Value: int(hr * 100), Max: 100, Key: "s4",
			Sub: fmt.Sprintf("n=%d", c.Funnel.Adopted), Href: drillHref(c.Technique.ID, w.Key)})
	}
	// Technique titles are the point of this panel; a fixed-width bar frees the rest
	// of the row for the (often long) name instead of ellipsizing it early.
	b.WriteString(`<div class="bars-wide-label">`)
	b.WriteString(string(HBars(lrows, "nothing measured")))
	b.WriteString(`</div></section></div>`)
	b.WriteString(`<section class="panel"><h2>All measured techniques</h2>` +
		ui.Sub("", "lowest helped rate first"))
	b.WriteString(capTable(funnelHeads, capRows(list), w.Key))
	b.WriteString(`</section>`)
	return page{active: "outcomes", crumbs: crumbs, content: b.String(), viewMenu: views}
}

// pageInsightSignalTrust compares inferred vs explicit feedback — the E1
// calibration view reached from the overview's Signal-trust bar.
func (s *Server) pageInsightSignalTrust(r *http.Request, user oidc.Claims) page {
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return insightsStoreErr(err)
	}
	st := insights.ComputeSignalTrust(techniques, events, now, w)
	crumbs := []crumb{{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(w.Key)}, {label: "Signal trust", href: ""}}
	views := outcomesViews("signal-trust", w.Key)

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Compares feedback inferred from behavior and casual reactions with explicit feedback. Inferred feedback has a low weight, and rankings depend on explicit feedback. “Positive” means helped among graded reactions.</p></div>`)
	b.WriteString(windowSelect("/outcomes/signal-trust", w.Key, now, insights.Earliest(events)))

	er, eok := st.Explicit.PositiveRate()
	ir, iok := st.Inferred.PositiveRate()
	pctOr := func(v float64, ok bool) string {
		if !ok {
			return "—"
		}
		return fmt.Sprintf("%.0f%%", v*100)
	}
	gap := "—"
	if eok && iok {
		d := er - ir
		if d < 0 {
			d = -d
		}
		gap = fmt.Sprintf("%.0f pp", d*100)
	}
	tiles := []Tile{
		{Label: "Explicit positive", Value: pctOr(er, eok), Delta: nIf(eok, fmt.Sprintf("n=%d", st.Explicit.Reactions())), Good: true},
		{Label: "Inferred positive", Value: pctOr(ir, iok), Delta: nIf(iok, fmt.Sprintf("n=%d", st.Inferred.Reactions())), Good: true},
		{Label: "Calibration gap", Value: gap, Delta: "inferred compared with explicit", Good: true},
	}
	// The verification class (autonomous sessions) appears only once it has
	// data — a deployment with no agent runs shouldn't carry the chrome.
	withVerification := st.Verification.Reactions() > 0
	if withVerification {
		vr, vok := st.Verification.PositiveRate()
		tiles = append(tiles, Tile{Label: "Verification positive", Value: pctOr(vr, vok),
			Delta: nIf(vok, fmt.Sprintf("n=%d", st.Verification.Reactions())), Good: true})
	}
	b.WriteString(string(TileRow(false, tiles...)))

	if len(st.Techniques) == 0 {
		b.WriteString(`<p class="empty">No graded reactions in this window. Try a longer window.</p>`)
		return page{active: "outcomes", crumbs: crumbs, content: b.String(), viewMenu: views}
	}
	heads := []string{"technique", "explicit +/n", "explicit&#8202;%", "inferred +/n", "inferred&#8202;%"}
	if withVerification {
		heads = append(heads, "verified +/n", "verified&#8202;%")
	}
	rows := make([]tableRow, 0, len(st.Techniques))
	for _, c := range st.Techniques {
		cells := []string{
			techniqueCell(c.Technique.Scope, c.Technique.Name),
			fmt.Sprintf("%d/%d", c.Explicit.Helped, c.Explicit.Reactions()),
			rateCell(c.Explicit.Helped, c.Explicit.Reactions(), 0, false),
			fmt.Sprintf("%d/%d", c.Inferred.Helped, c.Inferred.Reactions()),
			rateCell(c.Inferred.Helped, c.Inferred.Reactions(), 0, false),
		}
		if withVerification {
			cells = append(cells,
				fmt.Sprintf("%d/%d", c.Verification.Helped, c.Verification.Reactions()),
				rateCell(c.Verification.Helped, c.Verification.Reactions(), 0, false))
		}
		rows = append(rows, tableRow{Cells: cells, Href: techniqueHref(c.Technique.ID, w.Key)})
	}
	trustTable := dataTable(numCols(heads[0], heads[1:]...), rows)
	b.WriteString(`<section class="panel"><h2>By technique</h2>` +
		ui.Sub("", "helped, of graded reactions"))
	b.WriteString(trustTable)
	b.WriteString(`</section>`)
	return page{active: "outcomes", crumbs: crumbs, content: b.String(), viewMenu: views}
}

func nIf(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

func trustMix(o insights.Overview) []insights.MixEntry {
	var mix []insights.MixEntry
	if o.TrustExplicit > 0 {
		mix = append(mix, insights.MixEntry{Label: "explicit", Count: o.TrustExplicit})
	}
	if o.TrustInferred > 0 {
		mix = append(mix, insights.MixEntry{Label: "inferred", Count: o.TrustInferred})
	}
	return mix
}

func healthStrip(o insights.Overview) string {
	if len(o.Decayed) == 0 {
		return ""
	}
	return fmt.Sprintf(`<div class="health"><a class="status serious" href="/review?w=%s">▲ %d technique%s decayed · review →</a></div>`,
		url.QueryEscape(o.Window.Key), len(o.Decayed), plural(len(o.Decayed)))
}

func freshList(fresh []insights.TechniqueStat, now time.Time, window string) string {
	if len(fresh) == 0 {
		return `<p class="empty">nothing new in this window</p>`
	}
	var b strings.Builder
	b.WriteString(`<ul class="fresh">`)
	for _, c := range fresh {
		badge := "newly active"
		if c.New {
			badge = "new technique"
		}
		fmt.Fprintf(&b, `<li><a href="%s">%s</a><span class="badge">%s</span><span class="bar-sub">%s</span></li>`,
			drillHref(c.Technique.ID, window), html.EscapeString(c.Technique.Name), badge,
			html.EscapeString(age(c, now)))
	}
	b.WriteString(`</ul>`)
	return b.String()
}

func age(c insights.TechniqueStat, now time.Time) string {
	t := c.FirstEvent
	if c.New {
		if ct, err := time.Parse(time.RFC3339Nano, c.Technique.CreatedAt); err == nil {
			t = ct
		}
	}
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func (s *Server) pageInsightDetail(r *http.Request, user oidc.Claims) page {
	return s.pageTechniqueDetail(r, user)
}

// techniqueMetrics renders the outcome story as part of the technique rather than as a
// competing detail page. The definition above answers "what is this?"; this
// section answers "is it working?" without making the reader change views.
func (s *Server) techniqueMetrics(r *http.Request, technique models.Technique, events []models.FeedbackEvent) string {
	now := time.Now().UTC()
	w := insights.WindowByKey(r.URL.Query().Get("w"), now, insights.Earliest(events))
	d := insights.ComputeTechnique(technique, events, now, w)

	var b strings.Builder
	b.WriteString(`<section class="technique-performance"><div class="technique-performance-head"><div><p class="eyebrow">Evidence</p><h2>Performance</h2></div></div>`)

	f, pf := d.Stat.Funnel, d.PrevFunnel
	rateStr := "not yet measured"
	if rate, measured := f.HelpedRate(); measured {
		rateStr = fmt.Sprintf("%.0f%%", rate*100)
	}
	sd, sg := delta(f.Shown, pf.Shown)
	ad, ag := delta(f.Adopted, pf.Adopted)
	hd, hg := delta(f.Helped, pf.Helped)
	b.WriteString(string(TileRow(false,
		Tile{Label: "Shown", Value: fmtCount(f.Shown), Delta: sd, Good: sg, Spark: tileSpark(bucketCounts(d.Buckets, fShown))},
		Tile{Label: "Adopted", Value: fmtCount(f.Adopted), Delta: ad, Good: ag, Spark: tileSpark(bucketCounts(d.Buckets, fAdopted))},
		Tile{Label: "Helped", Value: fmtCount(f.Helped), Delta: hd, Good: hg, Spark: tileSpark(bucketCounts(d.Buckets, fHelped))},
		Tile{Label: "Helped rate", Value: rateStr, Good: true,
			Delta: nIf(f.Adopted > 0, fmt.Sprintf("n=%d", f.Adopted)), Spark: tileSpark(cumulativeRate(d.Buckets, fHelped, fAdopted))},
		Tile{Label: "Dismissed", Value: fmtCount(f.Dismissed), Good: true, Spark: tileSpark(bucketCounts(d.Buckets, fDismissed))},
	)))
	b.WriteString(s.autonomyNote(technique, events))

	cum := make([]int, len(d.Cumulative))
	for i, bkt := range d.Cumulative {
		cum[i] = bkt.Funnel.Adopted
	}
	// The two time charts pair side by side — both want height and both now fill
	// it. (The activity panel replaced the old line chart here and the decay-watch
	// chart; decay lives on in the status line, health strip, and Insights → Decay
	// watch.)
	b.WriteString(`<div class="grid two">`)
	b.WriteString(`<section class="panel chart-panel"><h2>Cumulative adoption</h2>` +
		ui.Sub("", "since first contact"))
	b.WriteString(string(LineChart(d.Cumulative, w.Bucket,
		[]VizSeries{{Name: "adopted (total)", Key: "s2", Values: cum}}, 230)))
	b.WriteString(`</section>`)
	b.WriteString(activityChartPanel(d.Buckets, w.Bucket))
	b.WriteString(`</div>`)

	// Cohort spread is a leaderboard that grows with the cohort count, so it runs
	// full width on its own row rather than stranding a short neighbour beside its
	// height. Tight labels keep the short cohort names hugging their bars instead
	// of a fractional column opening a gap across the wide panel.
	maxCohort := 1
	for _, c := range d.Cohorts {
		if c.Count > maxCohort {
			maxCohort = c.Count
		}
	}
	cohortRows := make([]BarRow, 0, len(d.Cohorts))
	for _, c := range d.Cohorts {
		cohortRows = append(cohortRows, BarRow{Label: c.Label, Value: c.Count, Max: maxCohort, Key: "s1",
			Href: "/outcomes/cohorts/" + url.PathEscape(c.Label) + "?w=" + url.QueryEscape(w.Key)})
	}
	b.WriteString(`<section class="panel" id="cohort-spread">`)
	b.WriteString(cohortSpreadHead(d.Cohorts))
	b.WriteString(ui.Sub("", "adoptions by cohort"))
	b.WriteString(`<div class="bars-tight-label">`)
	b.WriteString(string(HBars(cohortRows, "no cohort-tagged adoptions in this window")))
	b.WriteString(`</div>`)
	b.WriteString(cohortSpreadScript(d.Cohorts))
	b.WriteString(`</section>`)

	// Dismissal reasons is a single stacked bar — short by nature. Full width it
	// reads as the wide, thin strip it is, with no tall neighbour to strand it.
	// Segments link out to the reason, the way the overview's identical bar does
	// (outcomesComposition). Same markup, same segments, and the per-reason page
	// exists — the only difference here was that these ones didn't move.
	b.WriteString(`<section class="panel"><h2>Dismissal reasons</h2>`)
	b.WriteString(string(LinkedMixBar(d.Dismissals, mixKeys, "no dismissals in this window",
		func(reason string) string {
			return "/outcomes/dismissals/" + url.PathEscape(reason) + "?w=" + url.QueryEscape(w.Key)
		})))
	b.WriteString(`</section>`)
	b.WriteString(`</section>`)
	return b.String()
}

// autonomyPanel is the governance surface for evidence-gated autonomy
// (agent-delivery-plan Phase D): which techniques agents may apply silently, and
// how silent applications perform against visible suggestions. Renders
// nothing while the gate is off — the panel is chrome only an operator who
// enabled autonomy should carry.
func (s *Server) autonomyPanel(techniques []models.Technique, events []models.FeedbackEvent,
	now time.Time, w insights.Window) string {
	if !s.cfg().AutonomyEnabled {
		return ""
	}
	a := insights.ComputeAutonomy(techniques, events, now, w, s.cfg().AutonomyMinHelpedRate, s.cfg().AutonomyMinN)
	var b strings.Builder
	b.WriteString(`<section class="panel" id="autonomy"><h2>Autonomy</h2>`)
	fmt.Fprintf(&b, `<p class="hint">Stable techniques qualify for silent use in autonomous sessions at a helped rate of at least %.0f%% with n ≥ %d.</p>`,
		s.cfg().AutonomyMinHelpedRate*100, s.cfg().AutonomyMinN)
	if len(a.Techniques) == 0 {
		b.WriteString(`<p class="empty">No stable techniques have measured outcomes yet.</p></section>`)
		return b.String()
	}
	fmt.Fprintf(&b, `<p class="sub">%d of %d measured stable technique%s eligible · this window: %d silent application%s, %d visible suggestion%s</p>`,
		a.Eligible, len(a.Techniques), plural(len(a.Techniques)), a.Applied.Shown, plural(a.Applied.Shown),
		a.Suggested.Shown, plural(a.Suggested.Shown))
	// The comparison the whole graduation rule stands on: do applied techniques
	// help at the rate their suggested-era evidence promised?
	if a.Applied.Adopted > 0 && a.Suggested.Adopted > 0 {
		ar, _ := a.Applied.HelpedRate()
		sr, _ := a.Suggested.HelpedRate()
		fmt.Fprintf(&b, `<p class="sub">helped rate when applied: %.0f%% (n=%d) · when suggested: %.0f%% (n=%d)</p>`,
			ar*100, a.Applied.Adopted, sr*100, a.Suggested.Adopted)
	}
	rows := make([]tableRow, 0, len(a.Techniques))
	for i, c := range a.Techniques {
		if i >= 8 {
			break
		}
		status := "not yet"
		if c.Eligible {
			status = "eligible"
		}
		rows = append(rows, tableRow{
			Cells: []string{techniqueCell(c.Technique.Scope, c.Technique.Name), status,
				fmt.Sprintf("%.0f%%", c.HelpedRate*100), fmt.Sprintf("%d", c.N)},
			Href: techniqueHref(c.Technique.ID, w.Key),
		})
	}
	b.WriteString(dataTable(numCols("technique", "autonomy", "helped rate", "n"), rows))
	b.WriteString(`</section>`)
	return b.String()
}

// autonomyNote is a technique page's graduation status (agent-delivery-plan C4):
// the evidence corpus made legible as the thing that authorizes agent
// behavior. Empty while the gate is off.
func (s *Server) autonomyNote(technique models.Technique, events []models.FeedbackEvent) string {
	c := s.cfg()
	if !c.AutonomyEnabled {
		return ""
	}
	adopted, helped := 0, 0
	for _, e := range events {
		if e.TechniqueID != technique.ID {
			continue
		}
		switch e.Stage {
		case "adopted":
			adopted++
		case "helped":
			helped++
		}
	}
	minRate, minN := c.AutonomyMinHelpedRate, c.AutonomyMinN
	var msg string
	switch {
	case technique.Status != "stable":
		msg = "not eligible: only stable (promoted) techniques qualify for silent application"
	case adopted == 0:
		msg = "not eligible: no measured adoptions yet"
	default:
		rate := float64(helped) / float64(adopted)
		if rate >= minRate && adopted >= minN {
			msg = fmt.Sprintf("<strong>eligible</strong>: agents can apply this technique silently in autonomous sessions (helped %.0f%% · n=%d, required ≥%.0f%% · n≥%d)",
				rate*100, adopted, minRate*100, minN)
		} else {
			var needs []string
			if adopted < minN {
				needs = append(needs, fmt.Sprintf("%d more measured adoption%s", minN-adopted, plural(minN-adopted)))
			}
			if rate < minRate {
				needs = append(needs, fmt.Sprintf("a helped rate of %.0f%% (now %.0f%%)", minRate*100, rate*100))
			}
			msg = fmt.Sprintf("not yet: needs %s (n=%d)", strings.Join(needs, " and "), adopted)
		}
	}
	return `<section class="panel"><h2>Autonomy</h2><p class="sub">` + msg + `</p></section>`
}

// cohortDims lists the distinct dimensions — the part before ":" in labels
// like "surface:cli" — in first-appearance order. Labels without a dimension
// prefix produce no option; they show only under "all cohorts".
func cohortDims(cohorts []insights.MixEntry) []string {
	var dims []string
	seen := map[string]bool{}
	for _, c := range cohorts {
		if dim, _, ok := strings.Cut(c.Label, ":"); ok && dim != "" && !seen[dim] {
			seen[dim] = true
			dims = append(dims, dim)
		}
	}
	return dims
}

// cohortSpreadHead is the Cohort spread panel heading; with two or more
// dimensions present it gains a top-right dropdown that narrows the bars to
// one dimension at a time (a single dimension would make the filter a no-op).
func cohortSpreadHead(cohorts []insights.MixEntry) string {
	dims := cohortDims(cohorts)
	if len(dims) < 2 {
		return `<h2>Cohort spread</h2>`
	}
	var b strings.Builder
	b.WriteString(`<div class="panel-head"><h2>Cohort spread</h2>`)
	b.WriteString(`<select class="dim-select" id="cohort-dim" aria-label="Show one cohort dimension">`)
	b.WriteString(`<option value="">all cohorts</option>`)
	for _, dim := range dims {
		e := html.EscapeString(dim)
		fmt.Fprintf(&b, `<option value="%s">%s</option>`, e, e)
	}
	b.WriteString(`</select></div>`)
	return b.String()
}

// cohortSpreadScript wires the dropdown: bars whose label doesn't start with
// the chosen "<dimension>:" hide. Pure show/hide — bar widths keep the
// whole-panel scale, so values stay comparable across dimension views. The
// chosen dimension is also stripped from the surviving labels (redundant once
// the dropdown names it); "all cohorts" restores the full "<dimension>:value".
func cohortSpreadScript(cohorts []insights.MixEntry) string {
	if len(cohortDims(cohorts)) < 2 {
		return ""
	}
	return `<script>(function(){var s=document.getElementById('cohort-dim');if(!s)return;` +
		`document.querySelectorAll('#cohort-spread .bar-row .bar-label').forEach(function(l){l.dataset.full=l.textContent;});` +
		`s.addEventListener('change',function(){var d=this.value;` +
		`document.querySelectorAll('#cohort-spread .bar-row').forEach(function(r){` +
		`var l=r.querySelector('.bar-label');var full=l?l.dataset.full:'';` +
		`var match=!!d&&full.indexOf(d+':')===0;` +
		`r.style.display=(!d||match)?'':'none';` +
		`if(l)l.textContent=match?full.slice(d.length+1):full;});});})();</script>`
}
