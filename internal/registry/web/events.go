// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Events view: a reverse-chronological, cohort-scoped activity feed
// answering "what is OpenTacit actually doing?".
//
// It is the narrative complement to Outcomes (the aggregate funnel) and Usage
// (activity density over time). Two families of moment share the feed:
//
//   - DELIVERY moments, folded from the feedback event log grouped by
//     (audit, technique): a technique shown → adopted → helped, or dismissed, in one
//     interaction. This is the funnel as a story.
//   - CURATION moments, from the lifecycle log: the registry acting on the
//     playbook itself — a technique discovered from usage, auto-promoted on evidence,
//     retired, or flagged decaying — each with the reason it happened. This is
//     the half no other view shows.
//
// SCOPING. Every control is server-rendered URL state, exactly like Outcomes:
// the time window is a select, and the cohort filter is the shared .fmenu
// set-filter (filter.go) — one checkbox popover per cohort dimension (team,
// role, harness, …), multi-select, with removable chips. So drilling into a
// specific cohort, or a whole dimension, works the same way here as everywhere
// else. The feed data itself is fetched client-side from /outcomes/events/data with the
// same scoping baked into the query, so the browser and the fold agree.
//
// PRIVACY. The registry holds no member identity, and this feed must never
// re-introduce one. The fold happens SERVER-SIDE and the moment struct sent to
// the browser carries a cohort label and NOTHING that could tie a row to a
// person: no audit id, no session hash. There is deliberately no filter that
// narrows the feed to a single session. Delivery moments default to the
// technique-aggregate headline; the per-interaction stream is the detail beneath it.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// feedLimit caps how many moments the feed renders. The window filter does the
// real scoping; this is a backstop so a huge log can't produce an unbounded
// payload. When it bites, the client says the feed was truncated.
const feedLimit = 400

// eventsCohortDims is the fixed order cohort dimensions render in — the same set
// the rollups segment by (config.SegmentDimensions), pinned here so the feed
// needn't import the rollup config just for a label.
var eventsCohortDims = []string{"team", "role", "function", "domain", "harness", "surface", "model"}

// eventsCohortDimLabels is the plural, human label each dimension wears in its
// filter popover's summary ("3 Teams", "2 Harnesses").
var eventsCohortDimLabels = map[string]string{
	"team": "Teams", "role": "Roles", "function": "Functions",
	"domain": "Domains", "harness": "Harnesses", "surface": "Surfaces",
	"model": "Models",
}

func segmentTokens(seg models.Segment) []string {
	var out []string
	for _, d := range eventsCohortDims {
		if v := seg[d]; v != "" {
			out = append(out, d+":"+v)
		}
	}
	return out
}

// cohortLabel renders a segment as a compact "team:payments · role:eng" label,
// or "" when the interaction carried no cohort tags.
func cohortLabel(seg models.Segment) string {
	toks := segmentTokens(seg)
	if len(toks) == 0 {
		return ""
	}
	return strings.Join(toks, " · ")
}

func hasCohort(seg models.Segment) bool { return len(segmentTokens(seg)) > 0 }

// moment is one narrated entry in the feed. DE-IDENTIFIED by construction: it
// carries a cohort label and never an audit id or session hash. `Type` is
// "delivery" or "curation"; `Event` is the funnel terminal (shown|adopted|
// helped|dismissed|declined|shadow) or the curation kind (discovered|promoted|
// retired|decayed).
type moment struct {
	When          string `json:"when"`
	Type          string `json:"type"`
	TechniqueID   string `json:"technique_id"`
	TechniqueName string `json:"technique_name"`
	Event         string `json:"event"`
	Detail        string `json:"detail,omitempty"`
	Applied       bool   `json:"applied,omitempty"`
	Segment       string `json:"segment,omitempty"`
	TaskType      string `json:"task_type,omitempty"`
	Provenance    string `json:"provenance,omitempty"`
}

