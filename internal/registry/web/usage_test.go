// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The Usage page is a shell that fetches /usage/data SAME-ORIGIN (so it works
// over a Funnel from any device) — the registry side of that endpoint is the
// loopback proxy. The page must NOT bake a 127.0.0.1 URL into the browser (that
// was the bug: a remote browser's loopback isn't the host's, and HTTPS→http is
// mixed content).
func TestUsagePageFetchesSameOriginProxy(t *testing.T) {
	_, ts := newServer(t)
	resp, err := http.Get(ts.URL + "/usage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /usage status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw) + "\n" + usageJS // the document, and the renderer it links

	for _, want := range []string{
		"get('/usage/data')",     // the client fetches the same-origin proxies
		"get('/usage/sessions')", // and the session feed beside it
		`class="window-nav"`,     // the shared top-right period control (as on Outcomes)
		`class="window-select"`,
		`value="/usage?w=30d"`, // options navigate with ?w=, defaulting to 30d
		// The local-only copy. It leads with where the numbers ARE rather than
		// with what this page cannot do, because nothing here has failed.
		"Personal usage is stored on your machines",
		"tacit usage", // and the one command that shows them
		// Phase 0 of docs/delivery/out-of-band-plan.md: the handoff. This page
		// cannot serve a remote member their numbers, so it names the machine
		// that can rather than only apologising. The address rides in the
		// configuration and the renderer builds the link from it.
		`"agentURL":"http://127.0.0.1:8787/usage"`,
		`open <a href="'+AGENTURL+'">'+AGENTURL+'</a> on that machine`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Usage page missing %q", want)
		}
	}
	// The browser must not be told to FETCH the host's loopback — that was the
	// remote-browser/mixed-content bug, and it is a different thing from the
	// handoff link above. A link is followed by a person, on the machine the
	// agent runs on; a fetch is issued by this page, from wherever it was
	// opened. So the rule is about the fetch helper's argument, not about the
	// string appearing anywhere on the page.
	if strings.Contains(body, "get('http://127.0.0.1") || strings.Contains(body, "fetch('http://127.0.0.1") {
		t.Error("Usage page fetches loopback from the browser — the remote-browser/mixed-content bug")
	}
	// The store is read only for the technique ids the By-technique rows link to, and a
	// failure there costs the links, never the page.
	if strings.Contains(body, "Store unavailable") {
		t.Error("Usage page must not surface store errors")
	}
	// panel() escapes its title, so an entity there reaches the reader as text
	// ("Couldn&rsquo;t load your usage"). Body copy is inserted as HTML and may
	// keep its entities; the titles must carry the character itself.
	if strings.Contains(body, "Couldn&rsquo;t") {
		t.Error("an HTML entity in a client-escaped title shows through as text")
	}
}

// The By-technique rows are drill-downs like every other table on the dashboard: the
// technique page, carrying the window. The link set is server-supplied, so a technique the
// registry has since dropped (the local log outlives the library) stays text
// rather than pointing at a 404.
func TestUsagePageLinksTechniquesItStillHas(t *testing.T) {
	srv, ts := newServer(t)
	techniques, err := srv.Store.ListTechniques(nil, 0)
	if err != nil || len(techniques) == 0 {
		t.Fatalf("need seeded techniques: %v (%d)", err, len(techniques))
	}
	resp, err := http.Get(ts.URL + "/usage?w=7d")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw) + "\n" + usageJS // the document, and the renderer it links

	if !strings.Contains(body, `"`+techniques[0].ID+`"`) {
		t.Errorf("technique id %q not offered to the client for linking", techniques[0].ID)
	}
	for _, want := range []string{
		`APP+'/techniques/'+encodeURIComponent(cap)+'?w='+encodeURIComponent(WINDOW)`, // drill-down keeps the period
		`class="row-link"`,        // the shell's row idiom, hand-applied to this late table
		`'<tr data-href="'`,       // …and the whole row is clickable, as elsewhere
		`if(c.cap&&KNOWN[c.cap])`, // only techniques this registry can still open
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Usage page missing %q", want)
		}
	}
}

