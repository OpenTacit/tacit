// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package retrieval

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

type invalidEmbedder struct{ vector embed.Vector }

func (invalidEmbedder) ModelID() string { return "invalid" }
func (invalidEmbedder) Dim() int        { return 3 }
func (e invalidEmbedder) Embed([]string) []embed.Vector {
	return []embed.Vector{e.vector}
}

// kafka is the concierge fixture: two generic explain questions about Kafka
// offsets, no diagram requested.
func kafka() models.Characterization {
	return models.Characterization{
		SummaryText: "User asked two generic explain questions about Kafka offsets and " +
			"__consumer_offsets and received long textbook explanations. The topic " +
			"is structural and visual; no diagram was requested.",
		TaskType: "concept-explanation", Domain: "data-engineering",
		Modalities: []string{"text"}, ToolsAbsent: []string{"diagram"},
		Harness: "chatgpt", Surface: "web", SkillLevel: "intermediate",
		Segment: models.Segment{"role": "developer", "domain": "data-engineering",
			"harness": "chatgpt", "surface": "web"},
	}
}

func seeded(t *testing.T) (*store.Store, embed.Embedder) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	embedder, _ := embed.New("hashing-v1", 256)
	techniquesDir, _ := filepath.Abs("../../../techniques")
	if _, _, err := jobs.Startup(st, techniquesDir, embedder); err != nil {
		t.Fatal(err)
	}
	return st, embedder
}

func TestShrinkage(t *testing.T) {
	if Shrink(1, 1, 0) >= 0.6 {
		t.Fatalf("1/1 not regressed toward prior: %f", Shrink(1, 1, 0))
	}
	if Shrink(90, 100, 0) <= 0.8 {
		t.Fatalf("well-measured shrunk too far: %f", Shrink(90, 100, 0))
	}
	// Dismissals drag the score DOWN, not toward the prior: being told
	// "not relevant" fifty times is evidence, and it now counts as such.
	if got := Shrink(0, 0, 50); got >= config.Prior {
		t.Fatalf("50 dismissals score %f — not below the unmeasured prior %f", got, config.Prior)
	}
	if Shrink(5, 10, 10) >= Shrink(5, 10, 0) {
		t.Fatal("dismissals did not lower an otherwise-identical funnel's score")
	}
}

// A technique colleagues keep rejecting must rank BELOW an unmeasured technique of equal
// similarity — rejection is the one signal members give deliberately, and it
// used to feed nothing.
func TestDismissalsReorderRanking(t *testing.T) {
	st, embedder := seeded(t)
	// Rejection-only funnel: below the adoption floor, above the dismissal floor.
	if err := st.ReplaceOutcomes([]models.Outcome{{
		TechniqueID: "use-internal-data-connector", SegmentKey: "__overall__",
		Shown: 20, Adopted: 0, Helped: 0, Dismissed: 12,
		SampleSize: 0, LastUpdated: "2026-07-12T00:00:00Z",
	}}); err != nil {
		t.Fatal(err)
	}
	block, err := BuildEvidence(st, models.Characterization{
		SummaryText: "user pasted csv rows from the internal warehouse and asked for analysis",
	}, embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Candidates) == 0 {
		t.Fatal("no candidates")
	}
	// The dismissal-heavy technique must not be ranked first despite being the most
	// similar technique in the store for this query.
	if block.Candidates[0].TechniqueID == "use-internal-data-connector" {
		t.Fatal("a 0/12 rejected technique still outranks everything — dismissals reached nothing")
	}
}

func TestSupportsRecallFirst(t *testing.T) {
	technique := models.Technique{SupportMatrix: []map[string]any{
		{"harness": "claude-code", "surface": "cli", "supported": true, "verified": "2026-05"},
		{"harness": "old-tool", "supported": false},
	}}
	if ok, note := Supports(technique, "claude-code", "cli", ""); !ok || note == "" {
		t.Fatalf("positive match failed: %v %q", ok, note)
	}
	if ok, _ := Supports(technique, "old-tool", "", ""); ok {
		t.Fatal("explicit negative not excluded")
	}
	if ok, note := Supports(technique, "some-other-harness", "", ""); !ok || note != "" {
		t.Fatalf("unknown harness dropped (recall-first violated): %v %q", ok, note)
	}
	if ok, _ := Supports(models.Technique{}, "anything", "", ""); !ok {
		t.Fatal("empty matrix dropped")
	}
}

