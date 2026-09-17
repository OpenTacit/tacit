// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package conformance is the behavior suite every storage.Store backend must
// pass (docs/design/postgres-plan.md). The file store runs it unconditionally; the
// Postgres backend runs it when a test database is configured. One suite, N
// backends — this is what keeps "engine swap, not redesign" true.
package conformance

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/contracts"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) storage.Store

// Run executes the full behavioral contract against the backend.
func Run(t *testing.T, open Factory) {
	t.Run("UpsertPreservesRuntimeState", func(t *testing.T) { upsertPreserves(t, open(t)) })
	t.Run("UpsertNormalizesTags", func(t *testing.T) { upsertNormalizesTags(t, open(t)) })
	t.Run("ScopeDefaultsToGeneral", func(t *testing.T) { scopeDefaults(t, open(t)) })
	t.Run("DeleteTechniqueRemovesTechniqueAndRollups", func(t *testing.T) { deleteTechnique(t, open(t)) })
	t.Run("DeleteTechniqueKeepsEvents", func(t *testing.T) { deleteKeepsEvents(t, open(t)) })
	t.Run("ChannelsPreservedAcrossSync", func(t *testing.T) { channelsPreserved(t, open(t)) })
	t.Run("OriginPreservedAcrossSync", func(t *testing.T) { originPreserved(t, open(t)) })
	t.Run("UnknownTechniqueWritesError", func(t *testing.T) { unknownTechniqueWrites(t, open(t)) })
	t.Run("InsertEventIdempotent", func(t *testing.T) { insertIdempotent(t, open(t)) })
	t.Run("AllEventsSinceFilter", func(t *testing.T) { eventsSince(t, open(t)) })
	t.Run("LogReadsAreTimeOrdered", func(t *testing.T) { logsTimeOrdered(t, open(t)) })
	t.Run("LifecycleEventIdempotent", func(t *testing.T) { lifecycleIdempotent(t, open(t)) })
	t.Run("LifecycleEventsSinceFilter", func(t *testing.T) { lifecycleSince(t, open(t)) })
	t.Run("CandidateTechniquesFilter", func(t *testing.T) { candidateFilter(t, open(t)) })
	t.Run("ShadowCandidateTechniquesFilter", func(t *testing.T) { shadowCandidateFilter(t, open(t)) })
	t.Run("SetDecayLeavesDraftStatusAlone", func(t *testing.T) { decayDraft(t, open(t)) })
	t.Run("ListTechniquesStatusAndLimit", func(t *testing.T) { listTechniques(t, open(t)) })
	t.Run("TechniquesNeedingEmbedding", func(t *testing.T) { needingEmbedding(t, open(t)) })
	t.Run("EmbeddingClearedWhenEmbeddedTextChanges", func(t *testing.T) { embeddingFollowsText(t, open(t)) })
	t.Run("OutcomesReplaceAndQuery", func(t *testing.T) { outcomes(t, open(t)) })
	t.Run("SketchInsertIdempotentAndQuery", func(t *testing.T) { sketches(t, open(t)) })
	t.Run("Counts", func(t *testing.T) { countsCheck(t, open(t)) })
	t.Run("CountsAllKeys", func(t *testing.T) { countsAllKeys(t, open(t)) })
	t.Run("TechniqueVersionArchive", func(t *testing.T) { techniqueVersions(t, open(t)) })
	t.Run("AuditFactIdempotent", func(t *testing.T) { auditFactIdempotent(t, open(t)) })
	t.Run("AuditFactsSinceFilter", func(t *testing.T) { auditFactsSince(t, open(t)) })
	t.Run("AuditFactRoundTrip", func(t *testing.T) { auditFactRoundTrip(t, open(t)) })
	t.Run("AuditFactEnrichment", func(t *testing.T) { auditFactEnrich(t, open(t)) })
	t.Run("EnrichEmptyIsNoop", func(t *testing.T) { enrichEmptyNoop(t, open(t)) })
	t.Run("AuditFactByID", func(t *testing.T) { auditFactByID(t, open(t)) })
	t.Run("MemberKeyLifecycle", func(t *testing.T) { memberKeys(t, open(t)) })
	t.Run("FeedTokenLifecycle", func(t *testing.T) { feedTokens(t, open(t)) })
	t.Run("FeedTokenReinstate", func(t *testing.T) { feedTokenReinstate(t, open(t)) })
}