// The page's prose links — the empty states and the local-only explainer — must
// resolve, not 404. Every one of them is fetched here.
func TestUsagePageProseLinksResolve(t *testing.T) {
	_, ts := newServer(t)
	for _, href := range []string{
		"/techniques",
		"/outcomes",
		"/docs/user-guide/20-sessions/04-work-with-suggestions.md",
		"/docs/user-guide/50-reference/15-troubleshooting.md",
		"/docs/user-guide/50-reference/16-command-reference.md",
	} {
		resp, err := http.Get(ts.URL + href)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("Usage page links %s → %d", href, resp.StatusCode)
		}
	}
}

// The handoff link must name the DEFAULT agent port even when this host moved
// its own, because the browser that follows it is on the member's machine and
// that machine's port is not this one's to know. An operator who set
// TACIT_HOOKS_PORT here would otherwise send every remote member to a port only
// this server uses.
func TestHandoffLinkIgnoresThisHostsAgentPort(t *testing.T) {
	t.Setenv("TACIT_HOOKS_PORT", "9999")
	if got := memberAgentURL(); got != "http://127.0.0.1:8787/usage" {
		t.Errorf("memberAgentURL() = %q; the reader's machine is not this one", got)
	}
	// The server-side proxy's target is the opposite case: it IS this host.
	if got := localAgentBase(); got != "http://127.0.0.1:9999" {
		t.Errorf("localAgentBase() = %q; the proxy must follow this host's port", got)
	}
}

// The /usage/data proxy is gated: off by default it discloses no host usage
// (returns local_only), and it requires a signed-in session when OIDC is on.
func TestUsageDataProxyGated(t *testing.T) {
	_, ts := newServer(t) // OIDC off in tests, so no sign-in wall
	resp, err := http.Get(ts.URL + "/usage/data?window=30d")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["local_only"] != true {
		t.Fatalf("with TACIT_LOCAL_USAGE unset, proxy must return local_only, got %v", out)
	}
	if _, leaked := out["totals"]; leaked {
		t.Fatalf("gated proxy must not disclose host usage: %v", out)
	}
}

// The session feed is gated exactly like the usage feed, and for a sharper
// reason: it describes how the host works — their projects, their models, their
// cost — and a shared registry has no member identity to check it against.
func TestSessionsDataProxyGated(t *testing.T) {
	_, ts := newServer(t)
	resp, err := http.Get(ts.URL + "/usage/sessions?window=30d")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["local_only"] != true {
		t.Fatalf("with TACIT_LOCAL_USAGE unset, proxy must return local_only, got %v", out)
	}
	for _, leak := range []string{"totals", "models", "projects", "days"} {
		if _, found := out[leak]; found {
			t.Fatalf("gated proxy disclosed %q: %v", leak, out)
		}
	}
}