// techniqueAggregate is the default headline unit: one technique's funnel over the window,
// so the feed leads with "Technique X — shown 40× · adopted 12× · helped 4×" rather
// than 40 individual rows. Counts are per-interaction (a group that reached a
// stage counts once), so shown ≥ adopted ≥ helped reads honestly.
type techniqueAggregate struct {
	TechniqueID   string `json:"technique_id"`
	TechniqueName string `json:"technique_name"`
	Shown         int    `json:"shown"`
	Adopted       int    `json:"adopted"`
	Helped        int    `json:"helped"`
	Dismissed     int    `json:"dismissed"`
	Applied       int    `json:"applied"`
	Last          string `json:"last"`
}

// deliveryGroup accumulates the stages of one (audit, technique) interaction as the
// event log is folded. It never leaves this file — only the de-identified
// moment does. seg is the raw cohort tags, kept internally for per-dimension
// filtering and the display label; it is cohort-only (no identity).
type deliveryGroup struct {
	techniqueID string
	stages      map[string]bool
	applied     bool
	seg         models.Segment
	taskType    string
	last        string
}

// terminal collapses a group's stages to the single event that best names what
// happened, by funnel precedence. Returns ("", false) for a group that only
// ever carried retrieval telemetry (declined / shadow_*) — those are hidden
// unless the caller asked for telemetry.
func (g *deliveryGroup) terminal() (string, bool) {
	switch {
	case g.stages["helped"]:
		return "helped", true
	case g.stages["dismissed"]:
		return "dismissed", true
	case g.stages["adopted"]:
		return "adopted", true
	case g.stages["shown"]:
		return "shown", true
	case g.stages["shadow_shown"] || g.stages["shadow_declined"]:
		return "shadow", false
	case g.stages["declined"]:
		return "declined", false
	}
	return "", false
}

// matchesCohorts reports whether a group's cohort tags satisfy the filter:
// intersection ACROSS dimensions (every selected dimension must match), union
// WITHIN one (the group's value need only be one of that dimension's selected
// values) — the same set semantics the Outcomes filter bar uses. A group with
// no value on a filtered dimension (seg nil, or the dim empty) never matches, so
// filtering to a cohort excludes the un-tagged.
func matchesCohorts(seg models.Segment, cohorts map[string][]string) bool {
	for dim, vals := range cohorts {
		if !containsFold(vals, seg[dim]) {
			return false
		}
	}
	return true
}

// foldMoments builds the feed from the raw logs, server-side, so nothing
// identifying crosses the wire. It groups delivery events by (audit, technique),
// merges in curation events, filters by window and cohort, and returns both the
// per-technique aggregates (the headline) and the merged moment stream (the detail).
func foldMoments(events []models.FeedbackEvent, facts []models.AuditFact, lifecycle []models.LifecycleEvent,
	techniques []models.Technique, win insights.Window, cohorts map[string][]string, telemetry bool) ([]moment, []techniqueAggregate) {

	name := map[string]string{}
	for _, c := range techniques {
		name[c.ID] = c.Name
	}
	factByAudit := map[string]models.AuditFact{}
	for _, f := range facts {
		factByAudit[f.AuditID] = f
	}
	// Window as a comparable RFC3339Nano string: the logs are stored and ordered
	// lexicographically on this format, so a string compare is the same cursor
	// contract AllEvents/AuditFacts honour. "all" has a non-zero Start too.
	since := ""
	if !win.Start.IsZero() {
		since = win.Start.UTC().Format(time.RFC3339Nano)
	}

	groups := groupDeliveries(events, factByAudit)
	moments, aggByTechnique := deliveryMoments(groups, name, since, cohorts, telemetry)
	moments = append(moments, curationMoments(lifecycle, name, since, cohorts)...)

	// Newest first, then cap. The window is the real scope; the cap is a backstop.
	sort.Slice(moments, func(i, j int) bool { return moments[i].When > moments[j].When })
	if len(moments) > feedLimit {
		moments = moments[:feedLimit]
	}

	aggregates := make([]techniqueAggregate, 0, len(aggByTechnique))
	for _, a := range aggByTechnique {
		aggregates = append(aggregates, *a)
	}
	// Rank by the funnel: most-shown first, so the busiest techniques head the list.
	sort.Slice(aggregates, func(i, j int) bool {
		if aggregates[i].Shown != aggregates[j].Shown {
			return aggregates[i].Shown > aggregates[j].Shown
		}
		return aggregates[i].TechniqueName < aggregates[j].TechniqueName
	})

	return moments, aggregates
}

