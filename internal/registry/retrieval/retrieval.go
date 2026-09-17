// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package retrieval turns a Characterization into a COLLECTIVE EVIDENCE block.
//
// The heart of the service (docs/design/design.md): relevance gate by cosine
// similarity, then rank by Bayesian-shrinkage impact so a lucky 1/1 can't
// outrank a measured 90/100, with graceful collapse to similarity at
// cold-start. The registry computes the query embedding itself (same embedder
// as the techniques) so query and techniques share one space.
package retrieval

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/modelid"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
)

type evidenceStore interface {
	CandidateTechniques() ([]models.Technique, error)
	GetOutcome(string, string) (models.Outcome, bool, error)
	GetTechnique(string) (models.Technique, bool, error)
	OutcomesForSegment(string) ([]models.Outcome, error)
	ShadowCandidateTechniques() ([]models.Technique, error)
}

// Shrink is the confidence-weighted helped_rate: regress small samples toward
// the prior. Dismissals join the denominator — each one is a colleague saying
// "this did not apply to me", and until they counted here, the one piece of
// feedback members give DELIBERATELY was collected, charted, and used for
// nothing: a technique dismissed fifty times with zero adoptions ranked exactly as
// it did before the first dismissal.
func Shrink(helped, adopted, dismissed float64) float64 {
	return (helped + config.Prior*config.ShrinkK) /
		(adopted + dismissed + config.ShrinkK)
}

// weightedFunnel reads an outcome's confidence-weighted counts, falling back
// to the raw integers for rows written before the weighting existed (a
// restart recomputes them, but ranking must not misread the interim).
func weightedFunnel(o *models.Outcome) (helped, adopted, dismissed float64) {
	if o.WeightedAdopted > 0 || o.WeightedHelped > 0 || o.WeightedDismissed > 0 {
		return o.WeightedHelped, o.WeightedAdopted, o.WeightedDismissed
	}
	return float64(o.Helped), float64(o.Adopted), float64(o.Dismissed)
}

// Supports is the recall-first support check.
//
// support_matrix is a positive hint, not a hard whitelist. Harness labels
// proliferate in the wild, so hard equality gating silently drops every technique
// for an unrecognized harness. Instead: EXCLUDE only on an explicit negative
// for this harness or model; a positive match keeps the technique and adds a
// support note; no entry keeps it too (the audit layer confirms fit via
// applies_when).
//
// A row may name a harness, a model, or both. A model row is how "this stopped
// working on the model you are running" reaches retrieval at all
// (docs/design/single-user-value.md) — a technique that depends on a
// behaviour one model has and another does not is excluded on the model alone,
// with no harness in the row. Rows are matched on the canonical model key
// (internal/modelid), so a row verified against one release of a model holds for
// the next unless somebody writes a negative one.
func Supports(technique models.Technique, harness, surface, model string) (bool, string) {
	sm := technique.SupportMatrix
	if len(sm) == 0 || (harness == "" && model == "") {
		return true, ""
	}
	modelKey := modelid.Key(model)
	rowModel := func(e map[string]any) string { return modelid.Key(str(e["model"])) }

	// Explicit negatives first: one "supported: false" naming this harness or
	// this model settles the question, whatever else the matrix says.
	for _, e := range sm {
		b, ok := e["supported"].(bool)
		if !ok || b {
			continue
		}
		eh, em := str(e["harness"]), rowModel(e)
		if eh != "" && eh == harness && (em == "" || em == modelKey) {
			return false, ""
		}
		if em != "" && em == modelKey && eh == "" {
			return false, ""
		}
	}
	for _, e := range sm {
		if b, ok := e["supported"].(bool); ok && !b {
			continue
		}
		eh, em := str(e["harness"]), rowModel(e)
		if eh != "" && eh != harness {
			continue
		}
		if em != "" && em != modelKey {
			continue
		}
		if eh == "" && em == "" {
			continue
		}
		esurf := str(e["surface"])
		if surface != "" && esurf != "" && esurf != surface {
			continue
		}
		return true, supportNote(eh, esurf, em, str(e["verified"]))
	}
	return true, ""
}

