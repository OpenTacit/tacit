// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package insights

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/pkg/contracts"
)

var now = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func technique(id string, createdDaysAgo int) models.Technique {
	return models.Technique{
		ID: id, Name: "N " + id, Scope: "general", Status: "stable",
		CreatedAt: now.AddDate(0, 0, -createdDaysAgo).Format(time.RFC3339Nano),
	}
}

func event(capID, stage string, daysAgo int, extra func(*models.FeedbackEvent)) models.FeedbackEvent {
	// Source defaults to ambient: the unprompted fit-check burst is the path
	// every funnel metric is about, and it is the only one that can decline.
	// Pull-path cases set it explicitly via extra.
	e := models.FeedbackEvent{
		EventID: fmt.Sprintf("evt_%s_%s_%d_%p", capID, stage, daysAgo, extra),
		AuditID: "aud", TechniqueID: capID, Stage: stage, Confidence: "inferred",
		Segment:   models.Segment{},
		Source:    contracts.SourceAmbient,
		CreatedAt: now.AddDate(0, 0, -daysAgo).Format(time.RFC3339Nano),
	}
	if extra != nil {
		extra(&e)
	}
	return e
}

func repeat(n int, gen func(i int) models.FeedbackEvent) []models.FeedbackEvent {
	out := make([]models.FeedbackEvent, n)
	for i := range out {
		out[i] = gen(i)
		out[i].EventID = fmt.Sprintf("%s_%d", out[i].EventID, i)
	}
	return out
}

func TestComputeTechniqueRecentFunnelIsFixed14dWindow(t *testing.T) {
	c := technique("x", 120)
	// Inside the 14-day decay window: 3 adoptions, 1 helped.
	// Outside it (20 days ago): 5 adoptions, 5 helped — must NOT count.
	var events []models.FeedbackEvent
	events = append(events, event("x", "adopted", 3, nil), event("x", "helped", 3, nil))
	events = append(events, event("x", "adopted", 10, nil), event("x", "adopted", 13, nil))
	events = append(events, repeat(5, func(i int) models.FeedbackEvent { return event("x", "adopted", 20, nil) })...)
	events = append(events, repeat(5, func(i int) models.FeedbackEvent { return event("x", "helped", 20, nil) })...)

	// A wide page window (90d) must not change the fixed 14-day recent slice.
	d := ComputeTechnique(c, events, now, WindowByKey("90d", now, Earliest(events)))
	if d.RecentFunnel.Adopted != 3 || d.RecentFunnel.Helped != 1 {
		t.Fatalf("recent funnel = adopted %d, helped %d; want 3, 1",
			d.RecentFunnel.Adopted, d.RecentFunnel.Helped)
	}
}

func TestWindowFunnelAndVelocity(t *testing.T) {
	techniques := []models.Technique{technique("rising", 60), technique("steady", 60)}
	var events []models.FeedbackEvent
	// rising: 2 adoptions in the previous 30d window, 8 in the current one
	events = append(events, repeat(2, func(i int) models.FeedbackEvent {
		return event("rising", "adopted", 40, nil)
	})...)
	events = append(events, repeat(8, func(i int) models.FeedbackEvent {
		return event("rising", "adopted", 5, nil)
	})...)
	// steady: 4 and 4
	events = append(events, repeat(4, func(i int) models.FeedbackEvent {
		return event("steady", "adopted", 45, nil)
	})...)
	events = append(events, repeat(4, func(i int) models.FeedbackEvent {
		return event("steady", "adopted", 10, nil)
	})...)

	w := WindowByKey("30d", now, Earliest(events))
	o := Compute(techniques, events, now, w)

	if o.Funnel.Adopted != 12 || o.PrevFunnel.Adopted != 6 {
		t.Fatalf("funnel adopted: window=%d prev=%d", o.Funnel.Adopted, o.PrevFunnel.Adopted)
	}
	if len(o.Fastest) == 0 || o.Fastest[0].Technique.ID != "rising" {
		t.Fatalf("fastest: %+v", o.Fastest)
	}
	if o.Fastest[0].Velocity() != 6 {
		t.Fatalf("velocity = %d", o.Fastest[0].Velocity())
	}
	// bucketed series sums back to the funnel
	sum := 0
	for _, b := range o.Buckets {
		sum += b.Funnel.Adopted
	}
	if sum != 12 {
		t.Fatalf("bucket sum = %d", sum)
	}
	sparkSum := 0
	for _, v := range o.AdoptionSpark {
		sparkSum += v
	}
	if sparkSum != 12 {
		t.Fatalf("spark sum = %d", sparkSum)
	}
}