// groupDeliveries collapses the raw delivery events into one group per (audit,
// technique), which is the unit a moment is about: one suggestion, however many
// stages it went through. Cohort tags and task type come off the event when it
// carries them and off the joined audit fact when it does not.
func groupDeliveries(events []models.FeedbackEvent, factByAudit map[string]models.AuditFact) map[string]*deliveryGroup {
	groups := map[string]*deliveryGroup{}
	for _, e := range events {
		key := e.AuditID + "\x00" + e.TechniqueID
		g := groups[key]
		if g == nil {
			g = &deliveryGroup{techniqueID: e.TechniqueID, stages: map[string]bool{}}
			groups[key] = g
		}
		g.stages[e.Stage] = true
		if e.Delivery == "applied" {
			g.applied = true
		}
		if e.CreatedAt > g.last {
			g.last = e.CreatedAt
		}
		// Prefer the cohort tags on the event; fall back to the joined fact.
		if g.seg == nil {
			if hasCohort(e.Segment) {
				g.seg = e.Segment
			} else if f, ok := factByAudit[e.AuditID]; ok && hasCohort(f.Segment) {
				g.seg = f.Segment
			}
		}
		if g.taskType == "" {
			if e.TaskType != "" {
				g.taskType = e.TaskType
			} else if f, ok := factByAudit[e.AuditID]; ok {
				g.taskType = f.TaskType
			}
		}
	}
	return groups
}

// deliveryMoments turns the groups into moments and per-technique aggregates,
// applying the window, the cohort filter and the telemetry rule. A group with no
// terminal event has nothing to say and is dropped; retrieval telemetry is
// hidden unless asked for; and only the real funnel reaches the aggregates, so
// telemetry can never move the headline counts.
func deliveryMoments(groups map[string]*deliveryGroup, name map[string]string, since string,
	cohorts map[string][]string, telemetry bool) ([]moment, map[string]*techniqueAggregate) {

	var moments []moment
	aggByTechnique := map[string]*techniqueAggregate{}
	for _, g := range groups {
		if since != "" && g.last < since {
			continue
		}
		if len(cohorts) > 0 && !matchesCohorts(g.seg, cohorts) {
			continue
		}
		event, funnel := g.terminal()
		if event == "" {
			continue
		}
		if !funnel && !telemetry {
			continue // retrieval telemetry (declined / shadow) — hidden by default
		}
		techniqueName := name[g.techniqueID]
		if techniqueName == "" {
			techniqueName = g.techniqueID
		}
		moments = append(moments, moment{
			When: g.last, Type: "delivery", TechniqueID: g.techniqueID, TechniqueName: techniqueName,
			Event: event, Applied: g.applied, Segment: cohortLabel(g.seg), TaskType: g.taskType,
		})
		// Aggregates count only the real funnel, never telemetry-only groups.
		if funnel {
			a := aggByTechnique[g.techniqueID]
			if a == nil {
				a = &techniqueAggregate{TechniqueID: g.techniqueID, TechniqueName: techniqueName}
				aggByTechnique[g.techniqueID] = a
			}
			if g.stages["shown"] {
				a.Shown++
			}
			if g.stages["adopted"] {
				a.Adopted++
			}
			if g.stages["helped"] {
				a.Helped++
			}
			if g.stages["dismissed"] {
				a.Dismissed++
			}
			if g.applied {
				a.Applied++
			}
			if g.last > a.Last {
				a.Last = g.last
			}
		}
	}
	return moments, aggByTechnique
}

// curationMoments is the playbook acting on itself: promotions, retirements and
// the rest. These are org-level and cohort-free, so any cohort filter hides them
// — the filter means "this cohort's activity", and curation belongs to no cohort.
func curationMoments(lifecycle []models.LifecycleEvent, name map[string]string, since string,
	cohorts map[string][]string) []moment {

	if len(cohorts) > 0 {
		return nil
	}
	var moments []moment
	for _, e := range lifecycle {
		if since != "" && e.CreatedAt < since {
			continue
		}
		techniqueName := e.TechniqueName
		if techniqueName == "" {
			techniqueName = name[e.TechniqueID]
		}
		if techniqueName == "" {
			techniqueName = e.TechniqueID
		}
		moments = append(moments, moment{
			When: e.CreatedAt, Type: "curation", TechniqueID: e.TechniqueID, TechniqueName: techniqueName,
			Event: e.Kind, Detail: e.Reason, Provenance: e.Provenance,
		})
	}
	return moments
}

