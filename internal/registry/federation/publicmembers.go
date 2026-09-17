// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package federation

import (
	"fmt"
	"sort"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/contracts"
)

// Public-channel membership, event-sourced from the lifecycle log.
//
// Membership needs to persist — hysteresis compares against yesterday's set, and
// EnteredAt has nowhere else to live — but it deliberately does NOT get a storage
// object of its own. Two reasons. The lifecycle log already exists on every
// backend, so this needs no schema, no conformance additions and no migration.
// And membership genuinely is a sequence of events: this technique entered the commons
// on that date, that one left on this one. Putting them in the log an operator
// already reads to answer "what is OpenTacit doing?" means the commons explains
// itself in the place they are already looking, rather than in a table only this
// feature knows about.
//
// Current membership is therefore: every technique whose most recent published/delisted
// event is a `published`.

// CurrentPublicMembers folds the lifecycle log into the live membership set.
// Rank, Bound and Served are not carried by the log — they are recomputed on
// every reconcile — so they come back zero here and are filled in by Reconcile.
func CurrentPublicMembers(events []models.LifecycleEvent) []PublicMember {
	type state struct {
		entered string
		in      bool
	}
	latest := map[string]state{}
	// The log is append-only but not guaranteed sorted by the caller, so order it.
	sorted := make([]models.LifecycleEvent, 0, len(events))
	for _, e := range events {
		if e.Kind == "published" || e.Kind == "delisted" {
			sorted = append(sorted, e)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt < sorted[j].CreatedAt })
	for _, e := range sorted {
		if e.Kind == "published" {
			latest[e.TechniqueID] = state{entered: e.CreatedAt, in: true}
			continue
		}
		latest[e.TechniqueID] = state{in: false}
	}
	var out []PublicMember
	for id, st := range latest {
		if st.in {
			out = append(out, PublicMember{TechniqueID: id, EnteredAt: st.entered})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TechniqueID < out[j].TechniqueID })
	return out
}

// RecomputePublic re-ranks the Public channel and records the difference.
//
// Called on the jobs scheduler tick beside recompute and decay. It writes one
// lifecycle event per arrival and per departure and nothing at all for a technique
// whose rank merely moved, which is what keeps a channel recomputed every cycle
// from producing a stream of noise.
//
// It deliberately does not touch the techniques. Writing membership onto Technique.Channels
// would be the obvious shortcut — Publisher.Feed reads that field — but
// SetChannels bumps UpdatedAt, a feed entry's Updated comes from the technique's
// UpdatedAt, and that is how subscribers decide what is new. Every recompute
// would restamp every member and make every subscriber re-import the whole
// channel. The publisher consults membership separately instead.
func RecomputePublic(st storage.Store, p PublicPolicy) (members []PublicMember, delisted []PublicMember, err error) {
	techniques, err := st.ListTechniques(nil, 0)
	if err != nil {
		return nil, nil, err
	}
	events, err := st.LifecycleEvents("")
	if err != nil {
		return nil, nil, err
	}
	outcome := func(id string) (models.Outcome, bool) {
		o, ok, err := st.GetOutcome(id, "__overall__")
		if err != nil {
			return models.Outcome{}, false
		}
		return o, ok
	}
	ranked, _ := RankPublic(techniques, outcome, ApprovedFromLifecycle(events, techniques), p)
	now := models.Now()
	before := CurrentPublicMembers(events)
	wasMember := make(map[string]bool, len(before))
	for _, m := range before {
		wasMember[m.TechniqueID] = true
	}
	members, delisted = Reconcile(before, ranked, now, p)

	names := make(map[string]models.Technique, len(techniques))
	for _, c := range techniques {
		names[c.ID] = c
	}
	rankOf := make(map[string]int, len(ranked))
	for _, r := range ranked {
		rankOf[r.TechniqueID] = r.Rank
	}
	for _, m := range members {
		if wasMember[m.TechniqueID] {
			continue // already a member; a changed rank is not an event
		}
		appendLifecycle(st, names[m.TechniqueID], m.TechniqueID, "published",
			fmt.Sprintf("entered the Public feed at rank %d (bound %.2f)", m.Rank, m.Bound), now)
	}
	for _, m := range delisted {
		// The reason distinguishes a de-listing from a withdrawal. A subscriber
		// receiving a retraction is told to flag its local copy for review, and
		// "this dropped to 66th" is a very different message from "this technique was
		// wrong" — the wording is the only place that difference survives.
		reason := "left the Public feed: no longer eligible"
		if r, still := rankOf[m.TechniqueID]; still {
			reason = fmt.Sprintf("left the Public feed: fell to rank %d", r)
		}
		appendLifecycle(st, names[m.TechniqueID], m.TechniqueID, "delisted", reason, now)
	}
	return members, delisted, nil
}

func appendLifecycle(st storage.Store, c models.Technique, techniqueID, kind, reason, at string) {
	_, _ = st.AppendLifecycleEvent(models.LifecycleEvent{
		EventID:       contracts.DeterministicLifecycleEventID(techniqueID, kind, at),
		TechniqueID:   techniqueID,
		TechniqueName: c.Name,
		Kind:          kind,
		Provenance:    c.Provenance,
		Reason:        reason,
		CreatedAt:     at,
	})
}