func TestKpiSparklines(t *testing.T) {
	techniques := []models.Technique{technique("c", 60)}
	var events []models.FeedbackEvent
	events = append(events, repeat(6, func(i int) models.FeedbackEvent { return event("c", "shown", 3+i, nil) })...)
	events = append(events, repeat(4, func(i int) models.FeedbackEvent { return event("c", "adopted", 3+i, nil) })...)
	events = append(events, repeat(3, func(i int) models.FeedbackEvent { return event("c", "helped", 3+i, nil) })...)
	events = append(events, repeat(2, func(i int) models.FeedbackEvent { return event("c", "declined", 3+i, nil) })...)

	w := WindowByKey("30d", now, Earliest(events))
	o := Compute(techniques, events, now, w)

	if len(o.ShownSpark) != 12 || len(o.HelpedRateSpark) != 12 || len(o.DeclineRateSpark) != 12 {
		t.Fatalf("spark lengths: shown=%d helpedRate=%d declineRate=%d (want 12 each)",
			len(o.ShownSpark), len(o.HelpedRateSpark), len(o.DeclineRateSpark))
	}
	sum := 0
	for _, v := range o.ShownSpark {
		sum += v
	}
	if sum != 6 {
		t.Fatalf("shown spark sums to %d, want 6 (every shown event once)", sum)
	}
	// Final point is the CUMULATIVE helped rate over the window: 3/4 = 75%.
	if got := o.HelpedRateSpark[11]; got != 75 {
		t.Fatalf("final cumulative helped-rate = %d%%, want 75", got)
	}
	// Cumulative decline rate = declined/(declined+shown) = 2/(2+6) = 25%.
	if got := o.DeclineRateSpark[11]; got != 25 {
		t.Fatalf("final cumulative decline-rate = %d%%, want 25", got)
	}
}

func TestDeclineRateFromFitCheckRejections(t *testing.T) {
	techniques := []models.Technique{technique("c", 60)}
	var events []models.FeedbackEvent
	// Three candidate offers reached a shown verdict, one was declined at
	// fit-check: decline rate = 1/(1+3) = 25%.
	events = append(events, repeat(3, func(i int) models.FeedbackEvent {
		return event("c", "shown", 5, nil)
	})...)
	events = append(events, event("c", "declined", 5, nil))
	events = append(events, event("c", "adopted", 5, nil))

	w := WindowByKey("30d", now, Earliest(events))
	o := Compute(techniques, events, now, w)

	if o.Funnel.Declined != 1 || o.Funnel.Shown != 3 {
		t.Fatalf("counts: declined=%d shown=%d", o.Funnel.Declined, o.Funnel.Shown)
	}
	if r, ok := o.Funnel.DeclineRate(); !ok || r < 0.24 || r > 0.26 {
		t.Fatalf("decline rate = %v ok=%v (want ~0.25)", r, ok)
	}
	// declined is off the delivery funnel — it must not inflate adoption math.
	if o.Funnel.Adopted != 1 {
		t.Fatalf("declined leaked into adopted: %d", o.Funnel.Adopted)
	}
	if _, ok := (Funnel{}).DeclineRate(); ok {
		t.Fatalf("decline rate should be unmeasured with no verdicts")
	}
}

