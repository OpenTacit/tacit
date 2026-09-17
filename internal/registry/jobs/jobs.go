// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package jobs runs the in-process maintenance tasks: sync techniques, embed,
// recompute rollups, detect decay. No external scheduler/queue
// (docs/design/architecture.md). Startup runs sync -> embed -> recompute; a goroutine
// re-runs recompute + decay on an interval.
package jobs

import (
	"log"
	"time"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/internal/registry/techniques"
)

// RunSync loads techniques/*.md into the store. It writes techniques and
// nothing else, so it asks for the technique role rather than the whole store.
func RunSync(st storage.TechniqueStore, techniquesDir string) (int, error) {
	return techniques.SyncDir(techniquesDir, st.UpsertTechnique)
}

// RunEmbed embeds every technique whose vector is missing or stale (model changed).
// Two technique methods are all it touches, so it takes the technique role.
func RunEmbed(st storage.TechniqueStore, embedder embed.Embedder) (int, error) {
	pending, err := st.TechniquesNeedingEmbedding(embedder.ModelID())
	if err != nil {
		return 0, err
	}
	for _, technique := range pending {
		vec := embedder.Embed([]string{embed.TechniqueText(technique)})[0]
		if err := st.SetTechniqueEmbedding(technique.ID, vec, embedder.ModelID(), embedder.Dim()); err != nil {
			return 0, err
		}
	}
	return len(pending), nil
}

// RunRecompute rebuilds the rollups, re-checks decay, auto-retires shadow techniques
// whose evidence says they do not belong, and — when the gate is enabled —
// auto-promotes shadow techniques whose evidence says they do
// (docs/learning/validation-without-review.md). A zero-value gate promotes nothing.
func RunRecompute(st storage.Store, promote feedback.AutoPromoteGate) (int, error) {
	n, err := feedback.RecomputeOutcomes(st)
	if err != nil {
		return n, err
	}
	if _, err = feedback.DetectDecay(st); err != nil {
		return n, err
	}
	// Retire before promote: a technique poor enough to retire never also clears the
	// (higher) promotion bar, but ordering it first makes the exclusion explicit.
	if _, err = feedback.RetireStaleShadow(st); err != nil {
		return n, err
	}
	if _, err = feedback.AutoPromoteShadow(st, promote); err != nil {
		return n, err
	}
	// Last, on this cycle's fresh rollups: propose the support_matrix rows the
	// evidence justifies, into the drafts lane. Nothing here changes a serving
	// technique — a reviewer does that.
	_, err = feedback.ProposeSupportRows(st)
	return n, err
}

// Startup runs the boot sequence: sync -> embed -> recompute. The boot recompute
// does not auto-promote (a zero-value gate) — graduation belongs to the running
// scheduler, where the operator's gate is in force.
func Startup(st storage.Store, techniquesDir string, embedder embed.Embedder) (synced, embedded int, err error) {
	if synced, err = RunSync(st, techniquesDir); err != nil {
		return
	}
	if embedded, err = RunEmbed(st, embedder); err != nil {
		return
	}
	_, err = RunRecompute(st, feedback.AutoPromoteGate{})
	return
}

// StartScheduler launches the periodic recompute loop. Returns a stop func.
// When the backend implements storage.RecomputeLocker (a shared database),
// only the instance holding the advisory lock runs each cycle. The promote gate
// is the operator's automated-review bar, applied every cycle. discover, when
// non-nil, runs the observed-technique discovery pass under the same lock (so only
// one instance clusters), best-effort — it needs an LLM, which the jobs package
// deliberately does not know how to build, so the caller closes over it. after,
// when non-nil, runs once per cycle after the rollup and under the same lock —
// it is where the Public channel is re-ranked, which must see fresh outcomes and
// must not run on two instances at once.
func StartScheduler(st storage.Store, interval time.Duration, promote feedback.AutoPromoteGate, discover, after func()) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runRecomputeCoordinated(st, promote, discover, after)
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

func runRecomputeCoordinated(st storage.Store, promote feedback.AutoPromoteGate, discover, after func()) {
	if locker, isShared := st.(storage.RecomputeLocker); isShared {
		release, ok, err := locker.TryRecomputeLock()
		if err != nil {
			log.Printf("[jobs] recompute lock: %v", err)
			return
		}
		if !ok {
			return // another instance is rolling up this cycle
		}
		defer release()
	}
	if _, err := RunRecompute(st, promote); err != nil {
		log.Printf("[jobs] recompute failed: %v", err) // a job failure must not kill the service
	}
	if discover != nil {
		discover()
	}
	// Last, so it ranks on this cycle's rollups rather than the previous one's.
	if after != nil {
		after()
	}
}
