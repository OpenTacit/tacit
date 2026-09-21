// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Review — the one queue of work waiting on a human.
//
// Three lists used to live on three unlinked pages: /drafts (promote or reject
// contributed and mined techniques), the decay watch (techniques whose helped rate
// fell below baseline), and the retrieval worklist (techniques shown but never
// adopted). They are the same job — knowledge that needs a decision — and it is
// the only job the registry asks a person to do on a schedule, so it gets a
// destination instead of three.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

func (s *Server) pageReview(r *http.Request, user oidc.Claims) page {
	techniques, events, w, now, err := s.insightsInputs(r)
	if err != nil {
		return storeUnavailablePage("review", nil, err)
	}
	drafts, err := s.Store.ListTechniques([]string{"draft"}, 200)
	if err != nil {
		return storeUnavailablePage("review", nil, err)
	}
	shadow, err := s.Store.ListTechniques([]string{"shadow"}, 200)
	if err != nil {
		return storeUnavailablePage("review", nil, err)
	}

	decayed := decayedTechniques(techniques)
	misses := insights.RetrievalMisses(techniques, events, now, w)
	var never, wasteful []insights.TechniqueStat
	for _, c := range misses {
		if c.Funnel.Adopted == 0 {
			never = append(never, c)
		} else {
			wasteful = append(wasteful, c)
		}
	}

	gate := shadowGate{auto: s.cfg().AutoPromoteEnabled,
		minFit: s.cfg().AutoPromoteMinFit, minJudged: s.cfg().AutoPromoteMinJudged}

	var b strings.Builder
	// The lede claims the page is everything that waits on a decision. Under
	// auto-promotion the shadow lane does not, so it says which part of the page
	// is not asking for anything rather than letting the claim cover it.
	lede := `Review drafts, techniques with declining helped rates, and techniques that ` + productHTML() + ` often shows but members do not adopt.`
	if gate.auto && len(shadow) > 0 {
		lede += ` Techniques under evaluation are also listed. Automatic evaluation promotes or retires them based on fit evidence.`
	}
	b.WriteString(`<div class="page-head"><p class="sub">` + lede + `</p></div>`)
	b.WriteString(windowSelect("/review", w.Key, now, insights.Earliest(events)))
	b.WriteString(reviewNav(len(drafts), len(shadow), len(decayed), len(never), s.consolidateWaiting("queue"), gate.auto))
	// The lane itself, before the panels that are each one stage of it. Built
	// from the counts already computed above, so the picture and the sections
	// below it cannot disagree.
	serving, _ := s.Store.ListTechniques(servingStatuses, 0)
	b.WriteString(`<section class="panel rev-hero">` + reviewScene(reviewCounts{
		Drafts: len(drafts), Shadow: len(shadow), Serving: len(serving),
		Decayed:    len(decayed),
		AutoShadow: s.cfg().AutoShadow, AutoPromote: s.cfg().AutoPromoteEnabled,
	}) + `</section>`)
	b.WriteString(s.reviewDrafts(drafts))
	b.WriteString(reviewShadow(shadow, insights.ShadowFits(events, insights.MergedInto(techniques)), gate))
	b.WriteString(discoverForm())
	b.WriteString(s.consolidatePanel("queue"))
	b.WriteString(s.reviewDecay(decayed, events, now, w))
	b.WriteString(reviewAdoption(never, wasteful, w, len(events) > 0))
	return page{active: "review", content: b.String()}
}