// memberKeys: mint -> resolve by hash -> revoke -> reinstate -> touch. The
// store returns revoked keys from MemberKeyByHash (the auth layer decides),
// lists newest-first, and Touch/SetRevoked on an unknown id must not error.
func memberKeys(t *testing.T, st storage.Store) {
	a := models.MemberKey{ID: "k-a", Label: "alice-laptop", Hash: "hash-a", CreatedAt: "2026-07-01T00:00:00Z"}
	b := models.MemberKey{ID: "k-b", Label: "bob-desktop", Hash: "hash-b", CreatedAt: "2026-07-02T00:00:00Z"}
	for _, k := range []models.MemberKey{a, b} {
		if err := st.InsertMemberKey(k); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := st.MemberKeyByHash("hash-a")
	if err != nil || !ok || got.Label != "alice-laptop" {
		t.Fatalf("by hash: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := st.MemberKeyByHash("no-such"); ok {
		t.Fatal("unknown hash resolved")
	}
	if err := st.SetMemberKeyRevoked("k-a", "2026-07-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got, ok, _ = st.MemberKeyByHash("hash-a")
	if !ok || got.RevokedAt == "" {
		t.Fatalf("revoked key must still resolve, stamped: %+v ok=%v", got, ok)
	}
	if err := st.SetMemberKeyRevoked("k-a", ""); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = st.MemberKeyByHash("hash-a"); got.RevokedAt != "" {
		t.Fatalf("reinstate did not clear revocation: %+v", got)
	}
	if err := st.TouchMemberKey("k-b", "2026-07-04T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListMemberKeys()
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v err=%v", list, err)
	}
	if list[0].ID != "k-b" || list[0].LastSeen != "2026-07-04T00:00:00Z" {
		t.Fatalf("want newest-first with touch recorded, got %+v", list)
	}
	if err := st.TouchMemberKey("ghost", "2026-07-04T00:00:00Z"); err != nil {
		t.Fatalf("touch unknown id: %v", err)
	}
	if err := st.SetMemberKeyRevoked("ghost", "x"); err != nil {
		t.Fatalf("revoke unknown id: %v", err)
	}
	if err := st.DeleteMemberKey("k-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.MemberKeyByHash("hash-a"); ok {
		t.Fatal("deleted key still resolves")
	}
	if list, _ := st.ListMemberKeys(); len(list) != 1 {
		t.Fatalf("list after delete: %+v", list)
	}
	if err := st.DeleteMemberKey("ghost"); err != nil {
		t.Fatalf("delete unknown id: %v", err)
	}

	// Two keys minted in the same second need a tie-break, or "newest first"
	// means a different order on each backend and the admin list shuffles
	// between refreshes.
	for _, id := range []string{"k-tie-2", "k-tie-1"} {
		if err := st.InsertMemberKey(models.MemberKey{ID: id, Label: id,
			Hash: "hash-" + id, CreatedAt: "2026-07-05T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = st.ListMemberKeys()
	if len(list) != 3 || list[0].ID != "k-tie-1" || list[1].ID != "k-tie-2" || list[2].ID != "k-b" {
		t.Fatalf("equal created_at must tie-break on id ascending, got %+v", list)
	}
}

// auditFactEnrich: a model's later judgment (tools_absent) attaches to the fact
// by audit id — and must never overwrite what was observed, nor invent a fact
// for an audit that never happened.
func auditFactEnrich(t *testing.T, st storage.Store) {
	base := models.AuditFact{
		AuditID: "aud_e", CreatedAt: "2026-07-12T00:00:00Z",
		TaskType: "data-analysis", ToolsUsed: []string{"Bash"},
	}
	must(t, appended(st.AppendAuditFact(base)))

	found, err := st.EnrichAuditFact(models.AuditFactEnrichment{
		AuditID: "aud_e", ToolsAbsent: []string{"warehouse-connector"}})
	must(t, err)
	if !found {
		t.Fatal("enrichment did not find the fact it was keyed to")
	}

	facts, err := st.AuditFacts("")
	must(t, err)
	if len(facts) != 1 {
		t.Fatalf("enrichment changed the fact count: %d", len(facts))
	}
	f := facts[0]
	if len(f.ToolsAbsent) != 1 || f.ToolsAbsent[0] != "warehouse-connector" {
		t.Fatalf("tools_absent not applied: %+v", f.ToolsAbsent)
	}
	// The observed half is untouchable: an inference may fill a gap, never
	// rewrite a fact.
	if f.TaskType != "data-analysis" || len(f.ToolsUsed) != 1 || f.ToolsUsed[0] != "Bash" {
		t.Fatalf("enrichment clobbered observed fields: %+v", f)
	}

	// An enrichment for an audit we never recorded has nothing to attach to.
	found, err = st.EnrichAuditFact(models.AuditFactEnrichment{
		AuditID: "aud_ghost", ToolsAbsent: []string{"x"}})
	must(t, err)
	if found {
		t.Fatal("enriched a fact that does not exist — that would be a headless fact")
	}
	if facts, _ = st.AuditFacts(""); len(facts) != 1 {
		t.Fatalf("a ghost enrichment created a fact: %d", len(facts))
	}
}

// auditFactByID: the single-fact lookup feedback.Ingest joins against when it
// decides whether a `shown` event describes a member seeing something or an
// agent pulling. Surface has to survive the round trip — the whole check reads
// it — and an unknown audit must come back not-found rather than as an error or
// a zero fact, because Ingest treats those three cases differently.
func auditFactByID(t *testing.T, st storage.Store) {
	must(t, appended(st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_pull", CreatedAt: "2026-07-25T00:00:00Z", Surface: "mcp",
		Segment: models.Segment{"team": "tacit"}, TechniquesOffered: []string{"a", "b"},
	})))

	f, found, err := st.AuditFact("aud_pull")
	must(t, err)
	if !found {
		t.Fatal("a fact just appended was not found by its audit id")
	}
	if f.Surface != "mcp" {
		t.Fatalf("surface did not survive the lookup: %q", f.Surface)
	}
	if f.Segment["team"] != "tacit" || len(f.TechniquesOffered) != 2 {
		t.Fatalf("fact came back partial: %+v", f)
	}

	if _, found, err = st.AuditFact("aud_never_happened"); err != nil {
		t.Fatalf("unknown audit id must not error: %v", err)
	} else if found {
		t.Fatal("found a fact for an audit that was never recorded")
	}
}

func fact(auditID, createdAt string) models.AuditFact {
	return models.AuditFact{AuditID: auditID, CreatedAt: createdAt}
}

// auditFactIdempotent: one interaction is recorded once. A retried evidence
// request must not double-count the audit, or every rate computed over the fact
// log inherits the duplication.
func auditFactIdempotent(t *testing.T, st storage.Store) {
	f := fact("aud_1", "2026-07-11T00:00:00Z")
	inserted, err := st.AppendAuditFact(f)
	must(t, err)
	if !inserted {
		t.Fatal("first append rejected")
	}
	inserted, err = st.AppendAuditFact(f)
	must(t, err)
	if inserted {
		t.Fatal("replayed audit_id accepted twice")
	}
	facts, err := st.AuditFacts("")
	must(t, err)
	if len(facts) != 1 {
		t.Fatalf("stored %d facts, want 1", len(facts))
	}
}

// auditFactsSince: the same cursor contract AllEvents honours, so the synthesis
// pass can page the fact log incrementally.
func auditFactsSince(t *testing.T, st storage.Store) {
	must(t, appended(st.AppendAuditFact(fact("old", "2020-01-01T00:00:00Z"))))
	must(t, appended(st.AppendAuditFact(fact("new", "2030-01-01T00:00:00Z"))))

	all, err := st.AuditFacts("")
	must(t, err)
	if len(all) != 2 {
		t.Fatalf("unfiltered returned %d facts, want 2", len(all))
	}
	recent, err := st.AuditFacts("2025-01-01T00:00:00Z")
	must(t, err)
	if len(recent) != 1 || recent[0].AuditID != "new" {
		t.Fatalf("since filter returned %+v, want just 'new'", recent)
	}
}

// auditFactRoundTrip: every field survives storage. The learning layer's whole
// premise is that this context is preserved and joinable to outcomes, so a
// backend silently dropping tools_used or resources would quietly gut it.
func auditFactRoundTrip(t *testing.T, st storage.Store) {
	want := models.AuditFact{
		AuditID:           "aud_rt",
		SessionHash:       "0123456789abcdef0123456789abcdef",
		CreatedAt:         "2026-07-11T12:00:00Z",
		Segment:           models.Segment{"team": "revops", "role": "analyst"},
		TaskType:          "data-analysis",
		Model:             "claude-haiku-4-5",
		Domain:            "revenue-operations",
		Harness:           "claude-code",
		Surface:           "cli",
		SkillLevel:        "intermediate",
		ToolsUsed:         []string{"Bash", "Read"},
		ToolsAbsent:       []string{"warehouse-connector"},
		Resources:         []string{"pipeline.csv"},
		TechniquesOffered: []string{"use-internal-data-connector", "ask-for-a-diagram"},
		Thin:              true,
	}
	must(t, appended(st.AppendAuditFact(want)))

	facts, err := st.AuditFacts("")
	must(t, err)
	if len(facts) != 1 {
		t.Fatalf("stored %d facts, want 1", len(facts))
	}
	if !reflect.DeepEqual(facts[0], want) {
		t.Fatalf("round-trip lost data:\n got %+v\nwant %+v", facts[0], want)
	}
}

// appended adapts the (inserted, error) pair for must(), and fails loudly if a
// fact the test believed was new was actually rejected as a replay.
func appended(inserted bool, err error) error {
	if err == nil && !inserted {
		return errors.New("append rejected as a replay")
	}
	return err
}

// techniqueVersions: the prior-version archive returns snapshots newest-first,
// drops embeddings, and keeps techniques isolated from each other's history.
func techniqueVersions(t *testing.T, st storage.Store) {
	if vs, err := st.TechniqueVersions("a"); err != nil || len(vs) != 0 {
		t.Fatalf("history of an unknown technique: %v %v", vs, err)
	}
	v1 := technique("a")
	v1.Embedding, v1.EmbeddingModel, v1.EmbeddingDim = []float32{1, 0}, "hashing-v1", 2
	must(t, st.ArchiveTechniqueVersion(v1))
	v2 := technique("a")
	v2.Version, v2.Recipe = 2, "R2"
	must(t, st.ArchiveTechniqueVersion(v2))
	must(t, st.ArchiveTechniqueVersion(technique("b"))) // another technique's history

	vs, err := st.TechniqueVersions("a")
	must(t, err)
	if len(vs) != 2 {
		t.Fatalf("want 2 archived versions, got %d", len(vs))
	}
	if vs[0].Version != 2 || vs[1].Version != 1 {
		t.Fatalf("not newest-first: v%d, v%d", vs[0].Version, vs[1].Version)
	}
	if len(vs[1].Embedding) != 0 || vs[1].EmbeddingModel != "" {
		t.Fatal("embedding leaked into the archive")
	}
	if vs[0].Recipe != "R2" || vs[1].Recipe != "R" {
		t.Fatal("snapshot content wrong")
	}
}

func technique(id string) models.Technique {
	now := models.Now()
	return models.Technique{
		ID: id, Name: "N " + id, Description: "D", Scope: "general",
		Status: "stable", Provenance: "curated", Version: 1, Recipe: "R",
		Tags: []string{"t"}, CreatedAt: now, UpdatedAt: now,
	}
}

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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func upsertPreserves(t *testing.T, st storage.Store) {
	must(t, st.UpsertTechnique(technique("a")))
	must(t, st.SetTechniqueEmbedding("a", []float32{1, 0}, "hashing-v1", 2))
	must(t, st.SetDecay("a", true, models.Now()))
	created, _, err := st.GetTechnique("a")
	must(t, err)

	// A sync that does not touch the embedded text leaves the vector alone:
	// re-embedding the whole corpus on every boot would be pure waste.
	c2 := technique("a")
	c2.Recipe = "a different recipe"
	c2.CreatedAt = "2099-01-01T00:00:00Z"
	must(t, st.UpsertTechnique(c2))
	got, ok, err := st.GetTechnique("a")
	must(t, err)
	if !ok || got.Recipe != "a different recipe" {
		t.Fatal("content field not updated")
	}
	if got.CreatedAt != created.CreatedAt {
		t.Fatal("created_at clobbered by upsert")
	}
	if got.DecaySignal != 1 {
		t.Fatal("decay state clobbered by upsert")
	}
	if len(got.Embedding) == 0 || got.EmbeddingModel != "hashing-v1" {
		t.Fatal("embedding dropped by a sync that did not change the embedded text")
	}

	// Lifecycle state survives content syncs that don't declare one: an API
	// retirement must not be resurrected by the next boot's techniques/*.md sync.
	must(t, st.SetTechniqueStatus("a", "retired", models.Now()))
	c3 := technique("a")
	c3.Status = "" // "this sync doesn't mention lifecycle state"
	must(t, st.UpsertTechnique(c3))
	got, _, err = st.GetTechnique("a")
	must(t, err)
	if got.Status != "retired" {
		t.Fatalf("undeclared-status upsert resurrected a retired technique (status %q)", got.Status)
	}
	// An explicit status still wins…
	c4 := technique("a")
	c4.Status = "stable"
	must(t, st.UpsertTechnique(c4))
	if got, _, _ = st.GetTechnique("a"); got.Status != "stable" {
		t.Fatalf("explicit status ignored (status %q)", got.Status)
	}
	// …and a brand-new technique with no declared status serves as stable.
	fresh := technique("fresh-undeclared")
	fresh.Status = ""
	must(t, st.UpsertTechnique(fresh))
	if got, _, _ = st.GetTechnique("fresh-undeclared"); got.Status != "stable" {
		t.Fatalf("new undeclared-status technique = %q, want stable", got.Status)
	}
}

func insertIdempotent(t *testing.T, st storage.Store) {
	e := event(t, "a", "shown", nil)
	inserted, err := st.InsertEvent(e)
	must(t, err)
	if !inserted {
		t.Fatal("first insert rejected")
	}
	inserted, err = st.InsertEvent(e)
	must(t, err)
	if inserted {
		t.Fatal("replayed event_id accepted twice")
	}
	events, err := st.AllEvents("")
	must(t, err)
	if len(events) != 1 {
		t.Fatalf("stored %d events", len(events))
	}
}

func eventsSince(t *testing.T, st storage.Store) {
	old := event(t, "a", "shown", nil)
	old.CreatedAt = "2020-01-01T00:00:00Z"
	_, err := st.InsertEvent(old)
	must(t, err)
	recent := event(t, "a", "adopted", nil)
	recent.CreatedAt = "2030-01-01T00:00:00Z"
	_, err = st.InsertEvent(recent)
	must(t, err)

	got, err := st.AllEvents("2025-01-01T00:00:00Z")
	must(t, err)
	if len(got) != 1 || got[0].Stage != "adopted" {
		t.Fatalf("since filter: %+v", got)
	}
}

func lifecycleEvent(techniqueID, kind, createdAt string) models.LifecycleEvent {
	return models.LifecycleEvent{
		EventID:       contracts.DeterministicLifecycleEventID(techniqueID, kind, createdAt),
		TechniqueID:   techniqueID,
		TechniqueName: techniqueID,
		Kind:          kind,
		Reason:        "n=1",
		CreatedAt:     createdAt,
	}
}

func lifecycleIdempotent(t *testing.T, st storage.Store) {
	e := lifecycleEvent("a", "promoted", "2026-01-01T00:00:00Z")
	inserted, err := st.AppendLifecycleEvent(e)
	must(t, err)
	if !inserted {
		t.Fatal("first append rejected")
	}
	inserted, err = st.AppendLifecycleEvent(e)
	must(t, err)
	if inserted {
		t.Fatal("replayed event_id accepted twice")
	}
	got, err := st.LifecycleEvents("")
	must(t, err)
	if len(got) != 1 {
		t.Fatalf("stored %d lifecycle events", len(got))
	}
	if got[0].Kind != "promoted" || got[0].TechniqueID != "a" || got[0].Reason != "n=1" {
		t.Fatalf("round-trip mismatch: %+v", got[0])
	}
}

func lifecycleSince(t *testing.T, st storage.Store) {
	_, err := st.AppendLifecycleEvent(lifecycleEvent("a", "discovered", "2020-01-01T00:00:00Z"))
	must(t, err)
	_, err = st.AppendLifecycleEvent(lifecycleEvent("a", "promoted", "2030-01-01T00:00:00Z"))
	must(t, err)
	got, err := st.LifecycleEvents("2025-01-01T00:00:00Z")
	must(t, err)
	if len(got) != 1 || got[0].Kind != "promoted" {
		t.Fatalf("since filter: %+v", got)
	}
}

func candidateFilter(t *testing.T, st storage.Store) {
	stable := technique("stable")
	draft := technique("draft")
	draft.Status = "draft"
	decayed := technique("decayed")
	for _, c := range []models.Technique{stable, draft, decayed} {
		must(t, st.UpsertTechnique(c))
		must(t, st.SetTechniqueEmbedding(c.ID, []float32{1}, "hashing-v1", 1))
	}
	must(t, st.SetDecay("decayed", true, models.Now()))
	must(t, st.UpsertTechnique(technique("no-embed")))

	got, err := st.CandidateTechniques()
	must(t, err)
	if len(got) != 1 || got[0].ID != "stable" {
		ids := []string{}
		for _, c := range got {
			ids = append(ids, c.ID)
		}
		t.Fatalf("candidates = %v (want [stable])", ids)
	}
	if len(got[0].Embedding) != 1 {
		t.Fatal("candidate embedding not returned")
	}
}

// shadowCandidateFilter: ShadowCandidateTechniques returns only status=shadow techniques
// with an embedding and no decay signal — and never a serving technique. A shadow
// technique that picks up a decay signal drops out too (its status stays "shadow",
// but the signal revokes it), symmetric with CandidateTechniques.
func shadowCandidateFilter(t *testing.T, st storage.Store) {
	shadow := technique("shadow")
	shadow.Status = "shadow"
	shadowDecayed := technique("shadow-decayed")
	shadowDecayed.Status = "shadow"
	stable := technique("stable") // a serving technique must not leak into the shadow set
	for _, c := range []models.Technique{shadow, shadowDecayed, stable} {
		must(t, st.UpsertTechnique(c))
		must(t, st.SetTechniqueEmbedding(c.ID, []float32{1}, "hashing-v1", 1))
	}
	must(t, st.SetDecay("shadow-decayed", true, models.Now()))
	noEmbed := technique("shadow-no-embed")
	noEmbed.Status = "shadow"
	must(t, st.UpsertTechnique(noEmbed)) // status=shadow but no embedding -> excluded

	got, err := st.ShadowCandidateTechniques()
	must(t, err)
	if len(got) != 1 || got[0].ID != "shadow" {
		ids := []string{}
		for _, c := range got {
			ids = append(ids, c.ID)
		}
		t.Fatalf("shadow candidates = %v (want [shadow])", ids)
	}
	if len(got[0].Embedding) != 1 {
		t.Fatal("shadow candidate embedding not returned")
	}
}

func decayDraft(t *testing.T, st storage.Store) {
	d := technique("d")
	d.Status = "draft"
	must(t, st.UpsertTechnique(d))
	must(t, st.SetDecay("d", true, models.Now()))
	got, _, err := st.GetTechnique("d")
	must(t, err)
	if got.Status != "draft" {
		t.Fatalf("draft status flipped to %q", got.Status)
	}
	if got.DecaySignal != 1 {
		t.Fatal("decay signal not set")
	}
	// recovery flips stable/decayed only
	s := technique("s")
	must(t, st.UpsertTechnique(s))
	must(t, st.SetDecay("s", true, models.Now()))
	must(t, st.SetDecay("s", false, models.Now()))
	got, _, err = st.GetTechnique("s")
	must(t, err)
	if got.Status != "stable" || got.DecaySignal != 0 {
		t.Fatalf("decay recovery: %+v", got)
	}
}

func listTechniques(t *testing.T, st storage.Store) {
	for _, id := range []string{"c", "a", "b"} {
		must(t, st.UpsertTechnique(technique(id)))
	}
	d := technique("dr")
	d.Status = "draft"
	must(t, st.UpsertTechnique(d))

	all, err := st.ListTechniques(nil, 0)
	must(t, err)
	if len(all) != 4 || all[0].ID != "a" { // ordered by id
		t.Fatalf("list: %+v", ids(all))
	}
	drafts, err := st.ListTechniques([]string{"draft"}, 0)
	must(t, err)
	if len(drafts) != 1 || drafts[0].ID != "dr" {
		t.Fatalf("status filter: %v", ids(drafts))
	}
	limited, err := st.ListTechniques(nil, 2)
	must(t, err)
	if len(limited) != 2 {
		t.Fatalf("limit: %v", ids(limited))
	}
}

func needingEmbedding(t *testing.T, st storage.Store) {
	must(t, st.UpsertTechnique(technique("fresh")))
	must(t, st.SetTechniqueEmbedding("fresh", []float32{1}, "hashing-v1", 1))
	must(t, st.UpsertTechnique(technique("missing")))
	must(t, st.UpsertTechnique(technique("stale")))
	must(t, st.SetTechniqueEmbedding("stale", []float32{1}, "old-model", 1))

	got, err := st.TechniquesNeedingEmbedding("hashing-v1")
	must(t, err)
	if len(got) != 2 || got[0].ID != "missing" || got[1].ID != "stale" {
		t.Fatalf("needing embedding: %v", ids(got))
	}
}

// A technique whose embedded text changed must be re-embedded. Before this was
// enforced, the vector survived any edit: the registry served the new words and
// matched on the old ones, silently and with nothing in any log.
//
// It is not only file edits. Nine paths reach UpsertTechnique — the dashboard
// edit form, draft promotion, LLM suggestion, observed discovery, and federated
// import among them. federation/subscribe.go even clears the embedding itself
// before re-importing an updated technique, which the store used to undo.
//
// embed.TechniqueText is the one definition of what gets embedded, so both
// sides of the comparison come from it and a new field added there is covered
// for free.
func embeddingFollowsText(t *testing.T, st storage.Store) {
	base := technique("a")
	must(t, st.UpsertTechnique(base))
	must(t, st.SetTechniqueEmbedding("a", []float32{1, 0}, "hashing-v1", 2))

	// Recipe is not embedded: changing it must not cost a re-embed.
	quiet := technique("a")
	quiet.Recipe = "rewritten recipe"
	must(t, st.UpsertTechnique(quiet))
	pending, err := st.TechniquesNeedingEmbedding("hashing-v1")
	must(t, err)
	if len(pending) != 0 {
		t.Fatalf("a change outside the embedded text forced a re-embed: %v", ids(pending))
	}

	// Name is embedded: changing it must.
	renamed := technique("a")
	renamed.Name = "a quite different name"
	must(t, st.UpsertTechnique(renamed))
	pending, err = st.TechniquesNeedingEmbedding("hashing-v1")
	must(t, err)
	if len(pending) != 1 || pending[0].ID != "a" {
		t.Fatalf("renaming a technique left its old vector in place: %v", ids(pending))
	}

	// A federated import arrives carrying no vector of its own — embeddings are
	// local and never travel (federation/publish.go). That must not cost the
	// local vector when the text is the same, and must not keep it when the
	// text was scrubbed on the way in.
	must(t, st.SetTechniqueEmbedding("a", []float32{1, 0}, "hashing-v1", 2))
	sameText := technique("a")
	sameText.Name = "a quite different name" // as stored by the rename above
	sameText.Embedding, sameText.EmbeddingModel, sameText.EmbeddingDim = nil, "", 0
	must(t, st.UpsertTechnique(sameText))
	pending, err = st.TechniquesNeedingEmbedding("hashing-v1")
	must(t, err)
	if len(pending) != 0 {
		t.Fatalf("an import with unchanged text threw away the local vector: %v", ids(pending))
	}

	scrubbed := technique("a")
	scrubbed.Name = "a quite different name"
	scrubbed.Description = "redacted on the way in"
	scrubbed.Embedding, scrubbed.EmbeddingModel, scrubbed.EmbeddingDim = nil, "", 0
	must(t, st.UpsertTechnique(scrubbed))
	pending, err = st.TechniquesNeedingEmbedding("hashing-v1")
	must(t, err)
	if len(pending) != 1 || pending[0].ID != "a" {
		t.Fatalf("an import whose text changed kept its old vector: %v", ids(pending))
	}
}

func outcomes(t *testing.T, st storage.Store) {
	hr := 0.9
	must(t, st.ReplaceOutcomes([]models.Outcome{
		{TechniqueID: "a", SegmentKey: "__overall__", Adopted: 10, HelpedRate: &hr,
			WeightedAdopted: 5.5, WeightedHelped: 4.5, WeightedDismissed: 0.5,
			SampleSize: 10, LastUpdated: models.Now()},
		{TechniqueID: "b", SegmentKey: "__overall__", Adopted: 20, SampleSize: 20,
			LastUpdated: models.Now()},
	}))
	o, ok, err := st.GetOutcome("a", "__overall__")
	must(t, err)
	if !ok || o.HelpedRate == nil || *o.HelpedRate != 0.9 {
		t.Fatalf("get outcome: %+v", o)
	}
	if o.WeightedAdopted != 5.5 || o.WeightedHelped != 4.5 || o.WeightedDismissed != 0.5 {
		t.Fatalf("weighted funnel did not round-trip: %+v", o)
	}
	rows, err := st.OutcomesForSegment("__overall__")
	must(t, err)
	if len(rows) != 2 || rows[0].TechniqueID != "b" { // adopted desc
		t.Fatalf("segment order: %+v", rows)
	}
	// replace-all removes stale rows
	must(t, st.ReplaceOutcomes([]models.Outcome{
		{TechniqueID: "c", SegmentKey: "__overall__", Adopted: 1, SampleSize: 1,
			LastUpdated: models.Now()},
	}))
	if _, ok, _ := st.GetOutcome("a", "__overall__"); ok {
		t.Fatal("stale rollup survived replace")
	}
}

func sketches(t *testing.T, st storage.Store) {
	sk := models.Sketch{
		SketchID: "sk-1", TechniqueID: "technique-a", SessionHash: "h1",
		Trigger: "pasting warehouse rows into the prompt", Move: "use the @warehouse connector",
		Harness: "claude-code", TaskType: "data-analysis",
		Segment: models.Segment{"team": "revops"}, Tools: []string{"mcp__warehouse__query"},
		CreatedAt: "2026-07-16T00:00:01Z",
	}
	inserted, err := st.InsertSketch(sk)
	must(t, err)
	if !inserted {
		t.Fatal("first insert should report inserted")
	}
	if replay, err := st.InsertSketch(sk); err != nil || replay {
		t.Fatalf("replay must be idempotent: %v %v", replay, err)
	}
	if inserted, err := st.InsertSketch(models.Sketch{TechniqueID: "technique-a"}); err != nil || inserted {
		t.Fatalf("a sketch without an id must be refused: %v %v", inserted, err)
	}
	_, err = st.InsertSketch(models.Sketch{SketchID: "sk-2", TechniqueID: "technique-a",
		Trigger: "t2", Move: "m2", CreatedAt: "2026-07-16T00:00:02Z"})
	must(t, err)
	_, err = st.InsertSketch(models.Sketch{SketchID: "sk-other", TechniqueID: "technique-b",
		Trigger: "t", Move: "m", CreatedAt: "2026-07-16T00:00:03Z"})
	must(t, err)

	got, err := st.SketchesForTechnique("technique-a")
	must(t, err)
	if len(got) != 2 || got[0].SketchID != "sk-2" || got[1].SketchID != "sk-1" {
		t.Fatalf("sketches for technique-a (newest first): %+v", got)
	}
	if got[1].Segment["team"] != "revops" || len(got[1].Tools) != 1 {
		t.Fatalf("sketch payload did not round-trip: %+v", got[1])
	}
	if none, err := st.SketchesForTechnique("technique-none"); err != nil || len(none) != 0 {
		t.Fatalf("unknown technique should have no sketches: %v %v", none, err)
	}

	// Technique-less sketches (observed moves not yet tied to a technique) are returned by
	// TechniquelessSketches only, never by SketchesForTechnique, newest first, limited.
	_, err = st.InsertSketch(models.Sketch{SketchID: "sk-obs-1", // no TechniqueID
		Trigger: "wrote a migration by hand", Move: "generate it from the schema diff",
		CreatedAt: "2026-07-16T00:00:04Z"})
	must(t, err)
	_, err = st.InsertSketch(models.Sketch{SketchID: "sk-obs-2",
		Trigger: "debugged a flaky test by rerunning", Move: "bisect with --count",
		CreatedAt: "2026-07-16T00:00:05Z"})
	must(t, err)

	techniqueless, err := st.TechniquelessSketches(0)
	must(t, err)
	if len(techniqueless) != 2 || techniqueless[0].SketchID != "sk-obs-2" || techniqueless[1].SketchID != "sk-obs-1" {
		t.Fatalf("technique-less sketches (newest first): %+v", techniqueless)
	}
	for _, s := range techniqueless {
		if s.TechniqueID != "" {
			t.Fatalf("TechniquelessSketches returned a technique-tied sketch: %+v", s)
		}
	}
	if lim, err := st.TechniquelessSketches(1); err != nil || len(lim) != 1 || lim[0].SketchID != "sk-obs-2" {
		t.Fatalf("limit not honored: %+v %v", lim, err)
	}

	// Sketches arrive in batches and often share a created_at to the second.
	// "Newest first" then needs a tie-break, or the limit above returns a
	// different sketch each call and the clustering input is unstable.
	for _, id := range []string{"sk-tie-a", "sk-tie-b"} {
		if _, err := st.InsertSketch(models.Sketch{SketchID: id, TechniqueID: "technique-a",
			Trigger: "t", Move: "m", CreatedAt: "2026-07-16T00:00:09Z"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err = st.SketchesForTechnique("technique-a")
	must(t, err)
	if len(got) != 4 || got[0].SketchID != "sk-tie-b" || got[1].SketchID != "sk-tie-a" {
		t.Fatalf("equal created_at must tie-break on sketch_id descending: %+v", got)
	}
	for _, id := range []string{"sk-obs-tie-a", "sk-obs-tie-b"} {
		if _, err := st.InsertSketch(models.Sketch{SketchID: id,
			Trigger: "t", Move: "m", CreatedAt: "2026-07-16T00:00:09Z"}); err != nil {
			t.Fatal(err)
		}
	}
	techniqueless, err = st.TechniquelessSketches(2)
	must(t, err)
	if len(techniqueless) != 2 ||
		techniqueless[0].SketchID != "sk-obs-tie-b" || techniqueless[1].SketchID != "sk-obs-tie-a" {
		t.Fatalf("equal created_at must tie-break on sketch_id descending: %+v", techniqueless)
	}
}

func countsCheck(t *testing.T, st storage.Store) {
	must(t, st.UpsertTechnique(technique("a")))
	_, err := st.InsertEvent(event(t, "a", "shown", nil))
	must(t, err)
	must(t, st.ReplaceOutcomes([]models.Outcome{
		{TechniqueID: "a", SegmentKey: "__overall__", LastUpdated: models.Now()}}))
	counts, err := st.Counts()
	must(t, err)
	if counts["techniques"] != 1 || counts["events"] != 1 || counts["outcomes"] != 1 {
		t.Fatalf("counts: %v", counts)
	}
}

func ids(techniques []models.Technique) []string {
	out := make([]string, 0, len(techniques))
	for _, c := range techniques {
		out = append(out, c.ID)
	}
	return out
}

// channelsPreserved: publication state (channels) must survive an upsert
// that doesn't mention it — a git re-sync can't silently unpublish — while an
// explicit empty non-nil slice unpublishes (federation SetChannels).
func channelsPreserved(t *testing.T, st storage.Store) {
	c := technique("chan-1")
	c.Channels = []string{"general", "data-tools"}
	must(t, st.UpsertTechnique(c))

	resync := technique("chan-1") // fresh parse: no channels field
	resync.Channels = nil
	must(t, st.UpsertTechnique(resync))
	got, ok, err := st.GetTechnique("chan-1")
	must(t, err)
	if !ok || len(got.Channels) != 2 {
		t.Fatalf("channels lost on sync: %+v", got.Channels)
	}

	unpublish := got
	unpublish.Channels = []string{}
	must(t, st.UpsertTechnique(unpublish))
	got, _, err = st.GetTechnique("chan-1")
	must(t, err)
	if len(got.Channels) != 0 {
		t.Fatalf("explicit unpublish ignored: %+v", got.Channels)
	}
}

func originPreserved(t *testing.T, st storage.Store) {
	c := technique("origin-1")
	c.Origin = &contracts.FederationOrigin{ProviderID: "https://provider.example", ProviderKey: "ed25519:key",
		EntryID: "https://provider.example/techniques/a", ChannelID: "general",
		ContentHash: "sha256:first", ImportedAt: "2026-07-01T00:00:00Z"}
	must(t, st.UpsertTechnique(c))
	got, ok, err := st.GetTechnique(c.ID)
	must(t, err)
	if !ok || !reflect.DeepEqual(got.Origin, c.Origin) {
		t.Fatalf("origin did not round-trip: got %+v want %+v", got.Origin, c.Origin)
	}

	resync := technique(c.ID)
	resync.Origin = nil
	must(t, st.UpsertTechnique(resync))
	got, _, err = st.GetTechnique(c.ID)
	must(t, err)
	if !reflect.DeepEqual(got.Origin, c.Origin) {
		t.Fatalf("origin lost on sync: %+v", got.Origin)
	}
}

func deleteTechnique(t *testing.T, st storage.Store) {
	c := technique("del-1")
	must(t, st.UpsertTechnique(c))
	rate := 0.5
	must(t, st.ReplaceOutcomes([]models.Outcome{{TechniqueID: "del-1",
		SegmentKey: "__overall__", Adopted: 2, Helped: 1, HelpedRate: &rate,
		SampleSize: 2, LastUpdated: models.Now()}}))

	ok, err := st.DeleteTechnique("del-1")
	must(t, err)
	if !ok {
		t.Fatal("existing technique reported missing")
	}
	if _, found, _ := st.GetTechnique("del-1"); found {
		t.Fatal("technique survived deletion")
	}
	if _, found, _ := st.GetOutcome("del-1", "__overall__"); found {
		t.Fatal("rollup survived deletion")
	}
	if ok, _ := st.DeleteTechnique("del-1"); ok {
		t.Fatal("double delete reported success")
	}
}

// upsertNormalizesTags: the tag vocabulary is free — any technique may coin any tag,
// from four unbounded sources — so UpsertTechnique is the one gate that keeps it from
// filling with variant spellings of tags it already has. Every backend must
// apply it, or the discipline depends on which store you happen to be running.
func upsertNormalizesTags(t *testing.T, s storage.Store) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.UpsertTechnique(models.Technique{
		ID: "tagnorm", Name: "Tag normalization", Status: "stable",
		Tags:      []string{"Setup", "  audit  ", "task_type", "agent--setup", "setup", ""},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetTechnique("tagnorm")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	want := []string{"setup", "audit", "task-type", "agent-setup"}
	if !reflect.DeepEqual(got.Tags, want) {
		t.Fatalf("tags = %q, want %q — UpsertTechnique must normalize (models.NormalizeTags)", got.Tags, want)
	}
}

// feedTokens pins the federation-feed credential across backends: minted with a
// channel scope, resolvable by hash, listed, touched and revocable. Like member
// keys, a revoked token is still RETURNED by the lookup — the gate decides what a
// revocation means, and a store that hid them would make "why was this refused?"
// unanswerable.
func feedTokens(t *testing.T, st storage.Store) {
	a := models.FeedToken{ID: "ft-a", Label: "Acme Corp", Hash: "fh-a",
		Channels: []string{"partners", "vendor-pack"}, CreatedAt: "2026-07-01T00:00:00Z"}
	b := models.FeedToken{ID: "ft-b", Label: "Platform team", Hash: "fh-b",
		Channels: []string{"partners"}, CreatedAt: "2026-07-02T00:00:00Z"}
	for _, tok := range []models.FeedToken{a, b} {
		if err := st.InsertFeedToken(tok); err != nil {
			t.Fatalf("InsertFeedToken(%s): %v", tok.ID, err)
		}
	}

	got, ok, err := st.FeedTokenByHash("fh-a")
	if err != nil || !ok {
		t.Fatalf("FeedTokenByHash: ok=%v err=%v", ok, err)
	}
	if got.Label != a.Label || len(got.Channels) != 2 || got.Channels[0] != "partners" {
		t.Errorf("round-tripped %+v, want the label and channel scope intact", got)
	}
	if !got.Allows("vendor-pack") || got.Allows("secret-channel") {
		t.Error("the channel scope does not decide what the token allows")
	}
	if _, ok, _ := st.FeedTokenByHash("no-such"); ok {
		t.Error("an unknown hash resolved to a token")
	}

	list, err := st.ListFeedTokens()
	if err != nil || len(list) != 2 {
		t.Fatalf("ListFeedTokens = %d tokens, err %v; want 2", len(list), err)
	}
	if list[0].ID != "ft-b" {
		t.Errorf("list starts with %q, want newest first", list[0].ID)
	}

	if err := st.TouchFeedToken("ft-a", "2026-07-09T10:00:00Z"); err != nil {
		t.Fatalf("TouchFeedToken: %v", err)
	}
	if got, _, _ := st.FeedTokenByHash("fh-a"); got.LastUsed != "2026-07-09T10:00:00Z" {
		t.Errorf("LastUsed = %q, want the touched value", got.LastUsed)
	}

	if err := st.SetFeedTokenRevoked("ft-a", "2026-07-10T00:00:00Z"); err != nil {
		t.Fatalf("SetFeedTokenRevoked: %v", err)
	}
	got, ok, _ = st.FeedTokenByHash("fh-a")
	if !ok {
		t.Fatal("a revoked token vanished from the lookup; the gate can no longer explain a refusal")
	}
	if got.RevokedAt == "" {
		t.Error("revocation was not persisted")
	}
	if got.Allows("partners") {
		t.Error("a revoked token still allows a channel")
	}

	// Unknown ids are a no-op, matching the member-key contract.
	if err := st.TouchFeedToken("nope", "x"); err != nil {
		t.Errorf("TouchFeedToken(unknown) = %v, want nil", err)
	}
	if err := st.SetFeedTokenRevoked("nope", "x"); err != nil {
		t.Errorf("SetFeedTokenRevoked(unknown) = %v, want nil", err)
	}

	// Same tie-break rule as member keys: equal created_at orders by id.
	for _, id := range []string{"ft-tie-2", "ft-tie-1"} {
		if err := st.InsertFeedToken(models.FeedToken{ID: id, Label: id, Hash: "fh-" + id,
			Channels: []string{"partners"}, CreatedAt: "2026-07-05T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = st.ListFeedTokens()
	if len(list) != 4 || list[0].ID != "ft-tie-1" || list[1].ID != "ft-tie-2" {
		t.Fatalf("equal created_at must tie-break on id ascending, got %+v", list)
	}
}

// enrichEmptyNoop: an enrichment that carries no inferred field has nothing to
// add, so it reports found=false and leaves the fact — and the fact log —
// exactly as it was. The auditor calls EnrichAuditFact whenever the inference
// pass finishes, including when the model named no absent tool, and it reads
// the returned bool as "a fact changed". A backend answering true there
// invents work that never happened.
func enrichEmptyNoop(t *testing.T, st storage.Store) {
	must(t, appended(st.AppendAuditFact(models.AuditFact{
		AuditID: "aud_empty", CreatedAt: "2026-07-12T00:00:00Z",
		TaskType: "data-analysis", ToolsUsed: []string{"Bash"},
	})))

	found, err := st.EnrichAuditFact(models.AuditFactEnrichment{AuditID: "aud_empty"})
	must(t, err)
	if found {
		t.Fatal("an enrichment carrying nothing reported that it changed a fact")
	}
	facts, err := st.AuditFacts("")
	must(t, err)
	if len(facts) != 1 || len(facts[0].ToolsAbsent) != 0 {
		t.Fatalf("empty enrichment wrote something: %+v", facts)
	}

	// It must not erase an inference already recorded either.
	if found, err := st.EnrichAuditFact(models.AuditFactEnrichment{
		AuditID: "aud_empty", ToolsAbsent: []string{"warehouse-connector"}}); err != nil || !found {
		t.Fatalf("real enrichment: found=%v err=%v", found, err)
	}
	if found, err := st.EnrichAuditFact(models.AuditFactEnrichment{
		AuditID: "aud_empty", ToolsAbsent: []string{}}); err != nil || found {
		t.Fatalf("empty enrichment after a real one: found=%v err=%v, want false and no error", found, err)
	}
	facts, err = st.AuditFacts("")
	must(t, err)
	if len(facts) != 1 || len(facts[0].ToolsAbsent) != 1 {
		t.Fatalf("an empty enrichment disturbed the fact: %+v", facts)
	}

	// Unknown audit plus nothing to add is still just not-found.
	if found, err := st.EnrichAuditFact(models.AuditFactEnrichment{AuditID: "aud_ghost"}); err != nil || found {
		t.Fatalf("empty enrichment for an unknown audit: found=%v err=%v", found, err)
	}
}

// logsTimeOrdered: the three append-only logs read back in created_at order,
// with id as the tie-break — not in the order the rows happened to arrive.
// Every consumer that pages a log with a `since` cursor (synthesis, the events
// view, federation export) assumes the last row it saw is the newest one it
// saw. Insertion order and time order differ whenever a producer replays a
// backlog, and a backend that returns insertion order silently hands out a
// cursor that skips rows.
func logsTimeOrdered(t *testing.T, st storage.Store) {
	const (
		t1 = "2026-01-01T00:00:00Z"
		t2 = "2026-02-01T00:00:00Z"
		t3 = "2026-03-01T00:00:00Z"
	)
	// Written newest-first, and with the equal-timestamp pair reversed.
	rows := []struct{ id, createdAt string }{
		{"c", t3}, {"b2", t2}, {"b1", t2}, {"a", t1},
	}
	for _, r := range rows {
		e := event(t, "tech", "shown", nil)
		e.EventID, e.CreatedAt = "evt-"+r.id, r.createdAt
		if _, err := st.InsertEvent(e); err != nil {
			t.Fatal(err)
		}
		must(t, appended(st.AppendAuditFact(fact("aud-"+r.id, r.createdAt))))
		lc := lifecycleEvent("tech", "promoted", r.createdAt)
		lc.EventID = "lc-" + r.id
		if _, err := st.AppendLifecycleEvent(lc); err != nil {
			t.Fatal(err)
		}
	}

	wantAll := []string{"a", "b1", "b2", "c"}
	wantSince := []string{"b1", "b2", "c"}

	events, err := st.AllEvents("")
	must(t, err)
	checkOrder(t, "AllEvents", eventIDs(events, "evt-"), wantAll)
	events, err = st.AllEvents(t2)
	must(t, err)
	checkOrder(t, "AllEvents(since)", eventIDs(events, "evt-"), wantSince)

	facts, err := st.AuditFacts("")
	must(t, err)
	checkOrder(t, "AuditFacts", factIDs(facts, "aud-"), wantAll)
	facts, err = st.AuditFacts(t2)
	must(t, err)
	checkOrder(t, "AuditFacts(since)", factIDs(facts, "aud-"), wantSince)

	lcs, err := st.LifecycleEvents("")
	must(t, err)
	checkOrder(t, "LifecycleEvents", lifecycleIDs(lcs, "lc-"), wantAll)
	lcs, err = st.LifecycleEvents(t2)
	must(t, err)
	checkOrder(t, "LifecycleEvents(since)", lifecycleIDs(lcs, "lc-"), wantSince)
}

func checkOrder(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s returned %v, want %v — order by (created_at, id), not by arrival", what, got, want)
	}
}

func eventIDs(events []models.FeedbackEvent, trim string) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, strings.TrimPrefix(e.EventID, trim))
	}
	return out
}

func factIDs(facts []models.AuditFact, trim string) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, strings.TrimPrefix(f.AuditID, trim))
	}
	return out
}

func lifecycleIDs(events []models.LifecycleEvent, trim string) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, strings.TrimPrefix(e.EventID, trim))
	}
	return out
}

// unknownTechniqueWrites: the three technique-state writers refuse an id the
// store has never seen. They are the callers' only signal that a recompute or
// an embedding pass is working on a technique that has since been deleted, and
// a backend that swallows the miss turns that into a silent no-op. Note the
// deliberate asymmetry with the credential writers, where an unknown id IS a
// no-op (memberKeys, feedTokens) — deleting a key mid-request is ordinary.
// The refusal must also be readable: every backend reports the miss as
// storage.ErrNotFound, so a caller can tell a deleted technique apart from a
// backend failure with errors.Is instead of matching the message text.
func unknownTechniqueWrites(t *testing.T, st storage.Store) {
	if err := st.SetTechniqueEmbedding("ghost", []float32{1}, "hashing-v1", 1); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("SetTechniqueEmbedding on an unknown id = %v, want storage.ErrNotFound", err)
	}
	if err := st.SetTechniqueStatus("ghost", "retired", models.Now()); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("SetTechniqueStatus on an unknown id = %v, want storage.ErrNotFound", err)
	}
	if err := st.SetDecay("ghost", true, models.Now()); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("SetDecay on an unknown id = %v, want storage.ErrNotFound", err)
	}
	if _, ok, err := st.GetTechnique("ghost"); err != nil || ok {
		t.Fatalf("a refused write created a technique: ok=%v err=%v", ok, err)
	}
	// The same three writes on a technique that exists must succeed.
	must(t, st.UpsertTechnique(technique("real")))
	must(t, st.SetTechniqueEmbedding("real", []float32{1}, "hashing-v1", 1))
	must(t, st.SetTechniqueStatus("real", "retired", models.Now()))
	must(t, st.SetDecay("real", true, models.Now()))
}

