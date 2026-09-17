// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
)

// allWindow includes every event (zero Start → no since cursor), so the fold
// tests exercise folding, not windowing.
var allWindow = insights.Window{Key: "all"}

func fe(audit, cap, stage, created string) models.FeedbackEvent {
	return models.FeedbackEvent{
		EventID: audit + cap + stage, AuditID: audit, TechniqueID: cap,
		Stage: stage, CreatedAt: created,
	}
}

var oneTechnique = []models.Technique{{ID: "techniqueA", Name: "Technique A"}, {ID: "techniqueB", Name: "Technique B"}}

// TestFoldMomentsCollapsesInteraction: the stages of one (audit, technique) collapse
// to a single moment named by funnel precedence, and the aggregate counts the
// interaction once per stage reached.
func TestFoldMomentsCollapsesInteraction(t *testing.T) {
	events := []models.FeedbackEvent{
		fe("a1", "techniqueA", "shown", "2026-07-01T10:00:00Z"),
		fe("a1", "techniqueA", "adopted", "2026-07-01T10:01:00Z"),
		fe("a1", "techniqueA", "helped", "2026-07-01T10:02:00Z"),
	}
	moments, aggs := foldMoments(events, nil, nil, oneTechnique, allWindow, nil, false)
	if len(moments) != 1 {
		t.Fatalf("want 1 moment, got %d: %+v", len(moments), moments)
	}
	if moments[0].Event != "helped" || moments[0].Type != "delivery" || moments[0].TechniqueName != "Technique A" {
		t.Fatalf("moment = %+v", moments[0])
	}
	if len(aggs) != 1 || aggs[0].Shown != 1 || aggs[0].Adopted != 1 || aggs[0].Helped != 1 {
		t.Fatalf("aggregate = %+v", aggs)
	}
}

// TestFoldMomentsDeIdentified is the privacy invariant: no audit id and no
// session hash may reach the client. The fold joins a fact by audit id, so both
// are in scope during folding — and neither may survive into the output.
func TestFoldMomentsDeIdentified(t *testing.T) {
	const auditSecret = "AUDIT-DO-NOT-LEAK"
	const sessionSecret = "SESSION-DO-NOT-LEAK"
	events := []models.FeedbackEvent{
		fe(auditSecret, "techniqueA", "shown", "2026-07-01T10:00:00Z"),
		fe(auditSecret, "techniqueA", "adopted", "2026-07-01T10:01:00Z"),
	}
	facts := []models.AuditFact{{AuditID: auditSecret, SessionHash: sessionSecret, TaskType: "debug"}}
	moments, _ := foldMoments(events, facts, nil, oneTechnique, allWindow, nil, false)
	raw, err := json.Marshal(moments)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); strings.Contains(s, auditSecret) || strings.Contains(s, sessionSecret) {
		t.Fatalf("identifier leaked into feed JSON: %s", s)
	}
	// The de-identified context (cohort/task) that IS allowed still comes through.
	if moments[0].TaskType != "debug" {
		t.Fatalf("task type dropped: %+v", moments[0])
	}
}

// TestFoldMomentsHidesTelemetry: declined / shadow groups are retrieval
// telemetry, hidden unless the caller opts in.
func TestFoldMomentsHidesTelemetry(t *testing.T) {
	events := []models.FeedbackEvent{
		fe("a1", "techniqueA", "declined", "2026-07-01T10:00:00Z"),
		fe("a2", "techniqueB", "shadow_shown", "2026-07-01T10:00:00Z"),
	}
	if moments, _ := foldMoments(events, nil, nil, oneTechnique, allWindow, nil, false); len(moments) != 0 {
		t.Fatalf("telemetry shown by default: %+v", moments)
	}
	moments, _ := foldMoments(events, nil, nil, oneTechnique, allWindow, nil, true)
	if len(moments) != 2 {
		t.Fatalf("want 2 telemetry moments, got %d: %+v", len(moments), moments)
	}
}

