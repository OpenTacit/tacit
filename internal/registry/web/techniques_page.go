// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/techmap"
)

func (s *Server) pageIndex(r *http.Request, user oidc.Claims) page {
	techniques, err := s.Store.ListTechniques(nil, 200)
	if err != nil {
		return storeUnavailablePage("techniques", nil, err)
	}
	// Techniques uses the SAME set-filter as Outcomes (filter.go) — scope,
	// tag, and source, as repeated parameters. The row chips still filter with
	// one click; they now set a value in that filter rather than driving a
	// second, separate mechanism. A single-value link (?tag=x) is the degenerate
	// case of the encoding, so every link ever emitted still resolves.
	filters := parseOrganizationFilters(r.URL.Query())
	// The archive is its own ROUTE — /techniques/retired — not a query flag on the
	// list. It sits in the section nav beside Tags, and a nav whose items behave
	// differently from one another is not a nav: one tab navigating to a page
	// while its neighbour filters the page you are on is the kind of
	// inconsistency a reader feels before they can name it.
	archive := r.URL.Path == "/techniques/retired"
	statusFilter := ""
	if archive {
		statusFilter = "retired"
	}
	// The default view is the LIVE set — what retrieval can actually serve — so
	// its count matches every tile that links here (Outcomes' "Live techniques").
	basePath := "/techniques"
	if archive {
		basePath = "/techniques/retired"
	}
	// The row links stay single-valued: clicking a tag means "show me this tag",
	// not "add this tag to my set". These carry the first active value so the
	// two dimensions still compose.
	tagFilter, scopeFilter := "", ""
	if len(filters.Tag) > 0 {
		tagFilter = filters.Tag[0]
	}
	if len(filters.Scope) > 0 {
		scopeFilter = strings.ToLower(filters.Scope[0])
	}

	list := collectTechniqueRows(techniques, filters, statusFilter, tagFilter, archive)
	collected, filteredLive, scopesSeen := list.rows, list.live, list.scopesSeen
	shown, retiredCount := list.shown, list.retiredCount

	// The scope column is gone: it read "general" on all but a handful of rows,
	// spending a whole column of the reader's attention on a constant while
	// burying the org-scoped techniques — the ones unique to this
	// organization, and the entire point of having a registry. The exception
	// now rides on the name as a badge, and that badge is still the filter link
	// the column's cell used to be. Filtering to "general" is still one click
	// from the scope bar on Outcomes, and tag links carry the active scope, so
	// nothing composes any less well than before.
	// extra injects attributes into the <tr> — the grouped view uses it to mark a
	// row as a collapsible area member; the flat list passes "".
	renderRow := func(row capListRow, extra string) string {
		nameCell := row.name
		if row.scope == "org" {
			nameCell = scopeLink(row.scope, tagFilter) + " " + row.name
		}
		return fmt.Sprintf(
			`<tr%s data-search="%s" data-href="/techniques/%s"><td>%s</td><td>%s</td><td>%s</td>%s</tr>`,
			extra, row.searchKey, row.id, nameCell, row.source, row.tags, row.restore)
	}
	var rows strings.Builder
	for _, row := range collected {
		rows.WriteString(renderRow(row, ""))
	}

	// The org-scoped count leads the sub-line — it is the number that says how
	// much of this registry is genuinely the organization's own.
	orgNote := ""
	if n := scopesSeen["org"]; n > 0 && scopeFilter == "" {
		orgNote = fmt.Sprintf(` · <a href="/techniques?scope=org">%d org-scoped</a>`, n)
	}
	// Tags and the retired archive are sibling VIEWS of the library, not counts —
	// they live in the breadcrumb view-switcher now (techniquesViews), not buried
	// in this line.
	navActive := "all"
	if archive {
		navActive = "retired"
	}
	viewMenu := techniquesViews(navActive, liveCount(techniques), len(liveTagVocabulary(techniques)), retiredCount, groupParam(r))
	// The shared set-filter, over the LIVE set (so the popovers offer every tag
	// and source that exists, not only the ones surviving the current filter —
	// a filter that hides its own options can't be widened).
	live := make([]models.Technique, 0, len(techniques))
	for _, c := range techniques {
		if c.Status != "draft" && c.Status != "retired" {
			live = append(live, c)
		}
	}
	dims := techniqueFilterDims(live, filters, false)
	// The view is the path now, so nothing about it needs smuggling through the
	// form as a hidden field — the form simply posts back to the view it is on.
	hidden := map[string]string{}
	// The live list is grouped by the shared tags/cohort lens (remembered across
	// views); its control navigates with ?group=, carrying the current filters.
	// The archive stays a flat list, so it gets no grouping control.
	group := groupParam(r)
	arrange := ""
	if !archive {
		arrange = arrangeLinks(group, func(mode string) string {
			q := r.URL.Query()
			q.Set("group", mode)
			return basePath + "?" + q.Encode()
		})
	}
	bar := filterBar(basePath, dims, hidden, arrange, filterURL(basePath, hidden, organizationFilters{}), !filters.empty()) +
		filterChips(dims, func(param, value string) string {
			return filterURL(basePath, hidden, filters.without(param, value))
		})
	// A tag filter is also a question about outcomes — keep the door to it.
	if tagFilter != "" {
		bar += fmt.Sprintf(`<p class="hint"><a href="/outcomes/tag/%s">View outcomes for "%s" →</a></p>`,
			url.PathEscape(tagFilter), html.EscapeString(tagFilter))
	}

	// The live "All" view groups into areas of practice by default: the filtered
	// set is clustered with the same community detection the Map uses, and each
	// area's adoption funnel is rolled up. The areas match the current filter
	// because they are detected over the filtered set, and the LLM names the Map's
	// "Describe" run cached are reused here.
	//
	// Grouping "None" opts out of that entirely and renders the rows as they were
	// collected — one flat list, nothing collapsed. The archive (retired) is
	// always flat and gets no control: it is small and read as a whole.
	grouped := !archive && group != "none"
	body := rows.String()
	// groupNote explains a grouping that found nothing to group by, in the one
	// place the reader would otherwise be misled (see below).
	groupNote := ""
	if grouped {
		if events, err := s.Store.AllEvents(""); err == nil {
			g := techmap.Build(filteredLive, events)
			s.applyClusterLabels(&g)
			clusters := g.Clusters
			if group == "cohort" {
				clusters = g.CohortClusters
			}
			// A grouping that produced no areas would render as a single
			// "Ungrouped" heading over the whole library — which reads as "these
			// techniques failed to cluster" when the truth is "there is nothing here
			// to group them BY". The cohort case is the common one: while a single
			// team does all the adopting, every technique's dominant cohort is the same
			// value, so no dimension separates them (techmap.cohortClusters). Say
			// that plainly and show the plain list, which is what the reader gets
			// either way. The Map says the same thing in cmapCohortAreasPanel.
			if len(clusters) == 0 {
				grouped = false
				if group == "cohort" {
					groupNote = `All adopted techniques in this view have the same team and role. Cohort groups require adoption by another team or role.`
				} else {
					groupNote = `These techniques share too few tags to form groups. Groups will appear when more techniques share tags.`
				}
			} else {
				rowByID := make(map[string]capListRow, len(collected))
				for _, row := range collected {
					rowByID[row.rawid] = row
				}
				body = groupedTechniqueRows(g, clusters, rowByID, renderRow)
			}
		}
	}
	if shown == 0 && (!filters.empty() || statusFilter != "") {
		body = `<tr><td colspan="4" class="empty">No reviewed techniques match this filter. <a href="/techniques">Show all.</a></td></tr>`
		groupNote = "" // nothing is listed, so nothing needs explaining about its shape
	}
	noteHTML := ""
	if groupNote != "" {
		noteHTML = `<p class="hint techniques-groupnote">` + groupNote + `</p>`
	}
	// The archive carries a per-row Restore action, so its table is a column
	// wider and marks itself — on a phone the two low-value columns (source,
	// tags) fold away so the name and the button have room (assets/app.css).
	actionHead, tableClass := "", "techniques"
	if archive {
		actionHead = "<th></th>"
		tableClass = "techniques techniques-archive"
	}
	// The registry's ambient totals (techniques, events, rollups) are stated once, in
	// the shell's meta strip. Restating them here — and again on Outcomes, and
	// again on Learning — made an ambient number read as a load-bearing one.
	// Each view says what it is. The archive is not the library, and describing
	// it as "everything this organization has learned" while showing four retired
	// techniques is simply untrue.
	blurb := `Reviewed techniques available to members. Member outcomes provide evidence for each technique.`
	if archive {
		blurb = `Retired techniques are excluded from retrieval. Restore a technique to make it available again.`
	}
	// No API footnote under the table. It told a member reading the playbook to
	// send an X-Tacit-Key header, which is not a thing they will ever do; it
	// duplicated the one place that explains the key properly (Settings →
	// Access); and it called the page "read-only" on the archive, which carries
	// a Restore button on every row.

	// A member who merged has techniques in somebody else's queue, and the
	// answer lives on /team now (M5) — one line here, because a reader who was
	// finding it above their own library should not have to go looking.
	contributed := ""
	if !archive {
		contributed = s.teamPointer(r)
	}
	content := fmt.Sprintf(`<div class="page-head"><p class="sub">%s</p></div>`+
		`%s`+
		`%s`+
		`<div class="sub techniques-subline">%d reviewed technique%s%s · <span class="muted">select a row for details</span></div>`+
		`%s`+
		`<div class="table-wrap"><table class="`+tableClass+`"><thead><tr><th>name</th><th>source</th><th>tags</th>`+actionHead+`</tr></thead>
<tbody>%s</tbody></table></div>`,
		blurb, contributed, bar, shown, plural(shown), orgNote, noteHTML, body)
	if grouped {
		content += techniquesGroupScript
	}
	// The last crumb names the current view and is the dropdown that switches to
	// the others: Techniques / All, Techniques / Retired, Techniques / Tags.
	crumbs := []crumb{{label: "Playbook", href: playbookHome}, {label: techniquesViewLabel(navActive), href: ""}}
	return page{active: "techniques", crumbs: crumbs, content: content, viewMenu: viewMenu}
}

