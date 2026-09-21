// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Public channel: which techniques this registry contributes to the commons, in
// what order, and why each excluded technique was excluded
// (docs/distribution/global-access-plan.md).
//
// Everything here is a pure function over techniques, outcome rollups and an approval
// lookup. Membership is decided by evidence rather than curation, so the decision
// has to be reproducible and explicable: an operator who cannot see why a technique is
// ranked 40th, or why it is not in the channel at all, has been given a feature
// that publishes their work for reasons they cannot inspect.
package federation

import (
	"math"
	"sort"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/pkg/scrub"
)

// PublicChannel is the reserved channel name. It is computed, never assigned:
// SetChannels refuses it, because a hand-published technique sitting in a ranked
// channel would make the ranking a lie.
const PublicChannel = "public"

const (
	// PublicSize is how many techniques this registry contributes. It equals PageSize
	// deliberately: the channel is then exactly one feed page with no archive
	// paging, which keeps the commons cheap to poll.
	//
	// The cap is per REGISTRY — this instance's contribution to the pool. It is
	// not a quota divided among members and not a ceiling on what the pool as a
	// whole carries; sizing it by headcount would make the commons a function of
	// org size rather than of proven usefulness.
	PublicSize = PageSize

	// PublicDelistRank is the hysteresis band. A technique enters at rank <= PublicSize
	// and its membership survives until it falls past this rank.
	//
	// Without the band, recomputing a strict top-50 every tick would flap techniques
	// across the boundary, and the only "this entry is gone" signal the protocol
	// has is a retraction — which tells subscribers to flag their local copy for
	// review. That is the wrong alarm for a technique that is fine and merely placed
	// 51st, and it would fire every few hours.
	PublicDelistRank = 65
)

// wilsonZ is the 1.96 of a 95% interval.
const wilsonZ = 1.959963984540054

// Exclusion reasons. They are shown verbatim in the Federation view, because
// "not eligible" with no reason is a dead end for an operator — particularly for
// the scrub case, where the cause is a stray example address in a recipe and is
// unguessable.
const (
	ReasonOrgScoped    = "org-scoped — never leaves this registry"
	ReasonNotServing   = "not serving here"
	ReasonImported     = "imported from another provider"
	ReasonUnapproved   = "no person has approved it"
	ReasonNoEvidence   = "no measured outcome yet"
	ReasonThinEvidence = "not enough evidence yet"
	ReasonSecrets      = "the secret scanner found something"
)

// PublicPolicy is the tunable part of eligibility.
type PublicPolicy struct {
	// MinN is the sample-size floor. It defaults to AttestationMinN, the same
	// threshold the attestation path already applies.
	//
	// The design that produced this file argued for a floor "well above" that,
	// on the grounds that the helped rate is a known-biased measurement.
	// Measuring a real registry settled it the other way: on a registry of a
	// few dozen techniques and low-thousands of events, almost nothing reaches
	// n=10, so a higher floor ships a commons that is permanently empty. The
	// floor is therefore a knob an operator can raise as their evidence
	// deepens, and the honesty is carried by marking the attested rate
	// provisional instead.
	MinN int
	// Size and DelistRank default to PublicSize and PublicDelistRank.
	Size, DelistRank int
}

func (p PublicPolicy) withDefaults() PublicPolicy {
	if p.MinN <= 0 {
		p.MinN = AttestationMinN
	}
	if p.Size <= 0 {
		p.Size = PublicSize
	}
	if p.DelistRank < p.Size {
		p.DelistRank = PublicDelistRank
	}
	return p
}

// PublicCandidate is an eligible technique with the evidence that ranked it.
type PublicCandidate struct {
	TechniqueID string
	Name        string
	// Helped is the rate as measured, for display beside the bound.
	Helped float64
	// N is the trust denominator (weighted adopted), as SampleSize reports it.
	N int
	// Bound is the Wilson lower bound that actually decides the order.
	Bound float64
	// Rank is 1-based, assigned after sorting.
	Rank int
}

// PublicExclusion is an eligible-looking technique that did not qualify, with why.
type PublicExclusion struct {
	TechniqueID string
	Name        string
	Reason      string
	// N is the sample size so far, for the thin-evidence case where the operator
	// wants to know how far off it is.
	N int
}

// ApprovedFunc reports whether a person has approved this technique.
//
// Approval is a separate question from status. A technique reaches `stable` either
// because a reviewer promoted it or because the auto-promote gate graduated it
// from shadow "with no manual review" — and combined with auto-discover, that is
// a path from observed usage to the public internet with no human anywhere on it,
// under this org's signing key. Publication requires the human.
type ApprovedFunc func(techniqueID string) bool

