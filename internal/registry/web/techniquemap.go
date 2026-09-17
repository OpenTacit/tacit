// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The technique knowledge map (/techniques/map): the live library drawn as a network.
// A view of the Techniques section, beside All / Retired / Tags. The graph, its
// two relations (shared tags, shared cohort) and both layouts are computed
// server-side (internal/registry/techmap); the browser only draws and hit-tests —
// no physics engine, no external library.
package web

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/techmap"
)

// describeProblem is what to tell a reader when a naming run failed, by what
// actually failed. "error" is the old single code, kept so a bookmarked or
// in-flight URL still says something true.
func describeProblem(code string) string {
	switch code {
	case "nokey":
		return `No model API key is set. Add one under Settings &rarr; Model.`
	case "model":
		return `The model request failed. Check the registry log, then check the key, provider, and model name under Settings &rarr; Model.`
	case "nodata":
		return `The registry could not read the map data. It did not send a model request.`
	case "error":
		return `The naming run did not finish. The registry log has the reason.`
	}
	return ""
}

func (s *Server) pageTechniqueMap(r *http.Request, user oidc.Claims) page {
	crumbs := []crumb{{label: "Playbook", href: playbookHome}, {label: "Map", href: ""}}
	in, err := s.readAnalyticsInputs(false)
	if err != nil {
		return storeUnavailablePage("techniques", crumbs, err)
	}
	techniques, events := in.techniques, in.events
	// The map is an ALL-TIME view: node size and colour mirror a technique's
	// accumulated standing, which is what retrieval ranks on.
	retired := 0
	live := make([]models.Technique, 0, len(techniques))
	for _, c := range techniques {
		if c.Status == "retired" {
			retired++
		}
		if c.Status != "draft" && c.Status != "retired" {
			live = append(live, c)
		}
	}
	// The breadcrumb view-switcher (Techniques / Map ▾). The "All" count is the
	// whole live set, unaffected by the map's filter.
	viewMenu := techniquesViews("map", len(live), len(liveTagVocabulary(techniques)), retired, groupParam(r))

	// The same set-filter the All view carries (filter.go): scope, tags, source
	// as popovers with Apply and removable chips. The options come from the whole
	// live set so a filter never hides its own values; the graph is built from
	// the techniques that survive the filter, matched exactly as the list matches them
	// (general scope normalised from an empty value).
	filters := parseOrganizationFilters(r.URL.Query())
	dims := techniqueFilterDims(live, filters, false)
	bar := filterBar("/techniques/map", dims, map[string]string{}, arrangeSeg(groupParam(r)),
		filterURL("/techniques/map", map[string]string{}, organizationFilters{}), !filters.empty()) +
		filterChips(dims, func(param, value string) string {
			return filterURL("/techniques/map", map[string]string{}, filters.without(param, value))
		})

	g := techmap.Build(mapMatch(live, filters), events)
	// LLM-derived cluster names/descriptions, where a describe run produced them.
	s.applyClusterLabels(&g)

	var b strings.Builder
	b.WriteString(bar)

	if len(g.Nodes) == 0 {
		if !filters.empty() {
			b.WriteString(`<p class="empty">No techniques match this filter. <a href="/techniques/map">Show all.</a></p>`)
		} else {
			b.WriteString(`<p class="empty">No live techniques to map yet.</p>`)
		}
		return page{active: "techniques", crumbs: crumbs, content: b.String(), viewMenu: viewMenu}
	}

	sm := g.Summary
	fmt.Fprintf(&b, `<div class="sub cmap-sub">%d technique%s · <b>%d</b> clustered · <b>%d</b> isolated · <b>%d</b> adopted · <b>%d</b> measurably helped</div>`,
		sm.Total, plural(sm.Total), sm.Connected, sm.Isolated, sm.Adopted, sm.Helped)

	// The Areas list, for the overlay's first section; when there are none, the
	// overlay is legend only.
	tagPanel := s.cmapAreasPanel(g.Clusters, r)
	cohortPanel := cmapCohortAreasPanel(g.CohortClusters, g.CohortDim)
	aside := ""
	if tagPanel != "" || cohortPanel != "" {
		// The visible panel must match the arrangement the page LOADS in — the
		// two lists share ids per arrangement, so showing the tag list against
		// cohort hulls names the wrong areas. The mode buttons keep them in
		// step after load; this keeps them in step at load.
		tagHidden, cohortHidden := "", " hidden"
		if groupParam(r) == "cohort" {
			tagHidden, cohortHidden = " hidden", ""
		}
		aside = `<div class="cmap-aside">` +
			`<div id="cmap-areas-tags"` + tagHidden + `>` + tagPanel + `</div>` +
			`<div id="cmap-areas-cohort"` + cohortHidden + `>` + cohortPanel + `</div>` +
			`</div>`
	}
	b.WriteString(cmapStage(aside))

	// The graph as data (json.Marshal escapes <, >, & so nothing breaks out of
	// the script element), then the renderer.
	data, err := json.Marshal(g)
	if err != nil {
		return page{status: 500, active: "techniques", crumbs: crumbs,
			content: "<p>Could not render the map: " + html.EscapeString(err.Error()) + "</p>"}
	}
	b.WriteString(`<script id="cmap-data" type="application/json">` + string(data) + `</script>`)
	b.WriteString(techniqueMapScriptTag)
	return page{active: "techniques", crumbs: crumbs, content: b.String(), viewMenu: viewMenu}
}

