// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package feedback

import (
	"errors"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

func event(t *testing.T, capID, stage string, seg map[string]any) models.FeedbackEvent {
	t.Helper()
	body := map[string]any{"technique_id": capID, "stage": stage}
	if seg != nil {
		body["segment"] = seg
	}
	e, err := models.ParseFeedbackEvent(body)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRollupFansOutPerSegmentDimension(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	seg := map[string]any{"team": "revops", "role": "analyst"}
	for i := 0; i < 4; i++ {
		_, _ = Ingest(st, event(t, "technique", "shown", seg))
	}
	for i := 0; i < 2; i++ {
		_, _ = Ingest(st, event(t, "technique", "adopted", seg))
	}
	_, _ = Ingest(st, event(t, "technique", "helped", seg))

	n, err := RecomputeOutcomes(st)
	if err != nil {
		t.Fatal(err)
	}
	// one row per segment dimension value + overall = 3
	if n != 3 {
		t.Fatalf("rollup rows = %d (want 3)", n)
	}
	for _, key := range []string{"__overall__", "team:revops", "role:analyst"} {
		o, ok, err := st.GetOutcome("technique", key)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("missing rollup for %s", key)
		}
		if o.Shown != 4 || o.Adopted != 2 || o.Helped != 1 {
			t.Fatalf("%s counts wrong: %+v", key, o)
		}
		if *o.AdoptionRate != 0.5 || *o.HelpedRate != 0.5 || o.SampleSize != 2 {
			t.Fatalf("%s rates wrong: %+v", key, o)
		}
	}
}

func TestInferredVerdictsRollUpAtReducedWeight(t *testing.T) {
	// The guardrail docs/delivery/low-intrusion-plan.md promised: inferred
	// member verdicts must not drive rates at the same weight as explicit
	// ones. Two adoptions (one explicit, one inferred) and one inferred helped:
	// raw counts stay honest, rates read the weighted funnel.
	st, _ := store.Open(t.TempDir())
	withConf := func(stage, conf string) models.FeedbackEvent {
		e, err := models.ParseFeedbackEvent(map[string]any{
			"technique_id": "technique", "stage": stage, "confidence": conf})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	_, _ = Ingest(st, withConf("shown", "inferred")) // shown is never scaled
	_, _ = Ingest(st, withConf("shown", "inferred"))
	_, _ = Ingest(st, withConf("adopted", "explicit"))
	_, _ = Ingest(st, withConf("adopted", "inferred"))
	_, _ = Ingest(st, withConf("helped", "inferred"))

	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	o, ok, err := st.GetOutcome("technique", "__overall__")
	if err != nil || !ok {
		t.Fatalf("rollup missing: %v", err)
	}
	if o.Shown != 2 || o.Adopted != 2 || o.Helped != 1 {
		t.Fatalf("raw counts must stay raw: %+v", o)
	}
	wantAdopted := 1 + config.InferredWeight
	if o.WeightedAdopted != wantAdopted || o.WeightedHelped != config.InferredWeight {
		t.Fatalf("weighted funnel: adopted=%v helped=%v", o.WeightedAdopted, o.WeightedHelped)
	}
	// adoption_rate = (1 + w)/2 shown; helped_rate = w/(1+w) — both below the
	// unweighted 1.0 and 0.5 they'd read at full weight.
	if *o.AdoptionRate != wantAdopted/2 {
		t.Fatalf("adoption rate = %v", *o.AdoptionRate)
	}
	if want := config.InferredWeight / wantAdopted; *o.HelpedRate != want {
		t.Fatalf("helped rate = %v (want %v)", *o.HelpedRate, want)
	}
	// An all-explicit history is untouched by the weighting.
	if o.SampleSize != int(wantAdopted) {
		t.Fatalf("sample size = %d (want floor(weighted adopted) = %d)", o.SampleSize, int(wantAdopted))
	}
}

func TestIngestIdempotentOnReplay(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	e := event(t, "technique", "shown", nil)
	id, err := Ingest(st, e)
	if err != nil || id != e.EventID {
		t.Fatalf("first ingest: %q %v", id, err)
	}
	id2, err := Ingest(st, e)
	if err != nil || id2 != "" {
		t.Fatalf("replay not idempotent: %q %v", id2, err)
	}
	if events, _ := st.AllEvents(""); len(events) != 1 {
		t.Fatal("replay stored")
	}
}

// The funnel's denominator is defended at the write gate, not by trusting the
// client: a `shown` event whose audit the registry recorded as a retrieval PULL
// (surface=mcp) is rejected, because an agent browsing the playbook shows a
// member nothing. A stale or third-party producer cannot inflate `shown`.
func TestIngestRejectsShownForARetrievalPull(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_pull", CreatedAt: models.Now(), Surface: "mcp"})
	must(err)
	_, err = st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_push", CreatedAt: models.Now(), Surface: "claude-code"})
	must(err)

	pull := event(t, "technique", "shown", nil)
	pull.AuditID = "aud_pull"
	if _, err := Ingest(st, pull); !errors.Is(err, ErrRetrievalNotExposure) {
		t.Fatalf("a pull's shown event was not rejected: %v", err)
	}
	if events, _ := st.AllEvents(""); len(events) != 0 {
		t.Fatalf("the rejected event was stored anyway: %+v", events)
	}

	// Everything else still lands. A member-facing surface is the whole point.
	push := event(t, "technique", "shown", nil)
	push.AuditID = "aud_push"
	if id, err := Ingest(st, push); err != nil || id == "" {
		t.Fatalf("a real delivery was rejected: %q %v", id, err)
	}
	// An unknown audit is accepted: no fact is not evidence of a pull.
	orphan := event(t, "technique", "shown", nil)
	orphan.AuditID = "aud_never_recorded"
	if id, err := Ingest(st, orphan); err != nil || id == "" {
		t.Fatalf("an event for an unrecorded audit was rejected: %q %v", id, err)
	}
	// The guard is scoped to `shown`. A pull that later measurably helped the
	// member still records the outcome — only exposure is in question.
	adopted := event(t, "technique", "adopted", nil)
	adopted.AuditID = "aud_pull"
	if id, err := Ingest(st, adopted); err != nil || id == "" {
		t.Fatalf("adopted after a pull was rejected: %q %v", id, err)
	}
	if events, _ := st.AllEvents(""); len(events) != 3 {
		t.Fatalf("stored %d events, want 3", len(events))
	}
}

func TestDetectDecayFlagsCollapse(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	now := models.Now()
	technique := models.Technique{ID: "c", Name: "C", Description: "d", Scope: "general",
		Status: "stable", Provenance: "curated", Version: 1, Recipe: "r",
		CreatedAt: now, UpdatedAt: now}
	_ = st.UpsertTechnique(technique)

	// Baseline: excellent long-run helped_rate (injected directly as rollup);
	// recent window: 25 adoptions, 0 helped -> sharp drop.
	for i := 0; i < 25; i++ {
		_, _ = Ingest(st, event(t, "c", "adopted", nil))
	}
	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	hr := 0.9
	st.ReplaceOutcomes([]models.Outcome{{
		TechniqueID: "c", SegmentKey: "__overall__", Adopted: 100, Helped: 90,
		HelpedRate: &hr, SampleSize: 100, LastUpdated: now,
	}})
	flagged, err := DetectDecay(st)
	if err != nil {
		t.Fatal(err)
	}
	if flagged != 1 {
		t.Fatalf("flagged = %d (want 1)", flagged)
	}
	got, _, _ := st.GetTechnique("c")
	if got.DecaySignal != 1 || got.Status != "decayed" {
		t.Fatalf("technique not decayed: %+v", got)
	}
	if cands, _ := st.CandidateTechniques(); len(cands) != 0 {
		t.Fatal("decayed technique still retrievable")
	}
}

func TestRetireStaleShadowRetiresOnlyWellJudgedPoorFits(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	now := models.Now()
	shadowTechnique := func(id string) models.Technique {
		return models.Technique{ID: id, Name: id, Scope: "general", Status: "shadow",
			Provenance: "suggested", Version: 1, Recipe: "r", CreatedAt: now, UpdatedAt: now}
	}
	for _, id := range []string{"good", "bad", "young"} {
		if err := st.UpsertTechnique(shadowTechnique(id)); err != nil {
			t.Fatal(err)
		}
	}
	// good: fits often (8/9) — stays in shadow for the operator.
	for i := 0; i < 8; i++ {
		_, _ = Ingest(st, event(t, "good", "shadow_shown", nil))
	}
	_, _ = Ingest(st, event(t, "good", "shadow_declined", nil))
	// bad: 10 verdicts, fits once (0.1 <= floor) — auto-retires.
	_, _ = Ingest(st, event(t, "bad", "shadow_shown", nil))
	for i := 0; i < 9; i++ {
		_, _ = Ingest(st, event(t, "bad", "shadow_declined", nil))
	}
	// young: poor fit but only 3 verdicts (< min sample) — left alone.
	_, _ = Ingest(st, event(t, "young", "shadow_shown", nil))
	for i := 0; i < 2; i++ {
		_, _ = Ingest(st, event(t, "young", "shadow_declined", nil))
	}

	n, err := RetireStaleShadow(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("retired = %d (want 1: only 'bad')", n)
	}
	want := map[string]string{"good": "shadow", "bad": "retired", "young": "shadow"}
	for id, status := range want {
		got, _, _ := st.GetTechnique(id)
		if got.Status != status {
			t.Fatalf("%s status = %q (want %q)", id, got.Status, status)
		}
	}
	// The shadow stages never created a serving rollup for these techniques.
	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	if o, ok, _ := st.GetOutcome("bad", config.OverallKey); ok && (o.Shown > 0 || o.Adopted > 0) {
		t.Fatalf("shadow stages leaked into rollup: %+v", o)
	}
}

func TestAutoPromoteShadowGraduatesOnEvidence(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	now := models.Now()
	shadowTechnique := func(id string) models.Technique {
		return models.Technique{ID: id, Name: id, Scope: "general", Status: "shadow",
			Provenance: "suggested", Version: 1, Recipe: "r", CreatedAt: now, UpdatedAt: now}
	}
	for _, id := range []string{"strong", "weak", "thin"} {
		if err := st.UpsertTechnique(shadowTechnique(id)); err != nil {
			t.Fatal(err)
		}
		if err := st.SetTechniqueEmbedding(id, []float32{1}, "hashing-v1", 1); err != nil {
			t.Fatal(err)
		}
	}
	// strong: 12 verdicts, fits 10 (0.83 >= 0.6) -> graduates.
	for i := 0; i < 10; i++ {
		_, _ = Ingest(st, event(t, "strong", "shadow_shown", nil))
	}
	for i := 0; i < 2; i++ {
		_, _ = Ingest(st, event(t, "strong", "shadow_declined", nil))
	}
	// weak: 12 verdicts, fits 5 (0.42 < 0.6) -> stays in shadow.
	for i := 0; i < 5; i++ {
		_, _ = Ingest(st, event(t, "weak", "shadow_shown", nil))
	}
	for i := 0; i < 7; i++ {
		_, _ = Ingest(st, event(t, "weak", "shadow_declined", nil))
	}
	// thin: fits often but only 4 verdicts (< MinJudged) -> not yet.
	for i := 0; i < 4; i++ {
		_, _ = Ingest(st, event(t, "thin", "shadow_shown", nil))
	}

	gate := AutoPromoteGate{Enabled: true, MinFit: 0.6, MinJudged: 12}
	n, err := AutoPromoteShadow(st, gate)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("promoted = %d (want 1: only 'strong')", n)
	}
	want := map[string]string{"strong": "stable", "weak": "shadow", "thin": "shadow"}
	for id, status := range want {
		got, _, _ := st.GetTechnique(id)
		if got.Status != status {
			t.Fatalf("%s status = %q (want %q)", id, got.Status, status)
		}
	}
	// A disabled gate graduates nothing, however strong the evidence.
	got, err := AutoPromoteShadow(st, AutoPromoteGate{})
	if err != nil || got != 0 {
		t.Fatalf("disabled gate promoted %d (err %v)", got, err)
	}
	// The graduated technique now serves — retrieval eligibility follows status.
	cands, _ := st.CandidateTechniques()
	served := false
	for _, c := range cands {
		if c.ID == "strong" {
			served = true
		}
	}
	if !served {
		t.Fatal("graduated technique is not a retrieval candidate")
	}
}

// TestAuditLinkedOutcomesIngestOnce replays the double-count that motivated
// deterministic event ids: a standalone 'adopted' followed by the
// adopted+helped pair a 'helped' verdict implies, all against one audit.
// The overlapping 'adopted' must dedupe; without an audit id, repeated
// submissions stay separate observations.
func TestAuditLinkedOutcomesIngestOnce(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	ev := func(stage string) models.FeedbackEvent {
		e, err := models.ParseFeedbackEvent(map[string]any{
			"technique_id": "technique", "stage": stage, "audit_id": "aud_x"})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	ins := 0
	for _, e := range []models.FeedbackEvent{ev("adopted"), ev("adopted"), ev("helped")} {
		if id, err := Ingest(st, e); err != nil {
			t.Fatal(err)
		} else if id != "" {
			ins++
		}
	}
	if ins != 2 {
		t.Fatalf("audit-linked ingest accepted %d events (want 2: adopted deduped)", ins)
	}
	events, _ := st.AllEvents("")
	if len(events) != 2 {
		t.Fatalf("log holds %d events (want 2)", len(events))
	}

	// no audit id -> random ids -> both land
	free := 0
	for i := 0; i < 2; i++ {
		e := event(t, "technique2", "adopted", nil)
		if id, _ := Ingest(st, e); id != "" {
			free++
		}
	}
	if free != 2 {
		t.Fatalf("audit-less events deduped (%d accepted, want 2) — they are separate observations", free)
	}
}

// The model is a cohort like any other: the rollup fans out by it, so a member
// running two models can see the funnel for each rather than one number that
// averages a model they abandoned with the one they use.
func TestRollupFansOutByModel(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	sonnet := map[string]any{"model": "anthropic/claude-sonnet-5"}
	gpt := map[string]any{"model": "openai/gpt-4o"}
	for i := 0; i < 4; i++ {
		_, _ = Ingest(st, event(t, "technique", "shown", sonnet))
	}
	for i := 0; i < 3; i++ {
		_, _ = Ingest(st, event(t, "technique", "adopted", sonnet))
	}
	for i := 0; i < 4; i++ {
		_, _ = Ingest(st, event(t, "technique", "shown", gpt))
	}
	_, _ = Ingest(st, event(t, "technique", "adopted", gpt))

	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	for key, wantAdopted := range map[string]int{
		"model:anthropic/claude-sonnet-5": 3,
		"model:openai/gpt-4o":             1,
		config.OverallKey:                 4,
	} {
		o, ok, err := st.GetOutcome("technique", key)
		if err != nil || !ok {
			t.Fatalf("no rollup for %q: ok=%v err=%v", key, ok, err)
		}
		if o.Adopted != wantAdopted {
			t.Errorf("%s: adopted=%d, want %d", key, o.Adopted, wantAdopted)
		}
	}
}

// A producer that sends no model still lands in the right cohort: the audit
// fact recorded which model served the interaction, and ingest joins on
// audit_id to put it on the segment. Without this, every event from a build
// older than the dimension is invisible to it.
func TestIngestStampsTheModelFromTheAuditFact(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	if _, err := st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_1", CreatedAt: models.Now(), Surface: "claude-code",
		Model: "claude-sonnet-5-20260101"}); err != nil {
		t.Fatal(err)
	}
	e := event(t, "technique", "shown", map[string]any{"team": "revops"})
	e.AuditID = "aud_1"
	if _, err := Ingest(st, e); err != nil {
		t.Fatal(err)
	}
	stored, _ := st.AllEvents("")
	if len(stored) != 1 {
		t.Fatalf("stored %d events, want 1", len(stored))
	}
	// The canonical key, not the dated label: the cohort has to survive the
	// next release of the same model.
	if got := stored[0].Segment["model"]; got != "anthropic/claude-sonnet-5" {
		t.Fatalf("segment model = %q, want anthropic/claude-sonnet-5", got)
	}
	if got := stored[0].Segment["team"]; got != "revops" {
		t.Fatalf("the stamp clobbered the rest of the segment: %+v", stored[0].Segment)
	}
}

