// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlitestore

// SQLite-backed tests run UNGATED — a temp-file database needs no server, so
// unlike pgstore's env-gated suite this backend's conformance runs on every
// bare `go test ./...` (docs/design/sqlite-plan.md phase 1). That quietly
// keeps the SQL-shaped side of the storage contract exercised in CI even when
// no Postgres is configured.

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/migrate"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/retrieval"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/internal/registry/storage/conformance"
	"github.com/opentacit/tacit/internal/registry/store"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "tacit.db"))
	if err != nil {
		t.Fatalf("open sqlitestore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestPathFromURL(t *testing.T) {
	for _, tc := range []struct {
		in, path string
		ok       bool
	}{
		{"sqlite:///var/lib/tacit/tacit.db", "/var/lib/tacit/tacit.db", true},
		{"sqlite://tacit.db", "tacit.db", true},
		{"sqlite:tacit.db", "tacit.db", true},
		{"sqlite:", "", false},
		{"postgres://host/db", "", false},
		{"/bare/path/tacit.db", "", false}, // explicit scheme only — never guessed
	} {
		path, ok := PathFromURL(tc.in)
		if path != tc.path || ok != tc.ok {
			t.Errorf("PathFromURL(%q) = %q,%v (want %q,%v)", tc.in, path, ok, tc.path, tc.ok)
		}
	}
}

// TestNotARecomputeLocker pins the one capability this backend deliberately
// does NOT have. The shared SQL core it embeds is the same code Postgres runs,
// and Postgres is a storage.RecomputeLocker — so the moment the advisory-lock
// method migrated into that core, SQLite would silently start claiming
// cross-instance election it cannot provide, and the jobs scheduler (which
// probes for the interface) would believe it. Keep TryRecomputeLock in pgstore.
func TestNotARecomputeLocker(t *testing.T) {
	var st storage.Store = testStore(t)
	if _, ok := st.(storage.RecomputeLocker); ok {
		t.Fatal("sqlitestore implements storage.RecomputeLocker; " +
			"single-machine backends must not — a second instance is the Postgres trigger")
	}
}

// TestConformance runs the same behavior suite the file store and Postgres
// pass — a fresh temp-file database per subtest, no truncation dance needed.
func TestConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) storage.Store { return testStore(t) })
}

// TestRankingE2E is the seed-techniques ranking-shift e2e from the retrieval suite,
// run against SQLite: same techniques, same events, same 0.96 helped_rate.
func TestRankingE2E(t *testing.T) {
	st := testStore(t)
	embedder, _ := embed.New("hashing-v1", 256)
	techniquesDir, _ := filepath.Abs("../../../techniques")
	if _, _, err := jobs.Startup(st, techniquesDir, embedder); err != nil {
		t.Fatal(err)
	}

	seg := map[string]any{"role": "developer", "domain": "data-engineering"}
	post := func(capID, stage string) {
		e, err := models.ParseFeedbackEvent(map[string]any{
			"technique_id": capID, "stage": stage, "segment": seg})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := feedback.Ingest(st, e); err != nil {
			t.Fatal(err)
		}
	}
	for range 50 {
		post("ask-for-a-diagram", "shown")
		post("ask-for-a-diagram", "adopted")
	}
	for range 48 {
		post("ask-for-a-diagram", "helped")
	}
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}

	ev, err := retrieval.BuildEvidence(st, models.Characterization{
		SummaryText: "User asked two generic explain questions about Kafka offsets and " +
			"__consumer_offsets and received long textbook explanations. The topic " +
			"is structural and visual; no diagram was requested.",
		TaskType: "concept-explanation", ToolsAbsent: []string{"diagram"},
		Segment: models.Segment{"role": "developer"},
	}, embedder, retrieval.AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Meta.Thin {
		t.Fatal("measured evidence still thin")
	}
	if ev.Candidates[0].TechniqueID != "ask-for-a-diagram" {
		t.Fatalf("top candidate = %s", ev.Candidates[0].TechniqueID)
	}
	if hr := *ev.Candidates[0].Outcomes.HelpedRate; hr < 0.959 || hr > 0.961 {
		t.Fatalf("helped_rate = %f (want 0.96)", hr)
	}
}

// TestEmbeddingRoundTrip pins the packed-float32 encoding through the BLOB.
func TestEmbeddingRoundTrip(t *testing.T) {
	st := testStore(t)
	now := models.Now()
	technique := models.Technique{ID: "e", Name: "E", Description: "d", Scope: "general",
		Status: "stable", Provenance: "curated", Version: 1, Recipe: "r",
		CreatedAt: now, UpdatedAt: now}
	if err := st.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	vec := []float32{0.5, -1.25, 3.5e-3}
	if err := st.SetTechniqueEmbedding("e", vec, "hashing-v1", 3); err != nil {
		t.Fatal(err)
	}
	got, _, err := st.GetTechnique("e")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Embedding) != 3 {
		t.Fatalf("embedding dims: %v", got.Embedding)
	}
	for i := range vec {
		if got.Embedding[i] != vec[i] {
			t.Fatalf("embedding[%d] = %v (want %v)", i, got.Embedding[i], vec[i])
		}
	}
}