// techniquesGroupScript makes the by-area headings collapse toggles: areas render
// collapsed, and clicking a heading shows or hides its members (matched by the
// data-member id). Included only on the grouped view.
const techniquesGroupScript = `<script>(function(){
document.querySelectorAll('tr.techniques-group').forEach(function(h){
  h.addEventListener('click',function(){
    var a=h.getAttribute('data-area'), open=h.getAttribute('aria-expanded')==='true';
    h.setAttribute('aria-expanded',open?'false':'true');
    document.querySelectorAll('tr.techniques-member[data-member="'+a+'"]').forEach(function(r){r.hidden=open;});
  });
});
})();</script>`

// capListRow is one rendered row of the techniques list: its cells pre-escaped,
// plus the raw id (to join against the cluster graph's nodes) and the search key
// the top-bar filter matches against.
type capListRow struct{ searchKey, id, rawid, name, scope, source, tags, restore string }

// groupedTechniqueRows renders the technique rows grouped into areas of practice —
// the communities the shared-tag graph finds, the same ones the Map draws — with
// each area's adoption funnel rolled up in its header. Areas lead with the ones
// gaining the most adoption; techniques the graph places in no community fall
// into a final "Ungrouped" band, so the list still accounts for every row.
//
// The header row carries a data-search built from its members, so the top-bar
// filter hides an area heading exactly when it hides all of its techniques.
func groupedTechniqueRows(g techmap.Graph, clusters []techmap.Cluster, rowByID map[string]capListRow, render func(capListRow, string) string) string {
	type area struct {
		id              string
		name            string
		adopted, helped int
		ids             []string
		search          string
	}
	inCluster := map[string]bool{}
	areas := make([]area, 0, len(clusters))
	for _, c := range clusters {
		if c.Ungrouped {
			continue // its members fall through to the Ungrouped band below, kept last
		}
		a := area{id: "a" + strconv.Itoa(c.ID), name: c.Name}
		for _, m := range c.Members {
			nd := g.Nodes[m]
			a.ids = append(a.ids, nd.ID)
			a.adopted += nd.Adopt
			a.helped += nd.Helped
			inCluster[nd.ID] = true
			if row, ok := rowByID[nd.ID]; ok {
				a.search += row.searchKey + " "
			}
		}
		if len(a.ids) > 0 {
			areas = append(areas, a)
		}
	}
	sort.SliceStable(areas, func(i, j int) bool {
		if areas[i].adopted != areas[j].adopted {
			return areas[i].adopted > areas[j].adopted
		}
		return len(areas[i].ids) > len(areas[j].ids)
	})
	// Techniques the graph put in no community — kept, never dropped.
	ung := area{id: "ung", name: "Ungrouped"}
	for _, nd := range g.Nodes {
		if inCluster[nd.ID] {
			continue
		}
		ung.ids = append(ung.ids, nd.ID)
		ung.adopted += nd.Adopt
		ung.helped += nd.Helped
		if row, ok := rowByID[nd.ID]; ok {
			ung.search += row.searchKey + " "
		}
	}

	var b strings.Builder
	emit := func(a area, quiet bool) {
		if len(a.ids) == 0 {
			return
		}
		rate := "—"
		if a.adopted > 0 {
			rate = fmt.Sprintf("%.0f%% helped", float64(a.helped)/float64(a.adopted)*100)
		}
		cls := "techniques-group"
		if quiet {
			cls += " techniques-group-quiet"
		}
		// The heading row is a collapse toggle — collapsed by default; its members
		// carry data-member=<area id> so the script can show/hide them together.
		fmt.Fprintf(&b, `<tr class="%s" data-area="%s" aria-expanded="false" data-search="%s"><td colspan="3"><span class="techniques-group-caret" aria-hidden="true"></span><span class="techniques-group-name">%s</span><span class="techniques-group-meta">%d technique%s · %d adopted · %s</span></td></tr>`,
			cls, a.id, a.search, html.EscapeString(a.name), len(a.ids), plural(len(a.ids)), a.adopted, rate)
		member := fmt.Sprintf(` class="techniques-member" data-member="%s" hidden`, a.id)
		for _, id := range a.ids {
			if row, ok := rowByID[id]; ok {
				b.WriteString(render(row, member))
			}
		}
	}
	for _, a := range areas {
		emit(a, false)
	}
	emit(ung, true)
	return b.String()
}