// A technique the member pulled up (@tacit, tacit_search) is shown without any
// fit-check burst that could have declined it, so it must stay out of the
// decline-rate denominator — while still counting as a suggestion shown.
// Blending the two paths is what made the rate read as a retrieval failure.
func TestDeclineRateExcludesPullPath(t *testing.T) {
	techniques := []models.Technique{technique("c", 60)}
	pull := func(e *models.FeedbackEvent) { e.Source = contracts.SourcePull }

	var events []models.FeedbackEvent
	// Ambient: one shown, three declined -> 3/4 = 75%.
	events = append(events, event("c", "shown", 5, nil))
	events = append(events, repeat(3, func(i int) models.FeedbackEvent {
		return event("c", "declined", 5, nil)
	})...)
	// Pull: twenty shown, none declined. Enough to drag a blended rate from
	// 75% down to 3/24 = 13% if the split were not honoured.
	events = append(events, repeat(20, func(i int) models.FeedbackEvent {
		return event("c", "shown", 5, pull)
	})...)

	w := WindowByKey("30d", now, Earliest(events))
	o := Compute(techniques, events, now, w)

	if o.Funnel.Shown != 21 {
		t.Fatalf("pull techniques are still suggestions shown: got %d, want 21", o.Funnel.Shown)
	}
	if got := o.Funnel.DeclineSample(); got != 4 {
		t.Fatalf("decline sample = %d, want 4 (ambient shown+declined only)", got)
	}
	if r, ok := o.Funnel.DeclineRate(); !ok || r < 0.74 || r > 0.76 {
		t.Fatalf("decline rate = %v ok=%v (want ~0.75, not the blended ~0.13)", r, ok)
	}
	// The trend must use the same denominator as the headline, or the sparkline
	// tells a different story than the number above it.
	if got := o.DeclineRateSpark[11]; got != 75 {
		t.Fatalf("final cumulative decline-rate spark = %d%%, want 75", got)
	}
}

// Events written before FeedbackEvent.Source existed still have to land on the
// right side of the split, or the pre-change baseline is not comparable with
// what comes after it.
func TestDeclineRateClassifiesLegacyEventsWithoutSource(t *testing.T) {
	techniques := []models.Technique{technique("c", 60)}
	legacyAmbient := func(e *models.FeedbackEvent) { e.Source, e.TaskType = "", "editing" }
	legacySearch := func(e *models.FeedbackEvent) { e.Source, e.TaskType = "", "" }
	legacyMention := func(e *models.FeedbackEvent) {
		e.Source, e.TaskType, e.Confidence = "", "editing", "explicit"
	}

	var events []models.FeedbackEvent
	events = append(events, event("c", "shown", 5, legacyAmbient))
	events = append(events, event("c", "declined", 5, legacyAmbient))
	events = append(events, event("c", "shown", 5, legacySearch))
	events = append(events, event("c", "shown", 5, legacyMention))

	o := Compute(techniques, events, now, WindowByKey("30d", now, Earliest(events)))

	// Only the ambient pair counts: 1 declined / (1 declined + 1 shown) = 50%.
	if got := o.Funnel.DeclineSample(); got != 2 {
		t.Fatalf("decline sample = %d, want 2 (the legacy ambient pair only)", got)
	}
	if r, ok := o.Funnel.DeclineRate(); !ok || r < 0.49 || r > 0.51 {
		t.Fatalf("decline rate = %v ok=%v (want ~0.50)", r, ok)
	}
}

func TestShadowFitsAggregateAndStayOffFunnel(t *testing.T) {
	techniques := []models.Technique{technique("sh", 30)}
	var events []models.FeedbackEvent
	// A shadow technique judged three fits and one non-fit: fit rate 3/4 = 75%.
	events = append(events, repeat(3, func(i int) models.FeedbackEvent {
		return event("sh", "shadow_shown", 5, nil)
	})...)
	events = append(events, event("sh", "shadow_declined", 5, nil))

	fits := ShadowFits(events)
	f, ok := fits["sh"]
	if !ok || f.Shown != 3 || f.Declined != 1 || f.Judged() != 4 {
		t.Fatalf("shadow fit counts: %+v ok=%v", f, ok)
	}
	if r, ok := f.Rate(); !ok || r < 0.74 || r > 0.76 {
		t.Fatalf("shadow fit rate = %v ok=%v (want ~0.75)", r, ok)
	}
	if _, ok := (ShadowFit{}).Rate(); ok {
		t.Fatal("un-judged shadow technique should have no rate")
	}

	// The shadow stages must not touch the real funnel — a shadow technique is never
	// shown, adopted, or helped.
	w := WindowByKey("30d", now, Earliest(events))
	o := Compute(techniques, events, now, w)
	if o.Funnel.Shown != 0 || o.Funnel.Adopted != 0 || o.Funnel.Declined != 0 {
		t.Fatalf("shadow stages leaked into the funnel: %+v", o.Funnel)
	}
}