// --- cohort filter (the .fmenu set-filter, adopted from Outcomes) -------------

// selectedCohorts reads the per-dimension cohort selections from the query —
// each dimension is its own repeated param (?team=payments&team=core&role=eng),
// the same encoding the Outcomes filter uses.
func selectedCohorts(q url.Values) map[string][]string {
	out := map[string][]string{}
	for _, d := range eventsCohortDims {
		var vals []string
		for _, v := range q[d] {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, v)
			}
		}
		if len(vals) > 0 {
			out[d] = vals
		}
	}
	return out
}

// cohortDims builds one filterDim per cohort dimension present in the logs, so
// the feed's filter bar offers exactly the cohorts that exist — a checkbox
// popover per dimension, searchable once a dimension has many values.
func eventsFilterDims(events []models.FeedbackEvent, facts []models.AuditFact, selected map[string][]string) []filterDim {
	present := map[string]map[string]bool{}
	note := func(seg models.Segment) {
		for _, d := range eventsCohortDims {
			if v := seg[d]; v != "" {
				if present[d] == nil {
					present[d] = map[string]bool{}
				}
				present[d][v] = true
			}
		}
	}
	for _, e := range events {
		note(e.Segment)
	}
	for _, f := range facts {
		note(f.Segment)
	}
	var dims []filterDim
	for _, d := range eventsCohortDims {
		vals := present[d]
		if len(vals) == 0 {
			continue
		}
		opts := make([]filterOption, 0, len(vals))
		for v := range vals {
			opts = append(opts, filterOption{v, v})
		}
		sort.Slice(opts, func(i, j int) bool { return opts[i].value < opts[j].value })
		dims = append(dims, filterDim{
			param: d, label: eventsCohortDimLabels[d], chipPrefix: d + ": ",
			options: opts, selected: selected[d], searchable: len(opts) > 8,
		})
	}
	return dims
}

// selectedWithout drops one (dimension, value) from a selection — the target of
// a chip's × .
func selectedWithout(sel map[string][]string, param, value string) map[string][]string {
	out := map[string][]string{}
	for dim, vals := range sel {
		for _, v := range vals {
			if dim == param && strings.EqualFold(v, value) {
				continue
			}
			out[dim] = append(out[dim], v)
		}
	}
	return out
}

// eventsURL builds /outcomes/events?... from the feed's scoping state — the target of the
// window select, the telemetry toggle, every chip, and Clear.
func eventsURL(window string, telemetry bool, cohorts map[string][]string) string {
	q := url.Values{}
	if window != "" {
		q.Set("w", window)
	}
	if telemetry {
		q.Set("telemetry", "1")
	}
	for dim, vals := range cohorts {
		for _, v := range vals {
			q.Add(dim, v)
		}
	}
	if len(q) == 0 {
		return "/outcomes/events"
	}
	return "/outcomes/events?" + q.Encode()
}

// eventsDataQuery is the scoping the client feed forwards to /outcomes/events/data — the
// same window/telemetry/cohort state, so the browser and the server fold agree.
func eventsDataQuery(window string, telemetry bool, cohorts map[string][]string) string {
	q := url.Values{}
	q.Set("w", window)
	if telemetry {
		q.Set("telemetry", "1")
	}
	for dim, vals := range cohorts {
		for _, v := range vals {
			q.Add(dim, v)
		}
	}
	return q.Encode()
}

// eventsWindowSelect is the time-period picker, preserving the cohort/telemetry
// state so changing the window doesn't drop the filter (the params-preserving
// shape organizationWindowSelect uses on Outcomes).
func eventsWindowSelect(active string, now, earliest time.Time, q url.Values) string {
	return windowSelectURLs(active, now, earliest, func(key string) string {
		out := url.Values{}
		for _, k := range append([]string{"telemetry"}, eventsCohortDims...) {
			for _, v := range q[k] {
				if v != "" {
					out.Add(k, v)
				}
			}
		}
		out.Set("w", key)
		return "/outcomes/events?" + out.Encode()
	})
}

