// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/models"
)

// A heatmap column is an area, never an individual technique: techniques outside
// every Playbook-map community pool into one "Ungrouped" column. The map
// itself gets the same bucket, so no node is ever groupless.
func TestHeatmapNeverShowsTechniqueColumns(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	mk := func(id, name string, tags []string) {
		if err := srv.Store.UpsertTechnique(models.Technique{ID: id, Name: name, Status: "stable", Scope: "org",
			Tags: tags, CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	mk("rev-a", "Review checklist", []string{"review", "audit"})
	mk("rev-b", "Audit sweep", []string{"review", "audit"})
	mk("solo", "Solo deploy trick", []string{"one-of-a-kind"})
	adopt := func(technique, team string, i int) {
		e := models.FeedbackEvent{EventID: fmt.Sprintf("map-%s-%s-%d", technique, team, i), TechniqueID: technique,
			Stage: "adopted", Segment: models.Segment{"team": team}, Confidence: "explicit",
			CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}
		if _, err := srv.Store.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	adopt("rev-a", "platform", 0)
	adopt("solo", "payments", 0)
	adopt("rev-b", "payments", 1)

	_, body := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team")
	if !strings.Contains(body, `>Ungrouped</a>`) {
		t.Fatal("heatmap should carry an Ungrouped column")
	}
	if strings.Contains(body, `>Solo deploy trick</a><span class="head-total"`) {
		t.Fatal("an unclustered technique must pool into Ungrouped, not become its own column")
	}

	// The Playbook map carries the same bucket in its graph data.
	_, mp := fetchHTML(t, ts.URL+"/techniques/map")
	if !strings.Contains(mp, `"ungrouped":true`) {
		t.Fatal("the map graph should include the synthetic Ungrouped cluster")
	}
}

func TestOrganizationRouteNavigationAndEmptyState(t *testing.T) {
	_, ts := newServer(t)
	seedOneEvent(t, ts)
	status, body := fetchHTML(t, ts.URL+"/outcomes?w=7d")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{
		`href="/outcomes" class="active"`,
		// The root is now the Overview view in the peer-view switcher (Outcomes ›
		// Overview▾), like Playbook › All — so the current-page marker rides the
		// dropdown summary, and "Outcomes" becomes the link back to it.
		`aria-current="page">Overview`,
		`<details class="crumb-menu">`,
		// The page lede was dropped in the control-strip redesign; the hero panel
		// it restated now carries the page's self-description.
		`<div class="outcomes-controls">`,
		`How ` + product.Name() + ` helps`,
		`Adoption by cohort and area`,
		`No activity has cohort data`,
		`value="/outcomes?w=7d" selected`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("organization page missing %q", want)
		}
	}
}

// Data tables render with the shared client sort wiring so every column header
// is a sort control (unsorted → ascending → descending). The behaviour is
// client-side JS; this guards that a real table page ships both the table and
// the script that makes its headers sortable.
func TestDataTablesAreSortable(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "sortme", Name: "Sort me", Status: "stable", Scope: "org",
		CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		for _, st := range []string{"shown", "adopted", "helped"} {
			e := models.FeedbackEvent{EventID: fmt.Sprintf("sort-%s-%d", st, i), TechniqueID: "sortme", Stage: st,
				Segment: models.Segment{"team": "platform"}, Confidence: "explicit",
				CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}
			if _, err := srv.Store.InsertEvent(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, body := fetchHTML(t, ts.URL+"/outcomes/cohorts?w=30d")
	if !strings.Contains(body, `class="data-table"`) {
		t.Fatal("expected a sortable data-table on the cohorts page")
	}
	for _, marker := range []string{"dataset.sortidx", "aria-sort", "th.classList.add('sortable')"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("column-sort wiring missing marker %q", marker)
		}
	}
}

// The tags/cohort grouping choice is a persisted lens (like the time window):
// it drives both the map's arrangement and the All list's grouping, and travels
// across those views via ?group= and the localStorage init script.
func TestGroupingTravelsAcrossViews(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	mk := func(id string) {
		if err := srv.Store.UpsertTechnique(models.Technique{ID: id, Name: id, Status: "stable", Scope: "org",
			CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	mk("cap-plat")
	mk("cap-pay")
	adopt := func(technique, team string) {
		for i := 0; i < 3; i++ {
			e := models.FeedbackEvent{EventID: fmt.Sprintf("%s-%s-%d", technique, team, i), TechniqueID: technique, Stage: "adopted",
				Segment: models.Segment{"team": team}, Confidence: "explicit", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}
			if _, err := srv.Store.InsertEvent(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	adopt("cap-plat", "platform")
	adopt("cap-pay", "payments")

	// The All list, grouped by cohort, is banded by the adopting teams.
	_, all := fetchHTML(t, ts.URL+"/techniques?group=cohort")
	if !strings.Contains(all, `data-group="cohort"`) {
		t.Fatal("All view grouping control not set to cohort")
	}
	for _, area := range []string{"Platform", "Payments"} {
		if !strings.Contains(all, area) {
			t.Fatalf("All view (cohort grouping) missing the %q cohort band", area)
		}
	}

	// The map reflects the same choice in its arrange control, and the grouping
	// lives in the URL (no localStorage, no redirect-on-load): the view-switcher
	// carries ?group=cohort so the choice follows to the All view.
	_, mp := fetchHTML(t, ts.URL+"/techniques/map?group=cohort")
	if !strings.Contains(mp, `data-group="cohort"`) || !strings.Contains(mp, `data-mode="cohort" class="active"`) {
		t.Fatal("map arrange control not set to cohort")
	}
	if strings.Contains(mp, "tacit-grouping") {
		t.Fatal("grouping should be URL-state now, not localStorage (tacit-grouping)")
	}
	if !strings.Contains(mp, `href="/techniques?group=cohort"`) {
		t.Fatal("view-switcher must carry the grouping to the All view via ?group=cohort")
	}
	// The visible Areas panel must match the arrangement the page loads in —
	// otherwise the panel names tag areas while the map labels cohort areas.
	if strings.Contains(mp, `<div id="cmap-areas-cohort" hidden>`) || !strings.Contains(mp, `<div id="cmap-areas-tags" hidden>`) {
		t.Fatal("cohort-loaded map should show the cohort Areas panel, not the tag one")
	}
	_, mpTags := fetchHTML(t, ts.URL+"/techniques/map?group=tags")
	if strings.Contains(mpTags, `<div id="cmap-areas-tags" hidden>`) || !strings.Contains(mpTags, `<div id="cmap-areas-cohort" hidden>`) {
		t.Fatal("tags-loaded map should show the tag Areas panel, not the cohort one")
	}
}

func TestOrganizationFullFidelityMatrixAndFindings(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	// The event-carrying technique clusters with a buddy (two shared tags; the tie
	// between them breaks alphabetically, so the cluster is named "incident"),
	// and a tagless solo technique pools into Ungrouped — two columns, enough for
	// the matrix to draw.
	technique := models.Technique{ID: "org-map-test", Name: `Warehouse <bridge>`, Status: "stable", Scope: "org", Tags: []string{"incident", "incident-drill"}, CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)}
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	for _, buddy := range []models.Technique{
		{ID: "org-map-buddy", Name: "Buddy", Status: "stable", Scope: "org", Tags: []string{"incident", "incident-drill"}, CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)},
		{ID: "org-map-solo", Name: "Solo", Status: "stable", Scope: "org", CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339Nano)},
	} {
		if err := srv.Store.UpsertTechnique(buddy); err != nil {
			t.Fatal(err)
		}
	}
	add := func(team, stage string, n int, confidence string) {
		for i := 0; i < n; i++ {
			e := models.FeedbackEvent{EventID: fmt.Sprintf("org-%s-%s-%d", team, stage, i), TechniqueID: technique.ID,
				Stage: stage, Segment: models.Segment{"team": team, "chapter": `AI & Safety`}, Confidence: confidence,
				CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}
			if _, err := srv.Store.InsertEvent(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("platform", "shown", 4, "explicit")
	add("platform", "adopted", 3, "explicit")
	add("platform", "helped", 2, "explicit")
	add("platform", "dismissed", 1, "inferred")
	add(`R&D <west>`, "shown", 1, "inferred")
	add("signal-only", "adopted", 1, "inferred")

	status, body := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	for _, want := range []string{
		`class="org-heatmap"`, `class="org-heatmap-table"`, `role="region"`,
		`aria-label="Heatmap legend"`, `helped rate 0–100%`, `adoption volume`, `inferred evidence`, `mixed evidence`,
		`R&amp;D &lt;west&gt;`, `incident`,
		`R&amp;D &lt;west&gt;, incident: 1 shown, 0 adopted, 0 helped, 0 explicit, 0 inferred`,
		`platform, incident: 4 shown, 3 adopted, 2 helped, 5 explicit, 1 inferred`,
		`class="heat-cell mixed-evidence"`,
		`<a class="cell-value" href="/outcomes/cohorts/`, // active cells are links, not dead tab stops,
		`class="heat-cell inferred-only"`, `style="--helped:66.7%;--volume:100.0%"`,
		`Cohort adoption gaps`, `Cohorts with helped outcomes`,
		`0 of 1 suggestions adopted here`, // full-fidelity sparse target remains represented
		`href="/outcomes?dimension=chapter&amp;w=30d#cohorts"`,
	} {
		if !strings.Contains(body, want) {
			// Errorf, not Fatalf: a copy edit that moves one of these should
			// report every string it moved, not the first one.
			t.Errorf("populated organization page missing %q", want)
		}
	}
	if strings.Count(body, `class="org-heatmap-table"`) != 1 || strings.Contains(body, `<svg class="org-map"`) || strings.Contains(body, `org-matrix`) {
		t.Fatal("organization page does not contain one canonical heatmap table")
	}
	// The matrix totals both axes: per-cohort (the total column) and
	// per-area (in each column header), with the grand total under the
	// "total" header where the axes meet.
	if !strings.Contains(body, `class="head-total"`) ||
		!strings.Contains(body, `grand total `) {
		t.Fatal("heatmap missing per-area totals in the column headers")
	}
	if strings.Contains(body, "<tfoot>") {
		t.Fatal("totals row regrew — totals live in the headers now")
	}
	for _, raw := range []string{`R&D <west>`} {
		if strings.Contains(body, raw) {
			t.Fatalf("unescaped label leaked: %q", raw)
		}
	}
	_, chapterBody := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=chapter")
	if !strings.Contains(chapterBody, `AI &amp; Safety`) {
		t.Fatal("arbitrary chapter cohort was not rendered")
	}
}

func TestOrganizationRequestedLensAndWindowArePreserved(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	technique := models.Technique{ID: "org-lens-test", Name: "Lens", Status: "stable", Tags: []string{"testing"}}
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	_, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: "org-lens-event", TechniqueID: technique.ID, Stage: "shown",
		Segment: models.Segment{"team": "one", "role": "maintainer"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	_, body := fetchHTML(t, ts.URL+"/outcomes?w=90d&dimension=role")
	for _, want := range []string{`dimension=role`, `aria-current="page"`, `>role <span>1</span>`, `value="/outcomes?dimension=role&amp;w=90d" selected`, `role cohort`,
		`<span class="filters-label">Filters</span>`,
		`<span class="lens-label">Group by</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("requested lens/window missing %q", want)
		}
	}

	// A control must sit with what it changes. The FILTERS scope the whole page,
	// including the numbers above the fold, so they head the page. The LENS
	// regroups only the cohort block — after the merge it changes nothing a
	// reader can see without scrolling past four panels that ignore it — so it
	// heads that block, and nowhere else.
	filterBar := body[strings.Index(body, `class="organization-filters"`):]
	filterBar = filterBar[:strings.Index(filterBar, "</form>")]
	if strings.Contains(filterBar, "lens-nav") {
		t.Fatal("the group-by control is back in the page-level filter bar, above everything it does not affect")
	}
	band := body[strings.Index(body, `class="band-head"`):]
	if !strings.Contains(band[:400], "lens-nav") {
		t.Fatal("the group-by control does not head the cohort block it governs")
	}
	// The band is the anchor, so changing the grouping returns the reader to the
	// section rather than dumping them at the top of the page.
	if !strings.Contains(body, `class="band-head" id="cohorts"`) ||
		!strings.Contains(body, `#cohorts"`) {
		t.Fatal("changing the grouping does not return the reader to the cohorts")
	}
	// And it still sits above the map, not inside its header.
	if head := body[strings.Index(body, `class="panel organization-map-panel"`):]; strings.Contains(head[:600], "lens-nav") {
		t.Fatal("lens nav regrew inside the map panel header")
	}
}

func TestOrganizationTechniqueFiltersAndPersistence(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	// Each filtered technique gets a tag-sharing buddy so it lands in its own map
	// cluster ("warehouse" / "diagram"), keeping the two techniques' areas distinct.
	techniques := []models.Technique{
		{ID: "filter-org", Name: "Org filtered move", Status: "stable", Scope: "org", Provenance: "contributed", Tags: []string{"warehouse"}},
		{ID: "filter-org-buddy", Name: "Org buddy", Status: "stable", Scope: "org", Provenance: "contributed", Tags: []string{"warehouse"}},
		{ID: "filter-general", Name: "General excluded move", Status: "stable", Scope: "general", Provenance: "curated", Tags: []string{"diagram"}},
		{ID: "filter-general-buddy", Name: "General buddy", Status: "stable", Scope: "general", Provenance: "curated", Tags: []string{"diagram"}},
	}
	for _, technique := range techniques {
		if err := srv.Store.UpsertTechnique(technique); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: "event-" + technique.ID, TechniqueID: technique.ID, Stage: "shown", Segment: models.Segment{"team": "platform"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	_, body := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team&scope=org&tag=warehouse&provenance=contributed&technique=filter-org")
	for _, want := range []string{
		// popover checkboxes carry the selection state
		`<label class="fmenu-opt"><input type="checkbox" name="scope" value="org" checked>`,
		`<input type="checkbox" name="tag" value="warehouse" checked>`,
		`<input type="checkbox" name="provenance" value="contributed" checked>`,
		`<input type="checkbox" name="technique" value="filter-org" checked>`,
		// summaries show "selected of total"
		` of `,
		// active values restated as removable chips
		`<span class="fchip">tag: warehouse<a href=`,
		`<span class="fchip">Org filtered move<a href=`,
		// the window select preserves the whole filter set
		`value="/outcomes?dimension=team&amp;provenance=contributed&amp;scope=org&amp;tag=warehouse&amp;technique=filter-org&amp;w=30d" selected`,
		`>Clear</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("filtered organization page missing %q", want)
		}
	}
	// Area labels are the map's cluster names, which default to tag names — and
	// raw tags always appear in the filter menus. Scope the area check to the
	// momentum panel, which carries only the filtered report's areas.
	momentum := body[strings.Index(body, "Areas gaining adoption"):]
	momentum = momentum[:strings.Index(momentum, "</section>")]
	if !strings.Contains(momentum, "warehouse") {
		t.Fatal("filtered report lost the org technique's area")
	}
	if strings.Contains(momentum, "diagram") {
		t.Fatal("general technique leaked into org-scoped report")
	}
	// The summary names the dimension size and the selection within it.
	if !regexp.MustCompile(`<summary><b>1 of \d+</b> Tags</summary>`).MatchString(body) {
		t.Fatal("tag summary missing the selected-of-total count")
	}
	if !regexp.MustCompile(`<summary><b>1 of \d+</b> Techniques</summary>`).MatchString(body) {
		t.Fatal("technique summary missing the selected-of-total count")
	}
}

// Sets, not single choices: values within a dimension union (either tag
// keeps its technique), dimensions intersect, and a chip's × link removes exactly
// one value while preserving the rest.
func TestOrganizationMultiValueFilters(t *testing.T) {
	srv, ts := newServer(t)
	// The areas below are Playbook-map clusters, and a cluster is computed over
	// the whole corpus — including the starter set every web test loads. Adding
	// one starter technique can rename the cluster this test names, which is a
	// fixture changing under a test about filter sets, not a regression. Clear
	// the corpus so the areas here are only the ones this test builds.
	clearTechniques(t, srv)
	now := time.Now().UTC()
	techniques := []models.Technique{
		{ID: "multi-a", Name: "Warehouse move", Status: "stable", Scope: "org", Provenance: "contributed", Tags: []string{"warehouse"}, TaskTypes: []string{"area-a"}},
		{ID: "multi-b", Name: "Diagram move", Status: "stable", Scope: "org", Provenance: "curated", Tags: []string{"diagram"}, TaskTypes: []string{"area-b"}},
		{ID: "multi-c", Name: "General testing move", Status: "stable", Scope: "general", Provenance: "curated", Tags: []string{"testing"}, TaskTypes: []string{"area-c"}},
	}
	for _, technique := range techniques {
		if err := srv.Store.UpsertTechnique(technique); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: "event-" + technique.ID, TechniqueID: technique.ID, Stage: "shown", Segment: models.Segment{"team": "platform"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	// Tag-sharing buddies put each technique in its own map cluster ("warehouse",
	// "diagram", "testing") so the areas stay distinguishable. Kept out of the
	// `techniques` slice: the pure-function checks below filter that slice directly.
	for _, buddy := range []models.Technique{
		{ID: "multi-a-buddy", Name: "Warehouse buddy", Status: "stable", Scope: "general", Tags: []string{"warehouse"}},
		{ID: "multi-b-buddy", Name: "Diagram buddy", Status: "stable", Scope: "general", Tags: []string{"diagram"}},
		{ID: "multi-c-buddy", Name: "Testing buddy", Status: "stable", Scope: "general", Tags: []string{"testing"}},
	} {
		if err := srv.Store.UpsertTechnique(buddy); err != nil {
			t.Fatal(err)
		}
	}

	// Two tags union; the scope set intersects that union away from multi-c.
	// Area presence is checked inside the momentum panel: cluster names default
	// to tag names, and raw tags always appear in the filter menus.
	_, body := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team&tag=warehouse&tag=diagram&scope=org")
	momentum := body[strings.Index(body, "Areas gaining adoption"):]
	momentum = momentum[:strings.Index(momentum, "</section>")]
	for _, want := range []string{"warehouse", "diagram"} {
		if !strings.Contains(momentum, want) {
			t.Fatalf("union filtering missing the %q area", want)
		}
	}
	if !regexp.MustCompile(`<summary><b>2 of \d+</b> Tags</summary>`).MatchString(body) {
		t.Fatal("tag summary should show 2 selected of the total")
	}
	// Unfiltered dimensions state just their size.
	if !regexp.MustCompile(`<summary>\d+ Techniques</summary>`).MatchString(body) {
		t.Fatal("unfiltered technique summary missing its total count")
	}
	if strings.Contains(momentum, "testing") {
		t.Fatal("scope intersection failed: general technique leaked through the tag union")
	}

	// The chip's × for one tag keeps the other tag AND the scope.
	if !strings.Contains(body, `href="/outcomes?dimension=team&amp;scope=org&amp;tag=diagram&amp;w=30d"`) {
		t.Fatal("chip remove-link should drop only its own value")
	}

	// Pure-function check of the same semantics.
	f := parseOrganizationFilters(map[string][]string{"tag": {"warehouse", "diagram"}, "scope": {"org"}})
	kept, _, _ := filterOrganizationInputs(techniques, nil, nil, f)
	if len(kept) != 2 {
		t.Fatalf("filterOrganizationInputs kept %d techniques, want 2", len(kept))
	}
	f2 := f.without("tag", "warehouse")
	kept2, _, _ := filterOrganizationInputs(techniques, nil, nil, f2)
	if len(kept2) != 1 || kept2[0].ID != "multi-b" {
		t.Fatalf("without(): kept %+v", kept2)
	}
}

// Areas drill down to the Playbook map they come from — a spread row for a
// map area must not dead-end on the unfiltered techniques list. The
// task-type insight view remains a standalone drill-down with an honest
// empty state for unknown types.
func TestOrganizationAreaLinksAndTaskTypeView(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	technique := models.Technique{ID: "tt-move", Name: "Task typed move", Status: "stable", Scope: "org",
		TaskTypes: []string{"data-analysis"}, Tags: []string{"warehouse"}}
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	for i, stage := range []string{"shown", "adopted"} {
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: fmt.Sprintf("tt-%d", i), TechniqueID: technique.ID, Stage: stage,
			Segment: models.Segment{"team": "platform"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	_, body := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team")
	// The map link must NAME its area. A bare /techniques/map sends every column
	// header, bar and chip on this page to the same undifferentiated map, which
	// is what it used to do.
	if !strings.Contains(body, `href="/techniques/map?area=`) {
		t.Fatal("map area doesn't link back to the Playbook map with its area named")
	}
	// The top bar's own Playbook link is a bare /techniques/map by design — the map is
	// the section's default view. Every OTHER map link on this page names an area.
	if n := strings.Count(body, `href="/techniques/map"`); n > 1 {
		t.Fatalf("%d map link(s) dropped their area — those areas would land on the same undifferentiated map", n-1)
	}

	code, view := fetchHTML(t, ts.URL+"/outcomes/task-type/data-analysis?w=30d")
	if code != 200 || !strings.Contains(view, "Task typed move") || !strings.Contains(view, "data-analysis") {
		t.Fatalf("task-type view: %d", code)
	}
	// Unknown task type: honest empty state, not an error.
	code, view = fetchHTML(t, ts.URL+"/outcomes/task-type/no-such-type?w=30d")
	if code != 200 || !strings.Contains(view, "No reviewed techniques declare the task type") {
		t.Fatalf("empty task-type view: %d", code)
	}
}

// Registry health describes the whole registry: technique filters must not bias
// it (a tag filter once made the panel claim session grouping was inactive),
// and the all-time window has no predecessor, so spread deltas disappear.
func TestOrganizationHealthIgnoresFiltersAndAllTimeHasNoDeltas(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	technique := models.Technique{ID: "hf-move", Name: "Health filter move", Status: "stable",
		TaskTypes: []string{"hf-area"}, Tags: []string{"hf-tag"}}
	if err := srv.Store.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: "hf-1", TechniqueID: technique.ID, Stage: "adopted",
		Segment: models.Segment{"team": "platform"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	// A hashed audit fact that offered NO techniques — the case the old
	// offered-technique fact filter silently dropped.
	if _, err := srv.Store.AppendAuditFact(models.AuditFact{AuditID: "hf-fact", SessionHash: "sess-hash-1",
		TaskType: "editing", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}

	_, filtered := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team&tag=hf-tag")
	if !strings.Contains(filtered, "Session grouping is active") {
		t.Fatal("technique filter biased the health panel into denying active session grouping")
	}
	if !strings.Contains(filtered, "always describes the whole registry across the filters above") {
		t.Fatal("filtered health panel should disclose it ignores filters")
	}

	_, allTime := fetchHTML(t, ts.URL+"/outcomes?w=all&dimension=team")
	if strings.Contains(allTime, "Δ+") {
		t.Fatal("all-time window shows spread deltas against a nonexistent previous window")
	}
	if !strings.Contains(allTime, "all time") {
		t.Fatal("all-time spread hint should say so instead of promising a comparison")
	}
}

// The Organization health tile counts shown-never-adopted techniques; the page it
// links to leads with exactly that population under exactly that name, so
// the tile's number visibly corresponds to the first section's count.
func TestShownNeverAdoptedTileMatchesItsDestination(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	for i, spec := range []struct {
		id      string
		adopted bool
	}{{"never-a", false}, {"never-b", false}, {"was-adopted", true}} {
		if err := srv.Store.UpsertTechnique(models.Technique{ID: spec.id, Name: spec.id, Status: "stable", TaskTypes: []string{"x"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: fmt.Sprintf("sn-%d", i), TechniqueID: spec.id, Stage: "shown",
			Segment: models.Segment{"team": "t"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
		if spec.adopted {
			if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: fmt.Sprintf("sn-a-%d", i), TechniqueID: spec.id, Stage: "adopted",
				Segment: models.Segment{"team": "t"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, org := fetchHTML(t, ts.URL+"/outcomes?w=30d")
	if !strings.Contains(org, "Awaiting first adoption") {
		t.Fatal("health tile missing")
	}
	// The worklist itself now lives in the Review queue, where the decision it
	// asks for can actually be made.
	_, misses := fetchHTML(t, ts.URL+"/review?w=30d")
	if !strings.Contains(misses, `Awaiting first adoption <span class="hint">(2)</span>`) {
		t.Fatal("review queue doesn't restate the tile's population and count")
	}
	if !strings.Contains(misses, `Adopted, but shown far more often <span class="hint">(1)</span>`) {
		t.Fatal("adopted-but-wasteful section missing")
	}
	// The never-adopted section holds only its own population.
	neverSection := misses[strings.Index(misses, "Awaiting first adoption"):strings.Index(misses, "Adopted, but shown far more often")]
	if strings.Contains(neverSection, "was-adopted") {
		t.Fatal("adopted technique leaked into the never-adopted section")
	}
}

// The phone rendering: the same matrix as a cohort accordion — only active
// areas listed, quiet ones counted, first cohort open — swapped in by CSS
// below 640px while the grid serves wider screens.
func TestOrganizationPhoneHeatlist(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	// hl-a and hl-a2 share two tags, so they cluster into the "hl-area-a" map
	// group; tagless hl-b pools into Ungrouped — two areas, one active.
	for _, c := range []models.Technique{
		{ID: "hl-a", Name: "HL A", Status: "stable", Tags: []string{"hl-area-a", "hl-area-a2"}},
		{ID: "hl-a2", Name: "HL A2", Status: "stable", Tags: []string{"hl-area-a", "hl-area-a2"}},
		{ID: "hl-b", Name: "HL B", Status: "stable"},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	for i, stage := range []string{"shown", "adopted", "helped"} {
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: fmt.Sprintf("hl-%d", i), TechniqueID: "hl-a", Stage: stage,
			Segment: models.Segment{"team": "hl-team"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	// A second cohort, so there is something to compare the first against: the
	// map is a comparison and refuses to draw itself for a single cohort.
	if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: "hl-other", TechniqueID: "hl-b", Stage: "shown",
		Segment: models.Segment{"team": "hl-team-2"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	_, body := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team")
	for _, want := range []string{
		`class="org-heatlist"`,
		`<details class="heatlist-cohort" open>`, // the first cohort previews the shape
		`heatlist-chip`,                          // heat encoding survives the translation
		`>hl-area-a</a>`,                         // the active area is listed
		`with no activity`,                       // the zero cells are a count, not scrolling
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("phone heatlist missing %q", want)
		}
	}
	// The quiet area must not render a row in that cohort's accordion.
	sect := body[strings.Index(body, `class="org-heatlist"`):]
	first := sect[:strings.Index(sect, "</details>")]
	if strings.Contains(first, ">Ungrouped</a>") {
		t.Fatal("zero-activity area rendered a row on the phone list")
	}
}

// Organization and Technique Use were one story told twice — the same funnel
// tiles, cohort spread, dismissal reasons and source mix on two pages, neither
// complete alone. They are now ONE page: the funnel is the spine, the cohort
// breakdown is a section of it, and the old roots redirect here. This test
// guards the merge — that both halves are present, and that neither half's
// panels come back twice.
func TestOutcomesMergesTheFunnelAndTheCohorts(t *testing.T) {
	srv, ts := newServer(t)
	now := time.Now().UTC()
	if err := srv.Store.UpsertTechnique(models.Technique{ID: "bd-move", Name: "Boundary move", Status: "stable", TaskTypes: []string{"bd"}}); err != nil {
		t.Fatal(err)
	}
	for i, stage := range []string{"shown", "adopted", "helped"} {
		if _, err := srv.Store.InsertEvent(models.FeedbackEvent{EventID: fmt.Sprintf("bd-%d", i), TechniqueID: "bd-move", Stage: stage,
			Segment: models.Segment{"team": "bd-team"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
	}
	_, page := fetchHTML(t, ts.URL+"/outcomes?w=30d&dimension=team")
	// The funnel half (was Technique Use) and the cohort half (was
	// Organization) now share one page.
	for _, want := range []string{
		"How " + product.Name() + " helps", "Adoptions", "Helped rate", "Activity", // funnel spine
		`<a class="panel-link" href="/outcomes/events?w=30d"><section class="panel outcome-hero"`, // the whole funnel panel is a doorway to the activity feed
		"Adoption by cohort and area", "Areas gaining adoption", "All cohorts →", // cohort section
		"Registry and evidence health",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("merged Outcomes page missing %q", want)
		}
	}
	// The cross-links between the two former pages are gone: there is nowhere
	// left to send the reader.
	for _, gone := range []string{`class="tile tile-more"`, "the Technique Use view"} {
		if strings.Contains(page, gone) {
			t.Fatalf("Outcomes still links to a page it absorbed: %q", gone)
		}
	}
	// Panels that used to appear on BOTH pages appear once.
	for _, once := range []string{"How " + product.Name() + " helps", "Registry and evidence health", "Adoption by cohort and area"} {
		if n := strings.Count(page, ">"+once+"</h2>"); n > 1 {
			t.Fatalf("%q rendered %d times on the merged page", once, n)
		}
	}
	_, cohorts := fetchHTML(t, ts.URL+"/outcomes/cohorts?w=30d")
	if !strings.Contains(cohorts, `>Outcomes</a>`) {
		t.Fatal("cohorts page not homed under Outcomes")
	}
}

// clearTechniques empties the technique corpus newServer synced from
// techniques/, for the tests whose subject is the shape of a set they build
// themselves.
func clearTechniques(t *testing.T, srv *Server) {
	t.Helper()
	existing, err := srv.Store.ListTechniques(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, technique := range existing {
		if _, err := srv.Store.DeleteTechnique(technique.ID); err != nil {
			t.Fatal(err)
		}
	}
}
