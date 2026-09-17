// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/internal/registry/storage/conformance"
)

// TestConformance runs the backend-agnostic behavior suite against the file
// store (the Postgres backend runs the same suite — docs/design/postgres-plan.md).
func TestConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) storage.Store {
		st, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return st
	})
}

// The tests below are file-backend-specific: on-disk layout and reopen
// semantics, which the conformance suite deliberately doesn't assume.

func testTechnique(id string) models.Technique {
	now := models.Now()
	return models.Technique{
		ID: id, Name: "N " + id, Description: "D", Scope: "general",
		Status: "stable", Provenance: "curated", Version: 1, Recipe: "R",
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir)
	if err := st.UpsertTechnique(testTechnique("a")); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTechniqueEmbedding("a", []float32{0.5, -0.5}, "hashing-v1", 2); err != nil {
		t.Fatal(err)
	}
	e, _ := models.ParseFeedbackEvent(map[string]any{"technique_id": "a", "stage": "shown"})
	if ok, err := st.InsertEvent(e); !ok || err != nil {
		t.Fatalf("insert: %v %v", ok, err)
	}

	st2, err := Open(dir) // reopen: techniques + events reload from disk
	if err != nil {
		t.Fatal(err)
	}
	technique, ok, err := st2.GetTechnique("a")
	if err != nil || !ok {
		t.Fatal("technique lost across reopen")
	}
	if len(technique.Embedding) != 2 || technique.Embedding[0] != 0.5 {
		t.Fatalf("embedding lost across reopen: %v", technique.Embedding)
	}
	events, err := st2.AllEvents("")
	if err != nil || len(events) != 1 {
		t.Fatalf("events lost across reopen: %d %v", len(events), err)
	}
	// replayed id must still dedupe after reopen
	if ok, _ := st2.InsertEvent(e); ok {
		t.Fatal("event dedupe lost across reopen")
	}
}

// Enrichments live in their own append-only log so the fact log is never
// rewritten — which means the merge has to be replayed at startup. If it isn't,
// every inferred tools_absent silently vanishes on the next restart and nobody
// finds out until a detector quietly has nothing to condition on.
func TestEnrichmentSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_r", CreatedAt: "2026-07-12T00:00:00Z", ToolsUsed: []string{"Bash"},
	}); err != nil {
		t.Fatal(err)
	}
	if found, err := st.EnrichAuditFact(models.AuditFactEnrichment{
		AuditID: "aud_r", ToolsAbsent: []string{"warehouse-connector"},
	}); err != nil || !found {
		t.Fatalf("enrich: found=%v err=%v", found, err)
	}

	// Reopen from the same directory — this is the restart.
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := reopened.AuditFacts("")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("after restart: %d facts, want 1", len(facts))
	}
	if len(facts[0].ToolsAbsent) != 1 || facts[0].ToolsAbsent[0] != "warehouse-connector" {
		t.Fatalf("the inference did not survive the restart: %+v", facts[0].ToolsAbsent)
	}
	if len(facts[0].ToolsUsed) != 1 || facts[0].ToolsUsed[0] != "Bash" {
		t.Fatalf("the replay clobbered an observed field: %+v", facts[0])
	}
}
