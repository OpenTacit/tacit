// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package organization

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/models"
)

// taskTagTechnique is the grouping these fixtures were written against: first task
// type, else first tag, else the technique itself. Production groups by the
// Playbook map's clusters (the web layer's mapAreaOf); Compute itself is
// grouping-agnostic, which is what these tests exercise.
func taskTagTechnique(c models.Technique) (key, label, kind string) {
	if len(c.TaskTypes) > 0 && strings.TrimSpace(c.TaskTypes[0]) != "" {
		label = strings.TrimSpace(c.TaskTypes[0])
		return "task:" + label, label, "task_type"
	}
	if len(c.Tags) > 0 && strings.TrimSpace(c.Tags[0]) != "" {
		label = strings.TrimSpace(c.Tags[0])
		return "tag:" + label, label, "tag"
	}
	label = c.Name
	if label == "" {
		label = c.ID
	}
	return "technique:" + c.ID, label, "technique"
}

func TestComputeFullFidelityAndExclusiveAreas(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	w := insights.Window{Key: "7d", Start: now.Add(-7 * 24 * time.Hour), PrevStart: now.Add(-14 * 24 * time.Hour), Bucket: 24 * time.Hour}
	techniques := []models.Technique{
		{ID: "a", Name: "A", Status: "stable", Scope: "org", TaskTypes: []string{"review", "ignored"}, Tags: []string{"also-ignored"}},
		{ID: "b", Name: "B", Status: "stable", Tags: []string{"delivery", "ignored"}},
		{ID: "c", Name: "Fallback", Status: "draft"},
	}
	var events []models.FeedbackEvent
	addEvents := func(technique, team string, shown, adopted, helped int) {
		for stage, n := range map[string]int{"shown": shown, "adopted": adopted, "helped": helped} {
			for i := 0; i < n; i++ {
				events = append(events, models.FeedbackEvent{TechniqueID: technique, Stage: stage, Segment: models.Segment{"team": team, "office": "north"}, Confidence: "explicit", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)})
			}
		}
	}
	addEvents("a", "tiny", 1, 0, 0)
	addEvents("a", "established", 4, 3, 2)
	events = append(events, models.FeedbackEvent{TechniqueID: "a", Stage: "adopted", Segment: models.Segment{"team": "old"}, CreatedAt: now.Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)})

	r := Compute(techniques, events, nil, now, w, taskTagTechnique)
	if r.Lens != "team" {
		t.Fatalf("lens = %q", r.Lens)
	}
	if len(r.Dimensions) != 2 || r.Dimensions[1].Name != "office" {
		t.Fatalf("arbitrary dimension lost: %#v", r.Dimensions)
	}
	if len(r.Dimensions[0].Values) != 2 {
		t.Fatalf("low-volume cohort lost: %#v", r.Dimensions[0])
	}
	if len(r.Areas) != 2 {
		t.Fatalf("areas = %#v", r.Areas)
	}
	foundPrevious := false
	for _, spread := range r.Spread {
		if spread.Area == "task:review" && spread.Previous == 1 {
			foundPrevious = true
		}
	}
	if !foundPrevious {
		t.Fatalf("previous spread not retained: %#v", r.Spread)
	}
	if len(r.Opportunities) != 1 || r.Opportunities[0].Cohort != "tiny" || !r.Opportunities[0].OrganizationSpecific {
		t.Fatalf("opportunities = %#v", r.Opportunities)
	}
	if len(r.Proponents) != 1 || r.Proponents[0].Cohort != "established" {
		t.Fatalf("proponents = %#v", r.Proponents)
	}
}

