// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

func memberStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// seedEligible makes a technique that qualifies for the commons: general, serving,
// human-authored, and measured well enough to clear the floor.
func seedEligible(t *testing.T, st *store.Store, id string, helped, adopted float64) models.Technique {
	t.Helper()
	c := models.Technique{
		ID: id, Name: "Name " + id, Description: "d", Recipe: "recipe " + id,
		Scope: "general", Status: "stable", Provenance: "curated", Version: 1,
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
	}
	if err := st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	rate := helped / adopted
	if err := st.ReplaceOutcomes(append(existingOutcomes(t, st), models.Outcome{
		TechniqueID: id, SegmentKey: "__overall__",
		Helped: int(helped), Adopted: int(adopted),
		WeightedHelped: helped, WeightedAdopted: adopted,
		HelpedRate: &rate, SampleSize: int(adopted), LastUpdated: models.Now(),
	})); err != nil {
		t.Fatal(err)
	}
	return c
}

func existingOutcomes(t *testing.T, st *store.Store) []models.Outcome {
	t.Helper()
	techniques, err := st.ListTechniques(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []models.Outcome
	for _, c := range techniques {
		if o, ok, err := st.GetOutcome(c.ID, "__overall__"); err == nil && ok {
			out = append(out, o)
		}
	}
	return out
}

// Membership persists across process restarts without a storage object of its
// own: it is folded back out of the lifecycle log.
func TestMembershipSurvivesAsLifecycleEvents(t *testing.T) {
	st := memberStore(t)
	seedEligible(t, st, "good", 9, 10)

	members, delisted, err := RecomputePublic(st, PublicPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].TechniqueID != "good" {
		t.Fatalf("members = %v, want just good", idsOf(members))
	}
	if len(delisted) != 0 {
		t.Errorf("de-listed %v on the first run", idsOf(delisted))
	}
	entered := members[0].EnteredAt
	if entered == "" {
		t.Fatal("no arrival date recorded")
	}

	// A `published` event is what makes that durable, and it is visible in the
	// Events view rather than hidden in a table only this feature knows about.
	events, err := st.LifecycleEvents("")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.TechniqueID == "good" && e.Kind == "published" {
			found = true
			if e.Reason == "" {
				t.Error("the published event carries no reason")
			}
		}
	}
	if !found {
		t.Fatal("entering the channel wrote no lifecycle event")
	}

	// Folding the log back gives the same membership — this is the restart path.
	recovered := CurrentPublicMembers(events)
	if len(recovered) != 1 || recovered[0].TechniqueID != "good" || recovered[0].EnteredAt != entered {
		t.Errorf("recovered %v, want good with EnteredAt %q", recovered, entered)
	}

	// Recomputing again must be idempotent: no second arrival, no churn.
	again, _, err := RecomputePublic(st, PublicPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0].EnteredAt != entered {
		t.Errorf("a second recompute changed the arrival date to %q", again[0].EnteredAt)
	}
	published := 0
	after, _ := st.LifecycleEvents("")
	for _, e := range after {
		if e.Kind == "published" && e.TechniqueID == "good" {
			published++
		}
	}
	if published != 1 {
		t.Errorf("%d published events for one arrival; a recompute is emitting churn", published)
	}
}

// The hazard that is invisible at the publisher and floods every consumer:
// ranking must never restamp a technique, because a feed entry's Updated comes from
// the technique's UpdatedAt and that is how subscribers decide what is new.
func TestRankingNeverTouchesTheTechniques(t *testing.T) {
	st := memberStore(t)
	c := seedEligible(t, st, "good", 9, 10)

	if _, _, err := RecomputePublic(st, PublicPolicy{}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetTechnique("good")
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.UpdatedAt != c.UpdatedAt {
		t.Errorf("UpdatedAt moved from %q to %q: every subscriber would re-import the channel",
			c.UpdatedAt, got.UpdatedAt)
	}
	for _, ch := range got.Channels {
		if ch == PublicChannel {
			t.Error("membership was written onto Technique.Channels; UpdatedAt churn is one edit away")
		}
	}
}

// De-listing is recorded, and its reason distinguishes "fell down the ranking"
// from "no longer eligible" — a subscriber receiving a retraction is told to flag
// its local copy, and those two messages deserve different words.
func TestDeListingRecordsWhyWithoutClaimingTheTechniqueWasWrong(t *testing.T) {
	st := memberStore(t)
	seedEligible(t, st, "good", 9, 10)
	if _, _, err := RecomputePublic(st, PublicPolicy{}); err != nil {
		t.Fatal(err)
	}

	// Re-scope it: the operator's supported way out of the commons.
	c, _, _ := st.GetTechnique("good")
	c.Scope = "org"
	if err := st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	members, delisted, err := RecomputePublic(st, PublicPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Errorf("still serving %v after it was re-scoped to org", idsOf(members))
	}
	if len(delisted) != 1 || delisted[0].TechniqueID != "good" {
		t.Fatalf("de-listed %v, want good", idsOf(delisted))
	}
	events, _ := st.LifecycleEvents("")
	var reason string
	for _, e := range events {
		if e.Kind == "delisted" && e.TechniqueID == "good" {
			reason = e.Reason
		}
	}
	if reason == "" {
		t.Fatal("de-listing wrote no lifecycle event")
	}
	if reason != "left the Public feed: no longer eligible" {
		t.Errorf("de-list reason is %q, want the ineligibility wording", reason)
	}
}

// The reserved name: a technique cannot be hand-placed in a computed channel, or the
// ranking stops being a measurement.
func TestPublicChannelCannotBeAssignedByHand(t *testing.T) {
	st := memberStore(t)
	seedEligible(t, st, "good", 9, 10)
	if _, err := SetChannels(st, "good", []string{PublicChannel}); err == nil {
		t.Fatal("SetChannels accepted the computed channel")
	}
	if _, err := SetChannels(st, "good", []string{"partners"}); err != nil {
		t.Errorf("an ordinary channel was refused: %v", err)
	}
}

// A machine-authored technique that no person accepted stays out, even with perfect
// evidence — the auto-discover → auto-promote → publish chain has to stop here.
func TestUnapprovedMachineTechniqueIsNotPublished(t *testing.T) {
	st := memberStore(t)
	c := seedEligible(t, st, "machine", 20, 20)
	c.Provenance = "suggested"
	if err := st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	members, _, err := RecomputePublic(st, PublicPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("published %v with no human approval", idsOf(members))
	}

	// Approve it the way a reviewer does, and it becomes eligible.
	if _, err := st.AppendLifecycleEvent(models.LifecycleEvent{
		EventID: "e1", TechniqueID: "machine", Kind: "approved", CreatedAt: models.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	members, _, err = RecomputePublic(st, PublicPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 {
		t.Errorf("members = %v after approval, want the technique admitted", idsOf(members))
	}
}