// A technique that leans on a behaviour one model has and another does not is
// excluded on the model alone, with no harness in the row — and the exclusion
// survives the model arriving under a different label, because rows and
// sessions are compared on the canonical key (internal/modelid).
func TestSupportsOnModel(t *testing.T) {
	technique := models.Technique{SupportMatrix: []map[string]any{
		{"model": "anthropic/claude-sonnet-5", "supported": true, "verified": "2026-09"},
		{"model": "gpt-4o-mini", "supported": false},
	}}
	if ok, note := Supports(technique, "claude-code", "cli", "claude-sonnet-5-20260101"); !ok || note == "" {
		t.Fatalf("positive model row failed: %v %q", ok, note)
	}
	if ok, _ := Supports(technique, "claude-code", "cli", "openai/gpt-4o-mini"); ok {
		t.Fatal("explicit model negative not excluded")
	}
	if ok, note := Supports(technique, "claude-code", "cli", "some-unknown-model"); !ok || note != "" {
		t.Fatalf("unknown model dropped (recall-first violated): %v %q", ok, note)
	}
	// A negative for one model must not exclude a sibling size.
	if ok, _ := Supports(technique, "claude-code", "cli", "gpt-4o"); !ok {
		t.Fatal("gpt-4o excluded by a gpt-4o-mini negative — sizes are different models")
	}
}

// A row naming both a harness and a model applies only where both hold, and its
// note says both, so the member can see what was actually verified.
func TestSupportsHarnessAndModelRow(t *testing.T) {
	technique := models.Technique{SupportMatrix: []map[string]any{
		{"harness": "claude-code", "model": "claude-sonnet-5", "supported": true, "verified": "2026-09"},
	}}
	ok, note := Supports(technique, "claude-code", "", "claude-sonnet-5")
	if !ok || note != "works in claude-code on anthropic/claude-sonnet-5 (verified 2026-09)" {
		t.Fatalf("both-row note wrong: %v %q", ok, note)
	}
	if ok, note := Supports(technique, "claude-code", "", "gpt-4o"); !ok || note != "" {
		t.Fatalf("row claimed support on a model it never named: %v %q", ok, note)
	}
}

func TestColdStartRanksBySimilarityAndIsThin(t *testing.T) {
	st, embedder := seeded(t)
	ev, err := BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Meta.Thin {
		t.Fatal("cold start not flagged thin")
	}
	if len(ev.Candidates) == 0 {
		t.Fatal("cold start returned no candidates (graceful degradation broken)")
	}
	for _, c := range ev.Candidates {
		if c.Outcomes != nil {
			t.Fatalf("cold-start candidate carries outcomes: %+v", c)
		}
	}
}

func TestInvalidQueryEmbeddingReturnsNoCandidates(t *testing.T) {
	st, _ := seeded(t)
	for _, vector := range []embed.Vector{{0, 0, 0}, {float32(math.NaN()), 0, 0}, {float32(math.Inf(1)), 0, 0}} {
		ev, err := BuildEvidence(st, kafka(), invalidEmbedder{vector}, AutonomyGate{})
		if err != nil {
			t.Fatal(err)
		}
		if len(ev.Candidates) != 0 || len(ev.ShadowCandidates) != 0 || !ev.Meta.Thin {
			t.Fatalf("invalid query embedding must be a thin retrieval miss: %+v", ev)
		}
	}
}

func TestFeedbackShiftsRanking(t *testing.T) {
	st, embedder := seeded(t)
	seg := map[string]any{"role": "developer", "domain": "data-engineering",
		"harness": "chatgpt", "surface": "web"}
	// 50 developers adopted ask-for-a-diagram; 48 said it helped.
	for i := 0; i < 50; i++ {
		post(t, st, "ask-for-a-diagram", "shown", seg)
		post(t, st, "ask-for-a-diagram", "adopted", seg)
	}
	for i := 0; i < 48; i++ {
		post(t, st, "ask-for-a-diagram", "helped", seg)
	}
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}

	ev, err := BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Meta.Thin {
		t.Fatal("measured evidence still thin")
	}
	if ev.Candidates[0].TechniqueID != "ask-for-a-diagram" {
		t.Fatalf("top candidate = %s", ev.Candidates[0].TechniqueID)
	}
	top := ev.Candidates[0]
	if top.Outcomes == nil {
		t.Fatal("top candidate missing outcomes")
	}
	if top.Outcomes.Segment != "role:developer" {
		t.Fatalf("segment fallback picked %s", top.Outcomes.Segment)
	}
	if hr := *top.Outcomes.HelpedRate; hr < 0.959 || hr > 0.961 {
		t.Fatalf("helped_rate = %f (want 0.96)", hr)
	}
}

