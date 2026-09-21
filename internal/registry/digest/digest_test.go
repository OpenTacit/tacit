// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/models"
)

var now = time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

func technique(id, scope, provenance, status string, daysOld int) models.Technique {
	return models.Technique{
		ID: id, Name: "Name " + id, Scope: scope, Status: status, Provenance: provenance,
		Version: 1, Recipe: "r",
		CreatedAt: now.AddDate(0, 0, -daysOld).Format(time.RFC3339Nano),
		UpdatedAt: now.AddDate(0, 0, -daysOld).Format(time.RFC3339Nano),
	}
}

func event(cap, stage string, daysAgo, seq int) models.FeedbackEvent {
	return models.FeedbackEvent{
		EventID: fmt.Sprintf("e_%s_%s_%d_%d", cap, stage, daysAgo, seq),
		AuditID: "a", TechniqueID: cap, Stage: stage, Confidence: "inferred",
		Segment:   models.Segment{"team": "revops"},
		CreatedAt: now.AddDate(0, 0, -daysAgo).Format(time.RFC3339Nano),
	}
}

func TestDigestTellsTheWeeksStory(t *testing.T) {
	techniques := []models.Technique{
		technique("org-mined", "org", "federated", "stable", 2),
		technique("old-general", "general", "curated", "stable", 200),
		technique("pending", "org", "contributed", "draft", 1),
	}
	var events []models.FeedbackEvent
	for i := 0; i < 4; i++ {
		events = append(events, event("org-mined", "shown", 2, i))
	}
	for i := 0; i < 3; i++ {
		events = append(events, event("org-mined", "adopted", 2, i))
	}
	events = append(events,
		event("org-mined", "helped", 1, 0),
		event("old-general", "shown", 3, 0),
		event("old-general", "dismissed", 3, 0))
	events[len(events)-1].Value = "didnt-work"

	md := Build(techniques, events, now, "7d")

	for _, want := range []string{
		"# " + product.Name() + " digest",
		"**5 suggestions shown**, **3 adopted** (+3 vs the previous window)",
		"new technique",
		"of delivered suggestions used org-scoped techniques",
		"## New & newly active",
		"**Name org-mined** _(new, org, federated)_",
		"## Largest adoption increases",
		"## Where adopted knowledge came from",
		"federated: 3",
		"'didn't work' dismissal",
		"1 draft awaiting review",
		"aggregate and cohort data",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("digest missing %q:\n%s", want, md)
		}
	}
}

func TestQuietWeekIsCalm(t *testing.T) {
	md := Build([]models.Technique{technique("c", "org", "curated", "stable", 300)}, nil, now, "7d")
	if !strings.Contains(md, "**0 suggestions shown**, **0 adopted**") {
		t.Fatalf("quiet week: %s", md)
	}
	for _, absent := range []string{"## Largest adoption increases", "## Highest helped rates", "## Needs attention"} {
		if strings.Contains(md, absent) {
			t.Fatalf("empty section rendered: %s", absent)
		}
	}
}
