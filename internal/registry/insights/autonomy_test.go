// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package insights

import (
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
)

func TestComputeAutonomy(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-time.Hour).Format(time.RFC3339)
	techniques := []models.Technique{
		{ID: "proven", Status: "stable"},
		{ID: "thin", Status: "stable"},
		{ID: "draft", Status: "draft"},
	}
	var events []models.FeedbackEvent
	// proven: 20 adoptions, 18 helped — clears 0.8 x 20.
	for i := 0; i < 20; i++ {
		events = append(events, models.FeedbackEvent{TechniqueID: "proven", Stage: "adopted", CreatedAt: ts})
	}
	for i := 0; i < 18; i++ {
		events = append(events, models.FeedbackEvent{TechniqueID: "proven", Stage: "helped", CreatedAt: ts})
	}
	// thin: perfect rate, tiny n.
	events = append(events,
		models.FeedbackEvent{TechniqueID: "thin", Stage: "adopted", CreatedAt: ts},
		models.FeedbackEvent{TechniqueID: "thin", Stage: "helped", CreatedAt: ts},
		// drafts never qualify regardless of record
		models.FeedbackEvent{TechniqueID: "draft", Stage: "adopted", CreatedAt: ts},
	)
	// The window's delivery-mode split, joined by audit id: one applied
	// delivery that got adopted+helped, one suggested delivery adopted only.
	events = append(events,
		models.FeedbackEvent{AuditID: "a1", TechniqueID: "proven", Stage: "shown", Delivery: "applied", CreatedAt: ts},
		models.FeedbackEvent{AuditID: "a1", TechniqueID: "proven", Stage: "adopted", CreatedAt: ts},
		models.FeedbackEvent{AuditID: "a1", TechniqueID: "proven", Stage: "helped", CreatedAt: ts},
		models.FeedbackEvent{AuditID: "a2", TechniqueID: "proven", Stage: "shown", CreatedAt: ts}, // historical '' = suggested
		models.FeedbackEvent{AuditID: "a2", TechniqueID: "proven", Stage: "adopted", CreatedAt: ts},
	)
	w := WindowByKey("7d", now, now.Add(-30*24*time.Hour))
	a := ComputeAutonomy(techniques, events, now, w, 0.8, 20)

	if a.Eligible != 1 {
		t.Fatalf("eligible = %d, want 1", a.Eligible)
	}
	if len(a.Techniques) != 2 {
		t.Fatalf("standings = %d techniques, want 2 (draft has none)", len(a.Techniques))
	}
	if a.Techniques[0].Technique.ID != "proven" || !a.Techniques[0].Eligible {
		t.Fatalf("eligible-first ordering broken: %+v", a.Techniques)
	}
	if a.Techniques[1].Technique.ID != "thin" || a.Techniques[1].Eligible {
		t.Fatalf("thin n must not qualify: %+v", a.Techniques[1])
	}
	if a.Applied.Shown != 1 || a.Applied.Adopted != 1 || a.Applied.Helped != 1 {
		t.Fatalf("applied funnel = %+v", a.Applied)
	}
	if a.Suggested.Shown != 1 || a.Suggested.Adopted != 1 || a.Suggested.Helped != 0 {
		t.Fatalf("suggested funnel = %+v", a.Suggested)
	}
}