func TestSessionTracesReconstructsAndCollapsesShapes(t *testing.T) {
	fact := func(session, task, ts, team string) models.AuditFact {
		return models.AuditFact{SessionHash: session, TaskType: task,
			CreatedAt: ts, Segment: models.Segment{"team": team}}
	}
	facts := []models.AuditFact{
		// Session A: research → editing → verification (team a).
		fact("A", "research", "2026-07-20T00:00:01Z", "a"),
		fact("A", "editing", "2026-07-20T00:00:02Z", "a"),
		fact("A", "verification", "2026-07-20T00:00:03Z", "a"),
		// Session B: same shape with repeats that collapse (team b).
		fact("B", "research", "2026-07-20T00:00:01Z", "b"),
		fact("B", "editing", "2026-07-20T00:00:02Z", "b"),
		fact("B", "editing", "2026-07-20T00:00:03Z", "b"),
		fact("B", "verification", "2026-07-20T00:00:04Z", "b"),
		// Session C: a different shape, only once.
		fact("C", "conversation", "2026-07-20T00:00:01Z", "a"),
		fact("C", "editing", "2026-07-20T00:00:02Z", "a"),
	}
	shapes := SessionTraces(facts, 2)
	if len(shapes) != 1 {
		t.Fatalf("want 1 recurring shape (min 2 sessions), got %d: %+v", len(shapes), shapes)
	}
	s := shapes[0]
	if strings.Join(s.Phases, ",") != "research,editing,verification" {
		t.Fatalf("collapsed shape wrong: %v", s.Phases)
	}
	if s.Sessions != 2 || s.Cohorts != 2 {
		t.Fatalf("diversity wrong: sessions=%d cohorts=%d", s.Sessions, s.Cohorts)
	}
}

func TestBestUsesShrunkScoreNotLuckyRates(t *testing.T) {
	techniques := []models.Technique{technique("lucky", 60), technique("proven", 60)}
	var events []models.FeedbackEvent
	events = append(events, event("lucky", "adopted", 3, nil), event("lucky", "helped", 2, nil))
	events = append(events, repeat(40, func(i int) models.FeedbackEvent {
		return event("proven", "adopted", 3+i%20, nil)
	})...)
	events = append(events, repeat(36, func(i int) models.FeedbackEvent {
		return event("proven", "helped", 2+i%20, nil)
	})...)

	o := Compute(techniques, events, now, WindowByKey("30d", now, Earliest(events)))
	if len(o.Best) < 2 || o.Best[0].Technique.ID != "proven" {
		t.Fatalf("shrinkage failed: %+v", o.Best)
	}
	if rate, ok := o.Best[0].Funnel.HelpedRate(); !ok || rate < 0.89 || rate > 0.91 {
		t.Fatalf("helped rate: %v %v", rate, ok)
	}
}

func TestFreshnessNewAndNewlyActive(t *testing.T) {
	techniques := []models.Technique{
		technique("brand-new", 3),   // created inside the window
		technique("just-woke", 200), // old technique, first event inside the window
		technique("dormant", 200),   // old technique, no events
	}
	events := []models.FeedbackEvent{event("just-woke", "shown", 4, nil)}
	o := Compute(techniques, events, now, WindowByKey("30d", now, Earliest(events)))

	if o.NewTechniques != 1 {
		t.Fatalf("new techniques = %d", o.NewTechniques)
	}
	ids := map[string]bool{}
	for _, c := range o.Fresh {
		ids[c.Technique.ID] = true
	}
	if !ids["brand-new"] || !ids["just-woke"] || ids["dormant"] {
		t.Fatalf("fresh: %v", ids)
	}
}