// cmapStage renders the map's interactive stage: the field, the floating detail
// technique, and one overlay panel holding the Areas list and the legend. Shared by
// the /techniques/map page and the tacit_map MCP app so the two never drift; the
// client (assets/techniquemap.js) drives whatever this markup lays down.
//
// The map takes the full width of the page now. It used to give a fixed 300px
// column to the Areas list, which cost the graph a quarter of its width for a
// list that is read in glances — and a graph is the one thing on the dashboard
// that gets better the more room it has. Areas move into the overlay, which
// opens by default where there is room and sits over the field rather than
// beside it. Pass an empty aside for a map with no areas to list at all.
//
// Two canvases: the field draws into #cmap (WebGL, or 2-D where there is no
// WebGL), and #cmap-labels carries the type on top. Text through a point sprite
// would mean a glyph atlas and worse type; a 2-D canvas over the scene is one
// line of compositing and the labels stay crisp at any DPR.
func cmapStage(aside string) string {
	return `<div class="cmap-stage">` +
		`<div class="cmap-main">` +
		`<div class="cmap-canvas-wrap">` +
		`<canvas id="cmap" role="img" aria-label="Playbook map"></canvas>` +
		`<canvas id="cmap-labels" class="cmap-labels" aria-hidden="true"></canvas>` +
		`<div class="cmap-detail" id="cmap-detail" hidden></div>` +
		`<button type="button" class="cmap-info-btn" id="cmap-info-btn" title="Areas and legend" aria-label="Areas and legend" aria-expanded="false">` + cmapInfoSVG + `<span>Areas</span></button>` +
		// One overlay, two sections: the areas of the current arrangement, then
		// how to read the field. Two separate popovers meant two controls and two
		// mental models for one question — "what am I looking at".
		`<div class="cmap-controls" id="cmap-controls" hidden>` +
		aside +
		`<div class="cmap-legendbox">` +
		`<p class="cmap-lbl">Legend</p>` +
		`<div class="cmap-legend"><span class="cmap-dot" style="width:8px;height:8px"></span><span class="cmap-dot" style="width:16px;height:16px"></span>size = adoptions, relative to the highest count</div>` +
		`<div class="cmap-legend"><span class="cmap-ramp"></span>deeper = helped more often</div>` +
		`<div class="cmap-legend"><span class="cmap-swatch"></span>gray = never adopted</div>` +
		// Both fields draw an org-specific technique as a star — a silhouette, not trim
		// around a disc, so the distinction survives the small nodes that are most
		// of the map. One mark, one line, whichever field rendered.
		`<div class="cmap-legend"><span class="cmap-star" aria-hidden="true">★</span>star = org-specific</div>` +
		// The flat field says the same thing with a padded hull, so this line names
		// both and app.css shows whichever field drew.
		`<div class="cmap-legend cmap-legend-cloud"><span class="cmap-cloud" aria-hidden="true"></span>cloud = the area a technique belongs to</div>` +
		`<div class="cmap-legend cmap-legend-hull"><span class="cmap-hull" aria-hidden="true"></span>tint = the area a technique belongs to</div>` +
		`<p class="cmap-lbl" style="margin-top:1rem" id="cmap-colour-lbl">Color = area</p><div id="cmap-src-legend" class="cmap-srcs"></div>` +
		`<p class="cmap-hint-3d">Drag to orbit · scroll to zoom · click a technique to open it</p>` +
		`</div>` + // close .cmap-legendbox
		`</div>` + // close .cmap-controls
		`</div>` + // close .cmap-canvas-wrap
		`</div>` + // close .cmap-main
		`</div>`
}

