// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Usage page: a member's own overview of how they use OpenTacit — queries
// made, suggestions shown, adopted, helped, dismissed — over time, with a
// per-technique drill-down.
//
// The data does NOT come from s.Store — "cohorts, never identities" means the
// registry holds no per-member usage. (The store is read for one thing only:
// the technique ids the By-technique rows may link to; see knownTechniqueIDs.) It lives in the
// member's LOCAL usage log, served by their hook agent on loopback (usagelog.go's
// /v1/hooks/usage). The
// page reaches it through a SERVER-SIDE proxy (/usage/data): the browser calls
// the registry same-origin over HTTPS, and the registry — on the same host as
// the agent — fetches 127.0.0.1. That's the only shape that works when the
// dashboard is opened remotely (over a Funnel): a browser can't reach the host's
// loopback, and an HTTPS page can't fetch http://127.0.0.1 (mixed content). The
// proxy is gated on TACIT_LOCAL_USAGE — usage is machine-local and this page has
// no member identity, so it only serves the host's own log on a self-hosted,
// single-member registry; otherwise it says the data is local and stays put.
package web

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/auditor/hooks"
	"github.com/opentacit/tacit/internal/pricing"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// localAgentBase is the loopback origin the registry proxies to, server-side.
// The hook agent binds 127.0.0.1:TACIT_HOOKS_PORT (default 8787); the registry
// reads the same env so a member who moved the port still resolves. Only digits
// survive from the env value.
func localAgentBase() string {
	port := defaultAgentPort
	if p := digitsOnly(os.Getenv("TACIT_HOOKS_PORT")); p != "" {
		port = p
	}
	return "http://127.0.0.1:" + port
}

// defaultAgentPort is where a hook agent binds when nobody moved it
// (config.HooksPort).
const defaultAgentPort = "8787"