func TestCohortsTrustAndDismissals(t *testing.T) {
	techniques := []models.Technique{technique("c", 60)}
	seg := func(team string) func(*models.FeedbackEvent) {
		return func(e *models.FeedbackEvent) { e.Segment = models.Segment{"team": team} }
	}
	var events []models.FeedbackEvent
	events = append(events, repeat(5, func(i int) models.FeedbackEvent {
		return event("c", "adopted", 3, seg("revops"))
	})...)
	events = append(events, repeat(2, func(i int) models.FeedbackEvent {
		return event("c", "adopted", 3, seg("platform"))
	})...)
	events = append(events, event("c", "dismissed", 2, func(e *models.FeedbackEvent) {
		e.Value = "not-relevant"
		e.Confidence = "explicit"
	}))

	o := Compute(techniques, events, now, WindowByKey("30d", now, Earliest(events)))
	if len(o.Cohorts) != 2 || o.Cohorts[0].Label != "team:revops" || o.Cohorts[0].Count != 5 {
		t.Fatalf("cohorts: %+v", o.Cohorts)
	}
	if o.ActiveCohorts != 2 {
		t.Fatalf("active cohorts = %d", o.ActiveCohorts)
	}
	if len(o.Dismissals) != 1 || o.Dismissals[0].Label != "not-relevant" {
		t.Fatalf("dismissals: %+v", o.Dismissals)
	}
	if o.TrustExplicit != 1 || o.TrustInferred != 7 {
		t.Fatalf("trust: explicit=%d inferred=%d", o.TrustExplicit, o.TrustInferred)
	}
}

func TestComputeCohortsBreakdownByDimension(t *testing.T) {
	seg := func(dim, val string) func(*models.FeedbackEvent) {
		return func(e *models.FeedbackEvent) { e.Segment = models.Segment{dim: val} }
	}
	var events []models.FeedbackEvent
	// team:payments — 3 shown, 2 adopted, 1 helped
	events = append(events,
		event("a", "shown", 2, seg("team", "payments")),
		event("a", "shown", 2, seg("team", "payments")),
		event("a", "shown", 2, seg("team", "payments")),
		event("a", "adopted", 2, seg("team", "payments")),
		event("a", "adopted", 2, seg("team", "payments")),
		event("a", "helped", 2, seg("team", "payments")),
	)
	events = append(events, event("a", "shown", 2, seg("team", "core"))) // team:core — 1 shown
	events = append(events,                                              // role:eng — 2 shown, 1 adopted
		event("a", "shown", 2, seg("role", "eng")),
		event("a", "shown", 2, seg("role", "eng")),
		event("a", "adopted", 2, seg("role", "eng")),
	)
	events = append(events, event("a", "adopted", 400, seg("team", "payments"))) // out of window: ignored
	events = append(events, event("a", "shown", 2, nil))                         // untagged: in Overall, not Tagged

	rep := ComputeCohorts(events, now, WindowByKey("30d", now, Earliest(events)))

	if rep.Distinct != 3 {
		t.Fatalf("distinct cohorts = %d; want 3", rep.Distinct)
	}
	// Overall counts the untagged shown too (7); Tagged excludes it (6).
	if rep.Overall.Shown != 7 || rep.Tagged.Shown != 6 || rep.Tagged.Adopted != 3 || rep.Tagged.Helped != 1 {
		t.Fatalf("overall.shown %d, tagged = shown %d adopted %d helped %d; want 7 and 6,3,1",
			rep.Overall.Shown, rep.Tagged.Shown, rep.Tagged.Adopted, rep.Tagged.Helped)
	}
	// Canonical order puts team before role.
	if len(rep.Dimensions) != 2 || rep.Dimensions[0].Name != "team" || rep.Dimensions[1].Name != "role" {
		t.Fatalf("dimensions = %+v; want team then role", rep.Dimensions)
	}
	team := rep.Dimensions[0]
	// Most-shown first: payments (3) before core (1).
	if len(team.Cohorts) != 2 || team.Cohorts[0].Val != "payments" || team.Cohorts[1].Val != "core" {
		t.Fatalf("team cohorts = %+v; want payments then core", team.Cohorts)
	}
	pay := team.Cohorts[0]
	if pay.Funnel.Shown != 3 || pay.Funnel.Adopted != 2 || pay.Funnel.Helped != 1 {
		t.Fatalf("payments funnel = %+v; want shown3 adopted2 helped1", pay.Funnel)
	}
	if ar, ok := pay.AdoptionRate(); !ok || ar < 0.66 || ar > 0.67 {
		t.Fatalf("payments adoption rate = %v (ok=%v); want ~0.667", ar, ok)
	}
	if team.Funnel.Shown != 4 || team.Funnel.Adopted != 2 {
		t.Fatalf("team total = shown %d adopted %d; want 4,2", team.Funnel.Shown, team.Funnel.Adopted)
	}
}