// The window is URL-state like Outcomes: ?w= is validated server-side and the
// resolved key is handed to the client fetch. An unknown key falls back to 30d.
func TestUsageWindowFromQuery(t *testing.T) {
	_, ts := newServer(t)
	get := func(q string) string {
		resp, err := http.Get(ts.URL + "/usage" + q)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	// The server resolves the window and hands it to the renderer as
	// configuration; the renderer reads it as CFG.window.
	if !strings.Contains(get("?w=7d"), `"window":"7d"`) {
		t.Error("?w=7d not carried into the client fetch")
	}
	if !strings.Contains(get("?w=bogus"), `"window":"30d"`) {
		t.Error("an unknown window should default to 30d")
	}
	if !strings.Contains(usageJS, "var WINDOW=CFG.window;") {
		t.Error("the renderer no longer reads the window the server resolved")
	}
	// The selected option matches, exactly as the shared control renders it.
	if !strings.Contains(get("?w=90d"), `value="/usage?w=90d" selected`) {
		t.Error("the top-right control should mark the active window selected")
	}
}

// Usage is a TOP-LEVEL destination, not an account link.
//
// The first three nav items are questions about the organization's playbook and
// Usage is the fourth question — how am I using this, and what is it costing me
// — which is the only personal one and the only view the registry cannot
// collect. That made it the one entry in the avatar menu that was not an
// operator's, which is a note about the wrong home rather than a reason for it.
//
// It trails the three rather than splitting them: Outcomes, Playbook, Review is
// a reading as well as a list, and Review's badge sitting one slot from the end
// of the bar costs less than breaking that sentence.
func TestUsageIsATopLevelDestination(t *testing.T) {
	srv, _ := newServer(t)
	items := srv.navFor()
	if len(items) != 4 {
		t.Fatalf("nav has %d items, want 4: %+v", len(items), items)
	}
	last := items[len(items)-1]
	if last.key != "usage" || last.label != sectionYou || last.href != "/usage" {
		t.Errorf("last nav item = %+v, want usage/%s//usage", last, sectionYou)
	}
	// Now, the way Playbook opens on the map: the state is the question with the
	// shortest half-life, and the other three views are one click away in the
	// breadcrumb switcher.
	if last.href != usagePath("now") {
		t.Errorf("the nav lands on %q, not the section's own front door %q", last.href, usagePath("now"))
	}
	if strings.Contains(accountMenuItems, "/usage") {
		t.Error("the section is still in the avatar menu as well; one affordance, one home")
	}
	// The LABEL says whose numbers these are; the ROUTE does not follow it.
	// /usage is bookmarked and is what `tacit usage` prints, and a name is not a
	// reason to move an address.
	if sectionYou == "Usage" {
		t.Error("the section still says what is measured and not whose it is")
	}
	// And the page marks it active, so a reader knows where they are.
	page := signedInPage(t, "/usage")
	if !strings.Contains(page, `<a href="/usage" class="active">`+sectionYou+`</a>`) {
		t.Error("the section's nav item does not light up on its own page")
	}
}

// The loopback base honours a moved hook port so a member who changed it still
// resolves, and only digits from the env survive into the page JS.
func TestLocalAgentBaseHonoursPort(t *testing.T) {
	t.Setenv("TACIT_HOOKS_PORT", "9911")
	if got := localAgentBase(); got != "http://127.0.0.1:9911" {
		t.Errorf("localAgentBase = %q", got)
	}
	t.Setenv("TACIT_HOOKS_PORT", "99abc11\n")
	if got := localAgentBase(); got != "http://127.0.0.1:9911" {
		t.Errorf("localAgentBase sanitised = %q, want digits only", got)
	}
}

// The view is a path, like every other section's sibling views, and it arrives
// through the same viewMenu the Playbook and Outcomes use rather than a tab row
// of this page's own — which is what the page struct's note about switching
// "without a separate tab row" asks for.
// Four destinations, each a question. The menu used to read Summary, Trends,
// Models, Tools — one summary and three dimensions — and a dimension is not a
// destination: a member arriving at "Models" had to already know that cost
// lived under it.
func TestUsageViewIsAPathWithTheHouseMenu(t *testing.T) {
	for path, want := range map[string]string{
		"/usage":           "now",
		"/usage/work":      "work",
		"/usage/cost":      "cost",
		"/usage/results":   "results",
		"/usage/tools":     "tools",
		"/usage/models":    "models",
		"/usage/allowance": "allowance",
		// A mistyped path shows the page rather than an argument about the path.
		"/usage/trendz": "now",
	} {
		if got := usageView(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	opts := usageViews("work", "7d")
	if len(opts) != 4 {
		t.Fatalf("view menu has %d entries", len(opts))
	}
	if opts[0].label != "Now" || opts[1].label != "Work" ||
		opts[2].label != "Cost" || opts[3].label != "Outcomes" {
		t.Errorf("view menu labels = %q, %q, %q, %q",
			opts[0].label, opts[1].label, opts[2].label, opts[3].label)
	}
	if opts[0].active || !opts[1].active {
		t.Error("the menu does not mark the view being shown")
	}
	// The window rides across a view change, or switching view silently resets
	// the period the member chose.
	if opts[1].href != "/usage/work?w=7d" || opts[0].href != "/usage?w=7d" {
		t.Errorf("view menu hrefs drop the window: %q, %q", opts[0].href, opts[1].href)
	}
	// No count beside either, the way Outcomes' own views carry none.
	for _, o := range opts {
		if o.count != -1 {
			t.Errorf("%s carries a count it has no number for", o.label)
		}
	}
}

// crumbTrail is the breadcrumb nav on its own. The page carries aria-current on
// the top-bar link too, so counting it document-wide answers a different
// question than the one being asked.
func crumbTrail(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<nav class="crumbs"`)
	if i < 0 {
		t.Fatal("the page has no breadcrumb trail")
	}
	rest := body[i:]
	return rest[:strings.Index(rest, "</nav>")]
}

// THE TRAIL SHOWS WHAT IS UNDER THE PAGE. A member on You / Now had no way to
// learn that You / Now / Allowance existed: the only menu on the line offered
// the four destinations, and nothing said there was a level below at all. So a
// destination with children grows a third step — its own page, called Overview,
// wearing a dropdown of that level — and the caret is what says the level is
// there.
func TestTheTrailOpensTheLevelBelow(t *testing.T) {
	_, ts := newServer(t)
	for _, tc := range []struct {
		path    string
		here    string   // the deepest crumb's label
		offers  []string // hrefs the third dropdown must hold
		noThird bool     // a destination with nothing under it
	}{
		{path: "/usage?w=30d", here: "Overview",
			offers: []string{"/usage?w=30d", "/usage/allowance?w=30d"}},
		{path: "/usage/work?w=30d", here: "Overview",
			offers: []string{"/usage/work?w=30d", "/usage/tools?w=30d"}},
		{path: "/usage/cost?w=30d", here: "Overview",
			offers: []string{"/usage/cost?w=30d", "/usage/models?w=30d"}},
		// Outcomes holds nothing below it, and the trail stops rather than
		// growing a caret that opens onto one page. The absence is the other
		// half of the reading.
		{path: "/usage/results?w=30d", here: "Outcomes", noThird: true},
		// A page at that level names itself there, beside the Overview it
		// shares the level with.
		{path: "/usage/allowance?w=30d", here: "Allowance",
			offers: []string{"/usage?w=30d", "/usage/allowance?w=30d"}},
		{path: "/usage/tools?w=30d", here: "Tools",
			offers: []string{"/usage/work?w=30d", "/usage/tools?w=30d"}},
		{path: "/usage/models?w=30d", here: "Models",
			offers: []string{"/usage/cost?w=30d", "/usage/models?w=30d"}},
	} {
		_, body := fetchHTML(t, ts.URL+tc.path)
		// The deepest step is the current page, and within the trail it is the
		// only one that says so: a dropdown on an ancestor is a way DOWN, not
		// a claim to be where the reader is.
		trail := crumbTrail(t, body)
		if !strings.Contains(trail, `aria-current="page">`+tc.here) {
			t.Errorf("%s: the deepest step is not marked as the current page (%q)",
				tc.path, tc.here)
		}
		if tc.noThird {
			// Two carets would mean a level below; one means this is the floor.
			if n := strings.Count(trail, `<details class="crumb-menu`); n != 1 {
				t.Errorf("%s: %d dropdowns, want 1 — nothing lives under it", tc.path, n)
			}
			continue
		}
		if n := strings.Count(trail, `<details class="crumb-menu`); n != 2 {
			t.Errorf("%s: %d dropdowns, want 2 — the destination and the level below it",
				tc.path, n)
		}
		// The category above it is an ancestor, so its summary makes no claim
		// to be the page being read.
		if strings.Contains(trail, `<summary aria-current="page">`+viewLabel(usageViews(
			strings.TrimSuffix(strings.TrimPrefix(tc.path, "/usage/"), "?w=30d"), "30d"))) {
			t.Errorf("%s: an ancestor claims to be the current page", tc.path)
		}
		for _, href := range tc.offers {
			if !strings.Contains(body, `href="`+href+`"`) {
				t.Errorf("%s: the level below does not offer %s", tc.path, href)
			}
		}
	}
}