// countsAllKeys: /v1/health and the dashboard read these keys by name, so the
// set is part of the contract — a backend reporting five of the six leaves a
// tile reading zero with nothing to explain it.
func countsAllKeys(t *testing.T, st storage.Store) {
	must(t, st.UpsertTechnique(technique("a")))
	d := technique("d")
	d.Status = "draft"
	must(t, st.UpsertTechnique(d))
	if _, err := st.InsertEvent(event(t, "a", "shown", nil)); err != nil {
		t.Fatal(err)
	}
	must(t, appended(st.AppendAuditFact(fact("aud_counts", "2026-07-01T00:00:00Z"))))
	if _, err := st.AppendLifecycleEvent(lifecycleEvent("a", "promoted", "2026-07-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	must(t, st.ReplaceOutcomes([]models.Outcome{
		{TechniqueID: "a", SegmentKey: "__overall__", LastUpdated: models.Now()}}))

	counts, err := st.Counts()
	must(t, err)
	want := map[string]int{
		"techniques": 2, "drafts": 1, "events": 1,
		"outcomes": 1, "audit_facts": 1, "lifecycle": 1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("Counts() = %v, want exactly %v", counts, want)
	}
}

// deleteKeepsEvents: deleting a technique removes the technique and its rollups
// and nothing else. The three logs are append-only history — the funnel a
// retired technique earned is the evidence for having retired it, and a backend
// that cascades the delete erases the reason.
func deleteKeepsEvents(t *testing.T, st storage.Store) {
	must(t, st.UpsertTechnique(technique("gone")))
	if _, err := st.InsertEvent(event(t, "gone", "shown", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendLifecycleEvent(lifecycleEvent("gone", "retired", "2026-07-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	must(t, appended(st.AppendAuditFact(fact("aud_gone", "2026-07-01T00:00:00Z"))))
	must(t, st.ArchiveTechniqueVersion(technique("gone")))

	ok, err := st.DeleteTechnique("gone")
	must(t, err)
	if !ok {
		t.Fatal("existing technique reported missing")
	}

	events, err := st.AllEvents("")
	must(t, err)
	if len(events) != 1 || events[0].TechniqueID != "gone" {
		t.Fatalf("the event log lost the deleted technique's history: %+v", events)
	}
	lcs, err := st.LifecycleEvents("")
	must(t, err)
	if len(lcs) != 1 || lcs[0].Kind != "retired" {
		t.Fatalf("the lifecycle log lost the retirement: %+v", lcs)
	}
	facts, err := st.AuditFacts("")
	must(t, err)
	if len(facts) != 1 {
		t.Fatalf("the fact log lost an interaction: %+v", facts)
	}
	vs, err := st.TechniqueVersions("gone")
	must(t, err)
	if len(vs) != 1 {
		t.Fatalf("the version archive lost the deleted technique: %+v", vs)
	}
}

// feedTokenReinstate: revoking is reversible. SetFeedTokenRevoked("") clears the
// stamp and the token allows its channels again, and the channel scope survives
// the round trip — the admin UI offers reinstate as the undo for a revoke made
// in error, and a backend that only ever stamps makes that undo a re-mint.
func feedTokenReinstate(t *testing.T, st storage.Store) {
	must(t, st.InsertFeedToken(models.FeedToken{ID: "ft-r", Label: "Partner", Hash: "fh-r",
		Channels: []string{"partners", "vendor-pack"}, CreatedAt: "2026-07-01T00:00:00Z"}))
	must(t, st.SetFeedTokenRevoked("ft-r", "2026-07-02T00:00:00Z"))
	got, ok, err := st.FeedTokenByHash("fh-r")
	must(t, err)
	if !ok || got.RevokedAt == "" || got.Allows("partners") {
		t.Fatalf("revoke: %+v ok=%v", got, ok)
	}

	must(t, st.SetFeedTokenRevoked("ft-r", ""))
	got, ok, err = st.FeedTokenByHash("fh-r")
	must(t, err)
	if !ok {
		t.Fatal("a reinstated token vanished from the lookup")
	}
	if got.RevokedAt != "" {
		t.Fatalf("reinstate did not clear the revocation: %q", got.RevokedAt)
	}
	if !got.Allows("partners") || !got.Allows("vendor-pack") {
		t.Fatalf("reinstate lost the channel scope: %+v", got.Channels)
	}
	if list, err := st.ListFeedTokens(); err != nil || len(list) != 1 {
		t.Fatalf("list after reinstate: %+v err=%v", list, err)
	}

	// Member keys reinstate the same way (memberKeys covers the round trip);
	// the two credential namespaces answer to one rule.
}

// scopeDefaults: a technique with no declared scope is stored as "general".
// Scope drives who a technique is offered to, so an empty one is not a third
// option — models.ParseTechnique already defaults it, and the store has to
// agree for every other write path (federated import, admin edit, the mining
// pipeline) that builds a Technique directly.
func scopeDefaults(t *testing.T, st storage.Store) {
	c := technique("scope-new")
	c.Scope = ""
	must(t, st.UpsertTechnique(c))
	got, ok, err := st.GetTechnique("scope-new")
	must(t, err)
	if !ok || got.Scope != "general" {
		t.Fatalf("undeclared scope stored as %q, want %q", got.Scope, "general")
	}

	explicit := technique("scope-org")
	explicit.Scope = "org"
	must(t, st.UpsertTechnique(explicit))
	if got, _, _ = st.GetTechnique("scope-org"); got.Scope != "org" {
		t.Fatalf("explicit scope ignored: %q", got.Scope)
	}

	// Unlike status and channels, scope is a plain content field: a re-sync
	// that omits it writes the default rather than keeping the old value.
	resync := technique("scope-org")
	resync.Scope = ""
	must(t, st.UpsertTechnique(resync))
	if got, _, _ = st.GetTechnique("scope-org"); got.Scope != "general" {
		t.Fatalf("scope after an undeclared re-sync = %q, want %q", got.Scope, "general")
	}
}
