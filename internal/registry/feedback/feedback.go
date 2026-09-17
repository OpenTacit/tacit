// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package feedback is the write-back: ingest events and roll them up.
//
// The event log is the append-only source of truth; rollups are derived by
// full recompute (simple and idempotent at pilot scale). Each event fans out
// into one rollup row per segment dimension plus overall, so a technique is
// rankable per team/role/domain/etc. Also the decay check that powers the
// self-healing loop.
package feedback

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/opentacit/tacit/internal/modelid"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/contracts"
)

// logLifecycle records one curation moment for the Events view. It is
// best-effort telemetry: the technique has
// already moved by the time this is called, so a failure to log must never fail
// the transition it merely describes — the error is intentionally dropped.
// Cohort-free by construction: a technique's lifecycle carries no member or session.
func logLifecycle(st storage.Store, technique models.Technique, kind, reason, at string) {
	_, _ = st.AppendLifecycleEvent(models.LifecycleEvent{
		EventID:       contracts.DeterministicLifecycleEventID(technique.ID, kind, at),
		TechniqueID:   technique.ID,
		TechniqueName: technique.Name,
		Kind:          kind,
		Provenance:    technique.Provenance,
		Reason:        reason,
		CreatedAt:     at,
	})
}

// ErrRetrievalNotExposure rejects a `shown` event for an interaction the
// registry recorded as a PULL. Callers report it as a rejection, not a failure.
var ErrRetrievalNotExposure = errors.New("shown event for a retrieval pull: retrieval is not exposure")

// pullSurfaces are the surfaces on which retrieval happens without anyone being
// shown anything. "mcp" is a tacit_search: an agent browsing the playbook, which
// may pull a dozen candidates it will never act on. The push nudge and the
// @tacit answer — the two paths where a member actually sees a technique — carry the
// harness's own surface, never these.
var pullSurfaces = map[string]bool{"mcp": true}

// Ingest inserts one event. Returns the event id if inserted, "" for a
// replayed event_id (idempotent), and ErrRetrievalNotExposure for a `shown`
// event the fact log contradicts.
//
// The guard exists because the funnel's denominator is only as honest as the
// least current client writing to it. A search PULL emitting `shown` per result
// inflated `shown` with techniques nobody saw and nobody could adopt, depressing
// every adoption and helped rate derived from it. That client was fixed
// (internal/auditor/mcp), but the fix rode in a binary, and an MCP server
// spawned by a harness can outlive many deploys — one here served an 11-day-old
// build and kept writing for a day after the fix shipped. A client-side fix
// cannot reach a process already running, and third-party producers hold the
// same API key, so the invariant belongs at the write gate the registry owns.
//
// The check is a join, not a heuristic: the same request that retrieved the
// candidates recorded an audit fact stating what the interaction WAS, and its
// surface says whether a member was there. An unknown audit is accepted —
// absence of a fact is not evidence of a pull — and a store that cannot answer
// accepts too, because bookkeeping must never drop a member's real event.
func Ingest(st storage.Store, e models.FeedbackEvent) (string, error) {
	if e.AuditID != "" {
		f, found, err := st.AuditFact(e.AuditID)
		switch {
		case err != nil:
			log.Printf("feedback: audit fact %s unreadable, accepting event: %v", e.AuditID, err)
		case found && e.Stage == "shown" && pullSurfaces[f.Surface]:
			return "", ErrRetrievalNotExposure
		case found:
			e.Segment = withModel(e.Segment, f.Model)
		}
	}
	inserted, err := st.InsertEvent(e)
	if err != nil {
		return "", err
	}
	if !inserted {
		return "", nil
	}
	return e.EventID, nil
}

// withModel stamps the canonical model cohort onto an event's segment from the
// audit fact the event refers to, for producers that don't send one.
//
// The fact always knew: capture has latched the model since the harness first
// reported it, and the registry has stored it on every fact since. What was
// missing was the step onto the segment, which is where a cohort has to be for
// any rollup or view to see it — so an event written by any build before that
// step existed carries the model in the fact log and nowhere useful. This puts
// it where it belongs on the way in. A segment that already names a model is
// left alone: the producer was closer to the session than the fact log is.
func withModel(seg models.Segment, model string) models.Segment {
	key := modelid.Key(model)
	if key == "" || seg["model"] != "" {
		return seg
	}
	out := models.Segment{}
	for k, v := range seg {
		out[k] = v
	}
	out["model"] = key
	return out
}