// TestLuckyOneOfOneDoesNotLeapfrogSimilarity pins the cold-start collapse: a
// single 1/1 measurement (Shrink(1,1)=0.524 > Prior=0.5) must NOT let a technique
// jump above more-relevant unmeasured techniques, because its sample is below
// MinRankSample. Once the sample reaches the floor, a genuinely well-measured
// technique is allowed to take the top slot. Without the floor, one stray adoption
// makes its technique the universal rank-1 for every query.
func TestLuckyOneOfOneDoesNotLeapfrogSimilarity(t *testing.T) {
	st, embedder := seeded(t)
	seg := map[string]any{"role": "developer", "domain": "data-engineering",
		"harness": "chatgpt", "surface": "web"}

	// Natural cold-start order: whatever technique is most similar to the Kafka query.
	cold, err := BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	coldTop := cold.Candidates[0].TechniqueID
	const booster = "tailor-the-ask" // in the candidate set, but not the similarity winner
	if coldTop == booster {
		t.Fatalf("test premise broken: %s is already the cold-start top", booster)
	}

	// One adoption + one helped for the booster: a lucky 1/1.
	post(t, st, booster, "shown", seg)
	post(t, st, booster, "adopted", seg)
	post(t, st, booster, "helped", seg)
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	ev, err := BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Candidates[0].TechniqueID != coldTop {
		t.Fatalf("a lucky 1/1 (%s) leapfrogged the similarity winner (%s) to rank 1",
			booster, coldTop)
	}

	// Grow the sample to the floor: now the measured impact IS trusted to reorder.
	for i := 0; i < config.MinRankSample; i++ {
		post(t, st, booster, "shown", seg)
		post(t, st, booster, "adopted", seg)
		post(t, st, booster, "helped", seg)
	}
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	ev, err = BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Candidates[0].TechniqueID != booster {
		t.Fatalf("a well-measured technique (%s, >= MinRankSample adoptions) did not reach rank 1; top=%s",
			booster, ev.Candidates[0].TechniqueID)
	}
}

func TestSegmentFallbackNeedsSample(t *testing.T) {
	st, embedder := seeded(t)
	seg := map[string]any{"role": "developer"}
	// only 5 adoptions in role:developer — below MinSample(30) — so the
	// ranking must fall back to __overall__.
	for i := 0; i < 5; i++ {
		post(t, st, "ask-for-a-diagram", "shown", seg)
		post(t, st, "ask-for-a-diagram", "adopted", seg)
		post(t, st, "ask-for-a-diagram", "helped", seg)
	}
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	ev, err := BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ev.Candidates {
		if c.TechniqueID == "ask-for-a-diagram" && c.Outcomes != nil {
			if c.Outcomes.Segment != config.OverallKey {
				t.Fatalf("thin segment trusted: %s", c.Outcomes.Segment)
			}
			return
		}
	}
}