// InProject is the project scope check, and it is the one place recall-first
// does not hold — deliberately.
//
// support_matrix rows may name a `project`. Everywhere else in this file a
// positive row is a hint and only an explicit negative excludes, because harness
// labels proliferate and a hard whitelist silently drops everything under an
// unrecognised one. Project names do not proliferate: they are the member's own
// repositories, and a member who wrote `project: tacit` on a technique was
// saying where it applies, not offering a hint. So a technique carrying any
// positive project row is served ONLY in the projects it names.
//
// The recall-first half survives where it matters. A session that reports no
// project excludes nothing: the rule needs the member to have said where they
// are, and absence of that is not evidence they are somewhere else. This is what
// stops a Go technique firing in a docs repository, and it matters more to one
// member than to a team, because one person's projects differ more than one
// team's do (docs/design/single-user-value.md).
func InProject(technique models.Technique, project string) bool {
	if project == "" {
		return true
	}
	scoped := false
	for _, e := range technique.SupportMatrix {
		p := str(e["project"])
		if p == "" {
			continue
		}
		supported, stated := e["supported"].(bool)
		if stated && !supported {
			if p == project {
				return false // an explicit "not here"
			}
			continue
		}
		scoped = true
		if p == project {
			return true
		}
	}
	return !scoped
}

// ProjectOf reads the repository a characterization happened in from the
// resources the turn demonstrably touched. capture records it as `repo:<name>` —
// a basename, never a path — which is the only project identifier that crosses
// to the registry at all.
func ProjectOf(ch models.Characterization) string {
	for _, r := range ch.InternalResourcesInPlay {
		if name, ok := strings.CutPrefix(r, "repo:"); ok && name != "" {
			return name
		}
	}
	return ""
}

