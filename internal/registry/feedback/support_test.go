// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

func stableTechnique(t *testing.T, st *store.Store, id string, matrix []map[string]any) models.Technique {
	t.Helper()
	c := models.Technique{ID: id, Name: "T " + id, Description: "d", Scope: "general",
		Status: "stable", Provenance: "curated", Version: 2, Recipe: "r",
		SupportMatrix: matrix, CreatedAt: models.Now(), UpdatedAt: models.Now()}
	if err := st.UpsertTechnique(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func measure(t *testing.T, st *store.Store, id, model string, adopted, helped int) {
	t.Helper()
	seg := map[string]any{"model": model}
	for i := 0; i < adopted; i++ {
		if _, err := Ingest(st, event(t, id, "adopted", seg)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < helped; i++ {
		if _, err := Ingest(st, event(t, id, "helped", seg)); err != nil {
			t.Fatal(err)
		}
	}
}

// A support row is a fact somebody's own use established. The registry proposes
// it; it never writes it, because the row is the one thing a single-member
// registry can send upward past the Wilson floors and an unread assertion is
// exactly what that must not become.
func TestProposeSupportRowsFilesADraftAndLeavesTheBaseAlone(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	base := stableTechnique(t, st, "use-the-connector", []map[string]any{
		{"harness": "claude-code", "supported": true, "verified": "2026-05"},
	})
	measure(t, st, base.ID, "anthropic/claude-sonnet-5", 4, 2)

	n, err := ProposeSupportRows(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("proposed %d, want 1", n)
	}
	draft, ok, err := st.GetTechnique("use-the-connector" + supportSlot)
	if err != nil || !ok {
		t.Fatalf("no draft filed: ok=%v err=%v", ok, err)
	}
	if draft.Status != "draft" || draft.Supersedes != base.ID || draft.BaseVersion != base.Version {
		t.Fatalf("draft is not a revision of the base: %+v", draft)
	}
	if len(draft.SupportMatrix) != 2 {
		t.Fatalf("support matrix = %+v, want the existing row plus one", draft.SupportMatrix)
	}
	if got, _ := draft.SupportMatrix[1]["model"].(string); got != "anthropic/claude-sonnet-5" {
		t.Fatalf("proposed row names %q", got)
	}
	if draft.RevisionNote == "" {
		t.Fatal("a reviewer gets no why")
	}
	// The serving technique is untouched until a person says otherwise.
	served, _, _ := st.GetTechnique(base.ID)
	if len(served.SupportMatrix) != 1 || served.Version != base.Version {
		t.Fatalf("the base was modified: %+v", served)
	}
}

// The floors are low because this is not a rate — but they are floors. One
// adoption and no verdict is not evidence a pairing works.
func TestProposeSupportRowsHonoursTheFloors(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	stableTechnique(t, st, "thin", nil)
	measure(t, st, "thin", "anthropic/claude-sonnet-5", 2, 1) // below the adoption floor
	stableTechnique(t, st, "unverdicted", nil)
	measure(t, st, "unverdicted", "anthropic/claude-sonnet-5", 9, 0) // adopted, never confirmed

	if n, err := ProposeSupportRows(st); err != nil || n != 0 {
		t.Fatalf("proposed %d (err %v), want none", n, err)
	}
	for _, id := range []string{"thin", "unverdicted"} {
		if _, ok, _ := st.GetTechnique(id + supportSlot); ok {
			t.Fatalf("%s got a draft it has not earned", id)
		}
	}
}

// A model the matrix already names needs no proposal, whatever spelling the
// existing row used.
func TestProposeSupportRowsSkipsRowsAlreadyThere(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	stableTechnique(t, st, "known", []map[string]any{
		{"model": "claude-sonnet-5-20260101", "supported": true, "verified": "2026-05"},
	})
	measure(t, st, "known", "anthropic/claude-sonnet-5", 6, 3)
	if n, err := ProposeSupportRows(st); err != nil || n != 0 {
		t.Fatalf("proposed %d (err %v) for a model already in the matrix", n, err)
	}
}

// The slot is fixed, so a second pass replaces the pending proposal rather than
// stacking another draft on the reviewer's queue.
func TestProposeSupportRowsIsIdempotent(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	stableTechnique(t, st, "c", nil)
	measure(t, st, "c", "anthropic/claude-sonnet-5", 5, 2)
	for i := 0; i < 3; i++ {
		if _, err := ProposeSupportRows(st); err != nil {
			t.Fatal(err)
		}
	}
	drafts, err := st.ListTechniques([]string{"draft"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("filed %d drafts across three passes, want 1: %+v", len(drafts), drafts)
	}
	if len(drafts[0].SupportMatrix) != 1 {
		t.Fatalf("rows accumulated across passes: %+v", drafts[0].SupportMatrix)
	}
}
