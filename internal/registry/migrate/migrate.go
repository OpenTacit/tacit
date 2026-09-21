// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package migrate copies registry state between storage backends — the
// cutover tool from docs/design/postgres-plan.md (file store -> Postgres), written
// against the storage.Store interface so it works between any two backends.
//
// Idempotent by construction: techniques land via upsert and events via
// insert-if-absent (event_id primary key), so a run interrupted midway can
// simply be re-run. Rollups are derived state and are recomputed on the
// destination after the copy rather than copied. Copy does NOT move audit
// facts or lifecycle events — only techniques (with versions, embeddings, and
// decay state), the feedback event log, and adoption sketches.
package migrate

import (
	"fmt"

	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/storage"
)

// Stats reports what a copy did.
type Stats struct {
	Techniques      int // techniques written (insert or upsert)
	Versions        int // archived prior versions copied
	Events          int // events inserted
	DuplicateEvents int // events already present on the destination (replays)
	Sketches        int // adoption sketches copied
	OutcomeRows     int // rollup rows recomputed on the destination
}

// Copy moves all techniques (embeddings and decay state included) and the full
// event log (original event_id/created_at preserved) from src to dst, then
// recomputes the destination's rollups. Dry-run callers should use Plan.
func Copy(src, dst storage.Store) (Stats, error) {
	var stats Stats

	techniques, err := src.ListTechniques(nil, 0)
	if err != nil {
		return stats, fmt.Errorf("read source techniques: %w", err)
	}
	for _, c := range techniques {
		// Upsert first (insert path carries embedding + decay state); for
		// techniques that already exist on the destination, re-assert the
		// runtime state explicitly, since upsert deliberately preserves the
		// destination's runtime state on conflict.
		if err := dst.UpsertTechnique(c); err != nil {
			return stats, fmt.Errorf("write technique %q: %w", c.ID, err)
		}
		if len(c.Embedding) > 0 {
			if err := dst.SetTechniqueEmbedding(c.ID, c.Embedding, c.EmbeddingModel, c.EmbeddingDim); err != nil {
				return stats, fmt.Errorf("write embedding %q: %w", c.ID, err)
			}
		}
		if c.DecaySignal != 0 {
			if err := dst.SetDecay(c.ID, true, c.DecayChecked); err != nil {
				return stats, fmt.Errorf("write decay %q: %w", c.ID, err)
			}
		}
		// The prior-version archive rides along. TechniqueVersions is newest-first;
		// append oldest-first so a destination that orders by insertion (the
		// file store) agrees with one that orders by column (Postgres).
		vs, err := src.TechniqueVersions(c.ID)
		if err != nil {
			return stats, fmt.Errorf("read versions %q: %w", c.ID, err)
		}
		for i := len(vs) - 1; i >= 0; i-- {
			if err := dst.ArchiveTechniqueVersion(vs[i]); err != nil {
				return stats, fmt.Errorf("write version %q v%d: %w", c.ID, vs[i].Version, err)
			}
		}
		stats.Versions += len(vs)
		// Adoption sketches ride along (idempotent on sketch_id). Newest-first
		// from the reader; append oldest-first so insertion order agrees.
		sks, err := src.SketchesForTechnique(c.ID)
		if err != nil {
			return stats, fmt.Errorf("read sketches %q: %w", c.ID, err)
		}
		for i := len(sks) - 1; i >= 0; i-- {
			inserted, err := dst.InsertSketch(sks[i])
			if err != nil {
				return stats, fmt.Errorf("write sketch %q: %w", sks[i].SketchID, err)
			}
			if inserted {
				stats.Sketches++
			}
		}
		stats.Techniques++
	}

	events, err := src.AllEvents("")
	if err != nil {
		return stats, fmt.Errorf("read source events: %w", err)
	}
	for _, e := range events {
		inserted, err := dst.InsertEvent(e)
		if err != nil {
			return stats, fmt.Errorf("write event %q: %w", e.EventID, err)
		}
		if inserted {
			stats.Events++
		} else {
			stats.DuplicateEvents++
		}
	}

	// Member keys ride the cutover too — revoking access must survive a
	// backend swap. InsertMemberKey upserts by id, so re-runs are safe.
	keys, err := src.ListMemberKeys()
	if err != nil {
		return stats, fmt.Errorf("read source member keys: %w", err)
	}
	for _, k := range keys {
		if err := dst.InsertMemberKey(k); err != nil {
			return stats, fmt.Errorf("write member key %q: %w", k.ID, err)
		}
	}

	// Feed tokens likewise: a peer organization's access to a channel must not
	// silently lapse because the org changed storage backend.
	tokens, err := src.ListFeedTokens()
	if err != nil {
		return stats, fmt.Errorf("read source feed tokens: %w", err)
	}
	for _, t := range tokens {
		if err := dst.InsertFeedToken(t); err != nil {
			return stats, fmt.Errorf("write feed token %q: %w", t.ID, err)
		}
	}

	n, err := feedback.RecomputeOutcomes(dst)
	if err != nil {
		return stats, fmt.Errorf("recompute destination rollups: %w", err)
	}
	stats.OutcomeRows = n
	return stats, nil
}

// Plan reports what a Copy would move, without writing anything.
func Plan(src storage.Store) (techniques, events int, err error) {
	cs, err := src.ListTechniques(nil, 0)
	if err != nil {
		return 0, 0, err
	}
	es, err := src.AllEvents("")
	if err != nil {
		return 0, 0, err
	}
	return len(cs), len(es), nil
}

// Verify compares technique and event counts between two stores — the post-cutover
// smoke check the runbook calls for.
func Verify(src, dst storage.Store) error {
	a, err := src.Counts()
	if err != nil {
		return err
	}
	b, err := dst.Counts()
	if err != nil {
		return err
	}
	for _, k := range []string{"techniques", "events"} {
		if a[k] != b[k] {
			return fmt.Errorf("count mismatch for %s: source=%d destination=%d", k, a[k], b[k])
		}
	}
	return nil
}