// supportNote is the one-line "works in …" a matched row contributes. It names
// only what the row actually asserted: a row about a model says the model, a row
// about a harness says the harness, and a row about both says both.
func supportNote(harness, surface, model, verified string) string {
	note := "works"
	if harness != "" {
		note += " in " + harness
		if surface != "" {
			note += " " + surface
		}
	}
	if model != "" {
		note += " on " + model
	}
	if verified != "" {
		note += " (verified " + verified + ")"
	}
	return note
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// recentlyShipped reports whether shipped (YYYY-MM) is within the last 6 months.
func recentlyShipped(shipped string) bool {
	parts := strings.SplitN(shipped, "-", 3)
	if len(parts) < 2 {
		return false
	}
	y, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	now := time.Now().UTC()
	months := (now.Year()-y)*12 + int(now.Month()) - m
	return months >= 0 && months <= 6
}

// segmentOutcome picks the most specific segment dimension with enough sample,
// else overall, else none.
func segmentOutcome(st evidenceStore, techniqueID string, segment models.Segment) (string, *models.Outcome, error) {
	for _, dim := range config.SegmentPreference {
		val := segment[dim]
		if val == "" {
			continue
		}
		key := dim + ":" + val
		o, ok, err := st.GetOutcome(techniqueID, key)
		if err != nil {
			return "", nil, err
		}
		if ok && o.SampleSize >= config.MinSample {
			return key, &o, nil
		}
	}
	o, ok, err := st.GetOutcome(techniqueID, config.OverallKey)
	if err != nil {
		return "", nil, err
	}
	if ok {
		return config.OverallKey, &o, nil
	}
	return config.OverallKey, nil, nil
}

func cohort(st evidenceStore, segment models.Segment, usedIDs []string) (models.Cohort, error) {
	key := config.OverallKey
	for _, dim := range config.CohortPreference { // team first, then role
		if val := segment[dim]; val != "" {
			key = dim + ":" + val
			break
		}
	}
	rows, err := st.OutcomesForSegment(key)
	if err != nil {
		return models.Cohort{}, err
	}
	if len(rows) == 0 {
		if rows, err = st.OutcomesForSegment(config.OverallKey); err != nil {
			return models.Cohort{}, err
		}
	}
	var common []models.Outcome
	for _, r := range rows {
		if r.Adopted > 0 {
			common = append(common, r)
		}
		if len(common) >= config.CohortTop {
			break
		}
	}
	used := map[string]bool{}
	for _, id := range usedIDs {
		used[id] = true
	}
	uses := 0
	examples := []string{}
	for _, r := range common {
		if used[r.TechniqueID] {
			uses++
			continue
		}
		if len(examples) < 3 {
			if c, ok, err := st.GetTechnique(r.TechniqueID); err != nil {
				return models.Cohort{}, err
			} else if ok {
				examples = append(examples, c.Name)
			}
		}
	}
	return models.Cohort{Segment: key, Uses: uses, CommonTotal: len(common), Examples: examples}, nil
}

type rankedTechnique struct {
	impact    float64
	sim       float64
	support   string
	technique models.Technique
	segKey    string
	outcome   *models.Outcome
}

// AutonomyGate is the operator's evidence bar for silent agent application
// (docs/delivery/agent-delivery-plan.md Phase C). A zero value gates nothing:
// autonomy is opt-in.
type AutonomyGate struct {
	Enabled       bool
	MinHelpedRate float64
	MinN          int
}

// eligible: a stable technique whose measured record clears the bar. Draft, mined,
// decayed, and retired techniques never qualify — promotion is a human act, and
// decay revokes eligibility by construction.
func (g AutonomyGate) eligible(technique models.Technique, o *models.OutcomeSummary) bool {
	if !g.Enabled || o == nil || o.HelpedRate == nil || technique.Status != "stable" {
		return false
	}
	return *o.HelpedRate >= g.MinHelpedRate && o.SampleSize >= g.MinN
}

// BuildEvidence runs the retrieval algorithm from docs/design/design.md.
func BuildEvidence(st evidenceStore, ch models.Characterization, embedder embed.Embedder, gate AutonomyGate) (models.EvidenceBlock, error) {
	queryVectors := embedder.Embed([]string{embed.QueryText(ch)})
	// The ONNX embedder returns an invalid vector when inference fails for one
	// text. Treat that as a retrieval miss. With no similarity floor, allowing a
	// zero or non-finite score through would return candidates based on outcomes
	// and store order alone.
	if len(queryVectors) != 1 || !embed.Valid(queryVectors[0], embedder.Dim()) {
		return models.EvidenceBlock{Meta: models.EvidenceMeta{Thin: true}}, nil
	}
	qvec := queryVectors[0]

	// 1-3: similarity over eligible candidates that support the harness
	type scoredTechnique struct {
		sim       float64
		support   string
		technique models.Technique
	}
	candidates0, err := st.CandidateTechniques()
	if err != nil {
		return models.EvidenceBlock{}, err
	}
	var scored []scoredTechnique
	for _, technique := range candidates0 {
		ok, support := Supports(technique, ch.Harness, ch.Surface, ch.Model)
		if ok {
			ok = InProject(technique, ProjectOf(ch))
		}
		if !ok {
			continue
		}
		scored = append(scored, scoredTechnique{embed.Dot(qvec, technique.Embedding), support, technique})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].sim > scored[j].sim })

	// 4: relevance gate — a floor first, then the top-K truncation.
	//
	// The floor is what makes this a gate at all: without it a turn that
	// matches nothing still yields K candidates, and the fit-check absorbs the
	// whole cost of saying no. It is disabled by default (config.MinSimilarity
	// == 0) until the recorded FeedbackEvent.Similarity distribution says where
	// it belongs — so this loop is a no-op today and a real cut the moment the
	// constant moves. `scored` is already sorted descending, so the first
	// candidate below the floor ends the eligible prefix.
	if config.MinSimilarity > 0 {
		for i, sc := range scored {
			if sc.sim < config.MinSimilarity {
				scored = scored[:i]
				break
			}
		}
	}
	if len(scored) > config.RelevanceK {
		scored = scored[:config.RelevanceK]
	}

	// 5-6: rank by measured impact (shrunk), similarity as tiebreaker.
	//
	// A technique's impact only reorders it above more-similar techniques once its sample
	// is informative (>= MinRankSample adoptions). Below that floor it keeps the
	// cold-start Prior and sorts by similarity like the unmeasured cohort — so a
	// lucky 1/1 (Shrink(1,1)=0.524) can't leapfrog every more-relevant unmeasured
	// technique (Prior=0.5). This is what makes the collapse-to-similarity at cold
	// start real: one stray measurement no longer dominates the whole corpus.
	var ranked []rankedTechnique
	anyMeasured := false
	for _, sc := range scored {
		segKey, o, err := segmentOutcome(st, sc.technique.ID, ch.Segment)
		if err != nil {
			return models.EvidenceBlock{}, err
		}
		impact := config.Prior
		if o != nil && o.Adopted > 0 {
			anyMeasured = true
		}
		// Measured impact reorders a technique once EITHER signal is informative:
		// enough adoptions to trust the rate, or enough dismissals to trust the
		// rejection. Without the second arm, a never-adopted technique could absorb
		// dismissals forever and keep ranking on pure similarity. Both the
		// floors and the shrunk rate read the confidence-WEIGHTED funnel, so an
		// all-inferred technique needs proportionally more evidence to reorder.
		if o != nil {
			h, a, d := weightedFunnel(o)
			if a >= config.MinRankSample || d >= config.MinDismissSample {
				impact = Shrink(h, a, d)
			}
		}
		ranked = append(ranked, rankedTechnique{impact, sc.sim, sc.support, sc.technique, segKey, o})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].impact != ranked[j].impact {
			return ranked[i].impact > ranked[j].impact
		}
		return ranked[i].sim > ranked[j].sim
	})

	// 7: build candidates, with one slot reserved for exploration
	candidates := []models.EvidenceCandidate{}
	for _, r := range selectWithExploration(ranked) {
		var outcomes *models.OutcomeSummary
		if r.outcome != nil && r.outcome.Adopted > 0 {
			outcomes = &models.OutcomeSummary{
				HelpedRate:   round3(r.outcome.HelpedRate),
				AdoptionRate: round3(r.outcome.AdoptionRate),
				SampleSize:   r.outcome.SampleSize,
				Segment:      r.segKey,
			}
		}
		scope := r.technique.Scope
		if scope == "" {
			scope = "general"
		}
		candidates = append(candidates, models.EvidenceCandidate{
			TechniqueID: r.technique.ID,
			Name:        r.technique.Name,
			Scope:       scope,
			Recipe:      r.technique.Recipe,
			AppliesWhen: r.technique.AppliesWhen,
			NotWhen:     r.technique.NotWhen,
			Support:     r.support,
			Outcomes:    outcomes,
			Freshness: models.Freshness{
				Shipped:         r.technique.Shipped,
				RecentlyShipped: recentlyShipped(r.technique.Shipped),
			},
			AutonomyEligible: gate.eligible(r.technique, outcomes),
			Similarity:       round3f(r.sim),
		})
	}

	coh, err := cohort(st, ch.Segment, ch.UsedTechniqueIDs)
	if err != nil {
		return models.EvidenceBlock{}, err
	}
	shadow, err := shadowEvidence(st, qvec, ch)
	if err != nil {
		return models.EvidenceBlock{}, err
	}
	return models.EvidenceBlock{
		Candidates:       candidates,
		Cohort:           coh,
		Meta:             models.EvidenceMeta{Thin: len(candidates) < config.EvidenceThinBelow || !anyMeasured},
		ShadowCandidates: shadow,
	}, nil
}