// reviewNav is the queue's contents line: one count per section, each jumping
// to its section. It is also the honest summary — when every count is zero the
// reader can stop reading here. The shadow item only appears once techniques are
// under evaluation, so a registry not using the shadow rung sees no change.
//
// Under auto-promotion the shadow count is not backlog: those techniques resolve
// themselves. It goes last and quiet, and it is left out of the sum that decides
// whether anything waits on the reader at all — otherwise a lane that needs
// nobody keeps the page from ever saying "nothing waits on you".
func reviewNav(drafts, shadow, decayed, never, duplicates int, autoShadow bool) string {
	blocking := drafts + decayed + never + duplicates
	if !autoShadow {
		blocking += shadow
	}
	cleanQueue := blocking == 0 && shadow == 0
	item := func(href, label string, n int, warn, quiet bool) string {
		cls := "rq-count"
		if warn && n > 0 {
			cls += " warn"
		}
		itemCls := "rq-item"
		if quiet {
			itemCls += " rq-quiet"
		}
		return fmt.Sprintf(`<a class="%s" href="%s"><span class="%s">%d</span> %s</a>`,
			itemCls, href, cls, n, html.EscapeString(label))
	}
	lead := ""
	switch {
	case cleanQueue:
		lead = `<p class="empty">No items need review. Members can add drafts with <code>/tacit:contribute</code>, ` +
			`or you can run a suggestion search below.</p>`
	case blocking == 0:
		lead = `<p class="empty">No items need review.</p>`
	}
	nav := `<nav class="review-queue" aria-label="Review queue">`
	if blocking > 0 {
		nav += item("#drafts", "drafts to review", drafts, false, false)
		if shadow > 0 && !autoShadow {
			nav += item("#shadow", "under evaluation", shadow, false, false)
		}
		nav += item("#decay", "decaying techniques", decayed, true, false) +
			item("#adoption", "awaiting first adoption", never, false, false)
	}
	// Always, even at zero — unlike the others, this one is also the way IN.
	// The rest of the queue counts work that arrived on its own; duplicates are
	// found by asking, and a link that appears only after you have asked is a
	// signpost that shows up once you no longer need it.
	if duplicates > 0 {
		nav += item("#consolidate", "duplicate candidates", duplicates, false, false)
	} else {
		// Its own class, not rq-quiet: that one marks a lane resolving without a
		// reader, and this is the opposite — an action only a reader can start.
		nav += `<a class="rq-item rq-find" href="#consolidate">Find duplicate candidates</a>`
	}
	if shadow > 0 && autoShadow {
		nav += item("#shadow", "automatic evaluation", shadow, false, true)
	}
	return lead + nav + `</nav>`
}

// shadowGate is how a technique leaves the shadow lane, which is what decides
// how this section presents itself. With auto-promotion on, the lane resolves
// both ways without a person — feedback.AutoPromoteShadow graduates a technique
// that clears MinFit over MinJudged verdicts, RetireStaleShadow drops one that
// rarely fits — so the section is a readout of work in progress. With it off,
// nothing moves until a reviewer decides, so the same section is a queue.
type shadowGate struct {
	auto      bool
	minFit    float64
	minJudged int
}