// OutcomeFunc looks up a technique's overall rollup.
type OutcomeFunc func(techniqueID string) (models.Outcome, bool)

// HumanOrigin reports whether a technique's provenance means a person wrote it, so no
// separate approval record is needed.
//
// `curated` came out of the git techniques directory and `contributed` came from a
// member: in both cases a person authored the text. `suggested`, `mined` and
// `observed` are machine-authored and need an explicit approval event before they
// may leave the registry. `federated` is excluded earlier and on other grounds.
//
// This is deliberately not a blanket amnesty for techniques that predate the approval
// event. A machine-authored technique that was already serving when this shipped stays
// out of the commons until someone accepts it, which is the conservative answer
// and the honest one — there is no record that anybody read it.
func HumanOrigin(provenance string) bool {
	return provenance == "curated" || provenance == "contributed"
}

// ApprovedFromLifecycle builds an ApprovedFunc from the lifecycle log: a technique is
// approved if a person accepted it (an `approved` event) or if its provenance
// means a person wrote it in the first place.
func ApprovedFromLifecycle(events []models.LifecycleEvent, techniques []models.Technique) ApprovedFunc {
	byHuman := map[string]bool{}
	for _, e := range events {
		if e.Kind == "approved" {
			byHuman[e.TechniqueID] = true
		}
	}
	for _, c := range techniques {
		if HumanOrigin(c.Provenance) {
			byHuman[c.ID] = true
		}
	}
	return func(id string) bool { return byHuman[id] }
}

// RankPublic partitions techniques into the ranked candidates for the Public channel
// and the exclusions, with reasons. Candidates come back best-first with Rank
// filled in; exclusions are sorted by technique id so the view is stable.
//
// Only techniques that could plausibly qualify are reported as exclusions: a draft or
// a retired technique is not a near miss and listing every one of them would bury the
// two that an operator can actually act on.
func RankPublic(techniques []models.Technique, outcome OutcomeFunc, approved ApprovedFunc, p PublicPolicy) ([]PublicCandidate, []PublicExclusion) {
	p = p.withDefaults()
	var out []PublicCandidate
	var excluded []PublicExclusion
	note := func(c models.Technique, reason string, n int) {
		excluded = append(excluded, PublicExclusion{TechniqueID: c.ID, Name: c.Name, Reason: reason, N: n})
	}
	for _, c := range techniques {
		// A technique that is not serving locally has no business serving elsewhere,
		// and is not a near miss worth reporting.
		if c.Status != "stable" {
			continue
		}
		if c.Scope != "general" {
			note(c, ReasonOrgScoped, 0)
			continue
		}
		// Re-exporting a technique that arrived from another provider launders its
		// origin and double-counts its evidence. The original publisher is its
		// publisher.
		if c.Provenance == "federated" {
			note(c, ReasonImported, 0)
			continue
		}
		if approved != nil && !approved(c.ID) {
			note(c, ReasonUnapproved, 0)
			continue
		}
		// Scanned on the RENDERED document, because that is exactly the bytes
		// that ship: scanning the fields separately would leave a gap between
		// what was checked and what was served.
		if len(scrub.Scan(RenderTechniqueMarkdown(c))) > 0 {
			note(c, ReasonSecrets, 0)
			continue
		}
		o, ok := outcome(c.ID)
		if !ok || o.HelpedRate == nil {
			note(c, ReasonNoEvidence, 0)
			continue
		}
		if o.SampleSize < p.MinN {
			note(c, ReasonThinEvidence, o.SampleSize)
			continue
		}
		// The weighted pair, consistently. Outcome carries raw Helped/Adopted
		// counts AND weighted ones, with SampleSize defined as floor(weighted
		// adopted) — "the trust denominator" — and HelpedRate as weighted
		// helped over weighted adopted. Mixing a raw numerator with the weighted
		// denominator sitting next to it produces a bound that is quietly wrong
		// in whichever direction the weighting leans.
		trials := o.WeightedAdopted
		successes := o.WeightedHelped
		if trials <= 0 { // rate present but no weighted denominator: fall back to the raw pair
			trials, successes = float64(o.Adopted), float64(o.Helped)
		}
		out = append(out, PublicCandidate{
			TechniqueID: c.ID, Name: c.Name,
			Helped: *o.HelpedRate, N: o.SampleSize,
			Bound: WilsonLowerBound(successes, trials),
		})
	}
	// Best bound first. Ties break on sample size (more evidence wins), then id,
	// so the order is total and does not wobble between ticks.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bound != out[j].Bound {
			return out[i].Bound > out[j].Bound
		}
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].TechniqueID < out[j].TechniqueID
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].TechniqueID < excluded[j].TechniqueID })
	return out, excluded
}

