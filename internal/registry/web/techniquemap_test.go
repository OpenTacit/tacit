// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/techmap"
)

func TestTechniqueMapPage(t *testing.T) {
	srv, ts := newServer(t)
	for _, c := range []models.Technique{
		{ID: "m-a", Name: "Map A", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "m-b", Name: "Map B", Status: "stable", Tags: []string{"review", "audit"}}, // shares 2 -> edge
		{ID: "m-c", Name: "Map C", Status: "stable", Tags: []string{"setup"}},           // isolated
		{ID: "m-draft", Name: "Draft", Status: "draft", Tags: []string{"review"}},       // excluded
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}

	code, body := fetchHTML(t, ts.URL+"/techniques/map")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	// The section names itself, and Map is the current view — the last crumb is a
	// dropdown whose summary is Map and whose active option is Map.
	if !strings.Contains(body, `<summary aria-current="page">Map<svg`) ||
		!strings.Contains(body, `<a href="/techniques/map" class="active" aria-current="true">Map</a>`) {
		t.Fatal("Map is not marked as the current section view")
	}
	// The canvas and the embedded graph data are both present.
	if !strings.Contains(body, `id="cmap"`) || !strings.Contains(body, `id="cmap-data"`) {
		t.Fatal("the map is missing its canvas or its data")
	}
	// The renderer is an asset now, not an inline script: the page must ask for
	// it, and the asset must be served.
	if !strings.Contains(body, `src="/assets/techniquemap.js?v=`) {
		t.Fatal("the map page does not load the renderer")
	}
	jsCode, js := fetchHTML(t, ts.URL+"/assets/techniquemap.js")
	if jsCode != 200 {
		t.Fatalf("/assets/techniquemap.js status = %d", jsCode)
	}
	// The detail panel links a technique's name to its full page. The panel is
	// built client-side, so this guards the renderer's link template.
	if !strings.Contains(js, `href="/techniques/' + esc(nd.id) + '"`) {
		t.Fatal("the map detail panel does not link the technique to its detail page")
	}

	// The embedded data is valid JSON, holds only the live techniques, and carries the
	// drawn edge between the two that share two tags.
	raw := body[strings.Index(body, `id="cmap-data" type="application/json">`)+len(`id="cmap-data" type="application/json">`):]
	raw = raw[:strings.Index(raw, "</script>")]
	var g struct {
		Nodes []struct {
			ID   string   `json:"id"`
			Tags []string `json:"tags"`
		} `json:"nodes"`
		Edges []struct{ A, B, W int } `json:"tagEdges"`
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatalf("embedded graph is not valid JSON: %v", err)
	}
	// The live techniques are on the map (alongside whatever the server seeds); the
	// draft is not — the map is the live library.
	on := map[string]int{} // id -> node index
	for i, n := range g.Nodes {
		on[n.ID] = i
	}
	for _, id := range []string{"m-a", "m-b", "m-c"} {
		if _, ok := on[id]; !ok {
			t.Fatalf("live technique %s missing from the map", id)
		}
	}
	if _, ok := on["m-draft"]; ok {
		t.Fatal("a draft leaked onto the map — it is the live library only")
	}
	// The two techniques sharing two tags are joined by a weight-2 edge; the isolated
	// one is not joined to either.
	if w := edgeWeight(g.Edges, on["m-a"], on["m-b"]); w != 2 {
		t.Fatalf("m-a—m-b edge weight = %d, want 2", w)
	}
	if edgeWeight(g.Edges, on["m-a"], on["m-c"]) != 0 {
		t.Fatal("m-c shares no tag with m-a but got an edge")
	}
}

func edgeWeight(edges []struct{ A, B, W int }, a, b int) int {
	for _, e := range edges {
		if (e.A == a && e.B == b) || (e.A == b && e.B == a) {
			return e.W
		}
	}
	return 0
}