// reviewShadow lists techniques under evaluation (status=shadow): retrieved and
// fit-checked for relevance but never surfaced to a member, so they gather
// evidence with zero exposure (docs/learning/validation-without-review.md). The
// section renders nothing when no technique is in shadow, so it is invisible to
// a registry not using the rung.
//
// What it asks of the reader follows the gate. Under auto-promotion these
// techniques need nothing from anyone: the section says so, spends its evidence
// column on progress toward the gate rather than a bare rate, and drops the
// per-row buttons — a decision control on every row is what made a self-resolving
// lane read as a second backlog beside the drafts that genuinely block. The
// override still exists one click in, on the technique's own page, which is
// where the description and recipe the decision rests on actually are.
func reviewShadow(shadow []models.Technique, fits map[string]insights.ShadowFit, gate shadowGate) string {
	if len(shadow) == 0 {
		return ""
	}
	sort.Slice(shadow, func(i, j int) bool { return shadow[i].CreatedAt > shadow[j].CreatedAt })
	var b strings.Builder
	heading, intro := "Under evaluation: review required", ui.Fine(
		productHTML()+` retrieves and fit-checks these techniques for relevance but never shows `+
			`them to a member: evidence with zero exposure.`,
		`<strong>Observed</strong> techniques come from member actions recorded as successful.`,
		`Accept a technique to serve it, or reject it.`)
	if gate.auto {
		heading = "Under automatic evaluation"
		// The gate is a figure, so it goes in the sub-heading where a reader
		// scanning the fit column meets it.
		intro = ui.Sub("", fmt.Sprintf("promoted at %.0f%% fit over %d judgments",
			gate.minFit*100, gate.minJudged)) +
			ui.Fine(
				productHTML()+` retrieves and fit-checks these techniques for relevance but never `+
					`shows them to a member: evidence with zero exposure.`,
				`The registry promotes a technique when it meets the threshold above and retires it `+
					`when its fit rate is too low. Open a technique to accept or reject it now.`,
				`<strong>Observed</strong> techniques come from member actions recorded as successful.`)
	}
	fmt.Fprintf(&b, `<section class="panel" id="shadow"><h2>%s <span class="hint">(%d)</span></h2>`, heading, len(shadow))
	b.WriteString(intro)
	var rows strings.Builder
	for _, technique := range shadow {
		scope := technique.Scope
		if scope == "" {
			scope = "general"
		}
		tags := strings.Join(technique.Tags, ", ")
		// added stays the UTC date for the search key (the localiser adds the
		// reader's own date to it once the page loads); the cell shows a <time>
		// the browser re-dates into the reader's zone.
		added := dateOf(technique.CreatedAt)
		addedHTML := ui.LocalTimeISO(technique.CreatedAt, ui.LTYMD)
		cid := html.EscapeString(technique.ID)
		origin := technique.Provenance
		if origin == "" {
			origin = "—"
		}
		// Fit evidence gathered with zero exposure. Un-judged techniques read
		// "not yet judged" rather than a false 0%.
		//
		// Under auto-promotion the cell counts toward the bar the technique has
		// to clear, because that is the question a reader of this lane actually
		// has: not "how well does it fit" but "is it getting there". A bare rate
		// cannot answer it — 100% over one verdict and 62% over twenty look alike
		// and mean opposite things. Without auto-promotion there is no bar to
		// count toward, so the cell states the rate and the sample and stops.
		fit := "not yet judged"
		if f, ok := fits[technique.ID]; ok {
			if rate, has := f.Rate(); has {
				switch {
				// Auto-retire is not gated by the promotion flag — jobs.go runs
				// RetireStaleShadow on every recompute cycle — so a technique under
				// the floor is on its way out in BOTH modes. Counting it toward a
				// promotion bar it will never reach would be the wrong future.
				case f.Judged() >= config.ShadowRetireMinSample && rate <= config.ShadowRetireFloor:
					fit = fmt.Sprintf("%.0f%% · retiring", rate*100)
				case !gate.auto:
					fit = fmt.Sprintf("%.0f%% · judged %d", rate*100, f.Judged())
				case f.Judged() >= gate.minJudged && rate >= gate.minFit:
					fit = fmt.Sprintf("%.0f%% · clears the bar", rate*100)
				case f.Judged() >= gate.minJudged:
					fit = fmt.Sprintf("%.0f%% · under the bar", rate*100)
				default:
					fit = fmt.Sprintf("%.0f%% · %d of %d judged", rate*100, f.Judged(), gate.minJudged)
				}
			}
		}
		searchKey := html.EscapeString(strings.ToLower(strings.Join([]string{
			technique.ID, technique.Name, scope, tags, origin, added, technique.Description}, " ")))
		// The decision column exists only when the decision is actually being
		// asked for. Under auto-promotion it is on the technique's own page.
		actions := ""
		if !gate.auto {
			actions = fmt.Sprintf(
				`<td class="row-action"><form method="post" action="/admin/techniques/shadow-promote/%s"><button class="promote" type="submit" `+
					`title="Start to serve this technique in retrieval.">Accept</button></form>`+
					`<form method="post" action="/admin/techniques/shadow-reject/%s"><button class="reject" type="submit">Reject</button></form></td>`,
				cid, cid)
		}
		// The name cell carries the origin a second time, shown only at the width
		// where the origin column folds away (.row-meta) — the panel says to read
		// a technique by its source, so a phone must not be the one screen that
		// drops it.
		//
		// data-href opens the technique in full (description, recipe, applies
		// when, not when — a shadow technique carries all of it). Without it these
		// rows still took app.css's pointer cursor from table.techniques and then
		// did nothing, which is the dead-looking-clickable row RowLinkScript
		// exists to prevent. The detail page is /techniques/, not /drafts/: the
		// draft page serves status=draft only. The Accept/Reject forms sit inside
		// the row, and the shared handler leaves clicks on a form to it.
		fmt.Fprintf(&rows,
			`<tr data-search="%s" data-href="/techniques/%s"><td>%s<span class="row-meta">%s</span></td><td>%s</td><td>%s</td>`+
				`<td class="num">%s</td><td>%s</td>%s</tr>`,
			searchKey, url.PathEscape(technique.ID),
			html.EscapeString(technique.Name), html.EscapeString(origin),
			html.EscapeString(tags), html.EscapeString(origin), html.EscapeString(fit), addedHTML, actions)
	}
	// shadow-queue marks the six-column shape: the decision column is there, so
	// every other column has to give up width for it. Without it the five
	// columns keep the room.
	cls, evidenceHead, actionHead := "techniques techniques-shadow data-table", "evidence", ""
	if !gate.auto {
		cls += " shadow-queue"
		evidenceHead, actionHead = "fit", "<th></th>"
	}
	fmt.Fprintf(&b, `<div class="table-wrap"><table class="%s"><thead><tr>`+
		`<th>name</th><th>tags</th><th>origin</th><th class="num">%s</th><th>added</th>%s</tr></thead><tbody>%s</tbody></table></div></section>`,
		cls, evidenceHead, actionHead, rows.String())
	return b.String()
}