// eventsTelemetryToggle is a link, not a checkbox: it flips the evaluation-
// telemetry state (off-target & shadow moments) while preserving window and
// cohort. A link keeps it JS-free and always available, even when no cohort
// dimensions exist to render a filter bar.
func eventsTelemetryToggle(window string, telemetry bool, cohorts map[string][]string) string {
	label := "Show evaluation telemetry"
	if telemetry {
		label = "Hide evaluation telemetry"
	}
	return fmt.Sprintf(`<a class="ev-telemetry-toggle" href="%s">%s</a>`,
		html.EscapeString(eventsURL(window, !telemetry, cohorts)), html.EscapeString(label))
}

// handleEventsData is the JSON behind the feed. It reads the store directly
// (not the raw /v1/events endpoint) precisely so the de-identifying fold runs
// server-side — the browser never sees an audit id or session hash. Signed-in
// only, matching the page.
func (s *Server) handleEventsData(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrJSON(w, r) {
		return
	}
	events, err := s.Store.AllEvents("")
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}
	facts, err := s.Store.AuditFacts("")
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lifecycle, err := s.Store.LifecycleEvents("")
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().UTC()
	q := r.URL.Query()
	win := insights.WindowByKey(q.Get("w"), now, insights.Earliest(events))
	telemetry := q.Get("telemetry") == "1"
	cohorts := selectedCohorts(q)

	moments, aggregates := foldMoments(events, facts, lifecycle, techniques, win, cohorts, telemetry)
	s.sendJSON(w, http.StatusOK, map[string]any{
		"moments":    moments,
		"aggregates": aggregates,
		"window":     win.Key,
		"truncated":  len(moments) >= feedLimit,
	})
}

// pageEvents renders the controls server-side (window select + the .fmenu cohort
// filter + a telemetry toggle, all URL state like Outcomes) and the feed shell,
// which fetches /outcomes/events/data with the same scoping baked in.
func (s *Server) pageEvents(r *http.Request, user oidc.Claims) page {
	now := time.Now()
	// The logs feed the cohort filter's options; the feed's rows are fetched
	// separately by the client from /outcomes/events/data. Best-effort: an empty read just
	// yields no cohort dimensions, never a broken page.
	events, _ := s.Store.AllEvents("")
	facts, _ := s.Store.AuditFacts("")
	earliest := insights.Earliest(events)

	q := r.URL.Query()
	active := insights.WindowByKey(q.Get("w"), now, earliest).Key
	telemetry := q.Get("telemetry") == "1"
	selected := selectedCohorts(q)
	dims := eventsFilterDims(events, facts, selected)

	// Controls: window nav + control strip (filter bar + telemetry toggle). The
	// shell lifts .window-nav and .outcomes-controls onto the breadcrumb line, so
	// the page opens on its feed, not a stack of control bands — the same
	// treatment Outcomes gets.
	var head strings.Builder
	head.WriteString(eventsWindowSelect(active, now, earliest, q))
	head.WriteString(`<div class="outcomes-controls">`)
	if len(dims) > 0 {
		anySel := false
		for _, d := range dims {
			if len(d.selected) > 0 {
				anySel = true
			}
		}
		hidden := map[string]string{"w": active}
		if telemetry {
			hidden["telemetry"] = "1"
		}
		head.WriteString(filterBar("/outcomes/events", dims, hidden, "", eventsURL(active, telemetry, nil), anySel))
	}
	head.WriteString(eventsTelemetryToggle(active, telemetry, selected))
	head.WriteString(`</div>`)
	if len(dims) > 0 {
		head.WriteString(filterChips(dims, func(param, value string) string {
			return eventsURL(active, telemetry, selectedWithout(selected, param, value))
		}))
	}

	content := head.String() + strings.NewReplacer(
		"«DATAQUERY»", eventsDataQuery(active, telemetry, selected),
		"«WINDOW»", url.QueryEscape(active),
		"«PRODUCT»", productHTML(),
	).Replace(eventsPageHTML)
	// Homed under Outcomes: the feed is one of the Outcomes views, so it carries
	// active:"outcomes" (the top-bar Outcomes tab lights up, like every other
	// /outcomes sub-view) with the trail Outcomes › Events and the peer-view
	// switcher. The parent crumb carries the window back.
	return page{active: "outcomes", content: content,
		crumbs:   []crumb{{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(active)}, {label: "Events", href: ""}},
		viewMenu: outcomesViews("events", active)}
}

