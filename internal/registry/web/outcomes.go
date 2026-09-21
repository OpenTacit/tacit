// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Outcomes — the one page answering "is it working?".
//
// It is the merge of the former Organization and Technique Use views, which
// told one story twice: both carried funnel-shaped tiles, cohort spread,
// dismissal reasons and a source mix, and neither was complete alone (the
// landing page had to link to the other one from a tile). The funnel — shown →
// adopted → helped — is the spine; the cohort breakdown is a section of it, not
// a rival page. Every drill-down that hung off either page still exists and now
// hangs off this one.
//
// Aggregate/cohort analytics only: the registry holds no user identity by design.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/llmprovider"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/registry/organization"
	"github.com/opentacit/tacit/internal/ui"
)

// minRankedAdoptions is the floor a technique must clear before a RATE
// computed from it is allowed to rank. Counts (adoptions) are honest at any n;
// percentages are not — one adoption that helped is not a 100% helped rate, it
// is one data point. Panels that rank by rate say so when nothing clears it.
const minRankedAdoptions = 3

const helpedRateFloorNote = "no technique has enough adoptions to rank a rate yet"

// outcomesViews is the peer-view switcher for the Outcomes section — the same
// breadcrumb dropdown Playbook uses for All/Map/Retired/Tags (techniquesViews).
// Every standalone lens on the outcomes data is one option; the current one is
// marked active so the trail and the dropdown summary agree. Counts are omitted
// (-1): these are alternative views, not countable collections. The window rides
// every href so switching a lens keeps the period. The value drill-downs
// (a dismissal reason, a tag, one cohort, a technique) are a level deeper — like a
// Playbook technique detail — and carry no switcher, just their trail.
func outcomesViews(active, windowKey string) []crumbOpt {
	w := "?w=" + url.QueryEscape(windowKey)
	opt := func(key, href, label string) crumbOpt {
		return crumbOpt{label: label, href: href, count: -1, active: key == active}
	}
	return []crumbOpt{
		opt("overview", "/outcomes"+w, "Overview"),
		opt("cohorts", "/outcomes/cohorts"+w, "Cohorts"),
		opt("helped-rate", "/outcomes/helped-rate"+w, "Helped rate"),
		opt("signal-trust", "/outcomes/signal-trust"+w, "Signal trust"),
		opt("events", "/outcomes/events"+w, "Events"),
	}
}

func (s *Server) pageOutcomes(r *http.Request, user oidc.Claims) page {
	in, err := s.readAnalyticsInputs(true)
	if err != nil {
		return outcomesStoreErr(err)
	}
	events, allTechniques, facts := in.events, in.techniques, in.facts

	now := time.Now().UTC()
	earliest := insights.Earliest(events)
	w := insights.WindowByKey(r.URL.Query().Get("w"), now, earliest)
	// Before there is anything to report, report that — once, and instead of
	// everything else.
	//
	// This page draws fourteen panels, and on a registry with no events every
	// one is a correct sentence about nothing: no suggestions appeared · try a
	// longer window · no cohort dimensions attach to activity · no
	// comparison-supported opportunities are measurable · no helped outcomes
	// identify an established practice, and nine more. Each is true. Together
	// they are the second screen of the product for the operator who set it up
	// ninety seconds ago, and they read as a thing that is broken.
	//
	// The controls go too: a filter bar scoping nothing and a period select
	// changing nothing are affordances that answer no question here. The whole
	// page comes back on the first event — a cold start, not a threshold
	// anybody has to clear.
	if len(events) == 0 {
		return page{active: "outcomes", content: s.outcomesColdStart(allTechniques),
			crumbs:   []crumb{{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(w.Key)}, {label: "Overview", href: ""}},
			viewMenu: outcomesViews("overview", w.Key)}
	}

	filters := parseOrganizationFilters(r.URL.Query())
	techniques, filteredEvents, filteredFacts := filterOrganizationInputs(allTechniques, events, facts, filters)

	// One filter set now scopes the WHOLE page. Before the merge the filter bar
	// governed the cohort panels and the funnel panels ignored it, because they
	// lived on different pages — a reader who filtered to one tag still saw
	// org-wide adoption numbers above the filtered map.
	o := insights.Compute(techniques, filteredEvents, now, w)
	// The report's areas are the Playbook map's shared-tag clusters, so the
	// matrix, the momentum panels, and the map describe the org in one
	// vocabulary. (A task/tag/technique grouping used to be the default behind a
	// Techniques | Groups toggle, but the toggle silently regrouped every panel in
	// the cohort band, not just the heatmap's columns — two vocabularies for
	// the same panels confused more than they revealed.)
	areaOf := s.mapAreaOf(allTechniques, events)
	rep := organization.Compute(techniques, filteredEvents, filteredFacts, now, w, areaOf, r.URL.Query().Get("dimension"))
	if !filters.empty() {
		// Health answers "how healthy is the registry", which no technique filter
		// changes; recompute it over the full inputs so every number matches
		// the unfiltered pages its tiles link to.
		rep.Health = organization.Compute(allTechniques, events, facts, now, w, areaOf, rep.Lens).Health
	}

	// The page tells one story in reading order: the journey (hero funnel),
	// its pulse (trend tiles + activity), what stands out (computed
	// highlights), which techniques carry it (leaderboards), WHERE it is and
	// isn't happening (the cohort band: map, gaps, proponents, spreading
	// areas), what kind of knowledge is moving (mix), and finally whether the
	// data itself is healthy. The momentum panels used to trail the health
	// footer as a long-tail appendix; redesigned as compact visuals they
	// rejoin the cohort band they belong to.
	standouts := outcomesStandouts(o, rep, w)
	var b strings.Builder
	b.WriteString(organizationWindowSelect(w.Key, now, earliest, r.URL.Query()))
	// The set-filters ride up onto the title (breadcrumb) line — the shell's
	// inline JS lifts .outcomes-controls into nav.crumbs and drops the time-period
	// select in beside it — so the whole control region is one line and the funnel
	// opens directly beneath it. The active-filter chips fall full-width under the
	// title. The page lede is dropped: it only restated the hero panel below,
	// which names and explains itself. (There used to be an in-page section rail
	// here; it was anchor nav pinned to the top, reachable only before you
	// scrolled — the one place it wasn't needed — so it earned its removal.)
	bar, chips := organizationFilterBar(allTechniques, filters, w.Key, rep)
	b.WriteString(`<div class="outcomes-controls">`)
	b.WriteString(bar)
	b.WriteString(`</div>`)
	b.WriteString(chips)
	b.WriteString(outcomesHero(o, w))
	keys, _ := s.Store.ListMemberKeys()
	b.WriteString(outcomesPulse(o, w, memberPulse(liveKeys(keys), s.cfg().BasePath+s.membersHome())))
	if standouts != "" {
		b.WriteString(`<div class="grid two">` + activityChartPanel(o.Buckets, w.Bucket) + standouts + `</div>`)
	} else {
		b.WriteString(activityChartPanel(o.Buckets, w.Bucket))
	}
	b.WriteString(outcomesLeaderboards(o, w))
	b.WriteString(s.autonomyPanel(allTechniques, events, now, w))
	b.WriteString(outcomesCohorts(o, rep, filters, w))
	b.WriteString(organizationMomentum(rep))
	b.WriteString(outcomesComposition(o, w))
	b.WriteString(organizationHealth(rep, !filters.empty()))
	// The section root is the Overview view; the trail reads Outcomes › Overview
	// with the peer-view dropdown, exactly as Playbook opens on Playbook › Map.
	return page{active: "outcomes", content: b.String(),
		crumbs:   []crumb{{label: "Outcomes", href: "/outcomes?w=" + url.QueryEscape(w.Key)}, {label: "Overview", href: ""}},
		viewMenu: outcomesViews("overview", w.Key)}
}