// discoverForm is the "Find techniques from usage" button: run one observed-technique
// discovery pass now (docs/learning/observed-technique-discovery.md). Synchronous and
// admin-gated like the suggest button; the pass also runs automatically on the
// recompute cycle when TACIT_AUTO_DISCOVER is set.
func discoverForm() string {
	return `<section class="panel"><h2>Find techniques from usage</h2>` +
		`<div class="toolbar"><form method="post" action="/admin/discover">` +
		`<button type="submit">Group successful actions into candidate techniques</button></form></div>` +
		ui.Fine(`Groups successful member actions that have no technique into new candidates. `+
			productHTML()+` files them as <strong>observed</strong> and evaluates them above.`,
			`Auto-discovery runs this task on a schedule. This button runs it now.`) +
		`</section>`
}

func decayedTechniques(techniques []models.Technique) []models.Technique {
	var flagged []models.Technique
	for _, c := range techniques {
		if c.Status == "decayed" || c.DecaySignal != 0 {
			flagged = append(flagged, c)
		}
	}
	sort.Slice(flagged, func(i, j int) bool { return flagged[i].Name < flagged[j].Name })
	return flagged
}

// suggestForm is the research run that files new drafts. It is synchronous (the
// reviewer waits) and can take a minute or more, so a plain form POST would
// freeze the page and the progress bar could never advance. It submits via
// fetch instead: the page stays live and completion navigates back here. With
// no fetch the plain POST still works — just without the live feedback.
//
// The bar fills toward the median of recent run durations (data-est, seconds;
// 0 = no history yet, so an indeterminate bar). The fill reaches 90% at the
// estimate then creeps asymptotically toward 99%, so a slow run never sits at a
// false 100% — completion sets 100% just before navigating.
//
// A run outlives the page that started it: the pass is server-side, so a reload
// or a backgrounded phone tab loses the bar while the research keeps going.
// data-running carries the elapsed seconds of a pass already in flight, so the
// page comes back showing it rather than an armed button — and a click that
// does land on a busy registry is reported as "already running", not as a
// failure, because the run it collided with is the one about to file drafts.
func (s *Server) suggestForm() string {
	estSecs := s.suggestTimes.estimateSecs()
	running := -1 // -1 = idle; >=0 = seconds a pass has been going
	if elapsed, busy := suggestRuns.elapsed(); busy {
		running = int(elapsed.Seconds())
	}
	return `<form method="post" action="/admin/suggest" class="toolbar-right" id="suggest-form">` +
		`<button type="submit" id="suggest-btn" title="Search the web using this organization’s usage profile and add up to 10 general draft techniques for review.">` +
		`Suggest candidate techniques</button></form>` +
		// The button's own tooltip says what it does. What the drafts ARE is the
		// standing caveat, and it collapses like every other one on the
		// dashboard: the reader who never opens it is not misled, because the
		// drafts arrive tagged general in the table below.
		ui.Fine(`Techniques this button researches are <b>general</b>, not org-scoped: they count `+
			`toward the general share until members adopt them and the outcomes show help.`,
			`Org-scoped techniques come from member work and repository conventions. `+
				`<a href="/outcomes#mix">View the scope mix</a>.`) +
		// data-href carries the done-navigation target as markup (not a JS
		// literal) so the base-path rebase in sendHTML applies to it.
		fmt.Sprintf(`<div id="suggest-status" class="suggest-status" hidden role="status" aria-live="polite" data-est="%d" data-running="%d" data-href="/review">`, estSecs, running) +
		`<div class="suggest-bar"><div id="suggest-fill" class="suggest-bar-fill"></div></div>` +
		`<span class="suggest-line">` +
		`Searching the web using this organization’s usage profile… <span id="suggest-elapsed">0:00</span><span id="suggest-est"></span></span>` +
		`<span class="sub" id="suggest-hint">New drafts will appear here when it finishes. Keep this tab open.</span>` +
		`</div>` +
		`<script>(function(){` +
		`var f=document.getElementById('suggest-form'),b=document.getElementById('suggest-btn'),` +
		`s=document.getElementById('suggest-status'),el=document.getElementById('suggest-elapsed'),` +
		`bar=document.getElementById('suggest-fill'),estEl=document.getElementById('suggest-est'),` +
		`hint=document.getElementById('suggest-hint');` +
		`if(!f||!window.fetch)return;` +
		`function fmt(n){return Math.floor(n/60)+':'+String(Math.floor(n%60)).padStart(2,'0');}` +
		`var est=parseInt(s.getAttribute('data-est')||'0',10),tick=null;` +
		`function frac(t){if(t<est)return 0.9*(t/est);return 0.9+0.09*(1-Math.exp(-(t-est)/est));}` +
		// begin shows the live panel with the clock started `offset` seconds ago,
		// so a run adopted from the server resumes at its true elapsed time.
		`function begin(offset){s.hidden=false;b.disabled=true;b.textContent='Research in progress…';` +
		`if(est>0){estEl.textContent=' / ~'+fmt(est)+' expected';}else{s.querySelector('.suggest-bar').classList.add('indeterminate');}` +
		`var t0=Date.now()-offset*1000;clearInterval(tick);tick=setInterval(function(){var t=(Date.now()-t0)/1000;` +
		`el.textContent=fmt(t);if(est>0){bar.style.width=(frac(t)*100).toFixed(1)+'%';}},250);}` +
		`function finish(){clearInterval(tick);bar.style.width='100%';window.location=s.getAttribute('data-href');}` +
		`function stop(msg){clearInterval(tick);s.innerHTML='<span>'+msg+' <a href="'+s.getAttribute('data-href')+'">Reload review</a>.</span>';}` +
		// watch follows a pass this tab is not itself waiting on — one left
		// running by an earlier page load, or started by a colleague — and
		// reloads into its drafts when it lands.
		`function watch(){setTimeout(function(){` +
		`fetch(f.action+'/status',{credentials:'same-origin',headers:{'Accept':'application/json'}})` +
		`.then(function(r){return r.json();}).then(function(j){if(j.running){watch();}else{finish();}})` +
		`.catch(function(){watch();});},3000);}` +
		`var live=parseInt(s.getAttribute('data-running')||'-1',10);` +
		`if(live>=0){hint.textContent='This run was started earlier and is still going. Its drafts appear here when it finishes.';` +
		`begin(live);watch();}` +
		`f.addEventListener('submit',function(e){` +
		`e.preventDefault();begin(0);` +
		`fetch(f.action,{method:'POST',credentials:'same-origin',headers:{'Accept':'application/json'}}).then(function(r){` +
		`if(r.ok){finish();return;}` +
		// 409 is not a failure: a pass is already running and will file its
		// drafts. Keep the bar up and follow that one instead.
		`if(r.status===409){hint.textContent='A run was already in progress, so this click joined it rather than starting a second one.';watch();return;}` +
		`r.json().then(function(j){stop(j&&j.error?j.error:'Suggestion run failed ('+r.status+').');})` +
		`.catch(function(){stop('Suggestion run failed ('+r.status+').');});` +
		`}).catch(function(){stop('Could not reach the server.');});` +
		`});})();</script>`
}

