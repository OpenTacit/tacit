// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package contribute

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/store"
)

func setup(t *testing.T) (*store.Store, embed.Embedder) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	embedder, _ := embed.New("hashing-v1", 64)
	return st, embedder
}

func body(name string) map[string]any {
	return map[string]any{
		"name": name, "description": "how we query the warehouse",
		"recipe": "use @warehouse query ...", "scope": "org",
		"segment": map[string]any{"team": "revops"},
	}
}

func TestContributionHeldOutOfRetrievalUntilPromoted(t *testing.T) {
	st, embedder := setup(t)
	technique, err := Create(st, body("Warehouse move"), embedder)
	if err != nil {
		t.Fatal(err)
	}
	if technique.Status != "draft" || technique.Provenance != "contributed" {
		t.Fatalf("not a held-out draft: %+v", technique)
	}
	if len(technique.Embedding) == 0 {
		t.Fatal("draft not embedded (must be serve-ready on promotion)")
	}
	if cands, _ := st.CandidateTechniques(); len(cands) != 0 {
		t.Fatal("draft reached retrieval before review")
	}

	promoted, found, err := Promote(st, technique.ID, "", embedder)
	if err != nil || !found || promoted.Status != "stable" {
		t.Fatalf("promotion failed: %v %v %+v", err, found, promoted)
	}
	if cands, _ := st.CandidateTechniques(); len(cands) != 1 {
		t.Fatal("promoted technique not retrievable")
	}
}

func TestUniqueIDNeverOverwrites(t *testing.T) {
	st, embedder := setup(t)
	first, _ := Create(st, body("Same Name"), embedder)
	second, _ := Create(st, body("Same Name"), embedder)
	if first.ID == second.ID {
		t.Fatalf("duplicate id: %s", second.ID)
	}
	if second.ID != first.ID+"-2" {
		t.Fatalf("suffix scheme changed: %s", second.ID)
	}
}

func TestPromoteValidation(t *testing.T) {
	st, embedder := setup(t)
	if _, _, err := Promote(st, "whatever", "bogus", embedder); err == nil {
		t.Fatal("invalid status accepted")
	}
	if _, found, _ := Promote(st, "missing", "stable", embedder); found {
		t.Fatal("promoted a technique that doesn't exist")
	}
}

func TestRejectRetiresDraft(t *testing.T) {
	st, embedder := setup(t)
	technique, _ := Create(st, body("Bad idea"), embedder)
	retired, found, err := Promote(st, technique.ID, "retired", embedder)
	if err != nil || !found || retired.Status != "retired" {
		t.Fatalf("reject failed: %v %+v", err, retired)
	}
	if cands, _ := st.CandidateTechniques(); len(cands) != 0 {
		t.Fatal("retired technique retrievable")
	}
}

// stableTechnique creates and promotes a contribution so tests have a technique in
// service to revise.
func stableTechnique(t *testing.T, st *store.Store, embedder embed.Embedder, name string) string {
	t.Helper()
	technique, err := Create(st, body(name), embedder)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(st, technique.ID, "stable", embedder); err != nil {
		t.Fatal(err)
	}
	return technique.ID
}

func TestReviseHoldsBaseAndAppliesOnPromote(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move")

	draft, found, err := Revise(st, id, map[string]any{
		"not_when": "when the plugin is installed", "note": "trigger too broad"}, embedder)
	if err != nil || !found {
		t.Fatalf("revise failed: %v %v", err, found)
	}
	if draft.Status != "draft" || draft.Supersedes != id || draft.BaseVersion != 1 {
		t.Fatalf("not a pinned revision draft: %+v", draft)
	}
	if draft.ID != id+"@2" {
		t.Fatalf("revision id scheme changed: %s", draft.ID)
	}
	if draft.RevisionNote != "trigger too broad" {
		t.Fatalf("note lost: %q", draft.RevisionNote)
	}
	if len(draft.Embedding) == 0 {
		t.Fatal("revision draft not embedded")
	}

	// The base keeps serving its old text while the revision is in review.
	base, _, _ := st.GetTechnique(id)
	if base.NotWhen == "when the plugin is installed" || base.Version != 1 {
		t.Fatalf("base changed before review: %+v", base)
	}
	if cands, _ := st.CandidateTechniques(); len(cands) != 1 {
		t.Fatalf("revision draft leaked into retrieval")
	}

	applied, found, err := Promote(st, draft.ID, "stable", embedder)
	if err != nil || !found {
		t.Fatalf("apply failed: %v", err)
	}
	if applied.ID != id || applied.Version != 2 || applied.NotWhen != "when the plugin is installed" {
		t.Fatalf("revision not applied onto base: %+v", applied)
	}
	if applied.Supersedes != "" || applied.RevisionNote != "" {
		t.Fatalf("revision bookkeeping leaked onto the base: %+v", applied)
	}
	if applied.CreatedAt != base.CreatedAt {
		t.Fatal("base identity (created_at) not preserved")
	}
	if _, stillThere, _ := st.GetTechnique(draft.ID); stillThere {
		t.Fatal("applied revision draft not removed")
	}
	// The outgoing v1 is archived so prior versions stay viewable.
	vs, err := st.TechniqueVersions(id)
	if err != nil || len(vs) != 1 || vs[0].Version != 1 {
		t.Fatalf("prior version not archived: %v %+v", err, vs)
	}
	if vs[0].NotWhen == "when the plugin is installed" {
		t.Fatal("archive holds the new text, not the outgoing version")
	}
}

