// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/organization"
	"github.com/opentacit/tacit/internal/registry/techmap"
	"github.com/opentacit/tacit/internal/ui"
)

// organizationFilters holds a SET of accepted values per dimension — empty
// means "all". Within a dimension the values union (any match keeps the
// technique); across dimensions they intersect. Encoded as repeated query
// parameters (?tag=a&tag=b), so single-value links stay valid as the
// degenerate case.
type organizationFilters struct {
	Scope, Tag, Provenance, Technique []string
}

func parseOrganizationFilters(q url.Values) organizationFilters {
	clean := func(vals []string) []string {
		var out []string
		for _, v := range vals {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return organizationFilters{
		Scope:      clean(q["scope"]),
		Tag:        clean(q["tag"]),
		Provenance: clean(q["provenance"]),
		Technique:  clean(q["technique"]),
	}
}

func (f organizationFilters) empty() bool {
	return len(f.Scope)+len(f.Tag)+len(f.Provenance)+len(f.Technique) == 0
}

// without returns a copy with one value removed from one dimension — the
// target of a filter chip's × link.
func (f organizationFilters) without(dim, value string) organizationFilters {
	drop := func(vals []string) []string {
		var out []string
		for _, v := range vals {
			if v != value {
				out = append(out, v)
			}
		}
		return out
	}
	switch dim {
	case "scope":
		f.Scope = drop(f.Scope)
	case "tag":
		f.Tag = drop(f.Tag)
	case "provenance":
		f.Provenance = drop(f.Provenance)
	case "technique":
		f.Technique = drop(f.Technique)
	}
	return f
}

func containsFold(set []string, v string) bool {
	for _, s := range set {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func filterOrganizationInputs(techniques []models.Technique, events []models.FeedbackEvent, facts []models.AuditFact, f organizationFilters) ([]models.Technique, []models.FeedbackEvent, []models.AuditFact) {
	selected := map[string]bool{}
	var filteredTechniques []models.Technique
	for _, technique := range techniques {
		if len(f.Scope) > 0 && !containsFold(f.Scope, technique.Scope) {
			continue
		}
		if len(f.Provenance) > 0 && !containsFold(f.Provenance, technique.Provenance) {
			continue
		}
		if len(f.Technique) > 0 && !containsFold(f.Technique, technique.ID) {
			continue
		}
		if len(f.Tag) > 0 {
			matched := false
			for _, tag := range technique.Tags {
				if containsFold(f.Tag, tag) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		selected[technique.ID] = true
		filteredTechniques = append(filteredTechniques, technique)
	}
	var filteredEvents []models.FeedbackEvent
	for _, event := range events {
		if selected[event.TechniqueID] {
			filteredEvents = append(filteredEvents, event)
		}
	}
	if f.empty() {
		return filteredTechniques, filteredEvents, facts
	}
	// Audit facts pass through UNFILTERED: they feed only the health panel,
	// and registry health is a property of the whole registry. Filtering
	// facts by offered-technique once made the panel claim session grouping was
	// inactive whenever the filtered techniques happened not to be offered in
	// hashed sessions — a false operational alarm.
	return filteredTechniques, filteredEvents, facts
}

func organizationWindowSelect(active string, now, earliest time.Time, query url.Values) string {
	return windowSelectURLs(active, now, earliest, func(key string) string {
		q := cloneOrganizationQuery(query)
		q.Set("w", key)
		return "/outcomes?" + q.Encode()
	})
}

func cloneOrganizationQuery(query url.Values) url.Values {
	out := url.Values{}
	for _, key := range []string{"w", "dimension", "scope", "tag", "provenance", "technique"} {
		for _, value := range query[key] {
			if value != "" {
				out.Add(key, value)
			}
		}
	}
	return out
}

func organizationURL(window, dimension string, f organizationFilters) string {
	q := url.Values{"w": []string{window}}
	if dimension != "" {
		q.Set("dimension", dimension)
	}
	q["scope"], q["tag"], q["provenance"], q["technique"] =
		f.Scope, f.Tag, f.Provenance, f.Technique
	for k, v := range q {
		if len(v) == 0 {
			delete(q, k)
		}
	}
	return "/outcomes?" + q.Encode()
}

// organizationFilterBar is Outcomes' filter: the shared set-filter (filter.go)
// over all four dimensions, plus the cohort lens. The lens regroups EVERY panel
// below — the map, the areas, the gaps, the proponents — which is exactly why
// it lives here at page level and not inside any one panel's header.
// organizationFilterBar returns the filter bar and the active-selection chips
// separately so the caller can lay the bar into the control strip (beside the
// section rail) while the chips fall full-width beneath it.
func organizationFilterBar(techniques []models.Technique, active organizationFilters, window string, rep organization.Report) (bar, chips string) {
	dims := techniqueFilterDims(techniques, active, true)
	// The lens rides through the form so filtering does not silently regroup the
	// cohorts — but the CONTROL for it is not here. See cohortLensNav.
	hidden := map[string]string{"w": window, "dimension": rep.Lens}

	bar = filterBar("/outcomes", dims, hidden, "",
		organizationURL(window, rep.Lens, organizationFilters{}), !active.empty())
	chips = filterChips(dims, func(param, value string) string {
		return filterURL("/outcomes", hidden, active.without(param, value))
	})
	return bar, chips
}

// cohortLensNav is the "group by" control — team, role, harness, whatever
// segments the events carry.
//
// It used to sit in the page-level Filters bar, and on the old Organization page
// that was right: every panel there was a cohort panel, so the lens genuinely
// regrouped the whole page. After the merge it does not. It governs the cohort
// block and nothing else — not the funnel, not the leaderboards, not the
// composition — so a control at the top of the page changed nothing a reader
// could see without scrolling past four panels that ignore it. A control must
// sit with what it changes; placing it higher than its effect claims a scope it
// does not have.
//
// The filters above are different, and they stay where they are: they scope the
// WHOLE page, including the numbers above the fold.
func cohortLensNav(rep organization.Report, window string, filters organizationFilters) string {
	if len(rep.Dimensions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="band-head" id="cohorts"><span class="band-label">Cohorts</span>`)
	if n := rep.Overview.ActiveCohorts; n > 0 {
		fmt.Fprintf(&b, `<span class="band-count">%d active</span>`, n)
	}
	b.WriteString(`<span class="lens-label">Group by</span><nav class="lens-nav" aria-label="Group cohorts by">`)
	for _, dim := range rep.Dimensions {
		cls := ""
		if dim.Name == rep.Lens {
			cls = ` class="active" aria-current="page"`
		}
		fmt.Fprintf(&b, `<a%s href="%s#cohorts">%s <span>%d</span></a>`, cls,
			html.EscapeString(organizationURL(window, dim.Name, filters)), html.EscapeString(dim.Name), len(dim.Values))
	}
	b.WriteString(`</nav></div>`)
	return b.String()
}

// mapAreaOf groups techniques under the Playbook map's areas: the shared-tag
// clusters (techmap), carrying any model-given names from the describe cache
// so the heatmap's columns match the map's Areas panel word for word. Techniques
// outside every cluster pool into the map's Ungrouped bucket — a column is an
// area, never an individual technique. The "~" in the sentinel key sorts after
// every letter, pinning the bucket to the last column.
func (s *Server) mapAreaOf(techniques []models.Technique, events []models.FeedbackEvent) organization.AreaOf {
	live := make([]models.Technique, 0, len(techniques))
	for _, c := range techniques {
		if c.Status != "draft" && c.Status != "retired" {
			live = append(live, c)
		}
	}
	g := techmap.Build(live, events)
	s.applyClusterLabels(&g)
	type col struct{ key, label string }
	ungrouped := col{key: "map:~ungrouped", label: "Ungrouped"}
	byID := make(map[string]col, len(g.Nodes))
	for _, cl := range g.Clusters {
		c := col{key: "map:" + cl.Name, label: cl.Name}
		if cl.Ungrouped {
			c = ungrouped
		}
		for _, m := range cl.Members {
			if m >= 0 && m < len(g.Nodes) {
				byID[g.Nodes[m].ID] = c
			}
		}
	}
	return func(c models.Technique) (key, label, kind string) {
		a, ok := byID[c.ID]
		if !ok {
			a = ungrouped // no communities at all: still never a per-technique column
		}
		return a.key, a.label, "map-area"
	}
}

// organizationMap is the cohort × area grid. It is a COMPARISON: the colour of
// a cell only means something next to the cell above it. With a single cohort
// there is nothing to compare, and the grid degenerates into one row of mostly
// "0/0" — which reads to a new reader as a broken product rather than a young
// one. Below the floor it says what it needs instead of drawing itself.
// fallback is the plain adoptions-by-cohort ranking, rendered in the map's
// place when the map cannot honestly draw. The two are not duplicates: the
// ranking spans every segment dimension and works at any scale, while the map
// is a richer per-lens comparison that needs at least two cohorts. Showing both
// at once was the duplication; showing the right one is the fix.
func organizationMap(rep organization.Report, filters organizationFilters, fallback string) string {
	var b strings.Builder
	_ = filters // the lens moved to the Filters panel; filters ride its links there
	fmt.Fprintf(&b, `<section class="panel organization-map-panel"><div class="section-head"><div><h2>Adoption by cohort and area</h2></div><div class="heatmap-controls"><a href="/outcomes/cohorts?w=%s">All cohorts →</a></div></div>`,
		url.QueryEscape(rep.Overview.Window.Key))
	if rep.Lens == "" {
		b.WriteString(`<p class="empty">No activity has cohort data. Configure a team, role, function, domain, harness, or other segment to create the map.</p></section>`)
		return b.String()
	}
	if cohortsInLens(rep) < 2 {
		fmt.Fprintf(&b, `<p class="empty">Only one %s cohort has activity in this window. The map requires two cohorts. Adoption by cohort across all segments:</p>`,
			html.EscapeString(rep.Lens))
		b.WriteString(fallback)
		b.WriteString(`</section>`)
		return b.String()
	}
	// The same floor along the other axis: the columns are the Playbook map's
	// groups, and until the map has drawn a second one the grid is a single
	// column — a ranking wearing a matrix's clothes. Say so, and show the
	// ranking as itself.
	if len(rep.Areas) < 2 {
		b.WriteString(`<p class="empty">All live techniques are in one group. The grid requires two groups. Adoption by cohort across all segments:</p>`)
		b.WriteString(fallback)
		b.WriteString(`</section>`)
		return b.String()
	}

	b.WriteString(organizationHeatmap(rep))
	b.WriteString(`</section>`)
	return b.String()
}

// cohortsInLens counts the cohorts on the current grouping dimension — the
// number of rows the map would draw.
func cohortsInLens(rep organization.Report) int {
	n := 0
	for _, c := range rep.Cohorts {
		if c.Dimension == rep.Lens {
			n++
		}
	}
	return n
}

// heatmapOrder resolves the matrix's axes: the lens's cohorts sorted by
// adoption volume, the areas sorted by their column totals, plus those totals
// and the grand total. Both axes used to render alphabetically, which put the
// story wherever the alphabet dropped it; volume-ordered axes put the busiest
// cohort in the top row and the busiest area in the first column, so the
// top-left corner is where the reading starts AND where the action is.
func heatmapOrder(rep organization.Report) (cohorts []organization.Cohort, areas []organization.Area, areaTotals map[string]organization.Cell, grand organization.Cell) {
	for _, cohort := range rep.Cohorts {
		if cohort.Dimension == rep.Lens {
			cohorts = append(cohorts, cohort)
		}
	}
	sort.SliceStable(cohorts, func(i, j int) bool {
		if cohorts[i].Funnel.Adopted != cohorts[j].Funnel.Adopted {
			return cohorts[i].Funnel.Adopted > cohorts[j].Funnel.Adopted
		}
		return cohorts[i].Value < cohorts[j].Value
	})
	areaTotals = map[string]organization.Cell{}
	for _, cohort := range cohorts {
		for _, area := range rep.Areas {
			cell := organizationCell(rep.Cells, rep.Lens, cohort.Value, area.Key)
			sum := areaTotals[area.Key]
			sum.Current.Shown += cell.Current.Shown
			sum.Current.Adopted += cell.Current.Adopted
			sum.Current.Helped += cell.Current.Helped
			areaTotals[area.Key] = sum
			grand.Current.Shown += cell.Current.Shown
			grand.Current.Adopted += cell.Current.Adopted
			grand.Current.Helped += cell.Current.Helped
		}
	}
	areas = append(areas, rep.Areas...)
	sort.SliceStable(areas, func(i, j int) bool {
		a, b := areaTotals[areas[i].Key].Current, areaTotals[areas[j].Key].Current
		if a.Adopted != b.Adopted {
			return a.Adopted > b.Adopted
		}
		return areas[i].Label < areas[j].Label
	})
	return
}

func organizationHeatmap(rep organization.Report) string {
	var b strings.Builder
	maxAdopted := 1
	for _, cell := range rep.Cells {
		if cell.Dimension == rep.Lens && cell.Current.Adopted > maxAdopted {
			maxAdopted = cell.Current.Adopted
		}
	}
	cohorts, areas, areaTotals, grand := heatmapOrder(rep)
	b.WriteString(`<div class="org-heatmap-legend" aria-label="Heatmap legend"><span><i class="heat-gradient"></i>helped rate 0–100%</span><span><i class="heat-volume"></i>adoption volume</span><span><i class="heat-inferred"></i>inferred evidence</span><span><i class="heat-mixed"></i>mixed evidence</span></div>`)
	// Per-area totals across the rendered cohorts, shown right in the column
	// headers (columns visibly add up to them; events missing the lens
	// dimension are the map's blind spot, not the totals'). The grand total
	// sits under the "total" header where both axes meet.
	b.WriteString(`<div class="org-heatmap" tabindex="0" role="region" aria-label="Cohort and playbook area heatmap; scroll horizontally and vertically"><table class="org-heatmap-table"><caption class="sr-only">Cohorts by playbook area. Each cell gives shown, adopted, helped, explicit, and inferred counts. Column headers give totals for each area.</caption><thead><tr><th scope="col">` + html.EscapeString(rep.Lens) + ` cohort</th>`)
	for _, area := range areas {
		sum := areaTotals[area.Key].Current
		// data-tip carries the full label: a technique-name area is clamped to two
		// lines by the header CSS, and hover must recover what the clamp hides.
		label := fmt.Sprintf("%s, total %d shown, %d adopted, %d helped", area.Label, sum.Shown, sum.Adopted, sum.Helped)
		fmt.Fprintf(&b, `<th scope="col" aria-label="%s" data-tip="%s"><a href="%s">%s</a><span class="head-total"><b>%d</b>/%d</span></th>`,
			html.EscapeString(label), html.EscapeString(label),
			html.EscapeString(organizationAreaHref(area, rep.Overview.Window.Key)), html.EscapeString(area.Label),
			sum.Adopted, sum.Helped)
	}
	fmt.Fprintf(&b, `<th scope="col" class="total" aria-label="grand total %d shown, %d adopted, %d helped">total<span class="head-total"><b>%d</b>/%d</span></th></tr></thead><tbody>`,
		grand.Current.Shown, grand.Current.Adopted, grand.Current.Helped, grand.Current.Adopted, grand.Current.Helped)
	for _, cohort := range cohorts {
		href := "/outcomes/cohorts/" + url.PathEscape(rep.Lens+":"+cohort.Value) + "?w=" + url.QueryEscape(rep.Overview.Window.Key)
		fmt.Fprintf(&b, `<tr><th scope="row"><a href="%s">%s</a></th>`, html.EscapeString(href), html.EscapeString(cohort.Value))
		for _, area := range areas {
			cell := organizationCell(rep.Cells, rep.Lens, cohort.Value, area.Key)
			f := cell.Current
			rate := 0.0
			if v, ok := f.HelpedRate(); ok {
				rate = v
			}
			cls := "heat-cell"
			if f.Shown == 0 && f.Adopted == 0 && f.Helped == 0 && cell.Explicit == 0 && cell.Inferred == 0 {
				cls += " zero"
			}
			if cell.Inferred > 0 && cell.Explicit == 0 {
				cls += " inferred-only"
			} else if cell.Inferred > 0 && cell.Explicit > 0 {
				cls += " mixed-evidence"
			}
			label := fmt.Sprintf("%s, %s: %d shown, %d adopted, %d helped, %d explicit, %d inferred", cohort.Value, area.Label, f.Shown, f.Adopted, f.Helped, cell.Explicit, cell.Inferred)
			value := fmt.Sprintf(`<span class="cell-value"><b>%d</b><span>/%d</span></span>`, f.Adopted, f.Helped)
			if f.Shown > 0 || f.Adopted > 0 || f.Helped > 0 || cell.Explicit > 0 || cell.Inferred > 0 {
				// An active cell is a real destination — the cohort's detail
				// page — not a dead tab stop.
				value = fmt.Sprintf(`<a class="cell-value" href="%s" aria-label="%s"><b>%d</b><span>/%d</span></a>`,
					html.EscapeString(href), html.EscapeString(label), f.Adopted, f.Helped)
			}
			// data-tip mirrors the aria-label: the shared hover layer gives
			// pointer users the full five counts a 44px cell cannot carry.
			fmt.Fprintf(&b, `<td class="%s" aria-label="%s" data-tip="%s" style="--helped:%.1f%%;--volume:%.1f%%">%s</td>`,
				cls, html.EscapeString(label), html.EscapeString(label), rate*100, float64(f.Adopted)/float64(maxAdopted)*100, value)
		}
		fmt.Fprintf(&b, `<td class="total" aria-label="%s total: %d shown, %d adopted, %d helped"><b>%d</b><span>/%d</span></td></tr>`, html.EscapeString(cohort.Value), cohort.Funnel.Shown, cohort.Funnel.Adopted, cohort.Funnel.Helped, cohort.Funnel.Adopted, cohort.Funnel.Helped)
	}
	b.WriteString(`</tbody>`)

	b.WriteString(`</table></div>`)
	b.WriteString(organizationHeatlist(rep))
	// The footnote states the grouping rule in force: the columns are the
	// Playbook map's areas, so the matrix and the map agree.
	b.WriteString(ui.Fine(`Each cell is <b>adopted</b>/helped. The colour is the helped rate and the ` +
		`bar under it is adoption volume. Columns are the Playbook map\u2019s areas; a technique ` +
		`outside every area goes into Ungrouped.`))
	return b.String()
}

// organizationHeatlist is the phone rendering of the same matrix: a
// cohort-by-cohort accordion instead of a two-axis scroll. Each cohort's
// summary carries its totals; opening it lists only the areas with activity
// (the desktop grid's zero cells are the scrolling a phone can't afford),
// each with the same helped-rate heat chip and adopted/helped figures. CSS
// swaps the two renderings at the 640px breakpoint — same data, same
// encoding, a shape that fits the screen.
func organizationHeatlist(rep organization.Report) string {
	cohorts, areas, _, _ := heatmapOrder(rep)
	var b strings.Builder
	b.WriteString(`<div class="org-heatlist">`)
	for i, cohort := range cohorts {
		href := "/outcomes/cohorts/" + url.PathEscape(rep.Lens+":"+cohort.Value) + "?w=" + url.QueryEscape(rep.Overview.Window.Key)
		type entry struct {
			area organization.Area
			cell organization.Cell
		}
		var active []entry
		quiet := 0
		for _, area := range areas {
			cell := organizationCell(rep.Cells, rep.Lens, cohort.Value, area.Key)
			f := cell.Current
			if f.Shown == 0 && f.Adopted == 0 && f.Helped == 0 && cell.Explicit == 0 && cell.Inferred == 0 {
				quiet++
				continue
			}
			active = append(active, entry{area, cell})
		}
		sort.SliceStable(active, func(a, b int) bool {
			return active[a].cell.Current.Adopted > active[b].cell.Current.Adopted
		})
		open := ""
		if i == 0 {
			open = " open" // the first cohort previews the shape; the rest stay compact
		}
		fmt.Fprintf(&b, `<details class="heatlist-cohort"%s><summary><a href="%s">%s</a><span class="heatlist-total"><b>%d</b>/%d</span></summary>`,
			open, html.EscapeString(href), html.EscapeString(cohort.Value), cohort.Funnel.Adopted, cohort.Funnel.Helped)
		if len(active) == 0 {
			b.WriteString(`<p class="empty">no activity in this window</p>`)
		}
		for _, e := range active {
			f := e.cell.Current
			rate := 0.0
			if v, ok := f.HelpedRate(); ok {
				rate = v
			}
			mark := ""
			if e.cell.Inferred > 0 && e.cell.Explicit == 0 {
				mark = ` <span class="heatlist-mark" title="inferred only">◌</span>`
			}
			fmt.Fprintf(&b, `<div class="heatlist-row"><i class="heatlist-chip" style="--helped:%.1f%%"></i><a href="%s">%s</a>%s<span class="heatlist-nums"><b>%d</b>/%d</span></div>`,
				rate*100, html.EscapeString(organizationAreaHref(e.area, rep.Overview.Window.Key)), html.EscapeString(e.area.Label), mark, f.Adopted, f.Helped)
		}
		if quiet > 0 {
			fmt.Fprintf(&b, `<p class="heatlist-quiet">%d area%s with no activity</p>`, quiet, plural(quiet))
		}
		b.WriteString(`</details>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// momentumRowLimit is how many rows each momentum panel shows before the rest
// fold into a "show more" disclosure, so a long leaderboard opens at a readable
// length instead of running down the page.
const momentumRowLimit = 5

// moreBars renders up to limit bars, folding any remainder into a disclosure.
func moreBars(rows []BarRow, limit int, noun string) string {
	if len(rows) <= limit {
		return string(HBars(rows, ""))
	}
	return string(HBars(rows[:limit], "")) +
		fmt.Sprintf(`<details class="fed-more"><summary>Show %d more %s</summary>%s</details>`,
			len(rows)-limit, noun, string(HBars(rows[limit:], "")))
}

// moreFindings renders up to limit org-findings <li> items, folding the rest
// into a disclosure carrying its own list.
func moreFindings(items []string, limit int, noun string) string {
	head, rest := items, []string(nil)
	if len(items) > limit {
		head, rest = items[:limit], items[limit:]
	}
	var b strings.Builder
	b.WriteString(`<ul class="org-findings">` + strings.Join(head, "") + `</ul>`)
	if len(rest) > 0 {
		fmt.Fprintf(&b, `<details class="fed-more"><summary>Show %d more %s</summary><ul class="org-findings">%s</ul></details>`,
			len(rest), noun, strings.Join(rest, ""))
	}
	return b.String()
}

func organizationMomentum(rep organization.Report) string {
	areaByKey := map[string]organization.Area{}
	for _, a := range rep.Areas {
		areaByKey[a.Key] = a
	}
	spread := append([]organization.Spread(nil), rep.Spread...)
	sort.Slice(spread, func(i, j int) bool {
		di, dj := spread[i].Current-spread[i].Previous, spread[j].Current-spread[j].Previous
		if di != dj {
			return di > dj
		}
		return spread[i].CurrentAdopted > spread[j].CurrentAdopted
	})
	var b strings.Builder
	b.WriteString(`<div class="grid three organization-fields">`)
	// "all" resolves with PrevStart == Start: an empty previous interval, so
	// there is nothing honest to compare against.
	hasPrev := rep.Overview.Window.PrevStart.Before(rep.Overview.Window.Start)
	hint := "adoptions per area of practice, and how many " + rep.Lens + " cohorts they reached, compared with the previous window"
	if !hasPrev {
		hint = "adoptions per area of practice, and how many " + rep.Lens + " cohorts they reached, all time"
	}
	b.WriteString(`<section class="panel"><h2>Areas gaining adoption</h2>` + ui.Sub("", hint))
	if len(spread) == 0 {
		b.WriteString(`<p class="empty">no adoptions in this window</p>`)
	} else {
		max := 1
		for _, s := range spread {
			if s.CurrentAdopted > max {
				max = s.CurrentAdopted
			}
		}
		toRow := func(s organization.Spread) BarRow {
			sub := fmt.Sprintf("%d cohort%s", s.Current, plural(s.Current))
			if hasPrev {
				sub += fmt.Sprintf(" · Δ%+d", s.Current-s.Previous)
			}
			return BarRow{Label: areaByKey[s.Area].Label, Value: s.CurrentAdopted, Max: max, Key: "s2", Sub: sub,
				Href: organizationAreaHref(areaByKey[s.Area], rep.Overview.Window.Key)}
		}
		// An area with no adoptions is a row of zero — true, and worth being
		// able to see, but there are usually more of them than there are real
		// rows, and a leaderboard that opens with fifteen zeros buries the ten
		// numbers it exists to rank. The zeros fold into a disclosure; nothing
		// is lost, it just stops being the first thing read.
		var active, quiet []BarRow
		for _, s := range spread {
			if s.CurrentAdopted == 0 {
				quiet = append(quiet, toRow(s))
			} else {
				active = append(active, toRow(s))
			}
		}
		if len(active) == 0 {
			b.WriteString(`<p class="empty">no adoptions in this window</p>`)
		} else {
			b.WriteString(moreBars(active, momentumRowLimit, "areas"))
		}
		if len(quiet) > 0 {
			fmt.Fprintf(&b, `<details class="quiet-rows"><summary>%d area%s with no adoptions</summary>%s</details>`,
				len(quiet), plural(len(quiet)), string(HBars(quiet, "")))
		}
	}
	b.WriteString(`</section>`)
	b.WriteString(`<section class="panel" id="opportunities"><h2>Cohort adoption gaps</h2>` +
		ui.Sub("", "largest gap first") +
		ui.Fine(`Each row compares a cohort’s adoption rate with peers who adopted techniques in the same area and reported helped outcomes.`))
	if len(rep.Opportunities) == 0 {
		b.WriteString(`<p class="empty">No cohort adoption gaps can be measured in this window.</p>`)
	} else {
		// Ranked by the size of the rate gap — the reason the list exists.
		// Each row is a dumbbell (this cohort's adoption rate vs its peers'):
		// the old prose rows stated the same counts for every entry and left
		// the gap itself invisible. Full counts ride the hover tooltip.
		ops := append([]organization.Opportunity(nil), rep.Opportunities...)
		rate := func(num, den int) float64 {
			if den == 0 {
				return 0
			}
			return float64(num) / float64(den)
		}
		gapOf := func(op organization.Opportunity) float64 {
			return rate(op.Peer.Adopted, op.Peer.Shown) - rate(op.Target.Adopted, op.Target.Shown)
		}
		sort.SliceStable(ops, func(i, j int) bool { return gapOf(ops[i]) > gapOf(ops[j]) })
		rows := make([]GapRow, 0, len(ops))
		for _, op := range ops {
			a := areaByKey[op.Area]
			rows = append(rows, GapRow{
				Label:     op.Cohort,
				LabelHref: "/outcomes/cohorts/" + url.PathEscape(op.Dimension+":"+op.Cohort) + "?w=" + url.QueryEscape(rep.Overview.Window.Key),
				Area:      a.Label,
				AreaHref:  organizationAreaHref(a, rep.Overview.Window.Key),
				Here:      rate(op.Target.Adopted, op.Target.Shown),
				Peers:     rate(op.Peer.Adopted, op.Peer.Shown),
				Tip: fmt.Sprintf("%s · %s: %d of %d suggestions adopted here; peers adopted %d of %d with %d helped",
					op.Cohort, a.Label, op.Target.Adopted, op.Target.Shown, op.Peer.Adopted, op.Peer.Shown, op.Peer.Helped),
			})
		}
		head := rows
		if len(rows) > momentumRowLimit {
			head = rows[:momentumRowLimit]
		}
		b.WriteString(string(GapRows(head, true)))
		if len(rows) > momentumRowLimit {
			fmt.Fprintf(&b, `<details class="fed-more"><summary>Show %d more gaps</summary>%s</details>`,
				len(rows)-momentumRowLimit, string(GapRows(rows[momentumRowLimit:], false)))
		}
	}
	b.WriteString(`</section>`)
	b.WriteString(`<section class="panel" id="proponents"><h2>Cohorts with helped outcomes</h2>` +
		ui.Sub("", "cohorts by area and helped outcomes"))
	if len(rep.Proponents) == 0 {
		b.WriteString(`<p class="empty">No cohorts have helped outcomes in an area yet.</p>`)
	} else {
		items := make([]string, 0, len(rep.Proponents))
		for _, p := range rep.Proponents {
			a := areaByKey[p.Area]
			// Below the floor, state the counts and not a percentage: "helped
			// 100% · n=1" is one data point wearing the clothes of a rate.
			evidence := fmt.Sprintf("helped %d of %d adopted", p.Funnel.Helped, p.Funnel.Adopted)
			rate := 0.0
			if hr, ok := p.Funnel.HelpedRate(); ok {
				rate = hr
			}
			if p.Funnel.Adopted >= minRankedAdoptions {
				evidence = fmt.Sprintf("helped %.0f%% · n=%d", rate*100, p.Funnel.Adopted)
			}
			// The chip carries the same helped-rate ramp as the map above, so
			// the two read as one encoding.
			items = append(items, fmt.Sprintf(`<li><i class="prop-chip" style="--helped:%.1f%%"></i><a href="/outcomes/cohorts/%s?w=%s"><b>%s</b></a><span><a href="%s">%s</a></span><p>%s · explicit %d / inferred %d</p></li>`,
				rate*100,
				url.PathEscape(p.Dimension+":"+p.Cohort), url.QueryEscape(rep.Overview.Window.Key), html.EscapeString(p.Cohort),
				html.EscapeString(organizationAreaHref(a, rep.Overview.Window.Key)), html.EscapeString(a.Label),
				evidence, p.Explicit, p.Inferred))
		}
		b.WriteString(moreFindings(items, momentumRowLimit, "cohorts"))
	}
	b.WriteString(`</section></div>`)
	return b.String()
}

func organizationHealth(rep organization.Report, filtered bool) string {
	h := rep.Health
	note := ""
	if filtered {
		note = `; always describes the whole registry across the filters above`
	}
	coverage := "—"
	if h.Audit.Total > 0 {
		coverage = fmt.Sprintf("%.0f%%", float64(h.Audit.WithContext)/float64(h.Audit.Total)*100)
	}
	var b strings.Builder
	b.WriteString(`<section class="panel organization-health" id="health"><div class="section-head"><div><h2>Registry and evidence health</h2>` + ui.Sub("", "available for organization reporting"+note) + `</div><a href="/learning">Learning readiness →</a></div>`)
	// The window rides the two Review links because both counts are computed
	// over it — "Awaiting first adoption" especially, which counts techniques shown
	// but not adopted IN THIS WINDOW (organization.go's Health loop over the
	// windowed Overview). Without it the tile said one number and its
	// destination showed another.
	w := "?w=" + url.QueryEscape(rep.Overview.Window.Key)
	b.WriteString(string(TileRow(true,
		Tile{Label: "Live techniques", Value: fmtCount(h.Live), Delta: fmt.Sprintf("%d org-scoped", h.OrganizationTechniques), Good: true, Href: "/techniques"},
		Tile{Label: "Drafts to decide", Value: fmtCount(h.Drafts), Delta: "awaiting review", Good: true, Href: "/review" + w + "#drafts"},
		Tile{Label: "Awaiting first adoption", Value: fmtCount(h.ShownNeverAdopted), Delta: "shown but not adopted", Href: "/review" + w + "#adoption"},
		Tile{Label: "Context completeness", Value: coverage, Delta: fmt.Sprintf("n=%d audit facts", h.Audit.Total), Good: true},
	)))
	if h.Audit.Total == 0 {
		b.WriteString(`<p class="empty">Audit facts will enable model, tool, resource, and task context comparisons. Outcome reporting is available now.</p>`)
	} else {
		fmt.Fprintf(&b, `<p class="hint">Audit facts: %d total · %d with session hash · %d with segment · %d with task type · %d with model/harness/domain/surface context.</p>`, h.Audit.Total, h.Audit.WithSession, h.Audit.WithSegment, h.Audit.WithTaskType, h.Audit.WithContext)
	}
	if h.Audit.WithSession == 0 {
		b.WriteString(`<div class="readiness-note"><b>Session grouping is not configured.</b> Give members the same <code>TACIT_SESSION_SALT</code> with <code>tacit connect --session-salt</code>. New audit facts will then include an org-scoped pseudonymous session hash.</div>`)
	} else {
		fmt.Fprintf(&b, `<div class="readiness-note"><b>Session grouping is active.</b> %d of %d facts carry an org-scoped pseudonymous session hash. Dynamic community clustering will come later.</div>`, h.Audit.WithSession, h.Audit.Total)
	}
	b.WriteString(`</section>`)
	return b.String()
}

func organizationAreaHref(area organization.Area, window string) string {
	switch area.Kind {
	case "task_type":
		return "/outcomes/task-type/" + url.PathEscape(area.Label) + "?w=" + url.QueryEscape(window)
	case "tag":
		return "/outcomes/tag/" + url.PathEscape(area.Label) + "?w=" + url.QueryEscape(window)
	case "technique":
		return "/techniques/" + url.PathEscape(strings.TrimPrefix(area.Key, "technique:")) + "?w=" + url.QueryEscape(window)
	case "map-area":
		// The area's identity has to ride, or every column header, bar and chip
		// on this page lands on the same undifferentiated map. ?area= names the
		// cluster; the map selects and frames it on load (techniqueMapScript), which
		// is what clicking it in the Areas list does. The label is the cluster's
		// name — mapAreaOf builds both from the same techmap cluster, so they
		// match word for word, including the "Ungrouped" bucket.
		return "/techniques/map?area=" + url.QueryEscape(area.Label)
	default:
		return "/techniques"
	}
}

func organizationCell(cells []organization.Cell, dim, cohort, area string) organization.Cell {
	for _, cell := range cells {
		if cell.Dimension == dim && cell.Cohort == cohort && cell.Area == area {
			return cell
		}
	}
	return organization.Cell{}
}