// hasTag reports whether tags contains want (case-insensitive).
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// tagLinks renders a technique's tags as links to each tag's view — /outcomes/tag/<tag>,
// the page that carries the tag's outcomes and the techniques that share it. The
// tag matching the active filter (if any) is highlighted. Empty tags render as an
// em dash so the column never looks broken.
func tagLinks(tags []string, active string) string {
	if len(tags) == 0 {
		return "—"
	}
	var b strings.Builder
	b.WriteString(`<div class="tags">`)
	for _, t := range tags {
		class := "tag"
		if active != "" && strings.EqualFold(t, active) {
			class = "tag active"
		}
		fmt.Fprintf(&b, `<a class="%s" href="/outcomes/tag/%s">%s</a>`,
			class, url.PathEscape(t), html.EscapeString(t))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// sourceCell renders a technique's origin for the techniques table: the
// provenance word (curated / contributed / suggested / federated / mined),
// linked to the source when that's a URL — suggested and federated techniques carry
// the doc or feed URL, while curated (a git path) and contributed do not.
func sourceCell(technique models.Technique) string {
	label := technique.Provenance
	if label == "" {
		label = "curated"
	}
	src := strings.TrimSpace(technique.Source)
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener">%s</a>`,
			html.EscapeString(src), html.EscapeString(label))
	}
	return html.EscapeString(label)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func techniqueRow(label, contentHTML string) string {
	return fmt.Sprintf(`<div class="field"><span class="k">%s</span><div class="v">%s</div></div>`,
		label, contentHTML)
}

func techniqueField(label, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return techniqueRow(label, html.EscapeString(value))
}

// techniqueDetailHero is the canonical presentation of a live technique. It gives the
// instruction itself visual priority, then keeps context and provenance close
// enough to scan without turning the technique into a label/value form.
// commons is the technique's standing in the computed Public channel, rendered by
// Server.publicStandingFor and threaded in because this is a free function: the
// answer to "is this technique being shared with the world?" belongs on the technique
// itself, not only in the aggregate Federation view.
func techniqueDetailHero(technique models.Technique, versionCount int, commons string) string {
	scope := technique.Scope
	if scope == "" {
		scope = "general"
	}
	status := strings.TrimSpace(technique.Status)
	if status == "" {
		status = "stable"
	}
	provenance := strings.TrimSpace(technique.Provenance)
	if provenance == "" {
		provenance = "curated"
	}

	var context strings.Builder
	if v := strings.TrimSpace(technique.AppliesWhen); v != "" {
		fmt.Fprintf(&context, `<div class="technique-context good-context"><span>Use when</span><p>%s</p></div>`, html.EscapeString(v))
	}
	if v := strings.TrimSpace(technique.NotWhen); v != "" {
		fmt.Fprintf(&context, `<div class="technique-context bad-context"><span>Avoid when</span><p>%s</p></div>`, html.EscapeString(v))
	}
	contextHTML := ""
	if context.Len() > 0 {
		contextHTML = `<div class="technique-contexts">` + context.String() + `</div>`
	}

	recipeHTML := ""
	if recipe := strings.TrimSpace(technique.Recipe); recipe != "" {
		recipeHTML = `<section class="technique-recipe"><div class="technique-section-label">How to apply</div><pre>` + html.EscapeString(recipe) + `</pre></section>`
	}
	tagsHTML := ""
	if len(technique.Tags) > 0 {
		tagsHTML = `<div class="technique-detail-tags">` + tagLinks(technique.Tags, "") + `</div>`
	}
	historyHTML := ""
	if versionCount > 0 {
		historyHTML = fmt.Sprintf(`<a href="/techniques/history/%s">%d prior version%s</a>`,
			html.EscapeString(technique.ID), versionCount, plural(versionCount))
	}
	meta := []string{`<code>` + html.EscapeString(technique.ID) + `</code>`}
	if technique.Version > 0 {
		meta = append(meta, fmt.Sprintf("version %d", technique.Version))
	}
	meta = append(meta, html.EscapeString(provenance))
	if src := strings.TrimSpace(technique.Source); src != "" {
		meta = append(meta, html.EscapeString(src))
	}
	if historyHTML != "" {
		meta = append(meta, historyHTML)
	}
	// The lifecycle action this page offers. A serving technique can go back to
	// drafts. One under evaluation gets the decision it is waiting for: the
	// reviewer reaches this page from the Under-evaluation queue to read the
	// technique in full — the description, the recipe, the two contexts the row
	// cannot show — and the whole point of reading it is to accept or reject it.
	// Sending them back to the queue to act on what they just read splits one
	// decision across two pages.
	//
	// Both post to the same routes the queue's buttons use, and
	// handleAdminTechniqueAction pins each action to the one status it may act
	// on, so a stale page cannot flip anything.
	draftAction := ""
	switch technique.Status {
	case "stable":
		draftAction = fmt.Sprintf(`<form class="technique-detail-action" method="post" action="/admin/techniques/to-draft/%s" onsubmit="return confirm('Return this technique to drafts? %s does not suggest it again until you promote it.');">`+
			`<button class="btn" type="submit">Return to drafts</button></form>`, html.EscapeString(technique.ID), html.EscapeString(product.Name()))
	case "shadow":
		cid := html.EscapeString(technique.ID)
		draftAction = fmt.Sprintf(
			`<form class="technique-detail-action" method="post" action="/admin/techniques/shadow-promote/%s">`+
				`<button class="btn promote" type="submit" `+
				`title="Start to serve this technique in retrieval.">Accept</button></form>`+
				`<form class="technique-detail-action" method="post" action="/admin/techniques/shadow-reject/%s">`+
				`<button class="btn reject" type="submit" `+
				`title="Retire this technique. It stops gathering evidence and is never served.">Reject</button></form>`,
			cid, cid)
	}
	federationHTML := publicationDisclosure(technique, commons)
	descriptionHTML := ""
	if description := strings.TrimSpace(technique.Description); description != "" {
		descriptionHTML = `<p class="technique-lede">` + html.EscapeString(description) + `</p>`
	}

	return fmt.Sprintf(`<article class="technique-detail">
<header class="technique-detail-head">
<div class="technique-badges">%s<span class="technique-status">%s</span></div>
<h1>%s</h1>%s
</header>
%s%s
<footer class="technique-detail-foot">%s<div class="technique-detail-end"><div class="technique-detail-meta">%s</div>%s</div>%s</footer>
</article>`, scopeLink(scope, ""), html.EscapeString(status), html.EscapeString(technique.Name), descriptionHTML,
		recipeHTML, contextHTML, tagsHTML, strings.Join(meta, `<span aria-hidden="true">·</span>`), draftAction, federationHTML)
}

func techniqueBlock(technique models.Technique, outcome *models.Outcome, actionsHTML string) string {
	// Tags link to the techniques list filtered to that tag (chips, like the
	// list view), so a member can pivot from one technique to its whole area.
	tagsHTML := ""
	if len(technique.Tags) > 0 {
		tagsHTML = techniqueRow("Tags", tagLinks(technique.Tags, ""))
	}
	recipe := strings.TrimSpace(technique.Recipe)
	recipeHTML := ""
	if recipe != "" {
		recipeHTML = techniqueRow("Recipe", "<pre>"+html.EscapeString(recipe)+"</pre>")
	}
	var meta []string
	meta = append(meta, "<code>"+html.EscapeString(technique.ID)+"</code>")
	if technique.Version > 0 {
		meta = append(meta, fmt.Sprintf("v%d", technique.Version))
	}
	for _, v := range []string{technique.Status, technique.Provenance, technique.Source} {
		if v != "" {
			meta = append(meta, html.EscapeString(v))
		}
	}
	stats := ""
	if outcome != nil && outcome.HelpedRate != nil {
		ar := 0.0
		if outcome.AdoptionRate != nil {
			ar = *outcome.AdoptionRate
		}
		stats = techniqueField("Outcomes", fmt.Sprintf("helped %.0f%% · adopted %.0f%% · n=%d",
			*outcome.HelpedRate*100, ar*100, outcome.SampleSize))
	}
	descHTML := ""
	if d := strings.TrimSpace(technique.Description); d != "" {
		descHTML = `<div class="desc">` + html.EscapeString(d) + `</div>`
	}
	actions := ""
	if actionsHTML != "" {
		actions = `<div class="actions">` + actionsHTML + `</div>`
	}
	scope := technique.Scope
	if scope == "" {
		scope = "general"
	}
	return fmt.Sprintf(`<div class="techniqueblock">
<div class="head"><h3>%s</h3><span class="scope">%s</span></div>
<div class="meta">%s</div>
%s
%s
%s
%s
%s
%s
%s
</div>`, html.EscapeString(technique.Name), scopeLink(scope, ""), strings.Join(meta, " · "),
		descHTML, recipeHTML, techniqueField("Applies when", technique.AppliesWhen),
		techniqueField("Not when", technique.NotWhen), tagsHTML, stats, actions)
}

func (s *Server) pageTechniqueDetail(r *http.Request, user oidc.Claims) page {
	id, _ := url.PathUnescape(r.PathValue("id"))
	technique, ok, err := s.Store.GetTechnique(id)
	if err != nil || !ok {
		return page{status: 404, active: "techniques",
			content: "<p>No technique with that ID. <a href=\"/techniques\">All techniques.</a></p>"}
	}
	versionCount := 0
	if versions, err := s.Store.TechniqueVersions(id); err == nil && len(versions) > 0 {
		versionCount = len(versions)
	}
	windowNav := ""
	metrics := `<section class="panel technique-metrics-unavailable"><h2>Performance</h2><p class="empty">Outcome data is temporarily unavailable.</p></section>`
	if events, err := s.Store.AllEvents(""); err == nil {
		now := time.Now().UTC()
		earliest := insights.Earliest(events)
		w := insights.WindowByKey(r.URL.Query().Get("w"), now, earliest)
		windowNav = windowSelect("/techniques/"+url.PathEscape(technique.ID), w.Key, now, earliest)
		metrics = s.techniqueMetrics(r, technique, events)
	}
	return page{active: "techniques",
		crumbs:  techniqueCrumbs(technique.ID, technique.Name, technique.Status, "Overview", versionCount),
		content: windowNav + `<div class="technique-page">` + techniqueDetailHero(technique, versionCount, s.publicStandingFor(technique)) + metrics + `</div>`}
}

// publicationDisclosure keeps a technique's explicit publish/withdraw control with
// its metadata without spending a full page panel on an occasional operation.
// Drafts are held out of feeds, so they get no control.
func publicationDisclosure(technique models.Technique, commons string) string {
	if technique.Status == "draft" {
		return ""
	}
	cid := html.EscapeString(technique.ID)
	state := "Ready to publish to a federation feed."
	summaryState := "Not published"
	if len(technique.Channels) > 0 {
		channels := html.EscapeString(strings.Join(technique.Channels, ", "))
		state = "Published to <strong>" + channels + "</strong>."
		summaryState = channels
	}
	value := strings.Join(technique.Channels, ", ")
	if value == "" {
		value = "general"
	}
	verb := "Publish"
	if len(technique.Channels) > 0 {
		verb = "Update channels"
	}
	unpublish := ""
	if len(technique.Channels) > 0 {
		unpublish = fmt.Sprintf(`<form method="post" action="/admin/techniques/channels/%s"><button class="reject" type="submit">Unpublish</button></form>`, cid)
	}
	return fmt.Sprintf(`<details class="fineprint technique-federation"><summary><span>Federation</span><span class="technique-federation-state">%s</span></summary>`+
		`<p class="sub">%s Feeds update immediately (<a href="/federation">federation</a>).</p>`+
		commons+
		`<div class="pub-row"><form class="pub-form" method="post" action="/admin/techniques/channels/%s">`+
		`<input type="hidden" name="do" value="publish">`+
		`<label class="pub-label" for="pub-ch">Channels</label>`+
		`<input id="pub-ch" type="text" name="channels" value="%s" placeholder="comma-separated">`+
		`<button type="submit" class="promote">%s</button></form>%s</div></details>`,
		summaryState, state, cid, html.EscapeString(value), verb, unpublish)
}

func draftActions(cid string) string {
	// The title states promotion's full consequence: serving now, and — under
	// evidence-gated autonomy — a path to silent agent application once the
	// technique's measured record clears the operator's gate. Returning a technique to
	// draft revokes both at once. Shadow is the middle path: retrieved and
	// judged for relevance but never surfaced, so it gathers evidence with zero
	// member exposure (docs/learning/validation-without-review.md).
	return fmt.Sprintf(`<form method="post" action="/admin/techniques/promote/%s"><button class="promote" type="submit" `+
		`title="Make this technique available in retrieval. If agent autonomy is on, measured evidence can make it eligible for automatic use in autonomous sessions.">Accept</button></form>`+
		`<form method="post" action="/admin/techniques/to-shadow/%s"><button type="submit" `+
		`title="%s retrieves and checks this technique for relevance without showing it to members. This collects fit evidence before promotion.">Shadow</button></form>`+
		`<form method="post" action="/admin/techniques/reject/%s"><button class="reject" type="submit">Reject</button></form>`, cid, cid, html.EscapeString(product.Name()), cid)
}

func (s *Server) pageDraftDetail(r *http.Request, user oidc.Claims) page {
	id, _ := url.PathUnescape(r.PathValue("id"))
	technique, ok, err := s.Store.GetTechnique(id)
	if err != nil || !ok || technique.Status != "draft" {
		return page{status: 404, active: "review",
			crumbs:  []crumb{{label: "Review", href: "/review"}},
			content: "<p>No draft with that ID in the review queue. <a href=\"/review\">Back to the review queue.</a></p>"}
	}
	kind := `<div class="sub">Draft in the review queue</div>`
	if technique.Supersedes != "" {
		kind = `<div class="sub">Revision draft in the review queue</div>`
	}
	// The tag field autocompletes against the live vocabulary, so the reviewer
	// reuses the org's language by default instead of coining beside it.
	var vocab []tagStat
	if all, err := s.Store.ListTechniques(nil, 0); err == nil {
		vocab = liveTagVocabulary(all)
	}
	return page{active: "review",
		crumbs: []crumb{{label: "Review", href: "/review"}, {label: technique.Name, href: ""}},
		content: kind + s.revisionBlock(technique) +
			techniqueBlock(technique, nil, draftActions(html.EscapeString(technique.ID))) +
			draftEditForm(technique, vocab)}
}

// revisionBlock renders reviewer context for a revision draft
// (docs/design/revision-design.md): what it supersedes, the proposer's note, a
// staleness warning, and a field diff against the CURRENT base — the delta a
// promote would actually apply.
func (s *Server) revisionBlock(draft models.Technique) string {
	if draft.Supersedes == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="panel"><h2>Revision of <a href="/techniques/%s">%s</a></h2>`,
		html.EscapeString(draft.Supersedes), html.EscapeString(draft.Supersedes))
	if draft.RevisionNote != "" {
		fmt.Fprintf(&b, `<p class="sub">Why: %s</p>`, html.EscapeString(draft.RevisionNote))
	}
	base, ok, err := s.Store.GetTechnique(draft.Supersedes)
	switch {
	case err != nil || !ok:
		b.WriteString(`<p><strong>The base technique no longer exists.</strong> This revision can only be rejected.</p>`)
	case base.Version != draft.BaseVersion:
		fmt.Fprintf(&b, `<p><strong>Stale:</strong> drafted against v%d. The technique is now v%d. Promotion will be refused; request a new revision.</p>`,
			draft.BaseVersion, base.Version)
	default:
		fmt.Fprintf(&b, `<p class="sub">This draft targets the current version (v%d). Changed fields:</p>`, base.Version)
	}
	if ok {
		diff := func(label, oldV, newV string) {
			if oldV == newV {
				return
			}
			fmt.Fprintf(&b, `<h3>%s</h3><p class="diff-del">− %s</p><p class="diff-add">+ %s</p>`,
				label, html.EscapeString(oldV), html.EscapeString(newV))
		}
		diff("Name", base.Name, draft.Name)
		diff("Description", base.Description, draft.Description)
		diff("Recipe", base.Recipe, draft.Recipe)
		diff("Applies when", base.AppliesWhen, draft.AppliesWhen)
		diff("Not when", base.NotWhen, draft.NotWhen)
		diff("Before/after", base.BeforeAfter, draft.BeforeAfter)
		diff("Tags", strings.Join(base.Tags, ", "), strings.Join(draft.Tags, ", "))
	}
	b.WriteString(`</section>`)
	return b.String()
}

// techniqueList is what the collect pass produces: the rows the table renders,
// the live models the by-area grouping clusters, and the counts the nav and the
// empty states need.
type techniqueList struct {
	rows []capListRow
	// live is the set that survived the filter, kept as models for the by-area
	// grouping — it clusters exactly this set, so the areas match the filter.
	live         []models.Technique
	scopesSeen   map[string]int
	shown        int
	retiredCount int
}

// collectTechniqueRows applies the filters and builds one row per surviving
// technique. It reads nothing but the techniques handed to it: the row used to
// fetch each technique's overall outcome as well, purely to fold
// "helped 42% · n=17" into the hidden search string — one store read per row,
// for text no cell displayed and no test asserted.
//
// Rows are collected before they are rendered so the table can decide whether
// the scope column has earned its width: with every live technique "general" —
// the usual case — the column is a constant, and a column that says the same
// thing on every row spends the reader's eye for nothing. The org-scoped
// exceptions keep their badge on the name instead.
func collectTechniqueRows(techniques []models.Technique, filters organizationFilters,
	statusFilter, tagFilter string, archive bool) techniqueList {

	out := techniqueList{scopesSeen: map[string]int{}}
	for _, technique := range techniques {
		if technique.Status == "draft" {
			continue // drafts live under Review, not the technique list
		}
		if technique.Status == "shadow" {
			continue // under evaluation, never surfaced — listed under Review, not the live set
		}
		if technique.Status == "retired" {
			out.retiredCount++
			if statusFilter != "retired" {
				continue // the archive is one click away, not in the default count
			}
		} else if statusFilter == "retired" {
			continue
		}
		techniqueScope := technique.Scope
		if techniqueScope == "" {
			techniqueScope = "general"
		}
		// Within a dimension the selected values union; across dimensions they
		// intersect — the same rule the filter uses on Outcomes.
		if len(filters.Scope) > 0 && !containsFold(filters.Scope, techniqueScope) {
			continue
		}
		if len(filters.Provenance) > 0 && !containsFold(filters.Provenance, technique.Provenance) {
			continue
		}
		if len(filters.Tag) > 0 {
			matched := false
			for _, t := range technique.Tags {
				if containsFold(filters.Tag, t) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		out.shown++
		tags := strings.Join(technique.Tags, ", ")
		// Search still spans the full technique (incl. fields not shown in the row).
		searchKey := html.EscapeString(strings.ToLower(strings.Join([]string{
			technique.ID, technique.Name, technique.Status, techniqueScope, technique.Provenance, technique.Source, tags,
			technique.Description, technique.AppliesWhen, technique.NotWhen}, " ")))
		cid := html.EscapeString(technique.ID)
		restore := ""
		if statusFilter == "retired" {
			// Bring a technique back from retirement — the mirror of a draft's
			// Promote, one click, guarded server-side to retired techniques.
			restore = fmt.Sprintf(`<td class="row-action"><form method="post" action="/admin/techniques/restore/%s"><button class="btn btn-primary" type="submit">Restore</button></form></td>`, cid)
		}
		out.scopesSeen[techniqueScope]++
		out.rows = append(out.rows, capListRow{
			searchKey: searchKey, id: cid, rawid: technique.ID, name: html.EscapeString(technique.Name), scope: techniqueScope,
			source: sourceCell(technique), tags: tagLinks(technique.Tags, tagFilter), restore: restore})
		if !archive {
			out.live = append(out.live, technique)
		}
	}
	return out
}