// The destination keeps its own dropdown wherever the reader is in the section,
// so the four things You is for are one click from any page in it — including
// from a page two levels down.
func TestTheDestinationSwitchesFromAnyDepth(t *testing.T) {
	_, ts := newServer(t)
	for _, path := range []string{"/usage/tools?w=30d", "/usage/tools?w=30d&tool=Bash"} {
		_, body := fetchHTML(t, ts.URL+path)
		for _, want := range []string{
			`<summary>Work`, // the destination Tools lives under
			`href="/usage?w=30d"`, `href="/usage/cost?w=30d"`, `href="/usage/results?w=30d"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the destination switcher is missing %s", path, want)
			}
		}
	}
}

// A PAGE LIVES IN ONE PLACE. The Tool calls plate sits on two views, and the
// trail used to reflect whichever one the reader came through (?from=). That
// made the line a history rather than a location, and it made the level below
// incoherent — the third dropdown would have had to hold a different set per
// door. Tools lives under How you work wherever it is opened, and a stale
// ?from= in a bookmark changes nothing.
func TestADrillDownLivesInOnePlace(t *testing.T) {
	_, ts := newServer(t)
	for _, path := range []string{
		"/usage/tools?w=30d",
		"/usage/tools?w=30d&from=now",
		"/usage/tools?w=30d&from=cost",
		"/usage/tools?w=30d&from=%2Fevil",
	} {
		_, body := fetchHTML(t, ts.URL+path)
		// The destination is the category Tools lives under, so its caret is
		// how you leave it and its own page is "Overview" one step along.
		if !strings.Contains(body, `<summary>Work`) {
			t.Errorf("%s: Tools does not trail under the view it lives in", path)
		}
		if !strings.Contains(body, `href="/usage/work?w=30d"`) {
			t.Errorf("%s: that view's own page is not reachable from the trail", path)
		}
		if strings.Contains(body, "evil") {
			t.Errorf("%s: a made-up origin reached the page", path)
		}
		if strings.Contains(crumbTrail(t, body), "from=") {
			t.Errorf("%s: the trail still carries an origin", path)
		}
	}
}

// A leaf — one tool, one model — hangs off its own list, which is one click up
// and still wears the dropdown of its level. The model keeps the family and
// drops the vendor, the way every other model label on the page reads, and a
// long one is cut rather than pushing the trail off its line.
func TestALeafHangsOffItsOwnList(t *testing.T) {
	_, ts := newServer(t)
	_, body := fetchHTML(t, ts.URL+"/usage/tools?w=30d&tool=Bash")
	if !strings.Contains(body, `<a href="/usage/tools?w=30d">Tools</a>`) {
		t.Error("the tool does not hang off the tools list, one click up")
	}
	if !strings.Contains(body, `aria-current="page">Bash</span>`) {
		t.Error("the tool is not the current crumb")
	}
	// Four steps, and the two in the middle both open their level.
	if n := strings.Count(crumbTrail(t, body), `<details class="crumb-menu`); n != 2 {
		t.Errorf("%d dropdowns on a leaf's trail, want 2", n)
	}
	_, m := fetchHTML(t, ts.URL+"/usage/models?w=30d&model=anthropic%2Fclaude-opus-5")
	if !strings.Contains(m, `>claude-opus-5</span>`) {
		t.Error("the model crumb keeps its vendor, which no other model label here does")
	}
	if got := crumbLeaf(strings.Repeat("a", 90)); len([]rune(got)) != 61 {
		t.Errorf("a long leaf is %d characters, so it pushes the trail off the line", len([]rune(got)))
	}
}

// The period keeps the rest of the URL. Changing the window on a tool's own
// page used to drop the tool, landing the reader on a list with no trail back.
func TestThePeriodKeepsTheTrail(t *testing.T) {
	_, ts := newServer(t)
	_, body := fetchHTML(t, ts.URL+"/usage/tools?w=30d&tool=Bash")
	if !strings.Contains(body, `value="/usage/tools?tool=Bash&amp;w=7d"`) {
		t.Error("changing the period drops the tool")
	}
}

// Both views are served by one page from one payload. On the sealed-ledger path
// a second route would mean another fetch and another decrypt of a blob the
// member has already opened, so the switch must not become a link to somewhere
// else.
func TestBothViewsRenderFromOnePage(t *testing.T) {
	_, ts := newServer(t)
	for _, q := range []string{"/usage", "/usage/work", "/usage/cost", "/usage/results"} {
		resp, err := http.Get(ts.URL + q)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := string(raw)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d", q, resp.StatusCode)
		}
		body += "\n" + usageJS // the renderer arrives as a linked asset
		for _, want := range []string{"get('/usage/data')", "function seriesPanel("} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s missing %q", q, want)
			}
		}
	}
}