func (s *Server) reviewDrafts(drafts []models.Technique) string {
	// Newest first (RFC3339 sorts lexicographically); ListTechniques' by-id order
	// stays as the API contract.
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].CreatedAt > drafts[j].CreatedAt })

	var b strings.Builder
	// "waiting on you" is the half of the heading that distinguishes this lane
	// from the shadow one below it: a draft is inert — held out of retrieval,
	// fit-checked by nobody, gathering nothing — so it moves only when a person
	// moves it. "Drafts" stays in front because it is the status name the API,
	// the URLs and the /tacit:drafts skill all use.
	fmt.Fprintf(&b, `<section class="panel" id="drafts"><h2>Drafts to review <span class="hint">(%d)</span></h2>`, len(drafts))
	if len(drafts) == 0 {
		b.WriteString(ui.Fine(`Contributed and mined techniques arrive here for promotion. `+
			`Members contribute with <code>/tacit:contribute</code> in-harness.`) +
			`<div class="toolbar">` + s.suggestForm() + `</div></section>`)
		return b.String()
	}
	b.WriteString(ui.Fine(`Drafts are not retrieved or measured until you accept them.`))

	// The action bar sits ABOVE the table: the four decisions on the left
	// (disabled until something is checked, with a live count), suggest on the
	// right. One form carries all four — the checkboxes reference it by id, so
	// the table stays outside any form and row navigation keeps working — and
	// each button posts to its own action through formaction. The three
	// decisions are the draft page's own Accept, Shadow and Reject, applied to
	// a set; delete is the fourth because reject keeps a record and a missed
	// suggestion run is worth none. Every one of them re-checks the draft-only
	// rule server-side, and delete alone is confirmed client-side with the count.
	b.WriteString(`<div class="toolbar">` +
		`<form id="bulk-drafts" class="bulk-actions" method="post" action="/admin/techniques/bulk/reject">` +
		`<button class="promote" type="submit" disabled formaction="/admin/techniques/bulk/promote" ` +
		`title="Serve the selected drafts in retrieval. If agent autonomy is on, measured evidence can make them eligible for automatic use in autonomous sessions.">Accept</button>` +
		`<button type="submit" disabled formaction="/admin/techniques/bulk/to-shadow" ` +
		`title="` + productHTML() + ` retrieves and checks the selected drafts for relevance without showing them to members. This collects fit evidence before promotion.">Shadow</button>` +
		`<button class="reject" type="submit" disabled formaction="/admin/techniques/bulk/reject" ` +
		`title="Retire the selected drafts. They keep their record and are never served.">Reject</button>` +
		`<button class="danger" type="submit" disabled formaction="/admin/techniques/bulk/delete" ` +
		// Only the delete is confirmed: the other three are lifecycle moves a
		// reviewer can walk back from the technique itself.
		`data-confirm="Delete {n} draft{s}? You cannot undo this." ` +
		`title="Remove the selected drafts fully; reject keeps a record, delete keeps nothing">Delete selected</button></form>` +
		`<span class="sub" data-count-for="bulk-drafts"></span>` +
		s.suggestForm() + `</div>`)

	var rows strings.Builder
	for _, technique := range drafts {
		scope := technique.Scope
		if scope == "" {
			scope = "general"
		}
		contributor := strings.TrimSpace(technique.Source)
		if contributor == "" {
			contributor = "contributed"
		}
		if technique.Supersedes != "" {
			contributor = "revision of " + technique.Supersedes
		}
		tags := strings.Join(technique.Tags, ", ")
		// CreatedAt is RFC3339; the date prefix is what a reviewer needs, and it
		// still sorts correctly as text — in the reader's zone too, which is why
		// the localiser rewrites the cell to YYYY-MM-DD rather than to whatever
		// their locale would otherwise make of a date. added keeps the UTC text
		// for the search key.
		added := dateOf(technique.CreatedAt)
		searchKey := html.EscapeString(strings.ToLower(strings.Join([]string{
			technique.ID, technique.Name, scope, contributor, tags, added, technique.Description}, " ")))
		cid := html.EscapeString(technique.ID)
		fmt.Fprintf(&rows,
			`<tr data-search="%s" data-href="/drafts/%s">`+
				`<td class="sel"><input type="checkbox" form="bulk-drafts" name="id" value="%s" aria-label="Select %s"></td>`+
				`<td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			searchKey, cid, cid, html.EscapeString(technique.Name),
			html.EscapeString(technique.Name),
			html.EscapeString(contributor), html.EscapeString(tags), ui.LocalTimeISO(technique.CreatedAt, ui.LTYMD))
	}
	// data-table opts the table into the shell's shared header-sort script; the
	// selection column is skipped there (its header holds the select-all box).
	fmt.Fprintf(&b, `<div class="table-wrap"><table class="techniques data-table techniques-drafts"><thead><tr>`+
		`<th class="sel"><input type="checkbox" form="bulk-drafts" data-select-all aria-label="Select all drafts"></th>`+
		`<th>name</th><th>contributor</th><th>tags</th><th>added</th></tr></thead><tbody>%s</tbody></table></div>`, rows.String())

	b.WriteString(`</section>`)
	return b.String()
}

func (s *Server) reviewDecay(flagged []models.Technique, events []models.FeedbackEvent, now time.Time, w insights.Window) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<section class="panel" id="decay"><h2>Decaying techniques <span class="hint">(%d)</span></h2>`, len(flagged))
	if len(flagged) == 0 {
		b.WriteString(`<p class="empty">No technique’s helped rate has fallen below its baseline.</p></section>`)
		return b.String()
	}
	b.WriteString(ui.Sub("", "recent helped rate below baseline"))
	rows := make([]tableRow, 0, len(flagged))
	for _, c := range flagged {
		baseline := `<span class="muted">—</span>`
		if o, ok, err := s.Store.GetOutcome(c.ID, config.OverallKey); err == nil && ok && o.HelpedRate != nil {
			baseline = fmt.Sprintf("%.0f%%", *o.HelpedRate*100)
		}
		recent := `<span class="muted">—</span>`
		if hr, ok := insights.ComputeTechnique(c, events, now, w).RecentFunnel.HelpedRate(); ok {
			recent = fmt.Sprintf("%.0f%%", hr*100)
		}
		rows = append(rows, tableRow{
			Cells: []string{
				techniqueCell(c.Scope, c.Name),
				`<span class="status serious">` + html.EscapeString(c.Status) + `</span>`,
				baseline, recent, ui.LocalTimeISO(c.DecayChecked, ui.LTYMD),
			},
			Href: techniqueHref(c.ID, w.Key),
		})
	}
	cols := []tableCol{{Label: "technique"}, {Label: "status"}, {Label: "baseline", Num: true}, {Label: "recent", Num: true}, {Label: "checked"}}
	b.WriteString(dataTable(cols, rows))
	b.WriteString(`</section>`)
	return b.String()
}