// memberAgentURL is the address of the READER's own agent, which is a different
// question from localAgentBase's — that one resolves the agent on the host this
// registry runs on, for the proxy to fetch server-side.
//
// This link is followed by a browser on the member's machine, and that machine
// is not this one. So it must NOT read TACIT_HOOKS_PORT: an operator who moved
// the port on the registry host would otherwise send every remote member to a
// port only the server uses. The default is the only honest guess, and the copy
// beside the link says what to do when it is wrong.
func memberAgentURL() string {
	return "http://127.0.0.1:" + defaultAgentPort + "/usage"
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// handleUsageData is the server-side proxy behind the Usage page: signed-in, it
// fetches this host's loopback hook agent and relays the JSON. Gated on
// TACIT_LOCAL_USAGE because the registry has no member identity — on a shared
// registry it would hand every viewer the HOST's usage, so by default it returns
// {local_only:true} and the page explains the data is on the member's machine.
func (s *Server) handleUsageData(w http.ResponseWriter, r *http.Request) {
	s.proxyLocalAgent(w, r, "/v1/hooks/usage")
}

// handleSessionsData is the same proxy over the member-local SESSION log: how
// they work rather than how they use OpenTacit (internal/auditor/hooks/sessionlog.go).
// Same gate, for the same reason — on a shared registry it would hand every
// viewer the host's own working history.
func (s *Server) handleSessionsData(w http.ResponseWriter, r *http.Request) {
	s.proxyLocalAgent(w, r, "/v1/hooks/sessions")
}

// proxyLocalAgent relays one loopback endpoint of this host's hook agent.
//
// Gated on TACIT_LOCAL_USAGE because the registry has no member identity — on a
// shared registry it would hand every viewer the HOST's data, so by default it
// returns {local_only:true} and the page explains the data is on the member's
// machine. The proxy is server-side because a browser cannot reach the host's
// loopback and an HTTPS page cannot fetch http://127.0.0.1 (mixed content),
// which is what makes the dashboard work over a Funnel from another device.
func (s *Server) proxyLocalAgent(w http.ResponseWriter, r *http.Request, path string) {
	if !s.signedInOrJSON(w, r) {
		return
	}
	if os.Getenv("TACIT_LOCAL_USAGE") != "1" {
		s.sendJSON(w, http.StatusOK, map[string]any{"local_only": true})
		return
	}
	window := insights.WindowByKey(r.URL.Query().Get("window"), time.Now(), time.Time{}).Key
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	target := localAgentBase() + path + "?window=" + url.QueryEscape(window)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.sendError(w, http.StatusBadGateway, "Cannot reach the local hook agent. Start a "+product.Name()+" session on this host and try again.")
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// usageViews are the four questions this page answers, as the sibling-view menu
// every other section uses — the last crumb becomes a dropdown, exactly as
// Outcomes does with Overview/Cohorts and the Playbook with All/Map/Retired.
//
// Four questions, not four nouns. The menu used to read Summary, Trends,
// Models, Tools, which is one summary and three dimensions — and a dimension is
// not a destination: a member arriving at "Models" has to already know that
// cost lives under it. These are the things somebody actually comes here to
// ask, and the dimensions became a control on the views that answer them.
//
// Models and Tools keep their addresses and become drill-downs: they are
// reached from the figure that raises the question, which is what a drill-down
// is for, and they stay out of the menu because they answer a narrower question
// than the four above.
func usageViews(active, windowKey string) []crumbOpt {
	w := "?w=" + url.QueryEscape(windowKey)
	opt := func(key, label string) crumbOpt {
		return crumbOpt{label: label, href: usagePath(key) + w, count: -1, active: key == active}
	}
	return []crumbOpt{
		opt("now", "Now"),
		opt("work", "Work"),
		opt("cost", "Cost"),
		opt("results", "Outcomes"),
	}
}

// drillDown is a page one level under a destination: a narrower question than
// any of the four, reached from the figure that raises it.
//
// parent is where it LIVES, and there is exactly one. It used to be where the
// reader came from — a plate opening Tools sits on two views, so the link
// carried ?from= and the trail reflected the origin. That made the trail a
// history rather than a location, and it made the level below it incoherent:
// the third crumb's dropdown would have had to hold a different set depending
// on which door you came through. A breadcrumb answers "where am I"; the back
// button answers "where was I". So Tools lives under Work wherever it
// is opened from, and the trail says so.
type drillDown struct{ view, name, parent, param string }

var drillDowns = []drillDown{
	{"allowance", "Allowance", "now", ""},
	{"tools", "Tools", "work", "tool"},
	{"models", "Models", "cost", "model"},
}

// crumbLeaf is a query value as a crumb can carry it: the model's family
// without its vendor, the way every other model label on this page reads
// (modelName in the client), and short enough not to push the trail off the
// line. The value is escaped where it is rendered; this is about the shape of
// it, not its safety.
func crumbLeaf(v string) string {
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[i+1:]
	}
	if len(v) > 60 {
		v = v[:60] + "…"
	}
	return v
}

// drillFor names the drill-down a view is, if it is one.
func drillFor(view string) (drillDown, bool) {
	for _, d := range drillDowns {
		if d.view == view {
			return d, true
		}
	}
	return drillDown{}, false
}

// viewChildren is the dropdown the THIRD crumb wears: the destination's own
// page, then everything that lives under it.
//
// This is what the trail was missing. A member on You / Now could not learn
// that You / Now / Allowance existed, because the only menu on the line offered
// the four destinations and nothing said there was a level below. Now the
// destination's page is "Overview" at that level and its children sit beside
// it, so the caret that opens the level is the thing that says the level is
// there.
//
// Empty where a destination has no children — Outcomes has none — and that
// absence is the other half of the reading: no third crumb means nothing below.
func viewChildren(view, here, window string) []crumbOpt {
	kids := make([]drillDown, 0, len(drillDowns))
	for _, d := range drillDowns {
		if d.parent == view {
			kids = append(kids, d)
		}
	}
	if len(kids) == 0 {
		return nil
	}
	w := "?w=" + url.QueryEscape(window)
	opts := []crumbOpt{{label: usageOverview, href: usagePath(view) + w,
		count: -1, active: here == view}}
	for _, d := range kids {
		opts = append(opts, crumbOpt{label: d.name, href: usagePath(d.view) + w,
			count: -1, active: here == d.view})
	}
	return opts
}

// usageOverview is what a destination's own page is called at the level below
// it. "Now / Overview" beside "Now / Allowance" reads as two pages at one
// level, which is what they are; naming it "Now" twice would read as a loop.
const usageOverview = "Overview"

// usagePath is where a view lives. The view menu and the period select both
// build URLs, and they have to agree: a period select that hardcoded /usage
// sent a member reading the trends back to the summary every time they changed
// the window, silently undoing the view they had chosen.
func usagePath(view string) string {
	switch view {
	case "work":
		return "/usage/work"
	case "cost":
		return "/usage/cost"
	case "results":
		return "/usage/results"
	case "models":
		return "/usage/models"
	case "tools":
		return "/usage/tools"
	case "allowance":
		return "/usage/allowance"
	}
	return "/usage"
}

// viewLabel is what the dropdown is called: the label of the option that is
// selected.
func viewLabel(menu []crumbOpt) string {
	for _, o := range menu {
		if o.active {
			return o.label
		}
	}
	return menu[0].label
}

// usageView resolves the view from the request path. Anything else is the
// summary, because a mistyped URL should show the page rather than an argument
// about the URL.
func usageView(path string) string {
	switch {
	case strings.HasSuffix(path, "/usage/work"):
		return "work"
	case strings.HasSuffix(path, "/usage/cost"):
		return "cost"
	case strings.HasSuffix(path, "/usage/results"):
		return "results"
	case strings.HasSuffix(path, "/usage/models"):
		return "models"
	case strings.HasSuffix(path, "/usage/tools"):
		return "tools"
	case strings.HasSuffix(path, "/usage/allowance"):
		return "allowance"
	}
	return "now"
}

func sceneFor(view string) string {
	if view != "now" {
		return ""
	}
	return `<section class="panel usg-hero">` + usageScene() + `</section>`
}

func (s *Server) pageUsage(r *http.Request, user oidc.Claims) page {
	// The time period is URL-state, exactly like Outcomes: the shared
	// windowSelect control (lifted to the top-right by the shell) navigates to
	// /usage?w=<key>, and the saved window carries across pages. WindowByKey
	// validates the key and defaults to 30d; its presets are the tokens the
	// local endpoint's ParseUsageWindow understands.
	active := insights.WindowByKey(r.URL.Query().Get("w"), time.Now(), time.Time{}).Key
	view := usageView(r.URL.Path)
	drill, isDrill := drillFor(view)
	// The destination this page sits under: itself, or the parent of the
	// drill-down it is. One answer, from the route table rather than from the
	// URL, because a trail is a location and not a history (drillDown).
	under := view
	if isDrill {
		under = drill.parent
	}
	content := strings.NewReplacer(
		"«CONFIG»", s.usageConfigJSON(active, view),
		"«USAGEJS»", usageScriptTag,
		// The period stays inside the view being read, like every other page's
		// select, which passes its own path — and keeps the rest of the URL
		// with it: the tool or model being read, the cut a breakdown is on.
		// Changing the window used to drop both and land the reader on a list
		// with no trail back.
		"«WINDOWNAV»", windowSelectURLs(active, time.Now(), time.Time{}, func(key string) string {
			q := url.Values{}
			for k, vs := range r.URL.Query() {
				if k != "w" && k != "from" {
					q[k] = vs
				}
			}
			q.Set("w", key)
			return usagePath(view) + "?" + q.Encode()
		}),
		// The picture is server-rendered, so the page says what it is about
		// before any script runs — and still says it if none ever does.
		// The scene states what this page is before any script runs. That is
		// worth 600px on the view it introduces and nothing at all on the
		// second one, where the heading and the switch above it have already
		// said where the numbers come from.
		"«SCENE»", sceneFor(view),
		"«PRODUCT»", productHTML(),
	).Replace(usagePageHTML)
	// The trail, one level per level of the section, and a dropdown on every
	// step that has more than one page at it:
	//
	//   You / Now ▾ / Overview ▾            the destination's own page
	//   You / Now ▾ / Allowance ▾           a page under it
	//   You / Outcomes ▾                    a destination with nothing under it
	//   You / Cost ▾ / Models ▾ / claude-opus-5
	//
	// The second caret switches destination from anywhere in the section; the
	// third opens the level below it. Between them, everything the section
	// holds is reachable from the line, which is what the trail could not do
	// when the only menu on it was the four destinations.
	w := "?w=" + url.QueryEscape(active)
	views := usageViews(under, active)
	kids := viewChildren(under, view, active)
	trail := []crumb{{label: sectionYou, href: "/usage" + w}}
	// The destination is a CATEGORY once there is a level under it: its own page
	// is "Overview" at that level, so the name carries no href of its own and
	// its caret is the switch between the four things this section is for.
	// Where nothing is under it the name IS the page, and it is the last step.
	trail = append(trail, crumb{label: viewLabel(views), menu: views})
	if len(kids) == 0 {
		// Nothing below this destination, and the trail stops rather than
		// growing a caret that opens onto one page.
		return page{active: "usage", content: content, crumbs: trail}
	}
	here := usageOverview
	if isDrill {
		here = drill.name
	}
	leaf := ""
	if isDrill && drill.param != "" {
		leaf = crumbLeaf(r.URL.Query().Get(drill.param))
	}
	step := crumb{label: here, menu: kids}
	if leaf != "" {
		// A leaf hangs off a real page, so that page keeps its link and the
		// caret rides beside it.
		step.href = usagePath(view) + w
		return page{active: "usage", content: content,
			crumbs: append(trail, step, crumb{label: leaf})}
	}
	return page{active: "usage", content: content, crumbs: append(trail, step)}
}

// knownTechniqueIDs is the one thing this page asks the registry for: the ids it can
// still serve a technique page for. The By-technique rows link to /techniques/<id>, but the
// numbers come from the member's LOCAL log, which remembers techniques this registry
// has since deleted (and, if the member has connected elsewhere, techniques it never
// had) — linking those would land the reader on a 404. The ids ride along as a
// JSON array and the table links only the names it finds there. A store that
// can't answer costs the links, not the page.
// orgFunnelJSON is the registry-wide shown → adopted → helped for one window,
// for the comparison on You / Outcomes.
//
// A rate off 34 suggestions is a noisy thing to read alone, and "88% adopted"
// means something different on a registry where everybody adopts 68% than on
// one where everybody adopts 90%. The comparison is what makes the member's own
// figure legible, and it costs one pass over the events the Outcomes page
// already makes on every load.
//
// "null" on any failure, and on a registry that has shown nothing: the page
// draws its own funnel either way and simply omits the second rate. A
// comparison against a number nobody has measured would be worse than none.
func (s *Server) orgFunnelJSON(windowKey string) string {
	in, err := s.readAnalyticsInputs(false)
	if err != nil {
		return "null"
	}
	now := time.Now().UTC()
	w := insights.WindowByKey(windowKey, now, insights.Earliest(in.events))
	f := insights.Compute(in.techniques, in.events, now, w).Funnel
	if f.Shown == 0 {
		return "null"
	}
	b, err := json.Marshal(map[string]int{"shown": f.Shown, "adopted": f.Adopted, "helped": f.Helped})
	if err != nil {
		return "null"
	}
	return string(b)
}

// readableHarnesses is the list of clients the hook agent can read, for the
// page's coverage statement. It comes from the agent's own routing table
// (hooks.HarnessNames) rather than a copy here, because a copy is what goes
// stale the week somebody adds the tenth harness.
func readableHarnesses() string {
	b, err := json.Marshal(hooks.HarnessNames())
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (s *Server) knownTechniqueIDs() string {
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		return "[]"
	}
	ids := make([]string, 0, len(techniques))
	for _, c := range techniques {
		ids = append(ids, c.ID)
	}
	// json.Marshal escapes <, > and & — the array sits inside a <script>.
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// usageConfig is everything assets/usage.js needs and cannot know: what the
// server resolved from the URL, and the few registry-wide values the page
// quotes. It rides in the document as JSON rather than being pasted into the
// renderer's source, which is what lets that source be one cached file instead
// of part of every response.
//
// json.Marshal escapes <, > and &, so nothing here can close the script element
// it sits in — the same reason the playbook map carries its graph this way.
type usageConfig struct {
	Window    string `json:"window"`
	View      string `json:"view"`
	BloomDefs string `json:"bloomDefs"`
	MinSample int    `json:"minSample"`
	// The day the price table was read off the vendors' own pages. On the page
	// beside every estimate, because a member comparing this month against last
	// needs to know whether the prices moved under them.
	PriceDate string `json:"priceDate"`
	AgentURL  string `json:"agentURL"`
	Product   string `json:"product"`
	// Already JSON where the server built it that way.
	KnownTechniques json.RawMessage `json:"knownTechniques"`
	Harnesses       json.RawMessage `json:"harnesses"`
	// The registry's own funnel over the same window, so a member can read their
	// rates against everybody's. These are org aggregates — the very numbers the
	// Outcomes hero draws for any signed-in reader — travelling the other way for
	// once. Nothing about this member goes back.
	OrgFunnel json.RawMessage `json:"orgFunnel"`
}

func (s *Server) usageConfigJSON(window, view string) string {
	b, err := json.Marshal(usageConfig{
		Window:          window,
		View:            view,
		BloomDefs:       vizBloomDefs,
		MinSample:       config.MinSample,
		PriceDate:       pricing.AsOf,
		AgentURL:        memberAgentURL(),
		Product:         product.Name(),
		KnownTechniques: json.RawMessage(s.knownTechniqueIDs()),
		Harnesses:       json.RawMessage(readableHarnesses()),
		OrgFunnel:       json.RawMessage(s.orgFunnelJSON(window)),
	})
	if err != nil {
		// Nothing here comes from a request, so this cannot fail on user input;
		// an empty object leaves the page saying it could not load rather than
		// serving a renderer that throws on its first line.
		log.Printf("[usage] could not render the page configuration: %v", err)
		return "{}"
	}
	return string(b)
}

// usagePageHTML is the client shell: what the server renders, plus the two
// script tags that bring the rest. The renderer fetches /usage/data
// (same-origin, so it works over the Funnel from any device); the registry side
// of that endpoint is the loopback proxy above.
//
// The page's stylesheet rules moved to app.css and its three thousand lines of
// renderer to assets/usage.js. What is left is the markup a reader gets with no
// script at all — the heading, the period control and the scene — and the
// configuration the renderer needs, as JSON in the document rather than as
// values pasted through this string.
const usagePageHTML = `<div class="page-head"><p class="sub">Your AI-agent usage across connected machines. Personal usage stays encrypted outside those machines.</p></div>
«WINDOWNAV»
<div class="usage usg-band">
«SCENE»
<div id="usage-head"></div>
<div id="usage-root"><p class="hint">Loading usage…</p></div>
</div>
<script id="usage-config" type="application/json">«CONFIG»</script>
«USAGEJS»`
