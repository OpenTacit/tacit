// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/sqlitestore"
	"github.com/opentacit/tacit/internal/registry/store"
)

// The container image defaults to SQLite; a /data volume from before that
// default carries file-store state. First boot with a fresh database file
// must import it — and a genuinely fresh install must not invent one.
func TestSqliteAutoCutoverImportsSeededFileStore(t *testing.T) {
	dataDir := t.TempDir()
	src, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := models.Now()
	if err := src.UpsertTechnique(models.Technique{ID: "c1", Name: "C", Description: "d",
		Status: "stable", Provenance: "curated", Version: 2, Recipe: "r",
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := src.SetTechniqueEmbedding("c1", []float32{0.25, -4}, "hashing-v1", 2); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		e, _ := models.ParseFeedbackEvent(map[string]any{"technique_id": "c1", "stage": "shown"})
		if _, err := src.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	src.Close()

	dst, err := sqlitestore.Open(filepath.Join(t.TempDir(), "tacit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dst.Close() })
	if err := sqliteAutoCutover(dst, dataDir); err != nil {
		t.Fatalf("cutover: %v", err)
	}
	got, ok, err := dst.GetTechnique("c1")
	if err != nil || !ok {
		t.Fatalf("technique not imported: %v %v", ok, err)
	}
	if got.Version != 2 || len(got.Embedding) != 2 || got.Embedding[1] != -4 {
		t.Fatalf("technique fidelity: %+v", got)
	}
	events, err := dst.AllEvents("")
	if err != nil || len(events) != 3 {
		t.Fatalf("events: %d %v", len(events), err)
	}

	// Re-running (a crash between copy and first write) stays idempotent.
	if err := sqliteAutoCutover(dst, dataDir); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if events, _ = dst.AllEvents(""); len(events) != 3 {
		t.Fatalf("re-run duplicated events: %d", len(events))
	}

	// A fresh install: empty data dir, nothing to import, no error.
	empty, err := sqlitestore.Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { empty.Close() })
	if err := sqliteAutoCutover(empty, t.TempDir()); err != nil {
		t.Fatalf("fresh install: %v", err)
	}
	if counts, _ := empty.Counts(); counts["techniques"] != 0 || counts["events"] != 0 {
		t.Fatalf("fresh install invented state: %v", counts)
	}
}
