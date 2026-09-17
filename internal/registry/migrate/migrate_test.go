// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package migrate

import (
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/internal/registry/store"
)

// seed fills a source store with a technique (embedded + decayed) and events.
func seed(t *testing.T) storage.Store {
	t.Helper()
	src, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := models.Now()
	technique := models.Technique{ID: "c1", Name: "C1", Description: "d", Scope: "org",
		Status: "stable", Provenance: "curated", Version: 2, Recipe: "r",
		Tags: []string{"x"}, CreatedAt: now, UpdatedAt: now}
	if err := src.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	if err := src.SetTechniqueEmbedding("c1", []float32{0.25, -0.5}, "hashing-v1", 2); err != nil {
		t.Fatal(err)
	}
	if err := src.SetDecay("c1", true, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e, _ := models.ParseFeedbackEvent(map[string]any{
			"technique_id": "c1", "stage": "shown",
			"segment": map[string]any{"team": "revops"}})
		if _, err := src.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// TestCopyFileToFile runs the migration between two file stores — the same
// interface-level path the Postgres cutover uses, no database required.
func TestCopyFileToFile(t *testing.T) {
	src := seed(t)
	dst, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	stats, err := Copy(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Techniques != 1 || stats.Events != 3 || stats.DuplicateEvents != 0 {
		t.Fatalf("stats: %+v", stats)
	}
	if stats.OutcomeRows == 0 {
		t.Fatal("destination rollups not recomputed")
	}
	if err := Verify(src, dst); err != nil {
		t.Fatal(err)
	}

	got, ok, err := dst.GetTechnique("c1")
	if err != nil || !ok {
		t.Fatal("technique lost in migration")
	}
	if len(got.Embedding) != 2 || got.Embedding[0] != 0.25 {
		t.Fatalf("embedding lost: %v", got.Embedding)
	}
	if got.DecaySignal != 1 || got.Status != "decayed" {
		t.Fatalf("decay state lost: %+v", got)
	}
	srcEvents, _ := src.AllEvents("")
	dstEvents, _ := dst.AllEvents("")
	if len(dstEvents) != len(srcEvents) {
		t.Fatalf("events: %d vs %d", len(dstEvents), len(srcEvents))
	}
	if dstEvents[0].EventID != srcEvents[0].EventID ||
		dstEvents[0].CreatedAt != srcEvents[0].CreatedAt {
		t.Fatal("event identity not preserved")
	}
}

// TestCopyIsIdempotent re-runs the migration: nothing duplicates.
func TestCopyIsIdempotent(t *testing.T) {
	src := seed(t)
	dst, _ := store.Open(t.TempDir())
	if _, err := Copy(src, dst); err != nil {
		t.Fatal(err)
	}
	stats, err := Copy(src, dst) // re-run after "partial failure"
	if err != nil {
		t.Fatal(err)
	}
	if stats.Events != 0 || stats.DuplicateEvents != 3 {
		t.Fatalf("re-run not idempotent: %+v", stats)
	}
	if err := Verify(src, dst); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCatchesMismatch(t *testing.T) {
	src := seed(t)
	dst, _ := store.Open(t.TempDir())
	if err := Verify(src, dst); err == nil {
		t.Fatal("empty destination passed verification")
	}
}