// Map is reachable as a section view from the other views, not only by direct URL
// — it is an option in the breadcrumb view-switcher on the All page.
func TestTechniqueMapInSectionNav(t *testing.T) {
	_, ts := newServer(t)
	_, list := fetchHTML(t, ts.URL+"/techniques")
	if !strings.Contains(list, `<a href="/techniques/map">Map</a>`) {
		t.Fatal("the list does not offer Map as a section view")
	}
	// Map carries no count — it maps the same live set as All, so a second "43"
	// beside it would be noise.
	nav := list[strings.Index(list, `class="crumb-menu-pop"`):]
	nav = nav[:strings.Index(nav, "</details>")]
	if strings.Contains(nav, `>Map<span>`) {
		t.Fatal("Map should not restate All's count")
	}
}

func TestTechniqueMapEmptyRegistry(t *testing.T) {
	srv, ts := newServer(t)
	// Remove the seed techniques so nothing is live.
	all, _ := srv.Store.ListTechniques(nil, 0)
	for _, c := range all {
		_, _ = srv.Store.DeleteTechnique(c.ID)
	}
	code, body := fetchHTML(t, ts.URL+"/techniques/map")
	if code != 200 || !strings.Contains(body, "No live techniques to map yet") {
		t.Fatalf("empty registry should say so: %d", code)
	}
	if strings.Contains(body, `id="cmap-data"`) {
		t.Fatal("no data script should render for an empty map")
	}
}

// The Map carries the same set-filter as the All view, and it filters the graph.
func TestTechniqueMapFilter(t *testing.T) {
	srv, ts := newServer(t)
	for _, c := range []models.Technique{
		{ID: "f-a", Name: "F A", Status: "stable", Tags: []string{"review"}},
		{ID: "f-b", Name: "F B", Status: "stable", Tags: []string{"review"}},
		{ID: "f-c", Name: "F C", Status: "stable", Tags: []string{"setup"}},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}

	// The filter bar is the shared one, posting back to the map.
	_, body := fetchHTML(t, ts.URL+"/techniques/map")
	if !strings.Contains(body, `<form class="organization-filters" method="get" action="/techniques/map">`) {
		t.Fatal("the map is missing the shared set-filter, or it posts to the wrong view")
	}

	// Filtering to a tag narrows the graph to the matching techniques and shows
	// the active-value chip — same behaviour as the All view.
	nodeIDs := func(html string) map[string]bool {
		raw := html[strings.Index(html, `type="application/json">`)+len(`type="application/json">`):]
		raw = raw[:strings.Index(raw, "</script>")]
		var g struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		}
		_ = json.Unmarshal([]byte(raw), &g)
		out := map[string]bool{}
		for _, n := range g.Nodes {
			out[n.ID] = true
		}
		return out
	}
	_, filtered := fetchHTML(t, ts.URL+"/techniques/map?tag=review")
	ids := nodeIDs(filtered)
	if !ids["f-a"] || !ids["f-b"] || ids["f-c"] {
		t.Fatalf("tag=review should map only the review techniques, got %v", ids)
	}
	if !strings.Contains(filtered, `<span class="fchip">tag: review`) {
		t.Fatal("the active filter value is not shown as a removable chip")
	}
	// The nav's All count stays the whole live set, not the filtered count.
	if !strings.Contains(filtered, `<a href="/techniques">All<span>`) || strings.Contains(filtered, `>All<span>2</span>`) {
		t.Fatal("the filter changed the All nav count; it should show the whole live set")
	}
}

