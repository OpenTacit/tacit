// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package insights

import (
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

func seg(pairs ...string) models.Segment {
	s := models.Segment{}
	for i := 0; i+1 < len(pairs); i += 2 {
		s[pairs[i]] = pairs[i+1]
	}
	return s
}

// TestCohortDirectoryRanksByUse: the directory exists so a joining member can
// tell the name their colleagues use from somebody's one-off, so the order has
// to be use, and the count has to be sessions rather than events (one busy
// session must not outrank three people).
func TestCohortDirectoryRanksByUse(t *testing.T) {
	facts := []models.AuditFact{
		{SessionHash: "h1", Segment: seg("team", "payments", "role", "engineer")},
		{SessionHash: "h1", Segment: seg("team", "payments", "role", "engineer")}, // same session, counted once
		{SessionHash: "h2", Segment: seg("team", "payments")},
		{SessionHash: "h3", Segment: seg("team", "platform")},
		{SessionHash: "h4", Segment: seg("team", "payments-eng")}, // the near-duplicate
	}
	events := []models.FeedbackEvent{
		{Stage: "shown", Segment: seg("team", "platform")},
		{Stage: "shown", Segment: seg("team", "platform")},
		{Stage: "adopted", Segment: seg("team", "platform")}, // not an exposure count
	}
	dims := CohortDirectory(events, facts)
	if len(dims) != 2 || dims[0].Dimension != "team" || dims[1].Dimension != "role" {
		t.Fatalf("want team then role, got %+v", dims)
	}
	team := dims[0].Values
	if len(team) != 3 {
		t.Fatalf("want 3 team values, got %+v", team)
	}
	if team[0].Value != "payments" || team[0].Sessions != 2 {
		t.Fatalf("payments should lead with 2 sessions, got %+v", team[0])
	}
	if team[1].Value != "platform" || team[1].Shown != 2 {
		t.Fatalf("platform should follow with 2 shown, got %+v", team[1])
	}
	if team[2].Value != "payments-eng" {
		t.Fatalf("the one-off should sort last, got %+v", team[2])
	}
}

// TestCohortDirectorySeesFactsWithoutEvents: a member who joined this morning
// has sessions and no suggestions yet, and they are exactly the colleague the
// NEXT joiner needs to find. Reading only the event log would hide them.
func TestCohortDirectorySeesFactsWithoutEvents(t *testing.T) {
	dims := CohortDirectory(nil, []models.AuditFact{
		{SessionHash: "h1", Segment: seg("team", "growth")},
	})
	if len(dims) != 1 || len(dims[0].Values) != 1 || dims[0].Values[0].Value != "growth" {
		t.Fatalf("a cohort with no suggestions yet must still be listed, got %+v", dims)
	}
	if dims[0].Values[0].Shown != 0 {
		t.Fatalf("no suggestions shown, so no shown count: %+v", dims[0].Values[0])
	}
}

// TestCohortDirectoryMarksDerivedDimensions: harness and surface are filled in
// by the session, so a client offering cohorts to pick must be able to tell
// them apart from the four a member types.
func TestCohortDirectoryMarksDerivedDimensions(t *testing.T) {
	dims := CohortDirectory(nil, []models.AuditFact{
		{SessionHash: "h1", Segment: seg("team", "payments", "harness", "claude-code")},
	})
	for _, d := range dims {
		switch d.Dimension {
		case "team":
			if d.Derived {
				t.Fatal("team is typed by the member, not derived")
			}
		case "harness":
			if !d.Derived {
				t.Fatal("harness is derived from the session")
			}
		default:
			t.Fatalf("unexpected dimension %q", d.Dimension)
		}
	}
}

// TestCohortDirectoryHoldsNoIdentity: session hashes are the input to the
// session count and must not survive into the output. The directory is the
// most widely-read cohort surface there is — every joining member sees it.
func TestCohortDirectoryHoldsNoIdentity(t *testing.T) {
	const secret = "SESSION-DO-NOT-LEAK"
	dims := CohortDirectory(nil, []models.AuditFact{
		{AuditID: "a1", SessionHash: secret, Segment: seg("team", "payments")},
	})
	for _, d := range dims {
		for _, v := range d.Values {
			if v.Value == secret {
				t.Fatal("session hash leaked into the directory")
			}
		}
	}
	if dims[0].Values[0].Sessions != 1 {
		t.Fatalf("want 1 session, got %+v", dims[0].Values[0])
	}
}

// TestCohortDirectoryEmpty: a registry nobody has tagged yet returns nothing
// to pick from, so the client says "you would be the first" rather than
// drawing an empty menu.
func TestCohortDirectoryEmpty(t *testing.T) {
	if dims := CohortDirectory(nil, nil); len(dims) != 0 {
		t.Fatalf("want no dimensions, got %+v", dims)
	}
	if dims := CohortDirectory(
		[]models.FeedbackEvent{{Stage: "shown"}},
		[]models.AuditFact{{SessionHash: "h1", Segment: models.Segment{"team": ""}}},
	); len(dims) != 0 {
		t.Fatalf("untagged activity is not a cohort, got %+v", dims)
	}
}