// modelsByAudit reads the fact log once and indexes the model each audit ran
// against. Only audits some event actually refers to are kept, so the map costs
// what the join needs rather than what the fact log holds. A store that cannot
// answer yields an empty map and the recompute simply proceeds without the
// history — a rollup that is short some old cohort rows is a smaller problem
// than a recompute that refuses to run.
func modelsByAudit(st storage.Store, events []models.FeedbackEvent) map[string]string {
	wanted := map[string]bool{}
	for _, e := range events {
		if e.Segment["model"] == "" && e.AuditID != "" {
			wanted[e.AuditID] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	facts, err := st.AuditFacts("")
	if err != nil {
		log.Printf("feedback: fact log unreadable, model cohort not backfilled: %v", err)
		return nil
	}
	out := make(map[string]string, len(wanted))
	for _, f := range facts {
		if f.Model != "" && wanted[f.AuditID] {
			out[f.AuditID] = f.Model
		}
	}
	return out
}

func segmentKeys(segment models.Segment) []string {
	keys := []string{config.OverallKey}
	for _, dim := range config.SegmentDimensions {
		if val := segment[dim]; val != "" {
			keys = append(keys, dim+":"+val)
		}
	}
	return keys
}

type counts struct {
	shown, adopted, helped, dismissed int
	// The confidence-weighted funnel (config.InferredWeight): what rates and
	// ranking read, so uncalibrated behavioral inference cannot move
	// helped_rate at the same weight as an explicit member verdict.
	wAdopted, wHelped, wDismissed float64
}

// eventWeight scales a member-verdict event by its confidence. Anything not
// explicitly marked inferred counts in full: "explicit" by declaration, ""
// because producers that never set the field predate the distinction and were
// always full-weight.
func eventWeight(confidence string) float64 {
	if confidence == "inferred" {
		return config.InferredWeight
	}
	return 1
}

// add takes the whole event, not just its stage: "helped" carries the member's
// answer in Value, and the stage alone cannot tell a yes from a no.
func (c *counts) add(e models.FeedbackEvent) {
	w := eventWeight(e.Confidence)
	switch e.Stage {
	case "shown":
		c.shown++ // machine-observed delivery, never scaled
	case "adopted":
		c.adopted++
		c.wAdopted += w
	case "helped":
		if !e.CountsAsHelped() {
			break
		}
		c.helped++
		c.wHelped += w
	case "dismissed":
		c.dismissed++
		c.wDismissed += w
	}
}

// RecomputeOutcomes rebuilds every (technique, segment_key) rollup from the log.
// Raw counts record what happened; the weighted funnel is what the rates —
// and through them retrieval ranking — actually consume.
func RecomputeOutcomes(st storage.Store) (int, error) {
	events, err := st.AllEvents("")
	if err != nil {
		return 0, err
	}
	// Recover the model cohort for events written before it was a dimension.
	// The join is on audit_id and the source is the fact log, which recorded the
	// model all along — so a registry that upgrades gets its history back rather
	// than a dimension that starts at zero and looks like a fresh habit. The
	// event log itself is append-only and untouched: this enriches in memory, on
	// the way into the rollup, and re-running it changes nothing.
	factModels := modelsByAudit(st, events)
	acc := map[[2]string]*counts{}
	for _, e := range events {
		if e.Segment["model"] == "" && e.AuditID != "" {
			e.Segment = withModel(e.Segment, factModels[e.AuditID])
		}
		for _, key := range segmentKeys(e.Segment) {
			k := [2]string{e.TechniqueID, key}
			c := acc[k]
			if c == nil {
				c = &counts{}
				acc[k] = c
			}
			c.add(e)
		}
	}

	now := models.Now()
	rows := make([]models.Outcome, 0, len(acc))
	for k, c := range acc {
		var adoptionRate, helpedRate *float64
		if c.shown > 0 {
			r := c.wAdopted / float64(c.shown)
			adoptionRate = &r
		}
		if c.wAdopted > 0 {
			r := c.wHelped / c.wAdopted
			helpedRate = &r
		}
		rows = append(rows, models.Outcome{
			TechniqueID: k[0], SegmentKey: k[1],
			Shown: c.shown, Adopted: c.adopted, Helped: c.helped, Dismissed: c.dismissed,
			WeightedAdopted: c.wAdopted, WeightedHelped: c.wHelped, WeightedDismissed: c.wDismissed,
			AdoptionRate: adoptionRate, HelpedRate: helpedRate,
			SampleSize: int(c.wAdopted), LastUpdated: now,
		})
	}
	if err := st.ReplaceOutcomes(rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// shadowFit is one technique's shadow relevance tally: fit-checks that accepted it
// (shown) vs rejected it (declined). Only the shadow_shown/shadow_declined
// stages carry it; every other stage is ignored.
type shadowFit struct{ shown, declined int }

func (f shadowFit) judged() int { return f.shown + f.declined }
func (f shadowFit) rate() float64 {
	if f.judged() == 0 {
		return 0
	}
	return float64(f.shown) / float64(f.judged())
}

// shadowFitCounts tallies shadow relevance per technique from the full log.
func shadowFitCounts(st storage.Store) (map[string]shadowFit, error) {
	events, err := st.AllEvents("")
	if err != nil {
		return nil, err
	}
	out := map[string]shadowFit{}
	for _, e := range events {
		f := out[e.TechniqueID]
		switch e.Stage {
		case "shadow_shown":
			f.shown++
		case "shadow_declined":
			f.declined++
		default:
			continue
		}
		out[e.TechniqueID] = f
	}
	return out, nil
}

// RetireStaleShadow auto-retires shadow techniques that have been fit-checked enough
// times (ShadowRetireMinSample) and fit too rarely (fit rate <= ShadowRetireFloor)
// — the negative end of the shadow loop (docs/learning/validation-without-review.md,
// D7): a technique that entered evaluation without a human can leave it without one,
// once its own evidence says it does not belong. Techniques still gathering evidence,
// or fitting well enough, are left in shadow. Returns how many were retired.
func RetireStaleShadow(st storage.Store) (int, error) {
	fits, err := shadowFitCounts(st)
	if err != nil {
		return 0, err
	}
	shadow, err := st.ListTechniques([]string{"shadow"}, 0)
	if err != nil {
		return 0, err
	}
	now := models.Now()
	retired := 0
	for _, technique := range shadow {
		f := fits[technique.ID]
		if f.judged() < config.ShadowRetireMinSample {
			continue
		}
		if f.rate() > config.ShadowRetireFloor {
			continue
		}
		if err := st.SetTechniqueStatus(technique.ID, "retired", now); err != nil {
			return retired, err
		}
		logLifecycle(st, technique, "retired",
			fmt.Sprintf("shadow fit %.0f%% over %d verdicts — below the retire floor", f.rate()*100, f.judged()), now)
		retired++
	}
	return retired, nil
}

// AutoPromoteGate is the operator's evidence bar for graduating a shadow technique to
// serving with NO human (docs/learning/validation-without-review.md — the
// keystone of automated review). A zero value promotes nothing: like autonomy,
// automating the review decision is opt-in.
type AutoPromoteGate struct {
	Enabled   bool
	MinFit    float64 // shadow fit rate the technique must clear
	MinJudged int     // fit-check verdicts required behind that rate
}

// AutoPromoteShadow graduates shadow techniques whose fit-check evidence clears the
// gate to stable — the positive mirror of RetireStaleShadow, and the step that
// removes the human from the review decision. It promotes on RELEVANCE (the only
// signal a never-surfaced technique can have); helpfulness is then measured by the
// ordinary served funnel, and a technique that does not help decays and is revoked.
// So the loop is closed and self-correcting on both ends: relevance graduates a
// technique in, poor helpfulness (decay) or poor relevance (retire) takes it out.
// Returns how many were promoted.
func AutoPromoteShadow(st storage.Store, g AutoPromoteGate) (int, error) {
	if !g.Enabled {
		return 0, nil
	}
	fits, err := shadowFitCounts(st)
	if err != nil {
		return 0, err
	}
	shadow, err := st.ListTechniques([]string{"shadow"}, 0)
	if err != nil {
		return 0, err
	}
	now := models.Now()
	promoted := 0
	for _, technique := range shadow {
		f := fits[technique.ID]
		if f.judged() < g.MinJudged || f.rate() < g.MinFit {
			continue
		}
		if err := st.SetTechniqueStatus(technique.ID, "stable", now); err != nil {
			return promoted, err
		}
		logLifecycle(st, technique, "promoted",
			fmt.Sprintf("shadow fit %.0f%% over %d verdicts cleared the promote gate", f.rate()*100, f.judged()), now)
		promoted++
	}
	return promoted, nil
}

// DetectDecay flags techniques whose recent helped_rate drops sharply below their
// baseline. Returns how many were flagged.
func DetectDecay(st storage.Store) (int, error) {
	since := time.Now().UTC().AddDate(0, 0, -config.DecayWindowDays).Format(time.RFC3339Nano)
	events, err := st.AllEvents(since)
	if err != nil {
		return 0, err
	}
	recent := map[string]*counts{}
	for _, e := range events {
		c := recent[e.TechniqueID]
		if c == nil {
			c = &counts{}
			recent[e.TechniqueID] = c
		}
		// Same confidence weighting as the rollup: the baseline HelpedRate is
		// weighted, so the recent rate it is compared against must be too.
		c.add(e)
	}

	techniques, err := st.ListTechniques(nil, 0)
	if err != nil {
		return 0, err
	}
	now := models.Now()
	flagged := 0
	for _, technique := range techniques {
		base, ok, err := st.GetOutcome(technique.ID, config.OverallKey)
		if err != nil {
			return flagged, err
		}
		if !ok || base.HelpedRate == nil {
			continue
		}
		r := recent[technique.ID]
		if r == nil || r.adopted < config.DecayMinSample {
			continue
		}
		recentRate := 0.0
		if r.wAdopted > 0 {
			recentRate = r.wHelped / r.wAdopted
		}
		decayed := recentRate < *base.HelpedRate-config.DecayDrop
		if err := st.SetDecay(technique.ID, decayed, now); err != nil {
			return flagged, err
		}
		if decayed {
			// Log only the ONSET — DetectDecay runs every recompute cycle and
			// re-flags an already-decayed technique each time, but the Events feed
			// wants the moment it turned, not a heartbeat. technique.DecaySignal is the
			// prior stored state (the loop reads techniques before this write).
			if technique.DecaySignal == 0 {
				logLifecycle(st, technique, "decayed",
					fmt.Sprintf("recent helped rate %.0f%% fell %.0f points below its %.0f%% baseline",
						recentRate*100, (*base.HelpedRate-recentRate)*100, *base.HelpedRate*100), now)
			}
			flagged++
		}
	}
	return flagged, nil
}