// WilsonLowerBound is the lower end of the 95% Wilson score interval for
// successes out of trials.
//
// The raw rate is the wrong ranking key and it is worth being explicit about
// why: it puts a technique that helped once out of once (100%) above one that helped
// 40 times out of 50 (80%). Ranking a commons that way fills it with noise and
// buries exactly the techniques the pool exists to move. The Wilson bound asks
// instead "what is the worst this rate plausibly is, given how little we have
// seen?", which is the question a stranger deciding whether to import the technique
// actually has.
func WilsonLowerBound(successes, trials float64) float64 {
	if trials <= 0 {
		return 0
	}
	if successes < 0 {
		successes = 0
	}
	if successes > trials {
		successes = trials
	}
	phat := successes / trials
	z2 := wilsonZ * wilsonZ
	denom := 1 + z2/trials
	center := phat + z2/(2*trials)
	margin := wilsonZ * math.Sqrt(phat*(1-phat)/trials+z2/(4*trials*trials))
	lower := (center - margin) / denom
	if lower < 0 {
		return 0
	}
	return lower
}

// PublicMember is one technique's membership in the channel.
//
// Membership is stored state rather than a render-time computation, because
// hysteresis needs to know yesterday's set to decide today's and EnteredAt has
// nowhere else to live. It is event-sourced from the lifecycle log (see
// publicmembers.go) rather than a new storage object, which also puts the
// commons' history in the Events view, where an operator already looks to answer
// "what is OpenTacit doing?".
type PublicMember struct {
	TechniqueID string
	EnteredAt   string
	// Bound and Rank are recomputed each reconcile, for display.
	Bound float64
	Rank  int
	// Served is whether the technique is actually IN the feed right now. A member
	// ranked between Size and DelistRank is held — no longer served, not yet
	// retracted — which is what the band buys.
	Served bool
}

// Reconcile decides the next membership set from the current one and a fresh
// ranking, returning the members to keep and the ones to de-list.
//
// The band works as follows. A technique is served when it is in the top Size of the
// current ranking. Its MEMBERSHIP survives while it ranks within DelistRank, so a
// technique that dips to 52 and recovers to 48 keeps its original EnteredAt and never
// produces a retraction/re-add pair. Only when it falls past DelistRank, or stops
// being eligible at all, is the record dropped — and that is the one case worth
// telling subscribers about.
//
// An entry disappearing from a feed is not itself a signal in this protocol:
// subscribers keep what they imported, so a technique sliding out of the served window
// costs them nothing and warns them about nothing. The explicit retraction is
// reserved for the real de-listing.
func Reconcile(current []PublicMember, ranked []PublicCandidate, now string, p PublicPolicy) (next, delisted []PublicMember) {
	p = p.withDefaults()
	was := make(map[string]PublicMember, len(current))
	for _, m := range current {
		was[m.TechniqueID] = m
	}
	seen := map[string]bool{}
	for _, cand := range ranked {
		incumbent, isMember := was[cand.TechniqueID]
		switch {
		case isMember && cand.Rank <= p.DelistRank:
			// Held or served, but either way still a member, with its original
			// arrival date intact.
			seen[cand.TechniqueID] = true
			next = append(next, PublicMember{
				TechniqueID: cand.TechniqueID, EnteredAt: incumbent.EnteredAt,
				Bound: cand.Bound, Rank: cand.Rank, Served: cand.Rank <= p.Size,
			})
		case !isMember && cand.Rank <= p.Size && countServed(next) < p.Size:
			seen[cand.TechniqueID] = true
			next = append(next, PublicMember{
				TechniqueID: cand.TechniqueID, EnteredAt: now,
				Bound: cand.Bound, Rank: cand.Rank, Served: true,
			})
		}
	}
	// Anything that was a member and is not in the new set has either fallen past
	// the band or stopped being eligible. Both are de-listings.
	for _, m := range current {
		if !seen[m.TechniqueID] {
			delisted = append(delisted, m)
		}
	}
	sort.Slice(delisted, func(i, j int) bool { return delisted[i].TechniqueID < delisted[j].TechniqueID })
	return next, delisted
}

func countServed(ms []PublicMember) int {
	n := 0
	for _, m := range ms {
		if m.Served {
			n++
		}
	}
	return n
}

// ServedIDs is the set of technique ids the Public feed should actually carry.
func ServedIDs(members []PublicMember) map[string]bool {
	out := make(map[string]bool, len(members))
	for _, m := range members {
		if m.Served {
			out[m.TechniqueID] = true
		}
	}
	return out
}