// TestFoldMomentsCuration: lifecycle events render as curation moments, and a
// cohort filter hides them (curation is org-level, belonging to no cohort).
func TestFoldMomentsCuration(t *testing.T) {
	lc := []models.LifecycleEvent{{
		TechniqueID: "techniqueA", TechniqueName: "Technique A", Kind: "promoted",
		Reason: "shadow fit 82%", CreatedAt: "2026-07-01T09:00:00Z",
	}}
	moments, _ := foldMoments(nil, nil, lc, oneTechnique, allWindow, nil, false)
	if len(moments) != 1 || moments[0].Type != "curation" || moments[0].Event != "promoted" || moments[0].Detail != "shadow fit 82%" {
		t.Fatalf("curation moment = %+v", moments)
	}
	if moments, _ := foldMoments(nil, nil, lc, oneTechnique, allWindow, map[string][]string{"team": {"payments"}}, false); len(moments) != 0 {
		t.Fatalf("curation shown under a cohort filter: %+v", moments)
	}
}

// TestFoldMomentsCohortFilter: the per-dimension cohort filter scopes delivery
// moments to a cohort by its dimension value (team=payments), the set semantics
// the Outcomes filter uses.
func TestFoldMomentsCohortFilter(t *testing.T) {
	e := fe("a1", "techniqueA", "shown", "2026-07-01T10:00:00Z")
	e.Segment = models.Segment{"team": "payments"}
	events := []models.FeedbackEvent{e}
	if m, _ := foldMoments(events, nil, nil, oneTechnique, allWindow, map[string][]string{"team": {"payments"}}, false); len(m) != 1 {
		t.Fatalf("matching cohort dropped: %+v", m)
	}
	if m, _ := foldMoments(events, nil, nil, oneTechnique, allWindow, map[string][]string{"team": {"core"}}, false); len(m) != 0 {
		t.Fatalf("non-matching cohort kept: %+v", m)
	}
}

// TestEventsCohortFilterIsTheSetFilter: the cohort control is the shared .fmenu
// set-filter with per-dimension params (team, harness, …), not a raw compound
// select — one checkbox popover per cohort dimension present in the data.
func TestEventsCohortFilterIsTheSetFilter(t *testing.T) {
	srv, ts := newServer(t)
	now := models.Now()
	seed := func(id, stage, dim, val string) {
		e := fe("aud-"+id, "techniqueA", stage, now)
		e.Segment = models.Segment{dim: val}
		if _, err := srv.Store.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	seed("1", "shown", "team", "payments")
	seed("2", "shown", "harness", "claude-code")

	_, page := fetchHTML(t, ts.URL+"/outcomes/events")
	for _, want := range []string{
		`<details class="fmenu">`,               // the shared set-filter popover
		`<input type="checkbox" name="team"`,    // per-dimension params, not one opaque select
		`<input type="checkbox" name="harness"`, // a popover per present dimension
		`value="payments"`, `value="claude-code"`,
		`Show evaluation telemetry`, // the telemetry toggle link
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("cohort filter missing %q", want)
		}
	}
	if strings.Contains(page, `id="ev-cohort"`) {
		t.Fatal("the old compound cohort <select> is still present")
	}
}