// Turns run to hundreds and retries to single figures. The dashboard's rule —
// and the reason Queries and Suggestions are already separate plots — is that
// one shared axis flattens the smaller series into the baseline, so each measure
// gets its own plot.
func TestSeriesPlotsDoNotShareAnAxis(t *testing.T) {
	page := usageJS
	// Every measure with its own scale is its own single-series plot, and a
	// single-series plot needs no legend: its heading names it, which is what
	// lets all of them wear the same series key without ambiguity.
	for _, want := range []string{
		"[{name:'Turns',key:'s5',f:'turns'}]",
		"[{name:'Sessions',key:'s5',f:'sessions'}]",
		// In runs hundreds of times out, so they are separate plots with their
		// own scales rather than one stack: a stacked out segment is below a
		// pixel at that ratio and pins to its minimum on every column, where its
		// height stops meaning anything at all.
		"[{name:'In',key:'s8',f:'in_tokens'}]",
		"[{name:'Out',key:'s9',f:'out_tokens'}]",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing single-series plot %s", want)
		}
	}
	// Two series means a legend, because identity is never colour alone.
	for _, want := range []string{
		"legendFor([['Retries','s3'],['Corrections','s7']])",
		"legendFor([['Shown','s1'],['Adopted','s2']])",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("a two-series plot has no legend: %s", want)
		}
	}
}