// eventsPageHTML is the feed's client shell. It fetches /outcomes/events/data with the
// server-baked scoping query and renders the technique-aggregate headline plus the
// reverse-chronological moment stream. The controls are server-rendered above
// it (pageEvents), so changing a filter reloads the page and this re-fetches —
// no client-side filter state. Inline <style>/<script> follow the dashboard
// convention (usage.go, insights.go).
const eventsPageHTML = `<div class="page-head"><p class="sub">Technique delivery and playbook changes. Delivery events track shown, adopted, and helped stages. Playbook events track techniques that were discovered, promoted, retired, or marked as decaying. This feed includes cohort data but no member or session identity.</p></div>
<style>
.events>#events-root>*+*{margin-top:14px}
.events table.list td.num,.events table.list th.num{text-align:right;font-variant-numeric:tabular-nums;white-space:nowrap}
.feed{list-style:none;margin:0;padding:0}
.feed li{display:flex;gap:.7rem;padding:.55rem 0;border-top:1px solid var(--rule,rgba(128,128,128,.18));align-items:baseline}
.feed li:first-child{border-top:none}
.feed .when{color:var(--muted);font-size:.78rem;white-space:nowrap;min-width:8.5rem;font-variant-numeric:tabular-nums}
.feed .body{flex:1}
.feed .technique-name{font-weight:600}
.feed .technique-name a{color:var(--ink);text-decoration:none}
.feed .technique-name a:hover{color:var(--accent);text-decoration:underline}
.feed .meta a{color:var(--ink-2);text-decoration:none}
.feed .meta a:hover{color:var(--accent);text-decoration:underline}
#ev-aggtable td a{color:var(--ink);text-decoration:none}
#ev-aggtable td a:hover{color:var(--accent);text-decoration:underline}
.feed .meta{color:var(--ink-2);font-size:.82rem;margin-top:.1rem}
.ev-badge{display:inline-block;font-size:.72rem;font-weight:600;padding:.05rem .4rem;border-radius:3px;margin-right:.4rem;white-space:nowrap}
/* The ink is a token, not #fff. The ramp inverts between themes, so white on a
   badge held in the light theme (dark fills) became white on a bright one in the
   dark theme: "published" was white on #e3b341 at 1.9:1, "decayed" 2.1:1, and
   six of the seven fills failed AA. --on-series follows the ramp; s4 is dark in
   both themes and takes --on-deep.
   The hex fallbacks that used to sit in these var() calls could never fire —
   s1..s9 are defined on bare :root in app.css, which every one of these pages
   serves — and one of them said --s4 should fall back to --s2, which would have
   painted "helped" in "adopted"'s colour. The keys are fixed; they do not
   substitute for each other. */
.ev-shown{background:var(--s1);color:var(--on-series)}
.ev-adopted{background:var(--s2);color:var(--on-series)}
.ev-helped{background:var(--s4);color:var(--on-deep)}
.ev-dismissed{background:var(--s6);color:var(--on-series)}
/* An absence is not a status colour. Declined, shadow, retired and delisted all
   say "this is not in play", so they wear the outline rather than a fill —
   which is also the only treatment --muted can carry a label in: it fails AA
   against both inks, in both themes. */
.ev-declined,.ev-shadow,.ev-retired,.ev-delisted{background:transparent;color:var(--muted);border:1px solid var(--muted)}
.ev-discovered{background:var(--s1);color:var(--on-series)}
.ev-promoted{background:var(--s2);color:var(--on-series)}
.ev-decayed{background:var(--s8);color:var(--on-series)}
.ev-approved{background:var(--s2);color:var(--on-series)}
.ev-published{background:var(--s3);color:var(--on-series)}
.ev-applied{background:transparent;border:1px solid var(--s2);color:var(--s2);font-size:.68rem}
.ev-more{display:flex;flex-wrap:wrap;gap:.5rem;align-items:center;margin-top:.7rem}
.ev-more button{font-size:.8rem;padding:.28rem .7rem;border:1px solid var(--ring);border-radius:6px;background:var(--surface);color:inherit;cursor:pointer}
.ev-more button:hover{border-color:var(--accent)}
.ev-more-count{color:var(--muted);font-size:.78rem}
</style>
<div class="events">
<div id="events-root"><p class="hint">Load in progress…</p></div>
</div>
<script>
(function(){
 var APP=document.documentElement.getAttribute('data-base')||'';
 var DATAQUERY="«DATAQUERY»"; // server-baked: window + telemetry + cohort filters
 var WIN="«WINDOW»";           // so a link lands on the period the feed is showing
 var root=document.getElementById('events-root');
 function esc(s){var d=document.createElement('div');d.textContent=(s==null?'':String(s));return d.innerHTML;}
 function when(iso){ // compact local time; falls back to the raw string
  try{var d=new Date(iso);if(isNaN(d))return esc(iso);
   return esc(d.toLocaleString([], {month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'}));}catch(e){return esc(iso);}
 }
 var LABEL={shown:'Shown',adopted:'Adopted',helped:'Helped',dismissed:'Dismissed',declined:'Off-target',shadow:'Shadow eval',
  discovered:'Discovered',promoted:'Promoted',retired:'Retired',decayed:'Decaying',
  approved:'Approved',published:'Published',delisted:'De-listed'};
 var VERB={shown:'was shown to a member',adopted:'was adopted',helped:'was marked helpful',dismissed:'was dismissed',declined:'failed the fit check',
  shadow:'was checked in shadow evaluation',discovered:'was created from usage data',promoted:'was made available in retrieval',
  retired:'was retired',decayed:'was marked as decaying',
  approved:'was approved by a reviewer',published:'was added to the Public feed',
  delisted:'was removed from the Public feed'};
 function badge(ev){return '<span class="ev-badge ev-'+esc(ev)+'">'+esc(LABEL[ev]||ev)+'</span>';}
 function pct(n,d){return d>0?Math.round(100*n/d)+'%':'—';}
 // Every name in the feed is a thing with a page. These build the href and wrap
 // the label, carrying the feed's own window so the destination opens on the
 // same period rather than resetting to the default.
 function href(path,val){return APP+path+encodeURIComponent(val)+'?w='+WIN;}
 function link(path,val,label){
  if(!val)return esc(label==null?val:label);
  return '<a href="'+esc(href(path,val))+'">'+esc(label==null?val:label)+'</a>';
 }
 // A segment is dim:value pairs joined by · — each pair is its own cohort.
 function segLinks(seg){
  return String(seg).split(' \u00b7 ').map(function(part){
   return part.indexOf(':')>0?link('/outcomes/cohorts/',part):esc(part);
  }).join(' \u00b7 ');
 }
 // Progressive reveal: a busy window can hold hundreds of moments and dozens of
 // techniques. Open at a readable length; let the reader step through more, or show
 // all that was loaded. State resets on each control change (the page reloads).
 var TECHNIQUES_INIT=15, TECHNIQUES_STEP=25, FEED_INIT=50, FEED_STEP=50;
 var techniquesN=TECHNIQUES_INIT, feedN=FEED_INIT;
 function moreControls(shown,total,kind,noun,step){
  if(shown>=total)return '';
  var next=Math.min(step,total-shown);
  return '<div class="ev-more">'+
   '<button type="button" data-more="'+kind+'">Show '+next+' more</button>'+
   '<button type="button" data-all="'+kind+'">Show all '+total+'</button>'+
   '<span class="ev-more-count">showing '+shown+' of '+total+' '+esc(noun)+'</span>'+
   '</div>';
 }
 function aggTable(aggs){
  if(!aggs||!aggs.length)return '';
  var shown=Math.min(techniquesN,aggs.length),rows='';
  aggs.slice(0,shown).forEach(function(a){
   rows+='<tr><td>'+link('/outcomes/',a.technique_id,a.technique_name||a.technique_id)+'</td>'+
    '<td class="num">'+(a.shown||0)+'</td>'+
    '<td class="num">'+(a.adopted||0)+'</td>'+
    '<td class="num">'+pct(a.adopted||0,a.shown||0)+'</td>'+
    '<td class="num">'+(a.helped||0)+'</td>'+
    '<td class="num">'+(a.dismissed||0)+'</td>'+
    '<td class="num">'+(a.applied||0)+'</td></tr>';
  });
  return '<section class="panel"><h2>By technique</h2>'+
   '<h3 class="chart-sub"><span class="u">interactions this window, by stage reached</span></h3>'+
   '<div class="table-wrap"><table id="ev-aggtable" class="list data-table"><thead><tr><th>Technique</th>'+
   '<th class="num">Shown</th><th class="num">Adopted</th><th class="num">Adopt&nbsp;%</th>'+
   '<th class="num">Helped</th><th class="num">Dismissed</th><th class="num">Applied</th></tr></thead><tbody>'+
   rows+'</tbody></table></div>'+
   moreControls(shown,aggs.length,'techniques','techniques',TECHNIQUES_STEP)+
   '</section>';
 }
 function feedList(moments,truncated){
  if(!moments||!moments.length)
   return '<section class="panel"><h2>Activity</h2><p class="empty">No activity matches this period and cohort filter.</p></section>';
  var shown=Math.min(feedN,moments.length),items='';
  moments.slice(0,shown).forEach(function(m){
   var meta=[];
   if(m.type==='delivery'){
    if(m.segment)meta.push(segLinks(m.segment));
    if(m.task_type)meta.push(link('/outcomes/task-type/',m.task_type));
    if(m.applied)meta.push('<span class="ev-badge ev-applied">applied automatically</span>');
   }else{
    if(m.provenance)meta.push(link('/outcomes/source/',m.provenance));
    // detail is the curator's free-text reason, not one of the dismissal
    // slugs, so there is nothing for it to link to.
    if(m.detail)meta.push(esc(m.detail));
   }
   items+='<li><span class="when">'+when(m.when)+'</span><span class="body">'+
    badge(m.event)+'<span class="technique-name">'+link('/outcomes/',m.technique_id,m.technique_name||m.technique_id)+'</span> '+esc(VERB[m.event]||m.event)+
    (meta.length?'<div class="meta">'+meta.join(' · ')+'</div>':'')+
    '</span></li>';
  });
  var note=truncated?'<h3 class="chart-sub unit-only"><span class="u">most recent '+
   moments.length+' entries \u00b7 narrow the window to reach earlier ones</span></h3>':'';
  return '<section class="panel"><h2>Activity</h2>'+note+'<ul class="feed">'+items+'</ul>'+
   moreControls(shown,moments.length,'feed','moments',FEED_STEP)+'</section>';
 }
 var DATA=null;
 function draw(){
  var d=DATA||{};
  root.innerHTML=aggTable(d.aggregates||[])+feedList(d.moments||[],d.truncated);
  // The "By technique" table is injected here, after the shell's load-time sortable
  // pass — enhance it with the shell's own implementation so every column sorts.
  var tbl=document.getElementById('ev-aggtable');
  if(tbl&&window.tacitSortableTable)window.tacitSortableTable(tbl);
  root.querySelectorAll('[data-more]').forEach(function(b){b.addEventListener('click',function(){
   if(b.dataset.more==='feed')feedN+=FEED_STEP; else techniquesN+=TECHNIQUES_STEP; draw();});});
  root.querySelectorAll('[data-all]').forEach(function(b){b.addEventListener('click',function(){
   if(b.dataset.all==='feed')feedN=(d.moments||[]).length; else techniquesN=(d.aggregates||[]).length; draw();});});
 }
 function fail(detail){
  root.innerHTML='<section class="panel"><h2>Could not load activity</h2><p class="hint">'+esc(detail)+'</p></section>';
 }
 fetch(APP+'/outcomes/events/data?'+DATAQUERY,{headers:{'Accept':'application/json'},credentials:'same-origin'})
  .then(function(r){return r.json().then(function(d){return {ok:r.ok,d:d};});})
  .then(function(x){if(!x.ok||(x.d&&x.d.error)){fail((x.d&&x.d.error)||'load failed');return;}DATA=x.d;draw();})
  .catch(function(e){fail(e.message);});
})();
</script>`