// groupParam reads the persisted grouping choice from the request: "none" (no
// grouping — the plain list), "cohort", or (the default) "tags". It is the
// shared lens behind both the map's arrangement and the All list's grouping,
// remembered across views like the time window.
//
// "none", not "all": the list's own view is already named All, and a grouping
// value spelt the same would read as a second opinion about which techniques are
// shown rather than about how they are banded.
//
// Only the list can honour "none". A graph has no unarranged state — every node
// has to be somewhere — so the map coerces it to tags for display (arrangeSeg)
// while leaving the param itself alone, and the reader's choice is still there
// when they come back to the list.
func groupParam(r *http.Request) string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("group"))) {
	case "cohort":
		return "cohort"
	case "none":
		return "none"
	}
	return "tags"
}

// arrangeSeg is the map's "arrange by shared tags / shared cohort" control. The
// data-group attribute seeds the client's initial mode; techniqueMapScript wires the
// data-mode buttons to re-lay-out instantly (no reload) and persist the choice.
// current is "tags" or "cohort"; the list's third choice, "none", has no meaning
// for a graph and lands on tags here.
func arrangeSeg(current string) string {
	if current != "cohort" {
		current = "tags"
	}
	btn := func(mode, label string) string {
		cls, pressed := "", "false"
		if mode == current {
			cls, pressed = ` class="active"`, "true"
		}
		return fmt.Sprintf(`<button type="button" data-mode="%s"%s aria-pressed="%s">%s</button>`, mode, cls, pressed, label)
	}
	return `<div class="cmap-arrange" data-group="` + current + `"><span class="filters-label">Grouping</span>` +
		`<div class="cmap-seg" role="group" aria-label="Group the map by">` + btn("tags", "Tags") + btn("cohort", "Cohort") + `</div></div>`
}

