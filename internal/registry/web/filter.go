// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The one set-filter.
//
// There were two independent ones: Techniques filtered by clicking a tag chip
// or a scope badge, with the active filter restated in an ad-hoc "filter-note"
// carrying a clear link; Outcomes had a proper set-based popover bar with
// Apply, removable chips, and multi-value support. Same job, two mechanisms,
// neither reachable from the other — so a reader who learned one learned
// nothing about the other.
//
// This is Outcomes' bar, generalized: both pages now build it, the click-to-
// filter chips on Techniques set values IN it, and single-value links
// (?tag=x) remain valid as the degenerate case of the repeated-parameter
// encoding, so every link ever emitted still works.
//
// The top bar's text box is a different control and stays: it is a free-text
// filter over the rows on screen, not a set membership choice.
package web

import (
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
)

// filterOption is one checkbox in a dimension's popover.
type filterOption struct{ value, label string }

// filterDim is one filterable dimension: its query parameter, its plural label
// for the summary, the values on offer, and whether the popover needs a search
// box (40+ techniques don't scan as a flat list).
type filterDim struct {
	param, label string
	options      []filterOption
	searchable   bool
	selected     []string
	chipPrefix   string // "tag: ", "source: " — "" uses the option's own label
}

// filterBar renders the set-based control: one <details> popover per dimension
// (native, no JS required to operate), a single Apply that submits everything
// as repeated GET parameters, and the active values restated beneath as chips
// whose × removes exactly one value — so narrowing and widening are both one
// click.
//
// hidden carries the page's other state through the form (the time window and
// cohort lens on Outcomes; the retired-archive flag on Techniques). extra is
// appended inside the form — Outcomes puts its Group-by lens there, because it
// regroups every panel and so belongs at page level, not in any one panel.
func filterBar(action string, dims []filterDim, hidden map[string]string, extra string, clearHref string, anySelected bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<form class="organization-filters" method="get" action="%s"><span class="filters-label">Filters</span>`,
		html.EscapeString(action))
	keys := make([]string, 0, len(hidden))
	for k := range hidden {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if hidden[k] == "" {
			continue
		}
		fmt.Fprintf(&b, `<input type="hidden" name="%s" value="%s">`, html.EscapeString(k), html.EscapeString(hidden[k]))
	}
	selected := 0
	for _, d := range dims {
		b.WriteString(filterMenu(d))
		selected += len(d.selected)
	}
	b.WriteString(`<button type="submit">Apply</button>`)
	if anySelected && clearHref != "" {
		fmt.Fprintf(&b, `<a class="filter-reset" href="%s">Clear</a>`, html.EscapeString(clearHref))
	}
	b.WriteString(extra)
	b.WriteString(`</form>`)
	// On a phone the boxed picker technique pushes the page's data below the fold,
	// though most visits never touch it. Wrap the bar in a CSS-only disclosure:
	// a "Filters" button that stays hidden in the desktop layout (the bar shows
	// as before) and, on a phone, collapses the technique behind one tap. The toggle
	// checkbox is a SIBLING of the form, never inside it, so it is not part of
	// the GET the form submits; the badge counts active selections so the button
	// says what is filtered without being expanded. Chips (appended by callers)
	// stay outside the collapse, visible whether it is open or closed.
	badge := ""
	if selected > 0 {
		badge = fmt.Sprintf(`<b>%d</b>`, selected)
	}
	return `<div class="filters-collapse"><input type="checkbox" id="filters-toggle" class="filters-toggle" aria-label="Show filters">` +
		`<label for="filters-toggle" class="filters-open">Filters` + badge + `</label>` +
		b.String() + `</div>`
}

// filterMenu is one dimension's popover. The summary states the size of the
// dimension and, when filtering, how much of it survives: "42 Tags" unfiltered,
// "7 of 42 Tags" with seven selected — the control describes both the space and
// the selection at a glance.
func filterMenu(d filterDim) string {
	var b strings.Builder
	summary := fmt.Sprintf("%d %s", len(d.options), d.label)
	if len(d.selected) > 0 {
		summary = fmt.Sprintf("<b>%d of %d</b> %s", len(d.selected), len(d.options), d.label)
	}
	fmt.Fprintf(&b, `<details class="fmenu"><summary>%s</summary><div class="fmenu-pop">`, summary)
	if d.searchable {
		b.WriteString(`<input class="fmenu-search" type="search" placeholder="Filter…" aria-label="Filter options">`)
	}
	b.WriteString(`<div class="fmenu-opts">`)
	for _, o := range d.options {
		checked := ""
		if containsFold(d.selected, o.value) {
			checked = " checked"
		}
		fmt.Fprintf(&b, `<label class="fmenu-opt"><input type="checkbox" name="%s" value="%s"%s> <span>%s</span></label>`,
			html.EscapeString(d.param), html.EscapeString(o.value), checked, html.EscapeString(o.label))
	}
	b.WriteString(`</div></div></details>`)
	return b.String()
}

// filterChips restates the active selections. Each × links to the same page
// with exactly that one value dropped.
func filterChips(dims []filterDim, hrefWithout func(param, value string) string) string {
	any := false
	for _, d := range dims {
		if len(d.selected) > 0 {
			any = true
		}
	}
	if !any {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="filter-chips">`)
	for _, d := range dims {
		labels := map[string]string{}
		for _, o := range d.options {
			labels[o.value] = o.label
		}
		for _, v := range d.selected {
			label := d.chipPrefix + v
			if n, ok := labels[v]; ok && d.chipPrefix == "" {
				label = n
			}
			fmt.Fprintf(&b, `<span class="fchip">%s<a href="%s" aria-label="Remove %s filter %s">×</a></span>`,
				html.EscapeString(label), html.EscapeString(hrefWithout(d.param, v)),
				html.EscapeString(d.param), html.EscapeString(label))
		}
	}
	b.WriteString(`</div>`)
	return b.String()
}