// shadowEvidence scores shadow-status techniques (docs/learning/validation-without-review.md)
// by similarity and returns the top ShadowK as judge-only candidates. They are
// held apart from the real candidate pool by ShadowCandidateTechniques, carry no
// outcomes, and never rank by impact — a shadow technique has no funnel yet, which is
// exactly why it is in shadow. They do not touch the serving candidates, Meta,
// or the thin/measured signals, so they cannot influence what is surfaced.
func shadowEvidence(st evidenceStore, qvec []float32, ch models.Characterization) ([]models.EvidenceCandidate, error) {
	techniques, err := st.ShadowCandidateTechniques()
	if err != nil {
		return nil, err
	}
	type scored struct {
		sim       float64
		support   string
		technique models.Technique
	}
	var ss []scored
	for _, technique := range techniques {
		ok, support := Supports(technique, ch.Harness, ch.Surface, ch.Model)
		if ok {
			ok = InProject(technique, ProjectOf(ch))
		}
		if !ok {
			continue
		}
		ss = append(ss, scored{embed.Dot(qvec, technique.Embedding), support, technique})
	}
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].sim > ss[j].sim })
	out := []models.EvidenceCandidate{}
	for _, s := range ss {
		if len(out) >= config.ShadowK {
			break
		}
		scope := s.technique.Scope
		if scope == "" {
			scope = "general"
		}
		out = append(out, models.EvidenceCandidate{
			TechniqueID: s.technique.ID,
			Name:        s.technique.Name,
			Scope:       scope,
			Recipe:      s.technique.Recipe,
			AppliesWhen: s.technique.AppliesWhen,
			NotWhen:     s.technique.NotWhen,
			Support:     s.support,
			Freshness: models.Freshness{
				Shipped:         s.technique.Shipped,
				RecentlyShipped: recentlyShipped(s.technique.Shipped),
			},
		})
	}
	return out, nil
}

