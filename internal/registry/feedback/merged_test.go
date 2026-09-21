// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

func shadowTech(t *testing.T, st *store.Store, id, mergedInto string) {
	t.Helper()
	now := models.Now()
	if err := st.UpsertTechnique(models.Technique{
		ID: id, Name: id, Scope: "general", Status: "shadow", Provenance: "suggested",
		Version: 1, Recipe: "r", CreatedAt: now, UpdatedAt: now, MergedInto: mergedInto,
	}); err != nil {
		t.Fatal(err)
	}
}

// Consolidation exists because a group of duplicates splits its evidence. A
// merge that kept one copy WITHOUT the group's evidence would keep the
// fragment, so the fit verdicts follow the survivor — and these are the
// verdicts RetireStaleShadow and AutoPromoteShadow act on.
func TestFitVerdictsFollowTheSurvivorOfAMerge(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	shadowTech(t, st, "survivor", "")
	shadowTech(t, st, "restatement", "survivor")

	// Neither has enough on its own; together they clear the sample floor and
	// the fit rate is bad enough to retire.
	_, _ = Ingest(st, event(t, "survivor", "shadow_shown", nil))
	for i := 0; i < 4; i++ {
		_, _ = Ingest(st, event(t, "survivor", "shadow_declined", nil))
	}
	for i := 0; i < 5; i++ {
		_, _ = Ingest(st, event(t, "restatement", "shadow_declined", nil))
	}

	fits, err := shadowFitCounts(st)
	if err != nil {
		t.Fatal(err)
	}
	got := fits["survivor"]
	if got.judged() != 10 {
		t.Errorf("the survivor was judged on %d verdicts; the group collected 10", got.judged())
	}
	if fits["restatement"].judged() != 0 {
		t.Errorf("the merged technique kept %d verdicts of its own", fits["restatement"].judged())
	}
	if got.judged() < config.ShadowRetireMinSample {
		t.Fatalf("the inherited sample (%d) does not reach the floor (%d), so this proves nothing",
			got.judged(), config.ShadowRetireMinSample)
	}
}

// The counts a merge inherits are the ones that decide, not just the ones
// displayed: the survivor of a group whose declines say the move fits badly is
// retired on those declines.
func TestASurvivorIsRetiredOnItsGroupsVerdicts(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	shadowTech(t, st, "survivor", "")
	shadowTech(t, st, "restatement", "survivor")
	_, _ = Ingest(st, event(t, "survivor", "shadow_shown", nil))
	for i := 0; i < 5; i++ {
		_, _ = Ingest(st, event(t, "survivor", "shadow_declined", nil))
	}
	for i := 0; i < 5; i++ {
		_, _ = Ingest(st, event(t, "restatement", "shadow_declined", nil))
	}

	n, err := RetireStaleShadow(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("retired %d; the survivor's inherited verdicts should retire it", n)
	}
	if got, _, _ := st.GetTechnique("survivor"); got.Status != "retired" {
		t.Errorf("survivor is %q; its group's declines say the move does not fit", got.Status)
	}
}

// Outcome rollups redirect too, so the survivor ranks on the group's funnel.
func TestOutcomesFoldUnderTheSurvivor(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	shadowTech(t, st, "survivor", "")
	shadowTech(t, st, "restatement", "survivor")
	for i := 0; i < 3; i++ {
		_, _ = Ingest(st, event(t, "survivor", "shown", nil))
		_, _ = Ingest(st, event(t, "restatement", "shown", nil))
	}
	_, _ = Ingest(st, event(t, "restatement", "adopted", nil))

	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	o, ok, err := st.GetOutcome("survivor", config.OverallKey)
	if err != nil || !ok {
		t.Fatalf("no rollup for the survivor: ok=%v err=%v", ok, err)
	}
	if o.Shown != 6 {
		t.Errorf("survivor shown = %d, want the group's 6", o.Shown)
	}
	if o.Adopted != 1 {
		t.Errorf("survivor adopted = %d, want the adoption its restatement earned", o.Adopted)
	}
	if _, ok, _ := st.GetOutcome("restatement", config.OverallKey); ok {
		t.Error("the merged technique kept a rollup row of its own, so the group is counted twice")
	}
}

// A chain resolves to the end of it. B merged into A, then C merged into B, and
// C's evidence belongs to A — otherwise a second round of consolidation
// silently strands what the first round gathered.
func TestAChainOfMergesResolvesToTheSurvivor(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	shadowTech(t, st, "a", "")
	shadowTech(t, st, "b", "a")
	shadowTech(t, st, "c", "b")
	_, _ = Ingest(st, event(t, "c", "shadow_declined", nil))

	fits, err := shadowFitCounts(st)
	if err != nil {
		t.Fatal(err)
	}
	if fits["a"].judged() != 1 {
		t.Errorf("the end of the chain was judged on %d verdicts, want 1", fits["a"].judged())
	}
}

// A corrupted table must degrade to the un-redirected answer rather than hang.
func TestACycleDoesNotHang(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	shadowTech(t, st, "a", "b")
	shadowTech(t, st, "b", "a")
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := mergedInto(st); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cycle in merged_into hung the redirect")
	}
}