// filterURL rebuilds a page URL from its fixed state plus a filter set — the
// target of every chip's × and of the Clear link.
func filterURL(action string, hidden map[string]string, f organizationFilters) string {
	q := url.Values{}
	for k, v := range hidden {
		if v != "" {
			q.Set(k, v)
		}
	}
	q["scope"], q["tag"], q["provenance"], q["technique"] =
		f.Scope, f.Tag, f.Provenance, f.Technique
	for k, v := range q {
		if len(v) == 0 {
			delete(q, k)
		}
	}
	if len(q) == 0 {
		return action
	}
	return action + "?" + q.Encode()
}

// techniqueFilterDims builds the dimensions offered over a set of techniques. Outcomes
// offers all four; Techniques offers the three that describe a technique (its own
// identity is the list itself, so a technique picker there would be a list
// that filters a list).
func techniqueFilterDims(techniques []models.Technique, active organizationFilters, withTechnique bool) []filterDim {
	tags, provenances := map[string]bool{}, map[string]bool{}
	for _, technique := range techniques {
		if technique.Provenance != "" {
			provenances[technique.Provenance] = true
		}
		for _, tag := range technique.Tags {
			if strings.TrimSpace(tag) != "" {
				tags[tag] = true
			}
		}
	}
	sorted := func(set map[string]bool) []filterOption {
		out := make([]filterOption, 0, len(set))
		for value := range set {
			out = append(out, filterOption{value, value})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].value < out[j].value })
		return out
	}

	dims := []filterDim{
		{param: "scope", label: "Scopes", chipPrefix: "scope: ", selected: active.Scope,
			options: []filterOption{{"general", "general"}, {"org", "org"}}},
		{param: "tag", label: "Tags", chipPrefix: "tag: ", selected: active.Tag,
			options: sorted(tags), searchable: len(tags) > 8},
		{param: "provenance", label: "Sources", chipPrefix: "source: ", selected: active.Provenance,
			options: sorted(provenances)},
	}
	if !withTechnique {
		return dims
	}
	var caps []filterOption
	for _, technique := range techniques {
		if technique.Status == "draft" || technique.Status == "retired" {
			continue
		}
		caps = append(caps, filterOption{technique.ID, technique.Name})
	}
	sort.Slice(caps, func(i, j int) bool { return caps[i].label < caps[j].label })
	return append(dims, filterDim{param: "technique", label: "Techniques", selected: active.Technique,
		options: caps, searchable: true})
}