func TestDrillDownComputations(t *testing.T) {
	techniques := []models.Technique{
		{ID: "a", Name: "A", Status: "stable", Provenance: "mined", Scope: "org", Tags: []string{"data"},
			CreatedAt: now.AddDate(0, 0, -60).Format(time.RFC3339Nano)},
		{ID: "b", Name: "B", Status: "stable", Provenance: "curated", Tags: []string{"diagram"},
			CreatedAt: now.AddDate(0, 0, -60).Format(time.RFC3339Nano)},
	}
	seg := func(h string) func(*models.FeedbackEvent) {
		return func(e *models.FeedbackEvent) { e.Segment = models.Segment{"harness": h} }
	}
	dismiss := func(reason string) func(*models.FeedbackEvent) {
		return func(e *models.FeedbackEvent) { e.Value = reason }
	}
	var events []models.FeedbackEvent
	// a: shown 5, adopted 1 (under-adopted); dismissed not-relevant x2 (untagged)
	events = append(events, repeat(5, func(int) models.FeedbackEvent { return event("a", "shown", 2, seg("cc")) })...)
	events = append(events, event("a", "adopted", 2, seg("cc")),
		event("a", "dismissed", 2, dismiss("not-relevant")), event("a", "dismissed", 2, dismiss("not-relevant")))
	// b: shown 2, adopted 2, helped 1 — a healthy technique, not a miss
	events = append(events, event("b", "shown", 2, seg("cc")), event("b", "shown", 2, seg("cc")),
		event("b", "adopted", 2, seg("cc")), event("b", "adopted", 2, seg("cc")), event("b", "helped", 2, seg("cc")))
	w := WindowByKey("30d", now, Earliest(events))

	// Dismissals grouped by reason.
	dz := DismissalsFor(events, now, w, "not-relevant")
	if len(dz) != 1 || dz[0].Label != "a" || dz[0].Count != 2 {
		t.Fatalf("dismissals for not-relevant: %+v", dz)
	}
	if n := len(DismissalsFor(events, now, w, "didnt-work")); n != 0 {
		t.Fatalf("didnt-work dismissals = %d, want 0", n)
	}

	// Retrieval misses ranked by wasted impressions: a (5−1=4) before b (2−2=0).
	misses := RetrievalMisses(techniques, events, now, w)
	if len(misses) != 2 || misses[0].Technique.ID != "a" {
		t.Fatalf("retrieval misses: %+v", misses)
	}

	// By-source splits on provenance.
	if sc := SourceTechniques(techniques, events, now, w, "mined"); len(sc) != 1 || sc[0].Technique.ID != "a" {
		t.Fatalf("source mined: %+v", sc)
	}
	if sc := SourceTechniques(techniques, events, now, w, "curated"); len(sc) != 1 || sc[0].Technique.ID != "b" {
		t.Fatalf("source curated: %+v", sc)
	}

	// Helped-rate distribution: both techniques are measured; a (0%) sorts before b (50%).
	mc := MeasuredTechniques(techniques, events, now, w)
	if len(mc) != 2 || mc[0].Technique.ID != "a" {
		t.Fatalf("measured techniques worst-first: %+v", mc)
	}

	// Signal trust: every reaction here is inferred (the event helper's default),
	// so explicit is undefined and inferred carries the helped/dismissed split.
	st := ComputeSignalTrust(techniques, events, now, w)
	if st.Inferred.Helped != 1 || st.Inferred.Dismissed != 2 {
		t.Fatalf("inferred class = %+v; want helped 1, dismissed 2", st.Inferred)
	}
	if _, ok := st.Explicit.PositiveRate(); ok {
		t.Fatal("explicit positive rate should be undefined (no explicit reactions)")
	}
	if len(st.Techniques) != 2 || st.Techniques[0].Technique.ID != "a" {
		t.Fatalf("signal techniques (most reactions first): %+v", st.Techniques)
	}

	// Tag performance selects by tag (case-insensitive), reusing the funnels.
	if tc := TagTechniques(techniques, events, now, w, "Data"); len(tc) != 1 || tc[0].Technique.ID != "a" {
		t.Fatalf("tag Data: %+v", tc)
	}
	if n := len(TagTechniques(techniques, events, now, w, "nope")); n != 0 {
		t.Fatalf("unknown tag = %d techniques, want 0", n)
	}

	// Cohort detail restricts to tagged events: the untagged dismissals drop out,
	// so the cohort funnel is shown 7, adopted 3, helped 1; techniques ranked by
	// adoptions (b=2 before a=1).
	d := ComputeCohortDetail(techniques, events, now, w, "harness", "cc")
	if d.Funnel.Shown != 7 || d.Funnel.Adopted != 3 || d.Funnel.Helped != 1 {
		t.Fatalf("cohort detail funnel = %+v; want 7/3/1", d.Funnel)
	}
	if len(d.Techniques) != 2 || d.Techniques[0].Technique.ID != "b" {
		t.Fatalf("cohort detail techniques: %+v", d.Techniques)
	}
}