// A segment that already names a model wins. The producer was in the session;
// the fact log is a record of it.
func TestIngestDoesNotOverwriteAModelTheProducerSent(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	if _, err := st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_1", CreatedAt: models.Now(), Model: "gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	e := event(t, "technique", "shown", map[string]any{"model": "anthropic/claude-sonnet-5"})
	e.AuditID = "aud_1"
	if _, err := Ingest(st, e); err != nil {
		t.Fatal(err)
	}
	stored, _ := st.AllEvents("")
	if got := stored[0].Segment["model"]; got != "anthropic/claude-sonnet-5" {
		t.Fatalf("segment model = %q, want the producer's own value", got)
	}
}

// History is not lost. Events written before the dimension existed carry the
// model in the fact log and nowhere else; the recompute joins them back, so a
// registry that upgrades sees the cohort it always had rather than one that
// appears to start today.
func TestRecomputeBackfillsTheModelCohortFromHistory(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	if _, err := st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_old", CreatedAt: models.Now(), Model: "claude-sonnet-5"}); err != nil {
		t.Fatal(err)
	}
	// Write straight to the store, as an older build did: no model on the
	// segment, only the audit id that points at the fact.
	for i := 0; i < 3; i++ {
		e := event(t, "technique", "shown", nil)
		e.AuditID = "aud_old"
		if _, err := st.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	e := event(t, "technique", "adopted", nil)
	e.AuditID = "aud_old"
	if _, err := st.InsertEvent(e); err != nil {
		t.Fatal(err)
	}

	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	o, ok, err := st.GetOutcome("technique", "model:anthropic/claude-sonnet-5")
	if err != nil || !ok {
		t.Fatalf("history not recovered: ok=%v err=%v", ok, err)
	}
	if o.Shown != 3 || o.Adopted != 1 {
		t.Fatalf("recovered rollup = shown %d adopted %d, want 3 and 1", o.Shown, o.Adopted)
	}
	// The log itself is append-only and must be exactly as it was.
	stored, _ := st.AllEvents("")
	for _, ev := range stored {
		if ev.Segment["model"] != "" {
			t.Fatalf("the recompute rewrote the event log: %+v", ev.Segment)
		}
	}
	// And it is idempotent: a second pass changes nothing.
	before, _, _ := st.GetOutcome("technique", "model:anthropic/claude-sonnet-5")
	if _, err := RecomputeOutcomes(st); err != nil {
		t.Fatal(err)
	}
	after, _, _ := st.GetOutcome("technique", "model:anthropic/claude-sonnet-5")
	if before.Shown != after.Shown || before.Adopted != after.Adopted {
		t.Fatalf("second recompute moved the rollup: %+v -> %+v", before, after)
	}
}