// TestReopenKeepsState pins durability across close/open — the property the
// file store gets from rewriting whole files, delivered here by the database
// file (and the reason a growth deployment migrates to this backend).
func TestReopenKeepsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tacit.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := models.Now()
	if err := st.UpsertTechnique(models.Technique{ID: "r1", Name: "R", Description: "d",
		Status: "stable", Provenance: "curated", Version: 1, Recipe: "r",
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	e, _ := models.ParseFeedbackEvent(map[string]any{"technique_id": "r1", "stage": "shown"})
	if ok, err := st.InsertEvent(e); !ok || err != nil {
		t.Fatalf("insert: %v %v", ok, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path) // migrations must no-op on an up-to-date database
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })
	if _, ok, err := st2.GetTechnique("r1"); !ok || err != nil {
		t.Fatalf("technique lost across reopen: %v %v", ok, err)
	}
	if ok, err := st2.InsertEvent(e); ok || err != nil {
		t.Fatalf("replay accepted after reopen: %v %v", ok, err)
	}
}

// TestMigrateFromFileStore runs the real cutover path: seed a file store,
// migrate-store into SQLite, verify identity survives.
func TestMigrateFromFileStore(t *testing.T) {
	dst := testStore(t)
	src, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := models.Now()
	technique := models.Technique{ID: "m1", Name: "M1", Description: "d", Scope: "org",
		Status: "stable", Provenance: "curated", Version: 3, Recipe: "r",
		Tags: []string{"t"}, CreatedAt: now, UpdatedAt: now}
	if err := src.UpsertTechnique(technique); err != nil {
		t.Fatal(err)
	}
	if err := src.SetTechniqueEmbedding("m1", []float32{1.5, -2.25}, "hashing-v1", 2); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		e, _ := models.ParseFeedbackEvent(map[string]any{"technique_id": "m1", "stage": "adopted"})
		if _, err := src.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := migrate.Copy(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Techniques != 1 || stats.Events != 5 {
		t.Fatalf("stats: %+v", stats)
	}
	if err := migrate.Verify(src, dst); err != nil {
		t.Fatal(err)
	}
	got, ok, err := dst.GetTechnique("m1")
	if err != nil || !ok {
		t.Fatal("technique lost")
	}
	if len(got.Embedding) != 2 || got.Embedding[1] != -2.25 || got.Version != 3 {
		t.Fatalf("technique fidelity: %+v", got)
	}
	// idempotent re-run
	stats, err = migrate.Copy(src, dst)
	if err != nil || stats.Events != 0 || stats.DuplicateEvents != 5 {
		t.Fatalf("re-run: %+v %v", stats, err)
	}
}

// TestConcurrentIngestAndRecompute hammers one store from four goroutines —
// overlapping event ids ingested twice over while rollups recompute. The
// single-connection pool + WAL must serialize it all without SQLITE_BUSY
// surfacing, and the exact final count proves idempotency under concurrency.
// (One process, one store: the multi-INSTANCE story stays Postgres-only.)
func TestConcurrentIngestAndRecompute(t *testing.T) {
	st := testStore(t)

	const uniqueEvents = 200
	events := make([]models.FeedbackEvent, uniqueEvents)
	for i := range events {
		e, _ := models.ParseFeedbackEvent(map[string]any{
			"technique_id": "load-technique", "stage": "shown",
			"event_id": fmt.Sprintf("evt_load_%03d", i)})
		events[i] = e
	}

	var wg sync.WaitGroup
	insertAll := func() {
		defer wg.Done()
		for _, e := range events { // both workers insert ALL ids: total overlap
			if _, err := st.InsertEvent(e); err != nil {
				t.Errorf("insert: %v", err)
				return
			}
		}
	}
	recompute := func() {
		defer wg.Done()
		for range 5 {
			if _, err := feedback.RecomputeOutcomes(st); err != nil {
				t.Errorf("recompute: %v", err)
				return
			}
		}
	}
	wg.Add(4)
	go insertAll()
	go insertAll()
	go recompute()
	go recompute()
	wg.Wait()

	got, err := st.AllEvents("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != uniqueEvents {
		t.Fatalf("events = %d (want exactly %d: overlap must dedupe)", len(got), uniqueEvents)
	}
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	o, ok, err := st.GetOutcome("load-technique", "__overall__")
	if err != nil || !ok || o.Shown != uniqueEvents {
		t.Fatalf("final rollup: %+v %v %v", o, ok, err)
	}
}