func TestTechniqueDetailCumulativeCurve(t *testing.T) {
	c := technique("c", 100)
	var events []models.FeedbackEvent
	for _, daysAgo := range []int{80, 50, 20, 20, 5} {
		events = append(events, event("c", "adopted", daysAgo,
			func(e *models.FeedbackEvent) { e.EventID = fmt.Sprintf("evt_%d_%d", daysAgo, len(events)) }))
	}
	d := ComputeTechnique(c, events, now, WindowByKey("90d", now, Earliest(events)))

	if d.Stat.Funnel.Adopted != 5 { // 90d window covers all five
		t.Fatalf("window adopted = %d", d.Stat.Funnel.Adopted)
	}
	last := d.Cumulative[len(d.Cumulative)-1].Funnel.Adopted
	if last != 5 {
		t.Fatalf("cumulative end = %d", last)
	}
	// monotonic non-decreasing
	prev := 0
	for _, b := range d.Cumulative {
		if b.Funnel.Adopted < prev {
			t.Fatalf("cumulative decreased: %+v", d.Cumulative)
		}
		prev = b.Funnel.Adopted
	}
}

func TestEmptyRegistryIsCalm(t *testing.T) {
	o := Compute(nil, nil, now, WindowByKey("30d", now, time.Time{}))
	if o.Funnel.Shown != 0 || len(o.Fastest) != 0 || len(o.Fresh) != 0 {
		t.Fatalf("empty overview invented data: %+v", o)
	}
	if len(o.Buckets) == 0 {
		t.Fatal("no buckets for the axis")
	}
}

func TestOrgScopedShareMixes(t *testing.T) {
	orgTechnique := technique("org-move", 60)
	orgTechnique.Scope = "org"
	orgTechnique.Provenance = "federated"
	genTechnique := technique("gen-move", 60)
	genTechnique.Provenance = "curated"
	draft := technique("pending", 5)
	draft.Status = "draft"
	techniques := []models.Technique{orgTechnique, genTechnique, draft}

	var events []models.FeedbackEvent
	events = append(events, repeat(3, func(i int) models.FeedbackEvent {
		return event("org-move", "shown", 4, nil)
	})...)
	events = append(events, repeat(2, func(i int) models.FeedbackEvent {
		return event("org-move", "adopted", 3, nil)
	})...)
	events = append(events, event("gen-move", "shown", 4, nil))
	events = append(events, event("gen-move", "adopted", 3, nil))
	events = append(events, event("vanished-technique", "adopted", 2, nil))

	o := Compute(techniques, events, now, WindowByKey("30d", now, Earliest(events)))

	if got := mixMap(o.TechniquesByScope); got["org"] != 1 || got["general"] != 1 {
		t.Fatalf("techniques by scope (drafts excluded): %v", got)
	}
	if got := mixMap(o.TechniquesByProvenance); got["federated"] != 1 || got["curated"] != 1 {
		t.Fatalf("techniques by provenance: %v", got)
	}
	if got := mixMap(o.ShownByScope); got["org"] != 3 || got["general"] != 1 {
		t.Fatalf("shown by scope: %v", got)
	}
	if got := mixMap(o.AdoptedByScope); got["org"] != 2 || got["general"] != 1 || got["unknown"] != 1 {
		t.Fatalf("adopted by scope: %v", got)
	}
	if got := mixMap(o.AdoptedByProvenance); got["federated"] != 2 || got["curated"] != 1 {
		t.Fatalf("adopted by provenance: %v", got)
	}
	share, n := OrgShare(o.ShownByScope)
	if n != 4 || share != 0.75 {
		t.Fatalf("org share of shown: %v n=%d", share, n)
	}
	if _, n := OrgShare(nil); n != 0 {
		t.Fatal("empty mix must report unmeasured")
	}
}