func TestCohortComparison(t *testing.T) {
	st, embedder := seeded(t)
	seg := map[string]any{"team": "revops"}
	for i := 0; i < 3; i++ {
		post(t, st, "ask-for-a-diagram", "adopted", seg)
		post(t, st, "use-internal-data-connector", "adopted", seg)
	}
	if _, err := feedback.RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	ch := kafka()
	ch.Segment = models.Segment{"team": "revops"}
	ch.UsedTechniqueIDs = []string{"ask-for-a-diagram"}
	ev, err := BuildEvidence(st, ch, embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Cohort.Segment != "team:revops" {
		t.Fatalf("cohort segment = %s", ev.Cohort.Segment)
	}
	if ev.Cohort.CommonTotal != 2 || ev.Cohort.Uses != 1 {
		t.Fatalf("cohort = %+v", ev.Cohort)
	}
	for _, ex := range ev.Cohort.Examples {
		if ex == "Ask for a diagram" {
			t.Fatal("already-used technique offered as an example")
		}
	}
}

func post(t *testing.T, st *store.Store, capID, stage string, seg map[string]any) {
	t.Helper()
	e, err := models.ParseFeedbackEvent(map[string]any{
		"technique_id": capID, "stage": stage, "segment": seg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := feedback.Ingest(st, e); err != nil {
		t.Fatal(err)
	}
}

// Shadow techniques (status=shadow) are returned in ShadowCandidates for judge-only
// evaluation, capped at ShadowK, and must NEVER leak into the surfaced
// candidates — that zero-exposure guarantee is the whole point of the shadow
// rung (docs/learning/validation-without-review.md).
func TestShadowCandidatesNeverSurfaced(t *testing.T) {
	st, embedder := seeded(t)
	// More shadow techniques than ShadowK, so the cap is exercised. Text mirrors the
	// kafka() query so they score as plausible matches.
	shadow := map[string]string{
		"shadow-diagram-a": "Render a sequence diagram to explain Kafka consumer offsets visually",
		"shadow-diagram-b": "Draw an architecture diagram for the __consumer_offsets topic partitions",
		"shadow-diagram-c": "Visualize offset commits with a chart instead of textbook prose",
	}
	for id, text := range shadow {
		c := models.Technique{ID: id, Name: id, Scope: "general", Status: "shadow",
			Provenance: "suggested", Version: 1, Recipe: text,
			CreatedAt: models.Now(), UpdatedAt: models.Now()}
		if err := st.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
		if err := st.SetTechniqueEmbedding(id, embedder.Embed([]string{text})[0], "hashing-v1", 256); err != nil {
			t.Fatal(err)
		}
	}

	ev, err := BuildEvidence(st, kafka(), embedder, AutonomyGate{})
	if err != nil {
		t.Fatal(err)
	}

	if len(ev.ShadowCandidates) != config.ShadowK {
		t.Fatalf("shadow candidates = %d, want ShadowK=%d", len(ev.ShadowCandidates), config.ShadowK)
	}
	for _, c := range ev.Candidates {
		if _, isShadow := shadow[c.TechniqueID]; isShadow {
			t.Fatalf("shadow technique %q leaked into surfaced candidates", c.TechniqueID)
		}
	}
	for _, c := range ev.ShadowCandidates {
		if _, isShadow := shadow[c.TechniqueID]; !isShadow {
			t.Fatalf("non-shadow technique %q returned as shadow", c.TechniqueID)
		}
		if c.Outcomes != nil {
			t.Fatalf("shadow candidate %q carries outcomes (should have no funnel)", c.TechniqueID)
		}
	}
}

func TestExplorationFloorReservesASlotForColdTechniques(t *testing.T) {
	measured := func(id string, shown int) rankedTechnique {
		return rankedTechnique{technique: models.Technique{ID: id}, outcome: &models.Outcome{Shown: shown}}
	}
	cold := func(id string) rankedTechnique { return rankedTechnique{technique: models.Technique{ID: id}} }
	lastID := func(rs []rankedTechnique) string { return rs[len(rs)-1].technique.ID }

	// Top-N all well-explored, a cold technique just below the cut: it takes the last
	// slot, displacing the lowest measured technique; the higher slots are untouched.
	ranked := []rankedTechnique{
		measured("m1", 50), measured("m2", 40), measured("m3", 30), measured("m4", 20),
		cold("cold"), measured("m6", 100),
	}
	got := selectWithExploration(ranked)
	if len(got) != config.EvidenceN {
		t.Fatalf("len=%d want %d", len(got), config.EvidenceN)
	}
	if lastID(got) != "cold" {
		t.Fatalf("cold technique not reserved the last slot: got %q", lastID(got))
	}
	if got[0].technique.ID != "m1" || got[1].technique.ID != "m2" {
		t.Fatal("primary picks were disturbed")
	}

	// The top-N already carries a cold technique — no reservation, so a second cold
	// technique below the cut is not forced in.
	ranked2 := []rankedTechnique{
		measured("m1", 50), measured("m2", 40), cold("c0"), measured("m4", 20), cold("cold2"),
	}
	for _, r := range selectWithExploration(ranked2) {
		if r.technique.ID == "cold2" {
			t.Fatal("reserved a slot despite the top-N already exploring")
		}
	}

	// Small corpus (<= EvidenceN): returned whole, reservation logic never runs.
	small := []rankedTechnique{measured("a", 5), measured("b", 6)}
	if len(selectWithExploration(small)) != 2 {
		t.Fatal("small corpus altered")
	}
}

func TestAutonomyGateEligibility(t *testing.T) {
	rate := func(v float64) *float64 { return &v }
	stable := models.Technique{Status: "stable"}
	gate := AutonomyGate{Enabled: true, MinHelpedRate: 0.8, MinN: 20}
	for i, tc := range []struct {
		gate      AutonomyGate
		technique models.Technique
		o         *models.OutcomeSummary
		want      bool
	}{
		{gate, stable, &models.OutcomeSummary{HelpedRate: rate(0.9), SampleSize: 25}, true},
		{gate, stable, &models.OutcomeSummary{HelpedRate: rate(0.7), SampleSize: 25}, false}, // rate below bar
		{gate, stable, &models.OutcomeSummary{HelpedRate: rate(0.9), SampleSize: 5}, false},  // n below bar
		{gate, stable, nil, false}, // unmeasured
		{gate, models.Technique{Status: "draft"}, &models.OutcomeSummary{HelpedRate: rate(1), SampleSize: 100}, false},
		{AutonomyGate{}, stable, &models.OutcomeSummary{HelpedRate: rate(1), SampleSize: 100}, false}, // gate off
	} {
		if got := tc.gate.eligible(tc.technique, tc.o); got != tc.want {
			t.Errorf("case %d: eligible = %v, want %v", i, got, tc.want)
		}
	}
}

// Project scope is the one place recall-first does not hold, and the tests say
// why: a member who wrote `project: tacit` on a technique was stating where it
// applies, not offering a hint.
func TestInProjectScopesToNamedProjects(t *testing.T) {
	scoped := models.Technique{SupportMatrix: []map[string]any{
		{"project": "tacit", "supported": true},
		{"project": "omnigent", "supported": true},
	}}
	if !InProject(scoped, "tacit") {
		t.Fatal("excluded from a project it names")
	}
	if InProject(scoped, "some-docs-repo") {
		t.Fatal("served in a project it does not name")
	}
	// A session that never said where it was excludes nothing: absence of a
	// project is not evidence of being somewhere else.
	if !InProject(scoped, "") {
		t.Fatal("a session with no project was excluded")
	}
	// A technique naming no project at all is a technique for every project.
	unscoped := models.Technique{SupportMatrix: []map[string]any{
		{"harness": "claude-code", "supported": true},
	}}
	if !InProject(unscoped, "anything") {
		t.Fatal("an unscoped technique was excluded")
	}
	if !InProject(models.Technique{}, "anything") {
		t.Fatal("an empty matrix excluded")
	}
}

// An explicit negative excludes there and nowhere else — it must not turn an
// otherwise unscoped technique into one that serves only where it is named.
func TestInProjectNegativeDoesNotScope(t *testing.T) {
	c := models.Technique{SupportMatrix: []map[string]any{
		{"project": "legacy-monolith", "supported": false},
	}}
	if InProject(c, "legacy-monolith") {
		t.Fatal("an explicit negative did not exclude")
	}
	if !InProject(c, "tacit") {
		t.Fatal("a negative row scoped the technique to nowhere")
	}
}

// The project comes from what the turn demonstrably touched — a repository
// basename, never a path.
func TestProjectOfReadsTheRepoResource(t *testing.T) {
	ch := models.Characterization{InternalResourcesInPlay: []string{
		"mcp:warehouse", "repo:tacit"}}
	if got := ProjectOf(ch); got != "tacit" {
		t.Fatalf("ProjectOf = %q, want tacit", got)
	}
	if got := ProjectOf(models.Characterization{}); got != "" {
		t.Fatalf("ProjectOf on a bare characterization = %q", got)
	}
}