// The dropdown has to be LABELLED with the view that is selected, which is the
// bug this pins: the page defaulted to a single "Usage" crumb, so the menu named
// the section and never named the view you were looking at. Outcomes renders
// "Outcomes / Overview ▾" on its own front page for exactly this reason.
func TestViewMenuIsLabelledWithTheSelectedView(t *testing.T) {
	_, ts := newServer(t)
	for _, tc := range []struct{ path, want string }{
		{"/usage", "Now"},
		{"/usage/work", "Work"},
		{"/usage/cost", "Cost"},
		{"/usage/results", "Outcomes"},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := string(raw) + "\n" + usageJS // the document, and the renderer it links

		// The <summary> of the crumb menu is what a reader sees before opening
		// it. It carries aria-current only where the destination IS the page —
		// where a level sits under it, the destination is a category and the
		// current page is the Overview one step along.
		if !strings.Contains(body, `<summary>`+tc.want) &&
			!strings.Contains(body, `<summary aria-current="page">`+tc.want) {
			t.Errorf("%s: menu is not labelled %q", tc.path, tc.want)
		}
		// And the section stays one click back, carrying the window.
		if !strings.Contains(body, `<a href="/usage?w=30d">`+sectionYou+`</a>`) {
			t.Errorf("%s: no parent crumb back to the section", tc.path)
		}
		// The selected option is marked inside the menu too, not by label alone.
		// "true" rather than "page": an option is the current item in its own
		// set, and only one step on the line may claim to be the page.
		if !strings.Contains(body, `class="active" aria-current="true">`+tc.want) {
			t.Errorf("%s: %q is not marked selected in the menu", tc.path, tc.want)
		}
	}
}

