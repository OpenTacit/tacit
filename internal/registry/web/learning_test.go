// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
)

type pageResp struct {
	status int
	body   string
}

func httpGet(url string) (pageResp, error) {
	resp, err := http.Get(url)
	if err != nil {
		return pageResp{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return pageResp{status: resp.StatusCode, body: string(raw)}, err
}

// The readiness math is the plan's trigger table, live. If it drifts from the
// plan, the page lies about how close the layer is to speaking.
func TestComputeReadiness(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	day := func(d int) string { return now.AddDate(0, 0, -d).Format(time.RFC3339Nano) }

	facts := []models.AuditFact{
		{AuditID: "a1", CreatedAt: day(9), TaskType: "editing", Model: "claude-opus-4-8",
			Resources: []string{"repo:tacit"}, Segment: models.Segment{"team": "tacit"}},
		{AuditID: "a2", CreatedAt: day(1), Segment: models.Segment{"team": "payments"}},
		{AuditID: "a3", CreatedAt: day(0), TaskType: "conversation",
			ToolsAbsent: []string{"warehouse-connector"}, Segment: models.Segment{"team": "tacit"}},
	}
	events := []models.FeedbackEvent{
		// post-epoch helped, two techniques
		{Stage: "helped", TechniqueID: "c1", CreatedAt: day(2)},
		{Stage: "helped", TechniqueID: "c2", CreatedAt: day(1)},
		// pre-epoch helped: real, but old adoption provenance — counted apart
		{Stage: "helped", TechniqueID: "c3", CreatedAt: "2026-07-10T00:00:00Z"},
		// stale helped outside the 28d window
		{Stage: "helped", TechniqueID: "c4", CreatedAt: day(40)},
		// non-helped noise
		{Stage: "shown", TechniqueID: "c1", CreatedAt: day(1)},
	}

	rd := computeReadiness(facts, events, now)
	if rd.FactsTotal != 3 || rd.DaysOfFacts != 10 {
		t.Fatalf("facts=%d days=%d, want 3/10", rd.FactsTotal, rd.DaysOfFacts)
	}
	if rd.WithTaskType != 2 || rd.WithModel != 1 || rd.WithResources != 1 ||
		rd.WithCohort != 3 || rd.EnrichedAbsent != 1 {
		t.Fatalf("completeness: %+v", rd)
	}
	if rd.Cohorts != 2 {
		t.Fatalf("cohorts=%d, want 2 (tacit, payments)", rd.Cohorts)
	}
	if rd.Helped28d != 2 || rd.HelpedTechniques != 2 {
		t.Fatalf("helped=%d techniques=%d, want 2/2", rd.Helped28d, rd.HelpedTechniques)
	}
	if rd.PreEpoch != 1 {
		t.Fatalf("pre-epoch=%d, want 1 — old-provenance events must be excluded, visibly", rd.PreEpoch)
	}
	// FactsPerDay: day(9), day(1), day(0) land in the last-14-day series.
	total := 0
	for _, n := range rd.FactsPerDay {
		total += n
	}
	if total != 3 {
		t.Fatalf("facts/day series sums to %d, want 3", total)
	}
}

// The page is the mandated dark state: it must render the clock and say why
// it is empty — never look broken.
func TestLearningPage(t *testing.T) {
	srv, ts := newServer(t)
	if _, err := srv.Store.AppendAuditFact(models.AuditFact{
		AuditID: "a1", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		TaskType: "editing", Model: "claude-haiku-4-5",
		Segment: models.Segment{"team": "tacit"},
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := httpGet(ts.URL + "/learning")
	if err != nil {
		t.Fatal(err)
	}
	if resp.status != 200 {
		t.Fatalf("GET /learning = %d", resp.status)
	}
	for _, want := range []string{
		"Learning",                     // nav + crumb
		"Evidence requirements",        // the plan's gates, live
		"progress toward each minimum", // the bars are read against a stated mark
		"of 20 needed",                 // trigger arithmetic visible
		"each member forms one cohort",
		"Fact completeness",
	} {
		if !strings.Contains(resp.body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, unwanted := range []string{"Phase triggers", "1 · Findings", "3 · Experiment", "4 · Dynamic"} {
		if strings.Contains(resp.body, unwanted) {
			t.Errorf("page exposes internal phase copy %q", unwanted)
		}
	}
}