// selectWithExploration picks the EvidenceN techniques to return, reserving one slot
// for an under-explored technique so measured techniques cannot starve a cold technique forever
// (docs/learning/validation-without-review.md, D4). It is a bounded exploration
// floor, not a full bandit: exactly one slot, and only when the natural top-N is
// already all well-explored — we never displace one under-explored technique for
// another, and we never touch the primary picks when the corpus is small.
func selectWithExploration(ranked []rankedTechnique) []rankedTechnique {
	n := config.EvidenceN
	if len(ranked) <= n {
		return ranked
	}
	primary := ranked[:n]
	if n < 2 || config.ExploreFloorShown <= 0 {
		return primary
	}
	underExplored := func(r rankedTechnique) bool {
		return r.outcome == nil || r.outcome.Shown < config.ExploreFloorShown
	}
	// Already exploring — the top-N carries a cold technique on its own merits, so no
	// reservation is needed (and forcing one would only crowd out a second).
	for _, r := range primary {
		if underExplored(r) {
			return primary
		}
	}
	// The top-N are all well-explored: give the highest-ranked under-explored
	// candidate below the cut the last slot. Being unmeasured it sits at the
	// cold-start prior, so it was edged out by measured techniques, not by relevance.
	for _, r := range ranked[n:] {
		if underExplored(r) {
			out := append([]rankedTechnique(nil), primary...)
			out[n-1] = r
			return out
		}
	}
	return primary
}

func round3(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := float64(int(*v*1000+0.5)) / 1000
	return &r
}

// round3f is round3 for a plain float. It uses math.Round rather than round3's
// truncating +0.5 because a cosine similarity is genuinely signed — an
// off-topic technique scores below zero — and truncation toward zero rounds
// negatives the wrong way.
func round3f(v float64) float64 { return math.Round(v*1000) / 1000 }