// reviewAdoption is the retrieval worklist: techniques the registry keeps
// putting in front of members that nobody takes up. A miss is a retrieval
// problem (wrong technique, wrong moment) or a technique-quality problem — either
// way it is a human's call, which is why it belongs in the queue and not in a
// readings page.
// anyEvents says whether this window saw activity at all. Without it the empty
// state read "Members adopted every technique shown in this window at least
// once" on a registry where nothing had ever been shown to anybody — technically
// vacuous, and to a reader on their first morning a claim about colleagues they
// have not invited yet.
func reviewAdoption(never, wasteful []insights.TechniqueStat, w insights.Window, anyEvents bool) string {
	toRow := func(c insights.TechniqueStat) capRow {
		return capRow{c.Technique.ID, c.Technique.Name, c.Technique.Scope,
			[]string{fmtCount(c.Funnel.Shown), fmtCount(c.Funnel.Adopted), rateCell(c.Funnel.Adopted, c.Funnel.Shown, 0, false)}}
	}
	rows := func(list []insights.TechniqueStat) []capRow {
		out := make([]capRow, 0, len(list))
		for _, c := range list {
			out = append(out, toRow(c))
		}
		return out
	}
	heads := []string{"shown", "adopted", "adopt %"}

	var b strings.Builder
	fmt.Fprintf(&b, `<section class="panel" id="adoption"><h2>Awaiting first adoption <span class="hint">(%d)</span></h2>`, len(never))
	if len(never) == 0 {
		if anyEvents {
			b.WriteString(`<p class="empty">Members adopted every technique shown in this window at least once.</p>`)
		} else {
			b.WriteString(`<p class="empty">No techniques were shown in this period, so there is no adoption data.</p>`)
		}
	} else {
		b.WriteString(ui.Sub("", "shown, never adopted"))
		b.WriteString(capTable(heads, rows(never), w.Key))
	}
	if len(wasteful) > 0 {
		fmt.Fprintf(&b, `<h2 class="panel-sub">Adopted, but shown far more often <span class="hint">(%d)</span></h2>`, len(wasteful))
		b.WriteString(ui.Sub("", "ranked by the gap between shown and adopted"))
		b.WriteString(capTable(heads, rows(wasteful), w.Key))
	}
	b.WriteString(`</section>`)
	return b.String()
}