// The map's stage: one full-width field with the label layer over it, and a
// single overlay holding the Areas list ABOVE the legend. The Areas list used to
// be a 300px column beside the map; it is inside the overlay now, and a page
// that renders it outside would give the graph back to a sidebar.
func TestTechniqueMapStageIsFullWidthWithOneOverlay(t *testing.T) {
	srv, ts := newServer(t)
	for _, c := range []models.Technique{
		{ID: "st-a", Name: "Stage A", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "st-b", Name: "Stage B", Status: "stable", Tags: []string{"review", "audit"}},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	_, body := fetchHTML(t, ts.URL+"/techniques/map")

	if !strings.Contains(body, `<canvas id="cmap-labels"`) {
		t.Fatal("the map has no label layer over the field")
	}
	if strings.Contains(body, "cmap-stage-solo") {
		t.Fatal("the stage should no longer have a two-column variant")
	}
	// The Areas aside opens inside the overlay, before the legend section.
	ctrl := strings.Index(body, `id="cmap-controls"`)
	aside := strings.Index(body, `class="cmap-aside"`)
	legend := strings.Index(body, `class="cmap-legendbox"`)
	if ctrl < 0 || aside < 0 || legend < 0 {
		t.Fatalf("overlay pieces missing: controls=%d aside=%d legend=%d", ctrl, aside, legend)
	}
	if !(ctrl < aside && aside < legend) {
		t.Fatal("the overlay must hold the Areas list first and the legend second")
	}
	// One org-scope mark, one legend line: both fields draw a star, so a reader
	// never has to learn which renderer they got to know what a shape means.
	if !strings.Contains(body, "star = org-specific") || strings.Contains(body, "ring = org-specific") {
		t.Fatal("the legend must name the star as the org-specific mark, and nothing else")
	}
}

// The renderer is a cached asset with a content-hashed URL, and the MCP app —
// which renders in a sandboxed frame with no route back to us — inlines the very
// same bytes rather than linking them.
func TestTechniqueMapAssetIsCachedAndInlinedForTheApp(t *testing.T) {
	_, ts := newServer(t)
	get := func(url string) *http.Response {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	hashed := get(ts.URL + "/assets/techniquemap.js?v=" + techniqueMapJSHash[:12])
	if hashed.StatusCode != 200 {
		t.Fatalf("asset status = %d", hashed.StatusCode)
	}
	if got := hashed.Header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("a hashed asset URL should be cacheable forever, got %q", got)
	}
	if hashed.Header.Get("ETag") == "" {
		t.Fatal("the asset carries no ETag")
	}
	// A bare URL makes no promise about its content, so it gets the short public
	// policy instead of the immutable one — and never no-cache, which is
	// forbidden on a public response (cachepolicy).
	bare := get(ts.URL + "/assets/techniquemap.js").Header.Get("Cache-Control")
	if strings.Contains(bare, "immutable") || strings.Contains(bare, "no-cache") {
		t.Fatalf("an unversioned asset URL should carry the short public policy, got %q", bare)
	}
	if !strings.Contains(bare, "max-age=60") || !strings.Contains(bare, "stale-while-revalidate") {
		t.Fatalf("an unversioned asset URL should be bucket A, got %q", bare)
	}
	// A client that already has the bytes gets told so.
	req, _ := http.NewRequest("GET", ts.URL+"/assets/techniquemap.js", nil)
	req.Header.Set("If-None-Match", techniqueMapJSETag)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 304 {
		t.Fatalf("a matching ETag should 304, got %d", resp.StatusCode)
	}
	// The app document carries the renderer itself, not a link to it.
	app := MapAppHTML(techmap.Build([]models.Technique{
		{ID: "app-a", Name: "App A", Status: "stable", Tags: []string{"x"}},
	}, nil))
	if !strings.Contains(app, "webglField") {
		t.Fatal("the MCP map app does not inline the renderer")
	}
	if strings.Contains(app, `src="/assets/techniquemap.js`) {
		t.Fatal("the sandboxed app cannot fetch our origin, so it must not link the renderer")
	}
}

// Both arrangements ship 3-D coordinates, or the WebGL field has nothing to draw.
func TestTechniqueMapDataCarriesCubeCoordinates(t *testing.T) {
	srv, ts := newServer(t)
	for _, c := range []models.Technique{
		{ID: "c3-a", Name: "Cube A", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "c3-b", Name: "Cube B", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "c3-c", Name: "Cube C", Status: "stable", Tags: []string{"solo"}},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	_, body := fetchHTML(t, ts.URL+"/techniques/map")
	raw := body[strings.Index(body, `id="cmap-data" type="application/json">`)+len(`id="cmap-data" type="application/json">`):]
	raw = raw[:strings.Index(raw, "</script>")]
	var g struct {
		Nodes []struct {
			ID  string  `json:"id"`
			X3  float64 `json:"x3"`
			Y3  float64 `json:"y3"`
			Z3  float64 `json:"z3"`
			CX3 float64 `json:"cx3"`
			CY3 float64 `json:"cy3"`
			CZ3 float64 `json:"cz3"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		t.Fatalf("embedded graph is not valid JSON: %v", err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("no nodes in the embedded graph")
	}
	spread := false
	for _, n := range g.Nodes {
		for _, v := range []float64{n.X3, n.Y3, n.Z3, n.CX3, n.CY3, n.CZ3} {
			if v < 0 || v > 1 {
				t.Fatalf("node %s has a coordinate outside the unit cube: %v", n.ID, v)
			}
		}
		if n.Z3 > 0.001 && n.Z3 < 0.999 {
			spread = true
		}
	}
	if !spread {
		t.Fatal("every node sits on a face of the cube — the depth axis is unused")
	}
}

// The map's frame budget. Everything the field draws happens sixty times a
// second, so the renderer's cost per frame is the whole of whether the map is
// usable on a large playbook: at three hundred techniques the field was
// spending most of a second on a frame, and zooming in made it worse, because
// every area's fog grew with the camera until a few thousand blobs each covered
// the window.
//
// These are source assertions because the renderer runs in a browser and this
// suite does not have one. Each one guards a specific thing that was measured
// and fixed, and each is a thing an ordinary edit could quietly undo.
func TestMapRendererDrawsWithinAFrameBudget(t *testing.T) {
	js := techniqueMapJS
	// The palette is resolved in ONE place. Reading a CSS custom property costs
	// a style resolution, and the field used to ask for one per node and per end
	// of every relation — thousands a frame for an answer that only changes with
	// the theme.
	if n := strings.Count(js, "getComputedStyle("); n != 1 {
		t.Fatalf("the renderer resolves CSS custom properties in %d places; the palette is cached in one", n)
	}
	// The DOM measurement that fits the graph around the page's furniture is
	// taken through the cache, never straight from a draw: a measurement forces
	// the browser to lay the page out.
	if n := strings.Count(js, "safeFrame("); n != 2 {
		t.Fatalf("safeFrame is called from %d places; only framed() may call it", n)
	}
	// The area clouds thin themselves to a fill budget as the camera closes in,
	// and darken what is left by exactly the opacity the blobs they stand in for
	// had together, so the fog reads the same at a fraction of the pixels.
	if !strings.Contains(js, "CLOUD_FILL") || !strings.Contains(js, "Math.pow(1 - alpha, step)") {
		t.Fatal("the cloud pass has lost its fill budget or its opacity compensation")
	}
	// A blob wider than the window is trimmed to it, coordinates and all.
	if !strings.Contains(js, "Math.max(-1, (0 - cxp) / r)") {
		t.Fatal("the cloud pass no longer trims its blobs to the canvas")
	}
}

// THE GRAPH IS CENTRED IN THE SPACE THE FURNITURE LEAVES, on both axes.
//
// The map is full bleed, and what floats over it is an L: a band across the top,
// and the Areas overlay down the left wherever there is room for it to sit
// beside the graph rather than over it. So the free rectangle's own centre is
// well right of the window's — 175px of a 1440px board — and that is the centre
// to aim at. While the overlay holds the left of the glass, the space beside it
// is the space the map has.
//
// The aim did prefer the window's centre for a while, because the map read as
// shoved into the bottom-right and that looked like the reason. It was not: the
// camera fit had diverged, by half a screen of pan, and aiming at the window
// only moved where the mis-fitted picture sat (see the fit's own test below).
// With the fit right, one rule serves both axes and the graph carries equal
// margins in the space it was given.
//
// The browser is where this is actually measured — off-centre is not a thing a
// diff can see — and what is held here is the shape of the rule.
func TestTheMapCentresInTheSpaceTheOverlayLeaves(t *testing.T) {
	js := techniqueMapJS

	// One rule, both axes: the middle of the free area.
	if !strings.Contains(js, "out.panX = cxDes - (m.x0 + m.x1) / 2;") {
		t.Error("the horizontal aim is not the free area's centre, so the overlay's column is counted as room the map has")
	}
	if !strings.Contains(js, "out.panY = cyDes - (m.y0 + m.y1) / 2;") {
		t.Error("the vertical aim is not the free area's centre")
	}
	// And the two centres come from the free area, not the canvas.
	if !strings.Contains(js, "var cxDes = (freeArea.x0 + freeArea.x1) / 2, cyDes = (freeArea.y0 + freeArea.y1) / 2;") {
		t.Fatal("the aim is measured against something other than the free area")
	}
	// The window's own centre no longer enters the aim.
	if strings.Contains(js, "Math.max(W / 2, lo)") || strings.Contains(js, "function aimX(") {
		t.Error("the aim prefers the window's centre again, which offsets the graph by the overlay's half-width")
	}

	// And the framing is recomputed when the overlay opens or closes, because
	// opening it is exactly what moves the centre being aimed at.
	if !strings.Contains(js, "if (panelSettled && field && field.frame) field.frame();") {
		t.Error("toggling the overlay does not re-frame, so the map keeps the centre of a space that has changed")
	}
	if !strings.Contains(js, "panelSettled = true;") {
		t.Error("the opening state re-frames twice, or never")
	}
}

// EVERY TECHNIQUE IS INSIDE THE FRAME WHEN THE MAP OPENS.
//
// A library with 127 techniques, fifty of them sharing no tag with anything,
// opened with a third of the field below the bottom of the glass. Three faults
// compounded, and all three are arithmetic rather than taste — which is why what
// is held here is the shape of each fix, measured in a browser against that
// library and then written down.
//
// One: the fit aimed at the centre of MASS. Framing is a question about the
// outside of a cloud, and the mean is pulled off it by wherever the crowd is —
// a third of the way to one corner, on that library — which put the far side
// 2.03 world units from the target where the middle of the extent puts it 1.53.
//
// Two: nothing kept the camera out of the cloud. At a distance shorter than the
// near face is deep, a node's projection runs off to infinity and a node past
// the lens is dropped from the fit altogether — so the fit stopped seeing the
// very thing it had to make room for.
//
// Three: the distance was found by correcting it by the error in the size, and
// that loop cannot converge here. Projected size goes as 1/(dist - near), so the
// correction's gain at the answer is the near face's magnification, negated: any
// magnification worth having overshoots by more than it corrects. Three passes
// hid it while the magnification stayed near two. On a cloud wide enough to
// bring the camera in close the swings reached half a screen of pan.
func TestEveryTechniqueIsInsideTheFrameWhenTheMapOpens(t *testing.T) {
	js := techniqueMapJS

	// The aim is the middle of the extent.
	if !strings.Contains(js, "out.tx = (lo3[0] + hi3[0]) / 2;") {
		t.Error("the fit aims at the centre of mass again, which a crowd pulls off the cloud")
	}

	// The camera stands off the cloud, by a distance derived from the near face
	// along the view axis — and from the worst yaw, because the ambient spin
	// turns the cloud after the fit has run.
	if !strings.Contains(js, "var minD = Math.max(MIND, near * MAXMAG / (MAXMAG - 1));") {
		t.Fatal("nothing bounds the camera's distance below; it can stand inside the cloud")
	}
	if !strings.Contains(js, "near = Math.max(near, Math.sqrt(dx * dx + dz * dz) * cpt - dy * spt);") {
		t.Error("the standoff is no longer measured from the near face at the worst yaw")
	}

	// The distance comes from a monotone question, asked by bisection. A
	// corrective loop is what diverged.
	if !strings.Contains(js, "if (fitsAt(mid)) hi = mid; else lo = mid;") {
		t.Fatal("the distance search is not bisection, so it can diverge again")
	}
	if strings.Contains(js, "out.dist / scale") {
		t.Error("the corrective distance step is back")
	}

	// The box is of the discs, not of the centres: a node's radius on screen
	// scales with dist/zc, so one nominal allowance for all of them is wrong by
	// exactly the factor MAXMAG bounds.
	if !strings.Contains(js, "var r = (ps[i][3] || 0) * (cam.dist / p[2]) * (Math.min(W, H) / 1500);") {
		t.Error("the fit measures node centres again, so the near discs spill past the frame")
	}
	if !strings.Contains(js, "function worldR(i)") {
		t.Error("the fit is no longer given the node sizes it has to frame")
	}
}

// A FRAME THAT CHANGES ON ITS OWN GETS A NEW FIT — UNLESS A HAND IS ON THE CAMERA.
//
// The observer used to resize the buffers and redraw the old view, so a window
// that changed size after the map opened — a rotation, a Split View drag, a
// browser toolbar settling after first paint — kept a camera fitted to a frame
// that no longer existed. Re-framing unconditionally is the other failure: it
// would undo somebody's orbit every time the window twitched.
func TestTheMapRefitsWhenItsFrameChangesButNotUnderAHand(t *testing.T) {
	js := techniqueMapJS

	if !strings.Contains(js, "field.reframe();") {
		t.Fatal("the resize observer does not re-frame, so the camera keeps a frame that is gone")
	}
	if !strings.Contains(js, "reframe: function () { resize(); if (!taken) { home(); draw(); } },") {
		t.Fatal("re-framing is unconditional, or has stopped resizing the buffers")
	}
	// Every way a reader can move the camera claims it.
	for _, hand := range []string{
		"function orbit(dx, dy) {\n      taken = true;",
		"taken = true;\n      cam.dist = Math.max(MIND, Math.min(MAXD, cam.dist * Math.exp(ev.deltaY * 0.0012)));",
		"if (pinchD > 0) { taken = true;",
	} {
		if !strings.Contains(js, hand) {
			t.Errorf("a way of moving the camera does not claim it: %q", hand)
		}
	}
	// And framing hands it back, so the next frame change fits again.
	if !strings.Contains(js, "function frame() {\n      taken = false;") ||
		!strings.Contains(js, "function home() { taken = false;") {
		t.Error("framing does not release the camera, so one orbit stops every later re-fit")
	}
}

// A CROWD OF RELATIONS DOES NOT BURY THE AREA CLOUDS.
//
// The map's one colour rule is that the hue names the area. On a playbook with a
// few hundred techniques the relations were painting over it twice.
//
// The colour: a relation took the average of its two nodes' colours. Two area
// hues meet in the middle at something desaturated, a never-adopted node is grey
// by design, and the cohort arrangement links nearly every adopted pair — so
// almost every filament came out grey. Each end now takes its own area's hue and
// the filament runs from one to the other, so a relation inside an area is that
// area's colour and a bridge between two is never grey anywhere along it.
//
// The quantity: alpha per relation was fixed, so the hundred filaments arriving
// at a busy technique stacked to opaque. At 146 techniques the cohort field drew
// 7,748 of them and the clouds were gone under a flat mat. A filament now thins
// as its two ends get busier, which is the rule the area fog already followed.
//
// Measured in a browser against that library, before and after; what is held
// here is the shape of each rule.
func TestCrowdedRelationsDoNotBuryTheAreaClouds(t *testing.T) {
	js := techniqueMapJS

	// The ink a relation carries is per NODE — the area it belongs to — not one
	// flat colour per relation.
	if !strings.Contains(js, "inks = { pal: p, mode: mode, node: nc, end: ends };") {
		t.Fatal("relations are no longer coloured by the area at each end")
	}
	if strings.Contains(js, "mix(nc[E[i].a], nc[E[i].b], 0.5)") {
		t.Error("a relation averages its two node colours again, which is what turned the map grey")
	}
	// Members of one area share the ink, so a field can tell an internal relation
	// from a bridge by identity alone.
	if !strings.Contains(js, "if (!tbl[cl]) {") || !strings.Contains(js, "ends[i] = tbl[cl];") {
		t.Error("the area inks are no longer shared, so a bridge cannot be told from an internal relation")
	}

	// The thinning, and the floor under it that leaves a small map alone.
	if !strings.Contains(js, "var CROWD = 6;") {
		t.Fatal("the crowd thinning has lost its threshold")
	}
	if !strings.Contains(js, "t[i] = d <= CROWD ? 1 : CROWD / d;") {
		t.Fatal("a relation no longer thins as its ends get busier")
	}
	// Both fields thin, and both read the same ink. The flat map used to hold a
	// second opinion — one grey for every relation — and the busier the playbook
	// the more of the map that opinion covered.
	if n := strings.Count(js, "thinning()"); n != 3 {
		t.Errorf("thinning is read in %d places; it is defined once and used by both fields", n)
	}
	if strings.Contains(js, "rgba(mix(muted, ink, 0.2), a)") {
		t.Error("the flat map paints its relations grey again")
	}
	if !strings.Contains(js, "var c0 = ends[e.a], c1 = ends[e.b];") {
		t.Error("the 3-D field no longer reads an ink per end, so its filaments cannot graduate")
	}

	// One technique's own relations are still drawn whole: asking for a node's
	// reach is asking to see all of it.
	if !strings.Contains(js, "var full = focus >= 0 && strong;") {
		t.Error("a selected technique's relations are thinned with the crowd, so its reach cannot be read")
	}
}

// DRAGGING THE FIELD ORBITS IT AND SELECTS NOTHING.
//
// The map is full bleed: the field is the page's ground and the furniture —
// crumbs, filters, the count, the Areas overlay — floats on top of it. Two
// things followed from that, and a reader met both while trying to turn the
// graph.
//
// A press and a drag is also the browser's gesture for selecting text, and
// nothing said the canvas was not text. The anchor landed on the field and every
// word the pointer then crossed came up blue — 282 characters of overlay, in a
// wash that reads as the map changing colour.
//
// And a furniture ROW is as wide as the page even where it shows nothing. The
// breadcrumb line is 38px of block across eleven hundred pixels of graph, so a
// press up there reached the row rather than the field: no orbit at all, and a
// selection instead. That is the "sometimes" — it depended on where you aimed.
//
// Measured in a browser: a drag from the crumb band now turns the graph and
// selects nothing, and so does one from the open field.
func TestDraggingTheFieldOrbitsItAndSelectsNothing(t *testing.T) {
	js, css := techniqueMapJS, appCSS

	// The field never anchors a selection, in either renderer.
	if !strings.Contains(css, ".cmap-canvas-wrap canvas{display:block;width:100%;height:100%;touch-action:none;\n  -webkit-user-select:none;user-select:none}") {
		t.Error("the field can anchor a text selection again")
	}
	// A row over the field takes the pointer only where it shows something.
	if !strings.Contains(css, "main.wrap:has(.cmap-stage)>nav.crumbs{pointer-events:none}") ||
		!strings.Contains(css, "main.wrap:has(.cmap-stage)>nav.crumbs>*{pointer-events:auto}") {
		t.Fatal("the breadcrumb row covers the field again, so a press up there cannot orbit")
	}
	// And the page-wide guard exists for the drag itself, with the prefixed
	// property iPad needs.
	if !strings.Contains(css, "body.dragging,body.dragging *{-webkit-user-select:none;user-select:none") {
		t.Fatal("the drag guard has lost its rule or its -webkit- half")
	}

	// The press stops the gesture and claims the page; every way the hand comes
	// off gives it back, including a touch the system takes away.
	if !strings.Contains(js, "if (ev.button === 0) ev.preventDefault();") {
		t.Error("the press no longer cancels the selection gesture")
	}
	if !strings.Contains(js, "function handOn() { document.body.classList.add('dragging'); }") ||
		!strings.Contains(js, "function handOff() { document.body.classList.remove('dragging'); }") {
		t.Fatal("the map no longer marks the page as being dragged")
	}
	if n := strings.Count(js, "handOn();"); n != 2 {
		t.Errorf("the page is claimed from %d places; mouse and touch both claim it", n)
	}
	if n := strings.Count(js, "handOff();"); n != 3 {
		t.Errorf("the page is released from %d places; mouseup, touchend and touchcancel all release it", n)
	}
	if !strings.Contains(js, "cv.addEventListener('touchcancel'") {
		t.Error("a cancelled touch leaves the page unselectable, because nothing releases it")
	}
}

// THE FIELD IS FITTED TO THE PAGE'S COLUMN, NOT TO THE WINDOW.
//
// The glass is full bleed; the page is not. Every other row under the top bar —
// the crumbs, the filter bar, the count, the Areas overlay — is held inside
// main.wrap's 1320px column, and the field was the one thing on the page fitted
// to the whole window. On a 2200px monitor that put the graph's centre 356px
// right of the centre everything else is read at, and its right-hand cluster
// sixteen pixels past the end of the page.
//
// So the free area is capped to the column, measured off the element rather than
// repeated from the stylesheet — one max-width governs the page and the map
// together. Where nothing constrains the field, which is the MCP app framing it
// in a panel, the column is the canvas and the cap does nothing.
//
// The two floors that stop the furniture squeezing the graph away had to follow.
// Both were fractions of the WINDOW, and a fraction of the window goes slack as
// the window grows past the column: at 3000px the floor alone would have pulled
// the left edge four hundred pixels back outside the column and handed back the
// width the cap had just taken.
//
// Measured in a browser at 1280, 1440, 1900, 2200 and 3000: the drawn graph is
// inside the column at every one of them.
func TestTheFieldIsFittedToThePagesColumn(t *testing.T) {
	js := techniqueMapJS

	// The column is measured, and it can only ever narrow the free area.
	if !strings.Contains(js, "var col = stage && stage.parentElement ? stage.parentElement.getBoundingClientRect() : null;") {
		t.Fatal("the free area no longer measures the column the page is laid out in")
	}
	if !strings.Contains(js, "out.x0 = Math.max(out.x0, col.left - r.left);") ||
		!strings.Contains(js, "out.x1 = Math.min(out.x1, col.right - r.left);") {
		t.Fatal("the free area is not capped to the column, so the field spreads to the window again")
	}

	// And both horizontal floors are fractions of the column, not the window.
	if !strings.Contains(js, "var colX0 = out.x0, colW = out.x1 - out.x0;") {
		t.Fatal("the squeeze floors have lost the column width they are measured against")
	}
	if !strings.Contains(js, "Math.min(colX0 + colW * 0.42, c.right - r.left + 16)") {
		t.Error("how far the overlay may push the field is measured against the window again")
	}
	if !strings.Contains(js, "if (out.x1 - out.x0 < colW * 0.42) out.x0 = out.x1 - colW * 0.42;") {
		t.Error("the squeeze floor is measured against the window again, so it undoes the cap on a wide monitor")
	}
	if strings.Contains(js, "if (out.x1 - out.x0 < W * 0.42)") {
		t.Error("the window-relative squeeze floor is back")
	}
}