// Changing the period must leave you in the view you were reading. The select
// on every other page navigates to that page's own path; Usage hardcoded
// /usage, so a member looking at the trends was sent back to the summary every
// time they changed the window — silently undoing the choice they had just
// made.
func TestChangingThePeriodStaysInTheView(t *testing.T) {
	_, ts := newServer(t)
	for _, tc := range []struct{ path, want string }{
		{"/usage", "/usage?w="},
		{"/usage/work", "/usage/work?w="},
		{"/usage/cost", "/usage/cost?w="},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := string(raw) + "\n" + usageJS // the document, and the renderer it links

		// Every option in the period select points back into this view.
		for _, w := range []string{"7d", "30d", "90d", "all"} {
			if !strings.Contains(body, `value="`+tc.want+w+`"`) {
				t.Errorf("%s: period %s does not stay in this view", tc.path, w)
			}
		}
		if tc.path != "/usage" && strings.Contains(body, `value="/usage?w=7d"`) {
			t.Errorf("%s: the period select still offers a route back to Now", tc.path)
		}
	}
}

// $0.00 is the one number on this page that is certainly false: a client that
// reports no dollars has no cost, and an absent figure and a measured zero look
// identical once a renderer has run them through ||0. The breakdown table had
// learned this and the Models table had not, so an Amp session — which reports
// no money at all — read as a model that ran for free.
//
// The shape is what is asserted, because the failure is a shape: ||0 on a money
// or a lines field, anywhere on the page.
func TestNoTablePrintsACostNobodyMeasured(t *testing.T) {
	_, ts := newServer(t)
	body := get(t, ts.URL+"/usage") + "\n" + usageJS // the document, and the renderer it links

	// Guarding on ||0 is right — the defect is PRINTING through it.
	for _, banned := range []string{
		"fixed(m.cost_usd||0",
		"fixed(r.cost_usd||0",
		"fixed(dear.cost_usd||0",
		"fixed(c.before.cost_usd||0",
		"fixed(c.after.cost_usd||0",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("the page still renders %q, which prints an unmeasured figure as zero", banned)
		}
	}
	// One component, used by every table that shows money — the sessions
	// breakdown and the model list both reach for it rather than each writing
	// its own rule about what a missing dollar looks like.
	if strings.Count(body, "function moneyCell(r,extra)") != 1 {
		t.Error("moneyCell is not defined exactly once; a second money rule will drift from the first")
	}
	if strings.Count(body, "moneyCell(") < 3 {
		t.Error("moneyCell is defined but not used by both tables")
	}
	// And the fallback it renders instead: an em dash, not a dollar sign.
	if !strings.Contains(body, `return '<td class="'+cls+'">\u2014</td>';`) {
		t.Error("the money cell has no way to say nothing was measured")
	}
	// Lines is the same kind of figure from the same absent status line, so a
	// model nobody measured lines for says nothing there either.
	if !strings.Contains(body, "(m.lines_added||0)>0?fmtCount(m.lines_added)") {
		t.Error("the model list still prints an unmeasured line count as zero")
	}
}
