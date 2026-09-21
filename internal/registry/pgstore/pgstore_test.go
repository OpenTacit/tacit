// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

//go:build pg

package pgstore

// Postgres-backed tests are env-gated so `go test ./...` stays green on a
// bare machine (docs/design/postgres-plan.md): set TACIT_TEST_DB_URL to run them,
// e.g.
//
//	docker run -d --rm -e POSTGRES_PASSWORD=pg -p 127.0.0.1:15432:5432 postgres:17-alpine
//	TACIT_TEST_DB_URL="postgres://postgres:pg@127.0.0.1:15432/postgres?sslmode=disable" go test ./...

import (
	"fmt"
	"os"
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

// testDSN is the documented knob (README: TACIT_TEST_DB_URL). Every test that
// opens its own second connection must use this too, or it dials the default
// unix socket and fails on any machine without a local Postgres.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TACIT_TEST_DB_URL")
	if dsn == "" {
		t.Skip("TACIT_TEST_DB_URL not set; skipping Postgres backend tests")
	}
	return dsn
}

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := testDSN(t)
	st, err := Open(dsn)
	if err != nil {
		t.Fatalf("open pgstore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	// each test starts from an empty database — EVERY table the store writes
	// must be listed, or state leaks between subtests and the suite starts
	// lying (a fresh insert comes back as a replay).
	for _, table := range []string{
		"techniques", "feedback_events", "technique_outcomes", "technique_versions", "audit_facts",
		"member_keys", "sketches", "lifecycle_events", "feed_tokens",
	} {
		if _, err := st.DB().Exec("TRUNCATE " + table); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	return st
}

// TestConformance runs the same behavior suite the file store passes.
func TestConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) storage.Store { return testStore(t) })
}

// TestRankingE2E is the seed-techniques ranking-shift e2e from the retrieval suite,
// run against Postgres: same techniques, same events, same 0.96 helped_rate.
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
	for i := 0; i < 50; i++ {
		post("ask-for-a-diagram", "shown")
		post("ask-for-a-diagram", "adopted")
	}
	for i := 0; i < 48; i++ {
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

// TestEmbeddingRoundTrip pins the packed-float32 encoding across the wire.
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

// TestRecomputeLockElectsOneRunner validates the multi-instance coordination:
// two stores on one database, one lock holder at a time.
func TestRecomputeLockElectsOneRunner(t *testing.T) {
	st1 := testStore(t)
	st2, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })

	release1, ok, err := st1.TryRecomputeLock()
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	_, ok, err = st2.TryRecomputeLock()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("second instance acquired the lock concurrently")
	}
	release1()
	release2, ok, err := st2.TryRecomputeLock()
	if err != nil || !ok {
		t.Fatalf("lock not released: %v %v", ok, err)
	}
	release2()
}

// TestMigrateFromFileStore runs the real cutover path: seed a file store,
// migrate-store into Postgres, verify identity survives.
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
	for i := 0; i < 5; i++ {
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

// TestConcurrentIngestAndRecompute hammers two instances with overlapping
// event ids while rollups recompute — the multi-instance load story from the
// plan's phase 2. The exact final count proves global idempotency under
// concurrency.
func TestConcurrentIngestAndRecompute(t *testing.T) {
	st1 := testStore(t)
	st2, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })

	const uniqueEvents = 200
	events := make([]models.FeedbackEvent, uniqueEvents)
	for i := range events {
		e, _ := models.ParseFeedbackEvent(map[string]any{
			"technique_id": "load-technique", "stage": "shown",
			"event_id": fmt.Sprintf("evt_load_%03d", i)})
		events[i] = e
	}

	var wg sync.WaitGroup
	insertAll := func(st *Store) {
		defer wg.Done()
		for _, e := range events { // both instances insert ALL ids: total overlap
			if _, err := st.InsertEvent(e); err != nil {
				t.Errorf("insert: %v", err)
				return
			}
		}
	}
	recompute := func(st *Store) {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if _, err := feedback.RecomputeOutcomes(st); err != nil {
				t.Errorf("recompute: %v", err)
				return
			}
		}
	}
	wg.Add(4)
	go insertAll(st1)
	go insertAll(st2)
	go recompute(st1)
	go recompute(st2)
	wg.Wait()

	got, err := st1.AllEvents("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != uniqueEvents {
		t.Fatalf("events = %d (want exactly %d: overlap must dedupe)", len(got), uniqueEvents)
	}
	if _, err := feedback.RecomputeOutcomes(st1); err != nil {
		t.Fatal(err)
	}
	o, ok, err := st1.GetOutcome("load-technique", "__overall__")
	if err != nil || !ok || o.Shown != uniqueEvents {
		t.Fatalf("final rollup: %+v %v %v", o, ok, err)
	}
}

// TestCrossInstanceIdempotency: the same event_id ingested via two instances
// lands exactly once — the global dedupe the file store could never provide.
func TestCrossInstanceIdempotency(t *testing.T) {
	st1 := testStore(t)
	st2, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })

	e, _ := models.ParseFeedbackEvent(map[string]any{"technique_id": "a", "stage": "shown"})
	if ok, err := st1.InsertEvent(e); !ok || err != nil {
		t.Fatalf("first insert: %v %v", ok, err)
	}
	if ok, err := st2.InsertEvent(e); ok || err != nil {
		t.Fatalf("cross-instance replay accepted: %v %v", ok, err)
	}
	events, err := st1.AllEvents("")
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %d %v", len(events), err)
	}
}