func TestComputeHealthAuditCoverageAndPrivacy(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	w := insights.Window{Start: now.Add(-24 * time.Hour), PrevStart: now.Add(-48 * time.Hour), Bucket: time.Hour}
	techniques := []models.Technique{{ID: "technique", Name: "Technique", Status: "stable", DecaySignal: 1}}
	events := []models.FeedbackEvent{{EventID: "secret-event", AuditID: "secret-audit", TechniqueID: "technique", Stage: "shown", Segment: models.Segment{"solo": "only"}, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano)}}
	audits := []models.AuditFact{
		{AuditID: "raw-one", SessionHash: "0123456789abcdef0123456789abcdef", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), Segment: models.Segment{"solo": "audit-only"}, TaskType: "code", Harness: "cli"},
		{AuditID: "raw-two", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339Nano)},
	}
	r := Compute(techniques, events, audits, now, w, taskTagTechnique)
	if r.Health.Audit != (AuditCoverage{Total: 2, WithSession: 1, WithSegment: 1, WithTaskType: 1, WithContext: 1}) {
		t.Fatalf("coverage = %#v", r.Health.Audit)
	}
	if len(r.Cohorts) != 2 || r.Cohorts[0].Value != "audit-only" || r.Cohorts[1].Value != "only" {
		t.Fatalf("audit cohort was not retained: %#v", r.Cohorts)
	}
	if r.Health.ShownNeverAdopted != 1 || r.Health.Decayed != 1 {
		t.Fatalf("health = %#v", r.Health)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-event", "secret-audit", "raw-one", "raw-two"} {
		if stringContains(string(b), secret) {
			t.Fatalf("report leaked %q: %s", secret, b)
		}
	}
}

func TestOpportunityRetainsSparseDemonstratedSuccess(t *testing.T) {
	now := time.Now().UTC()
	w := insights.Window{Start: now.Add(-time.Hour), PrevStart: now.Add(-2 * time.Hour), Bucket: time.Minute}
	technique := models.Technique{ID: "a", Name: "A", Status: "stable", TaskTypes: []string{"work"}}
	events := []models.FeedbackEvent{
		{TechniqueID: "a", Stage: "shown", Segment: models.Segment{"team": "target"}, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)},
		{TechniqueID: "a", Stage: "adopted", Segment: models.Segment{"team": "peer"}, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)},
		{TechniqueID: "a", Stage: "helped", Segment: models.Segment{"team": "peer"}, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)},
	}
	if got := Compute([]models.Technique{technique}, events, nil, now, w, taskTagTechnique).Opportunities; len(got) != 1 {
		t.Fatalf("sparse peer opportunity hidden: %#v", got)
	}
}

func TestRequestedLens(t *testing.T) {
	now := time.Now().UTC()
	w := insights.Window{Start: now.Add(-time.Hour), PrevStart: now.Add(-2 * time.Hour), Bucket: time.Minute}
	events := []models.FeedbackEvent{{TechniqueID: "a", Stage: "shown", Segment: models.Segment{"team": "one", "role": "two"}, CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}}
	r := Compute([]models.Technique{{ID: "a", Status: "stable"}}, events, nil, now, w, taskTagTechnique, "role")
	if r.Lens != "role" {
		t.Fatalf("lens = %q", r.Lens)
	}
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Funnel counts aggregate under whatever grouping the caller supplies — one
// cluster pools techniques that a finer grouping keeps apart.
func TestComputeGroupsColumnsByCallerAreas(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-time.Hour).Format(time.RFC3339)
	techniques := []models.Technique{
		{ID: "a", Name: "A", Status: "stable", Tags: []string{"x"}},
		{ID: "b", Name: "B", Status: "stable", Tags: []string{"y"}},
	}
	seg := models.Segment{"team": "core"}
	events := []models.FeedbackEvent{
		{TechniqueID: "a", Stage: "adopted", Segment: seg, CreatedAt: ts},
		{TechniqueID: "b", Stage: "adopted", Segment: seg, CreatedAt: ts},
	}
	w := insights.WindowByKey("7d", now, now.Add(-30*24*time.Hour))
	// Both techniques belong to one map cluster.
	one := func(models.Technique) (string, string, string) { return "map:practice", "Practice", "map-area" }
	rep := Compute(techniques, events, nil, now, w, one)
	if len(rep.Areas) != 1 || rep.Areas[0].Label != "Practice" || rep.Areas[0].Techniques != 2 {
		t.Fatalf("areas = %+v, want one 'Practice' area holding 2 techniques", rep.Areas)
	}
	pooled := 0
	for _, c := range rep.Cells {
		if c.Dimension == "team" && c.Cohort == "core" && c.Area == "map:practice" {
			pooled = c.Current.Adopted
		}
	}
	if pooled != 2 {
		t.Fatalf("cells = %+v, want both adoptions pooled under the map area", rep.Cells)
	}
	// A finer grouping keeps them apart (distinct first tags).
	def := Compute(techniques, events, nil, now, w, taskTagTechnique)
	if len(def.Areas) != 2 {
		t.Fatalf("tag grouping should keep 2 areas, got %+v", def.Areas)
	}
}