// outcomesColdStart is the whole page before the first event, and the first
// screen of the product for everybody who installs it.
//
// Three jobs, in this order, because a reader who does not have the first cannot
// use the third: say what this is, show the thing it measures, and put the
// moves they can make today in front of them as controls rather than prose.
//
// What it used to do was answer "no measured outcomes yet" in three paragraphs,
// to somebody who had not yet been told what a technique is or why a registry
// would have one. Nothing on the screen could be pressed.
func (s *Server) outcomesColdStart(all []models.Technique) string {
	drafts := 0
	for _, t := range all {
		if t.Status == "draft" {
			drafts++
		}
	}
	keys, _ := s.Store.ListMemberKeys()
	live := liveKeys(keys)
	invited := len(live)
	// A key that has authenticated is a machine that is actually wired: the
	// member ran connect and their tools reached this registry. Minting a key
	// proves only that somebody was invited.
	connected := 0
	for _, k := range live {
		if k.LastSeen != "" {
			connected++
		}
	}

	var b strings.Builder

	// 1. The page addressing the reader, before it reports anything at them.
	b.WriteString(coldStartWelcome())

	// 2. What they can do, and on this view it is the whole page.
	//
	// NO FUNNEL HERE. The hero was drawn empty as a preview of the picture the
	// reader would keep — but it was a constant: a literal zero Overview, on a
	// view that renders only while no event has ever arrived, so it could not
	// show anything but three noughts however long anybody looked at it. A
	// diagram of the product wearing the chrome of a readout. The first event
	// swaps this whole page for the dashboard, and the funnel is drawn there
	// with something in it.
	//
	// Connecting a machine leads because it is the one step that gates a first
	// result: until some tool is wired, nothing can be shown, nothing can be
	// adopted, and every other panel here is preparation for evidence that
	// cannot arrive. It used to appear as an aside inside the Members panel —
	// one line of small print under the step it is a precondition for.
	//
	// Then: decide what serves, put somebody on the other end, and give the
	// registry a model. The model panel stays because the key it names is the
	// registry's own research key, and its absence is the thing a new operator
	// reads as the product having little to say.
	b.WriteString(`<div class="grid cs-next">`)
	b.WriteString(coldStartConnect(connected, invited))
	b.WriteString(coldStartDrafts(drafts))
	b.WriteString(coldStartModel())
	b.WriteString(coldStartMembers(invited))
	b.WriteString(`</div>`)

	b.WriteString(`<p class="hint cs-foot">The dashboard appears after the registry receives its first reaction. ` +
		`To preview it, run <code>tacit demo load</code> in a scratch registry to add one month of sample ` +
		`activity. This command does not change this registry. See the <a href="/docs/user-guide">user guide</a> for details.</p>`)
	return b.String()
}