func TestMemberRevisionClearsFederationOrigin(t *testing.T) {
	st, embedder := setup(t)
	base := models.Technique{ID: "ext/provider/move", Name: "Move", Description: "D", Scope: "general",
		Status: "stable", Provenance: "federated", Version: 1, Recipe: "old",
		CreatedAt: models.Now(), UpdatedAt: models.Now(),
		Origin: &models.FederationOrigin{ProviderID: "https://provider.example", ProviderKey: "ed25519:key",
			EntryID: "https://provider.example/techniques/move", ChannelID: "general",
			ContentHash: "sha256:old", ImportedAt: "2026-07-01T00:00:00Z"}}
	if err := st.UpsertTechnique(base); err != nil {
		t.Fatal(err)
	}
	draft, _, err := Revise(st, base.ID, map[string]any{"recipe": "local rewrite"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Origin != nil {
		t.Fatalf("member revision retained origin: %+v", draft.Origin)
	}
	applied, _, err := Promote(st, draft.ID, "stable", embedder)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Origin != nil {
		t.Fatalf("local rewrite still attributes upstream: %+v", applied.Origin)
	}
}

func TestUpstreamRevisionReplacesFederationOrigin(t *testing.T) {
	st, embedder := setup(t)
	old := &models.FederationOrigin{ProviderID: "https://provider.example", ProviderKey: "ed25519:key",
		EntryID: "https://provider.example/techniques/move", ChannelID: "general",
		ContentHash: "sha256:old", ImportedAt: "2026-07-01T00:00:00Z"}
	base := models.Technique{ID: "ext/provider/move", Name: "Move", Description: "D", Scope: "general",
		Status: "stable", Provenance: "federated", Version: 1, Recipe: "old", Origin: old,
		CreatedAt: models.Now(), UpdatedAt: models.Now()}
	if err := st.UpsertTechnique(base); err != nil {
		t.Fatal(err)
	}
	next := *old
	next.ContentHash = "sha256:new"
	draft := base
	draft.ID, draft.Status, draft.Recipe = base.ID+"@upstream", "draft", "new"
	draft.Supersedes, draft.BaseVersion, draft.Origin = base.ID, base.Version, &next
	if err := st.UpsertTechnique(draft); err != nil {
		t.Fatal(err)
	}
	applied, _, err := Promote(st, draft.ID, "stable", embedder)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Origin == nil || applied.Origin.ContentHash != next.ContentHash || applied.Origin.ImportedAt != old.ImportedAt {
		t.Fatalf("upstream origin not applied: %+v", applied.Origin)
	}
}

func TestRevertToPriorVersionLandsNewVersion(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move") // v1, recipe from body()
	v1Recipe := "use @warehouse query ..."

	// Land v2 with a changed recipe (revise + promote archives the outgoing v1).
	draft, _, err := Revise(st, id, map[string]any{"recipe": "use @lakehouse query ..."}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(st, draft.ID, "stable", embedder); err != nil {
		t.Fatal(err)
	}
	base, _, _ := st.GetTechnique(id)
	if base.Version != 2 || base.Recipe != "use @lakehouse query ..." {
		t.Fatalf("setup: expected v2 with the new recipe, got %+v", base)
	}

	// Revert to v1: a NEW version (v3) whose content equals v1 — not a rollback
	// of the counter.
	reverted, ok, err := RevertTo(st, id, 1, embedder)
	if err != nil || !ok {
		t.Fatalf("revert failed: %v %v", err, ok)
	}
	if reverted.Version != 3 {
		t.Fatalf("revert must land a new version, not roll the counter back: got v%d", reverted.Version)
	}
	if reverted.Recipe != v1Recipe {
		t.Fatalf("reverted content is not v1's: %q", reverted.Recipe)
	}
	if reverted.ID != id || reverted.CreatedAt != base.CreatedAt {
		t.Fatalf("revert must preserve technique identity (id, created_at): %+v", reverted)
	}
	if len(reverted.Embedding) == 0 {
		t.Fatal("reverted technique not re-embedded (fit conditions are retrieval inputs)")
	}
	// History is append-only: the version revert replaced (v2) and the older v1
	// are both archived, newest first.
	vs, err := st.TechniqueVersions(id)
	if err != nil || len(vs) != 2 || vs[0].Version != 2 || vs[1].Version != 1 {
		t.Fatalf("history not preserved as [v2, v1]: %v %+v", err, vs)
	}
	if vs[0].Recipe != "use @lakehouse query ..." {
		t.Fatal("archive should hold the version revert replaced, not the reverted content")
	}
}

func TestRevertToRejectsBadTargets(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move") // v1, no archived versions

	// The current version: nothing to revert.
	if _, _, err := RevertTo(st, id, 1, embedder); err == nil {
		t.Fatal("revert to the current version should error")
	}
	// A version that was never archived — a drive-by request can't point the
	// revert at an arbitrary number.
	if _, _, err := RevertTo(st, id, 99, embedder); err == nil {
		t.Fatal("revert to a nonexistent version should error")
	}
	// A pre-review draft keeps no served history.
	draft, err := Create(st, body("Draft move"), embedder)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RevertTo(st, draft.ID, 1, embedder); err == nil {
		t.Fatal("revert of a draft should error")
	}
}

func TestStaleRevisionRefused(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move")

	draft, _, err := Revise(st, id, map[string]any{"recipe": "new recipe"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	// The base moves before the revision is reviewed (e.g. a direct admin
	// edit bumped the version, or a racing revision was promoted first).
	base, _, _ := st.GetTechnique(id)
	base.Version++
	if err := st.UpsertTechnique(base); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Promote(st, draft.ID, "stable", embedder); err == nil ||
		!strings.Contains(err.Error(), "stale revision") {
		t.Fatalf("stale revision applied anyway: %v", err)
	}
	got, _, _ := st.GetTechnique(id)
	if got.Recipe == "new recipe" {
		t.Fatal("stale revision mutated the base")
	}
}

func TestReviseValidation(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move")

	if _, found, _ := Revise(st, "missing", map[string]any{"recipe": "x"}, embedder); found {
		t.Fatal("revised a technique that doesn't exist")
	}
	// No-op: every supplied field matches the current technique.
	if _, _, err := Revise(st, id, map[string]any{"recipe": "use @warehouse query ..."}, embedder); err == nil {
		t.Fatal("no-op revision accepted")
	}
	// A draft base is edited directly, not revised.
	draft, _ := Create(st, body("Held out"), embedder)
	if _, _, err := Revise(st, draft.ID, map[string]any{"recipe": "x"}, embedder); err == nil {
		t.Fatal("revision of a draft accepted")
	}
}

func TestConcurrentRevisionsFirstPromoteWins(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move")

	first, _, err := Revise(st, id, map[string]any{"recipe": "recipe A"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Revise(st, id, map[string]any{"recipe": "recipe B"}, embedder)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("concurrent revisions collided: %s", first.ID)
	}
	if _, _, err := Promote(st, first.ID, "stable", embedder); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Promote(st, second.ID, "stable", embedder); err == nil {
		t.Fatal("second revision applied over the first without a fresh look")
	}
	got, _, _ := st.GetTechnique(id)
	if got.Recipe != "recipe A" || got.Version != 2 {
		t.Fatalf("first-promote-wins broken: %+v", got)
	}
}

// A revision whose whole content is a support_matrix row — which is what a
// measured proposal is (feedback.ProposeSupportRows) — must actually land. Left
// out of the fields a promotion copies, it promoted to nothing and said so
// nowhere.
func TestPromoteAppliesTheSupportMatrix(t *testing.T) {
	st, embedder := setup(t)
	id := stableTechnique(t, st, embedder, "Warehouse move")

	base, _, _ := st.GetTechnique(id)
	draft := base
	draft.ID = id + "@support"
	draft.Status = "draft"
	draft.Supersedes = id
	draft.BaseVersion = base.Version
	draft.Version = base.Version + 1
	draft.SupportMatrix = []map[string]any{
		{"model": "anthropic/claude-sonnet-5", "supported": true, "verified": "2026-09"},
	}
	draft.RevisionNote = "measured"
	if err := st.UpsertTechnique(draft); err != nil {
		t.Fatal(err)
	}
	landed, found, err := Promote(st, draft.ID, "stable", embedder)
	if err != nil || !found {
		t.Fatalf("promote failed: %v %v", err, found)
	}
	if len(landed.SupportMatrix) != 1 {
		t.Fatalf("the support row was dropped on promotion: %+v", landed.SupportMatrix)
	}
	if got, _ := landed.SupportMatrix[0]["model"].(string); got != "anthropic/claude-sonnet-5" {
		t.Fatalf("wrong row landed: %+v", landed.SupportMatrix)
	}
	if landed.ID != id || landed.Version != base.Version+1 {
		t.Fatalf("the revision did not land on the base: %s v%d", landed.ID, landed.Version)
	}
}