// TestEventsPageHomedUnderOutcomes: the feed is a drill-down of the funnel, so
// its breadcrumb reads Outcomes › Events, with the parent carrying the window.
func TestEventsPageHomedUnderOutcomes(t *testing.T) {
	_, ts := newServer(t)
	_, page := fetchHTML(t, ts.URL+"/outcomes/events?w=7d")
	if !strings.Contains(page, `href="/outcomes?w=7d"`) {
		t.Fatal("Events breadcrumb missing linked Outcomes parent (with window)")
	}
	// The last crumb is the peer-view switcher (like Playbook's), with Events the
	// active summary and the sibling Outcomes views as options — window preserved.
	for _, want := range []string{
		`<details class="crumb-menu">`,
		`<summary aria-current="page">Events`,
		`href="/outcomes/cohorts?w=7d"`,
		`href="/outcomes/helped-rate?w=7d"`,
		`href="/outcomes/signal-trust?w=7d"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("Events peer-view switcher missing %q", want)
		}
	}
}

// TestEventsByTechniqueTableSortable: the "By technique" panel opts into the shell's
// sortable-table affordance, and enhances itself after its client-side render
// using the shell's one exposed implementation.
func TestEventsByTechniqueTableSortable(t *testing.T) {
	_, ts := newServer(t)
	_, page := fetchHTML(t, ts.URL+"/outcomes/events")
	for _, want := range []string{
		`id="ev-aggtable" class="list data-table"`, // the table opts into the affordance
		`window.tacitSortableTable`,                // the feed enhances it after render
		`window.tacitSortableTable=sortableTable`,  // the shell exposes the one implementation
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("sortable wiring missing %q", want)
		}
	}
}

// TestEventsProgressiveReveal: the feed and the "By technique" table open at a
// readable length and offer step-more / show-all, so a busy window doesn't dump
// everything at once.
func TestEventsProgressiveReveal(t *testing.T) {
	_, ts := newServer(t)
	_, page := fetchHTML(t, ts.URL+"/outcomes/events")
	for _, want := range []string{
		`var TECHNIQUES_INIT=15`, // the table opens at a readable length
		`FEED_INIT=50`,           // so does the feed
		`data-more="`,            // step through more
		`data-all="`,             // or show all
		`class="ev-more"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("progressive-reveal control missing %q", want)
		}
	}
}

// TestEventsNavHighlightsOutcomes: the Events view is an Outcomes sub-view, so
// the top-bar Outcomes tab is the active one (not a missing "Events" tab).
func TestEventsNavHighlightsOutcomes(t *testing.T) {
	_, ts := newServer(t)
	_, page := fetchHTML(t, ts.URL+"/outcomes/events")
	if !strings.Contains(page, `href="/outcomes" class="active"`) {
		t.Fatal("Events page does not mark the Outcomes nav tab active")
	}
	if strings.Contains(page, `href="/outcomes/events">Events</a>`) {
		t.Fatal("the redundant Events entry is still in the avatar menu")
	}
}

// TestEventsDataEndpointDeIdentified is the end-to-end guard: seed the store,
// hit /outcomes/events/data, and assert the JSON the browser receives carries no audit id
// or session hash — the same invariant as the unit test, but through the real
// handler and store.
func TestEventsDataEndpointDeIdentified(t *testing.T) {
	srv, ts := newServer(t)
	const auditSecret = "AUDIT-E2E-SECRET"
	const sessionSecret = "SESSION-E2E-SECRET"
	now := models.Now()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(srv.Store.UpsertTechnique(models.Technique{ID: "techniqueA", Name: "Technique A", Status: "stable"}))
	_, err := srv.Store.AppendAuditFact(models.AuditFact{AuditID: auditSecret, SessionHash: sessionSecret, CreatedAt: now, TaskType: "debug"})
	must(err)
	for _, st := range []string{"shown", "adopted", "helped"} {
		_, err := srv.Store.InsertEvent(fe(auditSecret, "techniqueA", st, now))
		must(err)
	}
	_, err = srv.Store.AppendLifecycleEvent(models.LifecycleEvent{
		EventID: "lc1", TechniqueID: "techniqueA", TechniqueName: "Technique A", Kind: "promoted", Reason: "fit 82%", CreatedAt: now,
	})
	must(err)

	resp, err := http.Get(ts.URL + "/outcomes/events/data?w=all")
	must(err)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if s := string(body); strings.Contains(s, auditSecret) || strings.Contains(s, sessionSecret) {
		t.Fatalf("identifier leaked from /outcomes/events/data: %s", s)
	}
	var payload struct {
		Moments    []moment             `json:"moments"`
		Aggregates []techniqueAggregate `json:"aggregates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	// One delivery moment (the collapsed interaction) + one curation moment.
	if len(payload.Moments) != 2 {
		t.Fatalf("want 2 moments, got %d: %+v", len(payload.Moments), payload.Moments)
	}
	if len(payload.Aggregates) != 1 || payload.Aggregates[0].Helped != 1 {
		t.Fatalf("aggregate = %+v", payload.Aggregates)
	}
}