// coldStartWelcome is the page speaking rather than reporting: what OpenTacit
// does for an organization before the page asks the reader to set it up.
//
// ON THE GROUND, NOT ON A PLATE. It sits between the funnel and four panels
// that are a to-do list, and a fifth plate carrying a paragraph read as a fifth
// step — one with no figure, no mark and no button, which is a step that looks
// broken. Bare text between two objects is the page addressing the reader, and
// it needs no frame to say so.
func coldStartWelcome() string {
	return `<div class="cs-welcome"><h2>` + productHTML() + ` tracks how your organization uses AI.</h2>` +
		`<p>It measures technique outcomes, stores approved techniques in the organization’s playbook, and suggests them to people and agents when relevant.</p></div>`
}

// csStep opens one of the three steps, and marks where it stands.
//
// THE MARK IS STATE, NOT DECORATION. Each of these is a thing to do, and the
// page read as three panels of prose about the product — so which of them still
// wanted the reader was something they had to work out from the words. Now the
// three say it: drafts in the queue, nobody invited, no model key.
//
// Two marks, at two weights. The flag is a band across the corner because a
// step that is still asking has to carry across a row of three; the done mark
// is the settings page's own chip, quiet, because it is confirming rather than
// asking. A finished step needs SOMETHING, though — left bare it does not read
// as a step that is finished, it reads as a panel that was never on the list.
func csStep(title string, todo bool) string {
	mark := `<span class="set-chip set-chip-good cs-done">&#10003; Done</span>`
	if todo {
		mark = `<span class="cs-todo">To do</span>`
	}
	return `<section class="panel cs-step">` + mark + `<h2>` + title + `</h2>`
}

// coldStartConnect is the step that gates every other one: a registry with no
// machine wired to it cannot be shown a technique, so it cannot measure one.
//
// It counts member keys that have actually authenticated rather than keys that
// exist. A minted key proves somebody was invited; a key that has been seen
// proves a member ran connect and their tools reached here. The difference is
// the whole question this panel asks.
//
// THE ONLY STEP IN THE ROW WITH NO BUTTON, because it is the only one that is
// finished somewhere else: `tacit connect`, at a terminal, on the machine that
// holds the tools. The panel carried an "Open Members" button for the shape of
// the row, and it sent a reader who wanted to do this to a page that cannot do
// it. The command in the sentence is the control.
func coldStartConnect(connected, invited int) string {
	var b strings.Builder
	b.WriteString(csStep("Connect your tools", connected == 0))
	if connected == 0 {
		b.WriteString(`<p class="cs-figure cs-figure-none">&mdash;</p>`)
		b.WriteString(`<p class="sub">No machine is connected yet. Run <code>tacit connect</code> ` +
			`on the machine where your AI tools are.</p>`)
		if invited > 0 {
			b.WriteString(`<p class="sub cs-aside">Invitations sent. This step completes after an invited machine connects.</p>`)
		}
	} else {
		fmt.Fprintf(&b, `<p class="cs-figure">%d</p>`, connected)
		fmt.Fprintf(&b, `<p class="sub">%s connected to this registry. The registry can show suggestions and record outcomes.</p>`, lanePhrase(connected, "machine"))
	}
	b.WriteString(`</section>`)
	return b.String()
}

