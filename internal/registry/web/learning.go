// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Learning page: the synthesis layer's evidence-readiness view — the
// "dark state" docs/learning/synthesis-design.md mandates. Findings land here when the
// detectors' floors are met (docs/learning/synthesis-plan.md phase 1); until then this
// page shows the one thing that IS true today: how close the evidence is to
// each phase's trigger. The design's kill-criteria warn that the moment a
// dashboard looks dead, someone proposes lowering the floors — this page is
// the counter-move: it makes the floors visible instead.
package web

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// pageWorkflowTraces is the "how members work" view: the recurring session shapes
// reconstructed from audit facts (docs/learning/workflow-technique-capture.md). The
// substrate for workflow-technique discovery, and useful on its own — it shows whether
// effective within-session workflows actually recur. Aggregate-only: ordered
// task_type phases per session, never content, never identities.
func (s *Server) pageWorkflowTraces(r *http.Request, user oidc.Claims) page {
	facts, err := s.Store.AuditFacts("")
	if err != nil {
		return storeUnavailablePage("learning", nil, err)
	}
	shapes := insights.SessionTraces(facts, 2)
	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Recurring sequences of session phases, inferred from tool use and ordered by session count. This view contains aggregate counts only, with no content or identities. Workflow-technique discovery groups these sequences into candidate techniques.</p></div>`)
	if len(shapes) == 0 {
		b.WriteString(`<p class="empty">No phase sequence occurs in two or more sessions yet.</p>`)
		return page{active: "learning", content: b.String(),
			crumbs: s.learningCrumbs("How members work")}
	}
	b.WriteString(`<section class="panel"><div class="table-wrap"><table class="list"><thead><tr>` +
		`<th>phase sequence</th><th class="num">sessions</th><th class="num">cohorts</th></tr></thead><tbody>`)
	for _, sh := range shapes {
		fmt.Fprintf(&b, `<tr><td>%s</td><td class="num">%d</td><td class="num">%d</td></tr>`,
			html.EscapeString(strings.Join(sh.Phases, " → ")), sh.Sessions, sh.Cohorts)
	}
	b.WriteString(`</tbody></table></div></section>`)
	return page{active: "learning", content: b.String(),
		crumbs: s.learningCrumbs("How members work")}
}

// Trigger thresholds from docs/learning/synthesis-plan.md. Deliberately duplicated as
// named constants here rather than imported from a synthesis package that does
// not exist yet — when phase 1 lands, these move with the detectors.
const (
	p1DaysOfFacts      = 14  // phase 1: ≥2 weeks of facts to run detectors against
	p3HelpedNeeded     = 20  // phase 3: helped events in a 28-day window ...
	p3TechniquesNeeded = 3   // ... across at least this many techniques
	p4FactsNeeded      = 500 // phase 4: facts for session clustering ...
	p4CohortsNeeded    = 3   // ... across distinct cohorts
)

// signalEpoch is when adoption became a verified act. Before this date
// adoption was inferred from single-token collision and the provenance is
// untrustworthy, which is why computeReadiness counts pre-epoch helped events
// separately (r.PreEpoch) instead of dropping or including them — the date is
// not arbitrary.
var signalEpoch = time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)

// readiness is the pure computation behind the page — testable without HTTP.
type readiness struct {
	FactsTotal  int
	FirstFactAt time.Time
	DaysOfFacts int   // distinct calendar days spanned since the first fact
	FactsPerDay []int // last 14 days, oldest first

	WithTaskType, WithModel, WithResources, WithCohort int
	EnrichedAbsent                                     int // facts carrying a tools_absent inference

	Helped28d        int // helped events in the last 28 days, post-epoch only
	HelpedTechniques int // distinct techniques those events cover
	PreEpoch         int // helped events excluded for old provenance

	Cohorts int // distinct non-empty team values across facts
}

func computeReadiness(facts []models.AuditFact, events []models.FeedbackEvent,
	now time.Time) readiness {
	r := readiness{FactsTotal: len(facts), FactsPerDay: make([]int, p1DaysOfFacts)}
	teams := map[string]bool{}
	for _, f := range facts {
		at, err := time.Parse(time.RFC3339Nano, f.CreatedAt)
		if err != nil {
			continue
		}
		if r.FirstFactAt.IsZero() || at.Before(r.FirstFactAt) {
			r.FirstFactAt = at
		}
		if day := int(now.Sub(at).Hours() / 24); day >= 0 && day < p1DaysOfFacts {
			r.FactsPerDay[p1DaysOfFacts-1-day]++
		}
		if f.TaskType != "" {
			r.WithTaskType++
		}
		if f.Model != "" {
			r.WithModel++
		}
		if len(f.Resources) > 0 {
			r.WithResources++
		}
		if len(f.ToolsAbsent) > 0 {
			r.EnrichedAbsent++
		}
		if team := f.Segment["team"]; team != "" {
			teams[team] = true
			r.WithCohort++
		}
	}
	r.Cohorts = len(teams)
	if !r.FirstFactAt.IsZero() {
		r.DaysOfFacts = int(now.Sub(r.FirstFactAt).Hours()/24) + 1
	}

	techniques := map[string]bool{}
	cutoff := now.AddDate(0, 0, -28)
	for _, e := range events {
		if e.Stage != "helped" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
		if err != nil || at.Before(cutoff) {
			continue
		}
		if at.Before(signalEpoch) {
			r.PreEpoch++ // real events, old provenance — counted separately, honestly
			continue
		}
		r.Helped28d++
		techniques[e.TechniqueID] = true
	}
	r.HelpedTechniques = len(techniques)
	return r
}

// triggerRow renders one phase trigger as "have / need" with a plain-language
// state — no invented progress theater, just the arithmetic.
func triggerRow(b *strings.Builder, phase, what string, have, need int, extra string) {
	state := fmt.Sprintf("%d of %d", have, need)
	cls := "trigger-wait"
	if have >= need {
		state, cls = "met", "trigger-met"
	}
	fmt.Fprintf(b, `<tr><td>%s</td><td>%s</td><td class="%s">%s</td><td class="hint">%s</td></tr>`,
		html.EscapeString(phase), html.EscapeString(what), cls, state, html.EscapeString(extra))
}

// learningGates is every capability and every requirement that opens it, built
// once from one readiness and read by the picture, the table and anything else
// that reports state — which is what keeps them from telling three stories.
//
// The plan's later triggers are COMPOUND, and that is the whole point of the
// list: Experiments wants twenty helped events and the spread of techniques
// those events cover, and Dynamic cohorts wants a corpus and the cohorts it
// spans. Passing one half of either is how a gate opened on evidence that meets
// nothing.
func learningGates(rd readiness) []learningGate {
	helped := learningNeed{What: "helped events (28d)", Have: rd.Helped28d, Need: p3HelpedNeeded}
	if rd.PreEpoch > 0 {
		helped.Note = fmt.Sprintf("excludes %d helped events recorded before verified adoption", rd.PreEpoch)
	}
	return []learningGate{
		{Name: "Findings and detectors", Needs: []learningNeed{
			{What: "days of accrued facts", Have: rd.DaysOfFacts, Need: p1DaysOfFacts},
		}},
		{Name: "Experiments", Needs: []learningNeed{
			helped,
			{What: "techniques with outcomes", Have: rd.HelpedTechniques, Need: p3TechniquesNeeded},
		}},
		{Name: "Dynamic cohorts", Needs: []learningNeed{
			{What: "audit facts", Have: rd.FactsTotal, Need: p4FactsNeeded},
			{What: "distinct cohorts", Have: rd.Cohorts, Need: p4CohortsNeeded,
				Note: "each member forms one cohort"},
		}},
	}
}

func (s *Server) pageLearning(r *http.Request, user oidc.Claims) page {
	facts, err := s.Store.AuditFacts("")
	if err != nil {
		return storeUnavailablePage("learning", nil, err)
	}
	events, err := s.Store.AllEvents("")
	if err != nil {
		return storeUnavailablePage("learning", nil, err)
	}
	rd := computeReadiness(facts, events, time.Now())

	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Evidence collected for findings, experiments, and cohort analysis.</p></div>`)
	b.WriteString(ui.Sub("", "progress toward each minimum") +
		ui.Fine(`Each capability becomes available when all of its evidence requirements are met.`))
	// The trail names it now: Learning readiness / Overview opens onto How
	// members work, so a link in the body saying the same thing is the second
	// copy of one door.

	// Which capabilities are running, and what is holding the rest shut. Built
	// from the same readiness the tiles and the table below are, so the picture
	// cannot drift from them. Each capability shows the ONE requirement still
	// short — the one an operator can act on.
	// The picture and the corpus it is drawn from, side by side: what is switched
	// on, and the four numbers that switch it. Full width the picture had half a
	// screen of empty board either side and the tiles were a row of four below it
	// — which put the gates and the figures that open them a scroll apart.
	gates := learningGates(rd)
	b.WriteString(`<div class="lrn-band"><section class="panel lrn-hero">` +
		learningScene(rd.FactsTotal, gates) + `</section><div class="lrn-tiles">`)

	// KPI tiles: the corpus as it accrues. Two by two beside the picture rather
	// than four across under it.
	b.WriteString(string(TileRow(false,
		Tile{Label: "Audit facts", Value: fmtCount(rd.FactsTotal), Good: true, Spark: Sparkline(rd.FactsPerDay)},
		Tile{Label: "Days of facts", Value: fmtCount(rd.DaysOfFacts),
			Delta: fmt.Sprintf("of %d needed", p1DaysOfFacts), Good: rd.DaysOfFacts >= p1DaysOfFacts},
		Tile{Label: "Helped (28d)", Value: fmtCount(rd.Helped28d),
			Delta: fmt.Sprintf("of %d needed", p3HelpedNeeded), Good: rd.Helped28d >= p3HelpedNeeded},
		Tile{Label: "Cohorts", Value: fmtCount(rd.Cohorts),
			Delta: fmt.Sprintf("of %d needed", p4CohortsNeeded), Good: rd.Cohorts >= p4CohortsNeeded},
	)))
	b.WriteString(`</div></div>`)

	// Trigger table: the plan's own gates, live.
	b.WriteString(`<section class="panel"><h2>Evidence requirements</h2>` +

		`<table class="list"><thead><tr><th>Capability</th><th>Needs</th><th>State</th><th></th></tr></thead><tbody>`)
	// The same gates the picture is drawn from, one row per requirement — so the
	// table cannot say a capability is two numbers short while the gate above it
	// stands open.
	for _, g := range gates {
		for _, n := range g.Needs {
			triggerRow(&b, g.Name, n.What, n.Have, n.Need, n.Note)
		}
	}
	b.WriteString(`</tbody></table></section>`)

	// Completeness: is what we capture worth analysing?
	b.WriteString(`<section class="panel"><h2>Fact completeness</h2>` +

		`<table class="list"><tbody>`)
	row := func(label string, have int, note string) {
		fmt.Fprintf(&b, `<tr><td>%s</td><td>%d of %d</td><td class="hint">%s</td></tr>`,
			html.EscapeString(label), have, rd.FactsTotal, html.EscapeString(note))
	}
	row("task type", rd.WithTaskType, "inferred from tools used in the turn")
	row("model", rd.WithModel, "comes from the transcript (Claude Code/Codex)")
	row("resources used", rd.WithResources, "repositories and organization systems used in the turn")
	row("cohort", rd.WithCohort, "")
	fmt.Fprintf(&b, `<tr><td>tools_absent inferred</td><td>%d</td><td class="hint">`+
		`absolute count of valid empty tool results</td></tr>`,
		rd.EnrichedAbsent)
	b.WriteString(`</tbody></table></section>`)

	return page{active: "learning", content: b.String(),
		crumbs: s.learningCrumbs("Overview")}
}