// arrangeLinks is the same control for a server-rendered list (the All view): the
// choices are links that navigate with ?group=, carrying the current filters, so
// the list regroups on the server. Shares the segmented styling and data-group.
// href maps a mode to the URL that selects it.
//
// "None" leads, because "just show me every technique" is the plainest thing a reader
// can want of a list and it was the one arrangement the control couldn't express:
// both other choices band the rows behind collapsed headings, so the ungrouped
// list — the section's own name — had no way back.
func arrangeLinks(current string, href func(mode string) string) string {
	if current != "cohort" && current != "none" {
		current = "tags"
	}
	var b strings.Builder
	b.WriteString(`<div class="cmap-arrange" data-group="` + current + `"><span class="filters-label">Grouping</span>` +
		`<div class="cmap-seg" role="group" aria-label="Group the list by">`)
	for _, m := range []struct{ mode, label string }{{"none", "None"}, {"tags", "Tags"}, {"cohort", "Cohort"}} {
		cls := ""
		if m.mode == current {
			cls = ` class="active" aria-current="true"`
		}
		fmt.Fprintf(&b, `<a href="%s"%s>%s</a>`, html.EscapeString(href(m.mode)), cls, m.label)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// cmapInfoSVG is the info glyph on the button that toggles the floating
// legend/controls panel.
const cmapInfoSVG = `<svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 16v-4"/><path d="M12 8h.01"/></svg>`

// mapMatch applies the set-filter to the live techniques, exactly as the All list
// does (general scope normalised from an empty value). Pure, so the page and the
// describe handler build the identical graph.
func mapMatch(live []models.Technique, filters organizationFilters) []models.Technique {
	var matched []models.Technique
	for _, c := range live {
		scope := c.Scope
		if scope == "" {
			scope = "general"
		}
		if len(filters.Scope) > 0 && !containsFold(filters.Scope, scope) {
			continue
		}
		if len(filters.Provenance) > 0 && !containsFold(filters.Provenance, c.Provenance) {
			continue
		}
		if len(filters.Tag) > 0 {
			hit := false
			for _, t := range c.Tags {
				if containsFold(filters.Tag, t) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		matched = append(matched, c)
	}
	return matched
}

// mapTechniquesForRequest returns the techniques the map shows for this request —
// the filtered live set — so the describe handler clusters the same graph the
// page renders.
func (s *Server) mapTechniquesForRequest(r *http.Request, techniques []models.Technique) []models.Technique {
	live := make([]models.Technique, 0, len(techniques))
	for _, c := range techniques {
		if c.Status != "draft" && c.Status != "retired" {
			live = append(live, c)
		}
	}
	return mapMatch(live, parseOrganizationFilters(r.URL.Query()))
}

// cmapAreasPanel lists the clusters the shared-tag graph found — the areas of
// practice. Each is named from its dominant tag until the "Describe with AI" pass
// gives it a name and a sentence; clicking one highlights it on the map. The
// current filter rides the describe form so a filtered view round-trips.
func (s *Server) cmapAreasPanel(clusters []techmap.Cluster, r *http.Request) string {
	if len(clusters) == 0 {
		return ""
	}
	q := r.URL.Query()
	describeErr := describeProblem(q.Get("describe"))
	q.Del("describe")
	action := "/techniques/map/describe"
	if enc := q.Encode(); enc != "" {
		action += "?" + enc
	}
	named := false
	for _, c := range clusters {
		if c.Desc != "" {
			named = true
			break
		}
	}
	label := "Describe with AI"
	if named {
		label = "Re-describe"
	}

	var b strings.Builder
	b.WriteString(`<section class="panel cmap-areas" id="cmap-areas">` +
		`<div class="cmap-areas-head"><p class="cmap-lbl">Areas</p>` +
		`<form method="post" action="` + html.EscapeString(action) + `" id="cmap-describe-form">` +
		`<button type="submit" class="cmap-describe" id="cmap-describe-btn">` + label + `</button></form></div>`)
	if describeErr != "" {
		b.WriteString(`<p class="hint" style="color:var(--bad)">` + describeErr + `</p>`)
	} else {

	}
	b.WriteString(`<ul class="cmap-area-list">`)
	for _, c := range clusters {
		fmt.Fprintf(&b, `<li data-cluster="%d"><span class="cmap-area-name">%s</span><span class="cmap-area-n">%d</span>`,
			c.ID, html.EscapeString(c.Name), len(c.Members))
		if c.Desc != "" {
			fmt.Fprintf(&b, `<p class="cmap-area-desc">%s</p>`, html.EscapeString(c.Desc))
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>` +
		// The naming run is a synchronous model call of a few seconds; disable the
		// button and say what's happening so the wait registers.
		`<script>(function(){var f=document.getElementById('cmap-describe-form');if(!f)return;` +
		`f.addEventListener('submit',function(){var b=document.getElementById('cmap-describe-btn');` +
		`b.disabled=true;b.textContent='Describing…';});})();</script>` +
		`</section>`)
	return b.String()
}

// cmapCohortAreasPanel lists the cohort areas — the techniques each cohort
// (team, or role) adopts most, named by that cohort. Shown when the map is
// arranged by cohort; its <li>s share the tag panel's markup so the same client
// wiring highlights them. No "describe" pass: a cohort names itself.
func cmapCohortAreasPanel(clusters []techmap.Cluster, dim string) string {
	var b strings.Builder
	b.WriteString(`<section class="panel cmap-areas"><div class="cmap-areas-head"><p class="cmap-lbl">Areas</p></div>`)
	if len(clusters) == 0 {
		b.WriteString(`<p class="hint">There is not enough adoption data to group these techniques by cohort. This view uses team or role values from adoption events.</p></section>`)
		return b.String()
	}
	lens := dim
	if lens == "" {
		lens = "cohort"
	}
	b.WriteString(`<h3 class="chart-sub"><span class="u">grouped by the ` +
		html.EscapeString(lens) + ` that adopts each most</span></h3>`)
	b.WriteString(`<ul class="cmap-area-list">`)
	for _, c := range clusters {
		fmt.Fprintf(&b, `<li data-cluster="%d"><span class="cmap-area-name">%s</span><span class="cmap-area-n">%d</span></li>`,
			c.ID, html.EscapeString(c.Name), len(c.Members))
	}
	b.WriteString(`</ul></section>`)
	return b.String()
}

// The renderer used to live here, as a 400-line inline script. It is now
// assets/techniquemap.js — one file holding the controller, the WebGL field and the
// 2-D fallback — because a second renderer would otherwise have meant a second
// copy of the detail technique, the legend and the mode switch. assets.go serves it
// by hashed URL for this page and hands the same bytes to the MCP app to inline.
//
// Encoding, shared by both fields: SIZE = adoption; COLOUR HUE = the area of
// practice a technique belongs to, or the cohort that adopts it most in the cohort
// arrangement — the same hue its swatch carries in the Areas list; COLOUR DEPTH
// = helped rate; grey = never adopted; ring (star, in the flat field) =
// org-scoped. Two arrangements share the node set, swapped by the segmented
// control without a reload.