// The two technique-list drill-downs no test reached directly. Both read the same
// per-technique core the overview reads, so pinning their exact output — which
// techniques, in which order, with which counts — is what proves a change to that
// core moved no number.
func TestRetrievalMissesAndTagTechniquesFixture(t *testing.T) {
	tagged := func(id, status string, tags ...string) models.Technique {
		c := technique(id, 60)
		c.Status = status
		c.Tags = tags
		return c
	}
	techniques := []models.Technique{
		tagged("a", "stable", "data", "Diagram"), // mixed case: the match folds case
		tagged("b", "stable", "diagram"),
		tagged("c", "draft", "diagram"),   // drafts are not part of what members see
		tagged("e", "retired", "diagram"), // nor are retired techniques
		tagged("d", "stable", "diagram"),  // no events at all
	}
	var events []models.FeedbackEvent
	events = append(events, repeat(6, func(int) models.FeedbackEvent { return event("a", "shown", 3, nil) })...)
	events = append(events, event("a", "adopted", 3, nil))
	events = append(events, repeat(4, func(int) models.FeedbackEvent { return event("b", "shown", 3, nil) })...)
	events = append(events, event("b", "adopted", 3, nil), event("b", "helped", 3, nil))
	events = append(events, repeat(9, func(int) models.FeedbackEvent { return event("c", "shown", 3, nil) })...)
	events = append(events, repeat(8, func(int) models.FeedbackEvent { return event("e", "shown", 3, nil) })...)
	// Outside the 30-day window: counted by neither list.
	events = append(events, repeat(7, func(int) models.FeedbackEvent { return event("a", "adopted", 45, nil) })...)
	w := WindowByKey("30d", now, Earliest(events))

	type row struct {
		id                     string
		shown, adopted, helped int
	}
	got := func(list []TechniqueStat) []row {
		out := make([]row, 0, len(list))
		for _, c := range list {
			out = append(out, row{c.Technique.ID, c.Funnel.Shown, c.Funnel.Adopted, c.Funnel.Helped})
		}
		return out
	}
	// Ranked by wasted impressions: a (6−1) before b (4−1). d was never shown, so
	// it has nothing to miss; c and e are not reviewed.
	misses := fmt.Sprint(got(RetrievalMisses(techniques, events, now, w)))
	if want := fmt.Sprint([]row{{"a", 6, 1, 0}, {"b", 4, 1, 1}}); misses != want {
		t.Fatalf("retrieval misses = %s; want %s", misses, want)
	}
	// Most-adopted first; a and b tie on adoptions, so impressions break it, and
	// the unmeasured d comes last.
	tags := fmt.Sprint(got(TagTechniques(techniques, events, now, w, "diagram")))
	if want := fmt.Sprint([]row{{"a", 6, 1, 0}, {"b", 4, 1, 1}, {"d", 0, 0, 0}}); tags != want {
		t.Fatalf("tag diagram = %s; want %s", tags, want)
	}
	if n := len(TagTechniques(techniques, events, now, w, "data")); n != 1 {
		t.Fatalf("tag data = %d techniques, want 1", n)
	}
	// The same list drives the task-type facet; no technique declares one here.
	if n := len(TaskTypeTechniques(techniques, events, now, w, "diagram")); n != 0 {
		t.Fatalf("task type diagram = %d techniques, want 0", n)
	}
}

func mixMap(mix []MixEntry) map[string]int {
	out := map[string]int{}
	for _, m := range mix {
		out[m.Label] = m.Count
	}
	return out
}