// coldStartDrafts is the move most first runs actually have waiting: `tacit
// init` reads the organization's own written conventions into the review queue,
// and until this panel nobody was told.
func coldStartDrafts(drafts int) string {
	var b strings.Builder
	b.WriteString(csStep("Approve draft techniques", drafts > 0))
	if drafts == 0 {
		b.WriteString(`<p class="cs-figure cs-figure-none">&mdash;</p>`)
		b.WriteString(`<p class="sub">No drafts need review. Drafts come from member contributions, ` +
			`research runs, and a repository&rsquo;s written conventions the next time ` +
			`you run <code>tacit init</code> inside one.</p>`)
		b.WriteString(`<p class="btn-row"><a class="btn" href="/review">Open Review</a></p>`)
	} else {
		fmt.Fprintf(&b, `<p class="cs-figure">%d</p>`, drafts)
		fmt.Fprintf(&b, `<p class="sub">%s need your review, gathered from your organization&rsquo;s own written `+
			`conventions. Promote what can help other members; reject the others.</p>`,
			lanePhrase(drafts, "draft"))
		b.WriteString(`<p class="btn-row"><a class="btn btn-lead" href="/review">Review them</a></p>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

// coldStartMembers is the step nobody can skip: there is no evidence without
// somebody generating it.
func coldStartMembers(invited int) string {
	var b strings.Builder
	b.WriteString(csStep("Share with colleagues", invited == 0))
	if invited == 0 {
		b.WriteString(`<p class="cs-figure cs-figure-none">&mdash;</p>`)
		b.WriteString(`<p class="sub">Invite colleagues to share techniques and add their outcomes to the organization’s totals.</p>`)
		b.WriteString(`<p class="btn-row"><a class="btn btn-lead" href="/members">Invite a colleague</a></p>`)
	} else {
		fmt.Fprintf(&b, `<p class="cs-figure">%d</p>`, invited)
		fmt.Fprintf(&b, `<p class="sub">%s. Their outcomes are included in the organization’s totals. The dashboard appears after the first reaction.</p>`,
			keyPhrase(invited))
		b.WriteString(`<p class="sub cs-aside">Or wire another machine: <code>tacit connect</code></p>`)
		b.WriteString(`<p class="btn-row"><a class="btn" href="/members">Who is here</a></p>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

// keyPhrase counts the keys that exist. KEYS, not people — the same distinction
// the members picture makes, for the same reason: a key is carried by a harness
// and the same key on two machines is one key.
func keyPhrase(n int) string {
	if n == 1 {
		return "1 member key is out"
	}
	return fmt.Sprintf("%d member keys are out", n)
}

// coldStartModel is the setting that decides how much the registry can do on
// its own, and the one `tacit init` does not ask for — a setup command that
// blocks on a secret cannot run in a script, so nobody was ever told it exists.
// The common outcome was an operator running without it and reading the quiet
// as the product having nothing to say.
//
// The registry-side key, not the agent's: this one lets the research pass on
// Review run at all, and merges tags and describes clusters. The dash is the
// figure because this panel counts nothing, which is also true of the one
// beside it.
func coldStartModel() string {
	var b strings.Builder
	if key := strings.TrimSpace(os.Getenv("TACIT_LLM_API_KEY")); key == "" {
		b.WriteString(csStep("Set up a model", true))
		b.WriteString(`<p class="cs-figure cs-figure-none">&mdash;</p>`)
		b.WriteString(`<p class="sub">` + productHTML() + ` can use a model to find candidate techniques and improve recommendations.</p>`)
		fmt.Fprintf(&b, `<p class="sub cs-aside">Or write it where the service reads it: `+
			`<code>TACIT_LLM_API_KEY</code> in <code>%s</code></p>`,
			html.EscapeString(homeRelative(config.RegistryEnvPath())))
		// Not the lead button: the row already has one, on the step nobody can
		// skip, and two accents in three panels is no accent at all. Last, so
		// it falls to the floor of the plate — the three controls then sit on
		// one line across the row instead of at three heights.
		b.WriteString(`<p class="btn-row"><a class="btn" href="` + settingsModelHref + `">Open settings</a></p>`)
		b.WriteString(`</section>`)
		return b.String()
	}
	provider := os.Getenv("TACIT_LLM_PROVIDER")
	if !llmprovider.Valid(provider) {
		provider = llmprovider.Default
	}
	b.WriteString(csStep("Your model", false))
	b.WriteString(`<p class="cs-figure cs-figure-none">&mdash;</p>`)
	// The control is named exactly as Review names it: one name per concept.
	fmt.Fprintf(&b, `<p class="sub"><b>%s</b> is configured. <b>Suggest candidate techniques</b> on `+
		`Review researches against it and files up to ten drafts for you to judge.</p>`,
		html.EscapeString(llmprovider.Lookup(provider).Label))
	b.WriteString(`<p class="btn-row"><a class="btn" href="` + settingsModelHref + `">Change it in settings</a></p>`)
	b.WriteString(`</section>`)
	return b.String()
}

// settingsModelHref is Settings opened ON the subject, not at the top of the
// page. The model rows live under "Automation and model", and a button that
// landed on Access left the reader to find the tab that the sentence above the
// button had just named.
const settingsModelHref = "/settings?tab=automation"

// homeRelative writes a path under the home directory as ~/…, because the
// settings file's address is a thing to read and retype, not a thing to parse.
// Spelled out it was three lines of monospace in a panel whose own button is
// one — the quietest line on the plate drawn loudest.
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return path
	}
	return "~" + path[len(home):]
}

func outcomesStoreErr(err error) page {
	return storeUnavailablePage("outcomes", nil, err)
}

// outcomesHero is the page's opening statement: the one number the whole page
// exists to report — how much of what OpenTacit showed went on to measurably help
// — beside the journey that produced it, drawn as a horizontal flow. The old
// page carried the same facts as five equal tiles plus a small vertical
// funnel two panels down; the reader had to assemble the story themselves.
//
// "Live techniques" is deliberately NOT here — it is a registry-size number,
// not a funnel position, and it has a home in the health panel at the foot of
// the page.
func outcomesHero(o insights.Overview, w insights.Window) string {
	f, pf := o.Funnel, o.PrevFunnel
	hasPrev := w.PrevStart.Before(w.Start)

	var b strings.Builder
	// The funnel is the aggregate; the Events feed is the same shown → adopted →
	// helped journey told moment by moment, so the whole panel is a doorway to the
	// story behind it — the ↗ on the title (a.panel-link h2::after) marks it, the
	// same affordance the KPI tiles below carry.
	b.WriteString(`<a class="panel-link" href="/outcomes/events?w=` + url.QueryEscape(w.Key) + `"><section class="panel outcome-hero" id="funnel"><h2>How ` + productHTML() + ` helps</h2>`)
	// Nothing to show: one sentence, across the panel. The two-column grid below
	// exists to stand a figure beside a flow, and both of its rules assume that
	// — the first column is capped at 250px for the figure, and the flow is
	// pulled up 1.45rem to ride alongside the title, which is safe only because
	// the flow's stage labels start ~170px in. With no figure rendered, the
	// empty sentence inherits the figure's narrow column AND the pull: it wrapped
	// after five words and sat on the title, in a panel that was otherwise
	// empty.
	b.WriteString(`<div class="hero-grid">`)
	// Nothing shown yet: the figure's column carries the orientation
	// instead. It used to sit ABOVE the funnel, in a panel of its own with a
	// title that described the product rather than the page — three sentences
	// of vertical space before the reader reached the one drawing that would
	// have explained it. The column is the right home: it is where this panel
	// already puts the sentence that reads the picture beside it.
	if f.Shown == 0 {
		b.WriteString(`<div class="hero-fig hero-fig-empty">`)
		b.WriteString(`<p class="hero-what"><b>No techniques have been shown yet.</b> ` + productHTML() +
			` shows relevant techniques to members and agents.</p>`)
		// One sentence here, not three. The second paragraph used to explain
		// that members go on working as normal while OpenTacit watches and offers
		// — which is what the welcome above the panel now says, in the page's
		// own voice and higher up. Two orientations stacked four lines apart is
		// the reader being told twice. What is left reads the funnel beside it,
		// which is the only job this column has while the figure is missing.
		b.WriteString(`</div>`)
	}
	if f.Shown > 0 {
		rate := float64(f.Helped) / float64(f.Shown) * 100
		b.WriteString(`<div class="hero-fig">`)
		fmt.Fprintf(&b, `<div class="hero-value">%.0f%%</div>`, rate)
		b.WriteString(`<p class="hero-what">of the shown suggestions <b>measurably helped</b></p>`)
		// No "31 of 157 suggestions" line here: the flow beside it already labels
		// both ends of the funnel with those very counts. All the sub-line adds
		// that the figure can't say for itself is which way the rate is moving.
		if hasPrev && pf.Shown > 0 {
			prevRate := float64(pf.Helped) / float64(pf.Shown) * 100
			d := rate - prevRate
			cls := "up"
			if d < 0 {
				cls = "down"
			}
			fmt.Fprintf(&b, `<p class="hero-sub"><span class="tile-delta %s">%+.0f&#8239;pp from the previous window</span></p>`, cls, d)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`<div class="hero-flow">`)
	b.WriteString(string(FunnelFlow(f, pf, hasPrev)))
	b.WriteString(`</div></div></section></a>`)
	return b.String()
}

// outcomesPulse is the trend row under the hero: the same funnel numbers as
// TRENDS (sparklines and deltas — the hero carries the levels), plus the one
// rate that isn't on the funnel at all, retrieval precision.
// memberPulse is the people tile, and it LEADS the pulse row.
//
// It sits in this row because this row is above the fold and the health panel
// three screens below it is not — measured on the dashboard at 1440x900, the
// pulse ends at y=626 and Registry and evidence health begins at y=3220. And it
// leads because outcomes are only measured where members work: every other
// figure in the row is a rate over what these people did, so the count of them
// is the first thing about the row that is true.
//
// The label says members; what is counted is live member keys. The distinction
// — a key is carried by a harness, and the same key on two machines is one key
// — is real, and it is made where there is room for it: on the Members page,
// and in this tile's own note.
func memberPulse(live []models.MemberKey, href string) Tile {
	joined := 0
	for _, k := range live {
		if k.LastSeen != "" {
			joined++
		}
	}
	// The second line says only what the figure doesn't. Where everyone has
	// used their invitation it repeated the number back — "5" beside "5
	// joined" — so that state is named instead, and a registry with nobody in
	// it drops the line altogether: the button is the ask. (The ask used to
	// live in this slot, and an imperative wearing the delta's colour said the
	// wrong thing about a count of zero.)
	delta := ""
	switch {
	case len(live) == 0:
	case joined == len(live):
		delta = "all joined"
	default:
		delta = fmt.Sprintf("%d joined", joined)
	}
	return Tile{Label: "Members", Value: fmtCount(len(live)),
		Delta: delta, Good: true,
		Action: TileAction{Label: "Add members", Href: href},
		Note: "How many members can access this playbook, and how many have used their invitation. " +
			"Their activity provides the outcomes shown on this page. " +
			"Counted as member keys: a key is carried by a harness, and the same key on two machines is one key."}
}

func outcomesPulse(o insights.Overview, w insights.Window, members Tile) string {
	helpedRate, measured := o.Funnel.HelpedRate()
	rateStr := "—"
	if measured {
		rateStr = fmt.Sprintf("%.0f%%", helpedRate*100)
	}
	adoptDelta, adoptGood := delta(o.Funnel.Adopted, o.PrevFunnel.Adopted)
	shownDelta, shownGood := delta(o.Funnel.Shown, o.PrevFunnel.Shown)
	declineRate, declineMeasured := o.Funnel.DeclineRate()
	declineStr := "—"
	if declineMeasured {
		declineStr = fmt.Sprintf("%.0f%%", declineRate*100)
	}
	// Org-scoped share is the headline MEASURE — whether the registry delivers
	// org-specific knowledge rather than generic advice — and it stays at the
	// front of this row's measures. Only the member count goes before it, and
	// only because every rate here is a rate over what those members did. Do
	// not demote org-scoped share into the composition panels.
	orgShare, orgShareN := insights.OrgShare(o.ShownByScope)
	orgShareStr := "—"
	if orgShareN > 0 {
		orgShareStr = fmt.Sprintf("%.0f%%", orgShare*100)
	}
	return string(TileRow(false,
		members,
		Tile{Label: "Org-scoped share", Value: orgShareStr, Good: true,
			Delta: nIf(orgShareN > 0, fmt.Sprintf("of %d shown", orgShareN)), Href: "/techniques?scope=org",
			Note: "The share of shown suggestions that use validated techniques from your organization."},
		Tile{Label: "Adoptions", Value: fmtCount(o.Funnel.Adopted), Delta: adoptDelta, Good: adoptGood,
			Spark: Sparkline(o.AdoptionSpark)},
		Tile{Label: "Helped rate", Value: rateStr, Delta: nIf(measured, fmt.Sprintf("n=%d", o.Funnel.Adopted)), Good: true,
			Spark: Sparkline(o.HelpedRateSpark), Href: "/outcomes/helped-rate?w=" + url.QueryEscape(w.Key)},
		// The evidence behind a count of shown suggestions is the record of those
		// moments, so this lands on the Events feed — not Review's adoption
		// worklist, which drills into adoption, not shown.
		Tile{Label: "Suggestions shown", Value: fmtCount(o.Funnel.Shown), Delta: shownDelta, Good: shownGood,
			Spark: Sparkline(o.ShownSpark), Href: "/outcomes/events?w=" + url.QueryEscape(w.Key)},
		// The share of candidates the unprompted fit-check judged off-target and
		// never showed — ambient only (insights.Funnel.DeclineRate), because a
		// technique a member pulled up was never at risk of being declined and its
		// presence in the denominator only flatters the number.
		//
		// With config.MinSimilarity at 0 there is no relevance floor: retrieval
		// proposes its top candidates on EVERY turn and the fit-check declines
		// whatever does not fit, so a high share is the designed behaviour, not a
		// verdict on the embedder. " — " until candidates reach a verdict.
		Tile{Label: "Filtered before showing", Value: declineStr,
			Delta: nIf(declineMeasured, fmt.Sprintf("n=%d", o.Funnel.DeclineSample())), Good: true,
			Spark: Sparkline(o.DeclineRateSpark),
			Note:  "The share of unprompted techniques that the fit-check removed before showing them. A high share can mean that many turns had no useful suggestion. The count excludes techniques requested by a member."},
	))
}

// outcomesStandouts computes the window's highlights — the sentences a
// colleague would actually say when asked "anything interesting in the
// numbers?" — and renders each as a doorway to its evidence. Every item is
// data-gated: nothing renders as an empty frame, and the whole panel is
// omitted when the window has nothing to say (the caller then gives the
// activity chart the full row).
type standout struct {
	eyebrow, head, detail, href string
}

func outcomesStandouts(o insights.Overview, rep organization.Report, w insights.Window) string {
	var items []standout

	// Highest helped rate: the top shrinkage-ranked technique that clears the rate
	// floor — the same bar the leaderboard applies.
	starID := ""
	for _, c := range o.Best {
		if c.Funnel.Adopted < minRankedAdoptions {
			continue
		}
		rate, _ := c.Funnel.HelpedRate()
		starID = c.Technique.ID
		items = append(items, standout{
			eyebrow: "Highest helped rate", head: c.Technique.Name,
			detail: fmt.Sprintf("%.0f%% helped rate · n=%d adoptions", rate*100, c.Funnel.Adopted),
			href:   drillHref(c.Technique.ID, w.Key)})
		break
	}
	// Largest adoption increase: the fastest mover by adoption velocity — skipping the
	// star above; two slots naming the same technique would say half as much.
	for _, c := range o.Fastest {
		if c.Funnel.Adopted == 0 || c.Technique.ID == starID {
			continue
		}
		detail := fmt.Sprintf("%d adoptions this window", c.Funnel.Adopted)
		if w.PrevStart.Before(w.Start) && c.Velocity() != 0 {
			detail += fmt.Sprintf(" · Δ%+d from the previous window", c.Velocity())
		}
		items = append(items, standout{eyebrow: "Largest adoption increase", head: c.Technique.Name,
			detail: detail, href: drillHref(c.Technique.ID, w.Key)})
		break
	}
	// Widest gap: the cohort with the most headroom against peers who already
	// see helped outcomes in the same area. Rate gaps only rank on real
	// exposure (≥3 shown) — one impression is not a pattern.
	if gap, ok := widestGap(rep); ok {
		items = append(items, gap)
	}
	// The fourth slot: decay first (it erodes trust), else the loudest dismissal
	// reason (it names the friction).
	if n := len(o.Decayed); n > 0 {
		items = append(items, standout{eyebrow: "Needs review",
			head:   fmt.Sprintf("%d technique%s in decay", n, plural(n)),
			detail: "recent outcomes are below their baseline",
			href:   "/review?w=" + url.QueryEscape(w.Key)})
	} else if len(o.Dismissals) > 0 && o.Dismissals[0].Count > 0 {
		d := o.Dismissals[0]
		items = append(items, standout{eyebrow: "Most common dismissal",
			head:   fmt.Sprintf("%d dismissed as %s", d.Count, d.Label),
			detail: dismissalReasonNote(d.Label),
			href:   "/outcomes/dismissals/" + url.PathEscape(d.Label) + "?w=" + url.QueryEscape(w.Key)})
	}

	if len(items) == 0 {
		return ""
	}
	if len(items) > 4 {
		items = items[:4]
	}
	var b strings.Builder
	b.WriteString(`<section class="panel" id="standouts"><h2>Summary</h2><ul class="standouts">`)
	for _, s := range items {
		fmt.Fprintf(&b, `<li><a class="standout" href="%s"><span class="standout-eyebrow">%s</span><span class="standout-head">%s</span><span class="standout-detail">%s</span></a></li>`,
			html.EscapeString(s.href), html.EscapeString(s.eyebrow),
			html.EscapeString(s.head), html.EscapeString(s.detail))
	}
	b.WriteString(`</ul></section>`)
	return b.String()
}

// widestGap picks the single largest cohort-vs-peers adoption-rate gap for the
// standouts panel — the one opportunity worth leading with.
func widestGap(rep organization.Report) (standout, bool) {
	var out standout
	areaByKey := map[string]organization.Area{}
	for _, a := range rep.Areas {
		areaByKey[a.Key] = a
	}
	best := 0.0
	found := false
	for _, op := range rep.Opportunities {
		if op.Target.Shown < 3 || op.Peer.Shown == 0 {
			continue
		}
		gap := float64(op.Peer.Adopted)/float64(op.Peer.Shown) - float64(op.Target.Adopted)/float64(op.Target.Shown)
		if gap <= best {
			continue
		}
		best, found = gap, true
		a := areaByKey[op.Area]
		out = standout{
			eyebrow: "Widest gap",
			head:    op.Cohort + " · " + a.Label,
			detail: fmt.Sprintf("peers adopt at %.0f%%, this cohort at %.0f%%",
				float64(op.Peer.Adopted)/float64(op.Peer.Shown)*100,
				float64(op.Target.Adopted)/float64(op.Target.Shown)*100),
			href: "/outcomes/cohorts/" + url.PathEscape(op.Dimension+":"+op.Cohort) +
				"?w=" + url.QueryEscape(rep.Overview.Window.Key),
		}
	}
	return out, found
}

func outcomesLeaderboards(o insights.Overview, w insights.Window) string {
	maxVel, maxScoreAdopted := 1, 1
	for _, c := range o.Fastest {
		if c.Funnel.Adopted > maxVel {
			maxVel = c.Funnel.Adopted
		}
	}
	for _, c := range o.Best {
		if c.Funnel.Adopted > maxScoreAdopted {
			maxScoreAdopted = c.Funnel.Adopted
		}
	}
	// "all" resolves with PrevStart == Start: an empty previous interval, so a
	// Δ against it would restate the current count while looking like growth.
	hasPrev := w.PrevStart.Before(w.Start)
	fastest := make([]BarRow, 0, len(o.Fastest))
	for _, c := range o.Fastest {
		sub := ""
		if hasPrev {
			sub = fmt.Sprintf("Δ%+d", c.Velocity())
		}
		fastest = append(fastest, BarRow{
			Label: c.Technique.Name, Value: c.Funnel.Adopted, Max: maxVel, Key: "s2",
			Sub: sub, Href: drillHref(c.Technique.ID, w.Key)})
	}
	// A rate needs a denominator before it means anything. Below the floor the
	// board used to open with four techniques at "100%, n=1" — a ranking that
	// says nothing and reads as though it says everything. Those techniques
	// are not hidden: they are on Techniques, and each has its own detail page
	// with the raw counts.
	//
	// Selection is shrinkage-ranked (so a 100%-of-3 doesn't outrank a 92%-of-
	// 100), but DISPLAY sorts by the printed rate: a board whose bars step
	// downward by a hidden score looks mis-sorted. Each bar also carries the
	// org average as a reference tick, because bars that all sit at 83–94%
	// of a 0–100 track are indistinguishable without something to differ from.
	orgRate, orgMeasured := o.Funnel.HelpedRate()
	best := make([]BarRow, 0, len(o.Best))
	for _, c := range o.Best {
		if c.Funnel.Adopted < minRankedAdoptions {
			continue
		}
		rate, _ := c.Funnel.HelpedRate()
		row := BarRow{
			Label: c.Technique.Name, Value: int(rate * 100), Max: 100, Key: "s4",
			Sub: fmt.Sprintf("n=%d", c.Funnel.Adopted), Href: drillHref(c.Technique.ID, w.Key)}
		if orgMeasured {
			row.Ref = orgRate
		}
		best = append(best, row)
	}
	sort.SliceStable(best, func(i, j int) bool { return best[i].Value > best[j].Value })

	// Two-up, not three-up: technique names are sentences, and a third column
	// squeezed the label track until every name wrapped to shreds. New activity
	// takes its own full-width row below — it is a list of names, and reads
	// better long than narrow.
	var b strings.Builder
	b.WriteString(`<div class="grid even">`)
	b.WriteString(`<section class="panel"><h2>Most adoptions</h2>` +
		ui.Sub("", "this window · \u0394 from the previous"))
	b.WriteString(string(HBars(fastest, "Try a longer window to include adoptions")))
	unit := "of adoptions"
	legend := ""
	if orgMeasured {
		// The marker on every track is the org average. It is named once, in a
		// legend beside the mark, instead of in a sentence over the panel.
		legend = fmt.Sprintf(`<div class="legend"><span class="lg"><i class="ref-key"></i>`+
			`org average <b>%.0f%%</b></span></div>`, orgRate*100)
	}
	fmt.Fprintf(&b, `</section><section class="panel"><h2>Highest helped rate</h2>%s%s`,
		ui.Sub("", unit), legend)
	b.WriteString(string(HBars(best, helpedRateFloorNote)))
	b.WriteString(ui.Fine(fmt.Sprintf(
		`Techniques need at least %d adoptions to rank by helped rate.`, minRankedAdoptions)))
	b.WriteString(`</section></div>`)
	b.WriteString(`<section class="panel"><h2>New activity</h2>`)
	b.WriteString(freshList(o.Fresh, o.Now, w.Key))
	b.WriteString(`</section>`)
	return b.String()
}

// outcomesCohorts is the former Organization page, now a section: the map of
// cohort × technique area, then what is spreading, where the gaps are, and who
// has established a practice.
func outcomesCohorts(o insights.Overview, rep organization.Report, filters organizationFilters, w insights.Window) string {
	// The cross-dimension ranking, which the map falls back to when it has
	// fewer than two cohorts to compare.
	maxCohort := 1
	for _, c := range o.Cohorts {
		if c.Count > maxCohort {
			maxCohort = c.Count
		}
	}
	cohortRows := make([]BarRow, 0, len(o.Cohorts))
	for _, c := range o.Cohorts {
		cohortRows = append(cohortRows, BarRow{Label: c.Label, Value: c.Count, Max: maxCohort, Key: "s1",
			Href: "/outcomes/cohorts/" + url.PathEscape(c.Label) + "?w=" + url.QueryEscape(w.Key)})
	}
	// Tight-label variant: the default .bars label column is fractional, sized
	// for narrow three-up panels with long technique names. In the full-width
	// map panel the labels are short cohort tags, so a fractional column leaves
	// most of the panel as a void between label and bar.
	fallback := `<div class="bars-tight-label">` +
		string(HBars(cohortRows, "no cohort-tagged adoptions in this window")) + `</div>`

	var b strings.Builder
	// The lens heads the block it governs, so its effect is visible from where it
	// is operated — every panel below this line regroups when it changes, and
	// nothing above it does.
	b.WriteString(cohortLensNav(rep, w.Key, filters))
	b.WriteString(organizationMap(rep, filters, fallback))
	return b.String()
}

// outcomesComposition answers "what KIND of knowledge is moving" — how much of
// it is the organization's own, where it came from, why it gets turned down,
// and how far the inferred signals can be trusted. Org shares render as
// meters (a nearly-full bar says "nearly all" the way two digits don't), and
// a distribution that has collapsed to a single segment renders as the
// sentence it is instead of an all-one-color bar pretending to be a chart.
func outcomesComposition(o insights.Overview, w insights.Window) string {
	srcHref := func(prov string) string {
		return "/outcomes/source/" + url.PathEscape(prov) + "?w=" + url.QueryEscape(w.Key)
	}

	var b strings.Builder
	b.WriteString(`<div class="grid three" id="mix"><section class="panel"><h2>Organizational knowledge</h2>` +
		ui.Sub("", "of what members receive"))
	if share, n := insights.OrgShare(o.ShownByScope); n > 0 {
		b.WriteString(string(Meter("Org-scoped share of suggestions", share, fmt.Sprintf("of %d shown", n))))
		if adoptedShare, adoptedN := insights.OrgShare(o.AdoptedByScope); adoptedN > 0 {
			b.WriteString(string(Meter("Org-scoped share of adoptions", adoptedShare, fmt.Sprintf("of %d adopted", adoptedN))))
		}
	} else {
		b.WriteString(`<p class="empty">no suggestions in this window yet</p>`)
	}
	b.WriteString(`</section><section class="panel"><h2>Live techniques by scope and source</h2>`)
	b.WriteString(`<div class="mix-label">by scope</div>`)
	b.WriteString(mixOrLine(o.TechniquesByScope, []string{"s1", "s3"}, "no live techniques", "live techniques",
		func(scope string) string { return techniquesFilterHref("", scope) }))
	b.WriteString(`<div class="mix-label">by source</div>`)
	b.WriteString(mixOrLine(o.TechniquesByProvenance, mixKeys, "no live techniques", "live techniques", srcHref))
	b.WriteString(`<div class="mix-label">adoptions by source</div>`)
	b.WriteString(mixOrLine(o.AdoptedByProvenance, mixKeys, "no adoptions in this window", "adoptions", srcHref))
	b.WriteString(`</section><section class="panel"><h2>Dismissals and signal trust</h2>` +
		ui.Fine(`Not-relevant feedback adjusts retrieval. Didn\u2019t-work feedback contributes to decay. Explicit feedback corrects inferred feedback.`))
	b.WriteString(`<div class="mix-label">dismissal reasons</div>`)
	b.WriteString(string(LinkedMixBar(o.Dismissals, mixKeys, "no dismissals in this window",
		func(reason string) string {
			return "/outcomes/dismissals/" + url.PathEscape(reason) + "?w=" + url.QueryEscape(w.Key)
		})))
	b.WriteString(`<div class="mix-label">signal trust</div>`)
	b.WriteString(string(LinkedMixBar(trustMix(o), []string{"s1", "s3"}, "no reactions in this window",
		func(string) string { return "/outcomes/signal-trust?w=" + url.QueryEscape(w.Key) })))
	b.WriteString(healthStrip(o))
	b.WriteString(`</section></div>`)
	return b.String()
}

// mixOrLine renders a distribution bar — unless the distribution has only one
// segment, in which case a 100%-one-color bar carries no information and the
// fact reads better as a sentence ("All 25 · contributed").
func mixOrLine(entries []insights.MixEntry, keys []string, empty, noun string, hrefFor func(string) string) string {
	if len(entries) == 1 {
		e := entries[0]
		label := html.EscapeString(e.Label)
		if h := hrefFor(e.Label); h != "" {
			label = fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(h), label)
		}
		return fmt.Sprintf(`<p class="mix-one">All <b>%s</b> %s · %s</p>`,
			fmtCount(e.Count), html.EscapeString(noun), label)
	}
	return string(LinkedMixBar(entries, keys, empty, hrefFor))
}
