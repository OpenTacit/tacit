// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package contribute handles member-contributed techniques.
//
// The highest-value path for org techniques — the proprietary moves an org
// cannot get from any public source (docs/concepts/concepts.md) — is members
// capturing what they discover
// in their own work, via the LLM, rather than a central author guessing. This
// is the registry-side orchestration shared by the HTTP route and the tests:
// validate -> unique id -> store as a held-out draft -> embed. Promotion
// (draft -> stable) is a separate reviewer action, so a contribution never
// reaches colleagues before review.
package contribute

import (
	"errors"
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/pkg/contracts"
)

type techniqueStore interface {
	AppendLifecycleEvent(models.LifecycleEvent) (bool, error)
	ListTechniques([]string, int) ([]models.Technique, error)
	ArchiveTechniqueVersion(models.Technique) error
	DeleteTechnique(string) (bool, error)
	GetTechnique(string) (models.Technique, bool, error)
	SetTechniqueEmbedding(string, []float32, string, int) error
	SetTechniqueStatus(string, string, string) error
	TechniqueVersions(string) ([]models.Technique, error)
	UpsertTechnique(models.Technique) error
	SetTechniqueOrigin(string, *models.FederationOrigin) error
}

// UniqueID returns a technique id not already taken — slug, then slug-2, slug-3, …
// — so a contribution never silently overwrites an existing technique.
func UniqueID(st techniqueStore, base string) (string, error) {
	id := base
	for n := 2; ; n++ {
		_, ok, err := st.GetTechnique(id)
		if err != nil {
			return "", err
		}
		if !ok {
			return id, nil
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// DuplicateError reports a contribution the registry already holds, and names
// what it holds it as.
//
// It is an error rather than a silent success because a member is waiting on
// the answer. UniqueID will happily mint slug-2 for a name already taken, and
// that is how the same technique comes to sit in the registry twice under one
// name — filed by somebody who was never told.
type DuplicateError struct {
	Match      models.Technique
	Similarity float64
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("this is already in the playbook as %q (%s)", e.Match.Name, e.Match.ID)
}

// Create validates + stores a contribution as an embedded, held-out draft.
//
// A contribution that restates a technique already here is refused and the
// existing one is named, so the member can use it, revise it, or say what is
// different. Passing "confirm_new": true in the body is that last answer: the
// member has read the match and means to file anyway.
func Create(st techniqueStore, body map[string]any, embedder embed.Embedder) (models.Technique, error) {
	technique, err := models.ParseTechniqueContribution(body)
	if err != nil {
		return models.Technique{}, err
	}
	// Embed before storing anything. The vector is wanted either way — it is
	// what a reviewer's "similar techniques" reads and what makes a promoted
	// draft serve-ready — and a refusal must not leave a row behind.
	vec := embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	if confirmed, _ := body["confirm_new"].(bool); !confirmed {
		existing, err := st.ListTechniques(nil, 0)
		if err != nil {
			return models.Technique{}, err
		}
		if match, sim, dup := embed.NewMatcher(embedder).Match(technique, vec, existing); dup {
			return models.Technique{}, &DuplicateError{Match: match, Similarity: sim}
		}
	}
	technique.ID, err = UniqueID(st, technique.ID)
	if err != nil {
		return models.Technique{}, err
	}
	if err := st.UpsertTechnique(technique); err != nil {
		return models.Technique{}, err
	}
	if err := st.SetTechniqueEmbedding(technique.ID, vec, embedder.ModelID(), embedder.Dim()); err != nil {
		return models.Technique{}, err
	}
	got, _, err := st.GetTechnique(technique.ID)
	return got, err
}

// Revise proposes a change to an existing technique as a revision draft
// (docs/design/revision-design.md): the base technique keeps serving untouched while the
// draft rides the ordinary review lane; promotion applies it onto the base.
// body carries any subset of the reviewable fields plus an optional "note"
// (the proposer's why, shown to the reviewer). found is false when no technique
// has baseID; a no-op edit or a draft base is a ValidationError.
func Revise(st techniqueStore, baseID string, body map[string]any, embedder embed.Embedder) (models.Technique, bool, error) {
	base, found, err := st.GetTechnique(baseID)
	if err != nil || !found {
		return models.Technique{}, found, err
	}
	if base.Status == "draft" {
		return models.Technique{}, true, models.ValidationErrorf(
			"%s is a draft. Edit it directly; revisions apply to techniques already in service", baseID)
	}
	draft := base
	if !models.ApplyTechniqueEditFromMap(&draft, body) {
		return models.Technique{}, true, models.ValidationErrorf("no changes: every supplied field matches the current technique")
	}
	draft.ID, err = UniqueID(st, fmt.Sprintf("%s@%d", base.ID, base.Version+1))
	if err != nil {
		return models.Technique{}, true, err
	}
	draft.Status = "draft"
	draft.Supersedes = base.ID
	draft.BaseVersion = base.Version
	draft.Version = base.Version + 1
	if note, ok := body["note"].(string); ok {
		draft.RevisionNote = strings.TrimSpace(note)
	}
	// A held-out proposal, not a serving technique: no publication, no decay
	// state, and its own timestamps. Embedded like any contribution so the
	// reviewer's similar-techniques view works.
	draft.Channels = nil
	draft.Origin = nil
	draft.Embedding, draft.EmbeddingModel, draft.EmbeddingDim = nil, "", 0
	draft.DecaySignal, draft.DecayChecked = 0, ""
	now := models.Now()
	draft.CreatedAt, draft.UpdatedAt = now, now
	if err := st.UpsertTechnique(draft); err != nil {
		return models.Technique{}, true, err
	}
	vec := embedder.Embed([]string{embed.TechniqueText(draft)})[0]
	if err := st.SetTechniqueEmbedding(draft.ID, vec, embedder.ModelID(), embedder.Dim()); err != nil {
		return models.Technique{}, true, err
	}
	got, _, err := st.GetTechnique(draft.ID)
	return got, true, err
}

// Promote is the reviewer action: flip a draft's status (default -> stable,
// making it retrievable). Promoting a revision draft to stable instead
// applies it onto its base technique — the base keeps its id and outcome history,
// gains the revised fields and a version bump, and the draft is removed.
// Returns the updated (base, for revisions) technique, or false if no such technique.
func Promote(st techniqueStore, techniqueID, status string, embedder embed.Embedder) (models.Technique, bool, error) {
	if status == "" {
		status = "stable"
	}
	if status != "stable" && status != "draft" && status != "retired" && status != "shadow" {
		return models.Technique{}, false, errors.New("status must be stable, draft, retired, or shadow")
	}
	technique, ok, err := st.GetTechnique(techniqueID)
	if err != nil || !ok {
		return models.Technique{}, false, err
	}
	if technique.Supersedes != "" && status == "stable" {
		noteApproved(st, technique)
		return applyRevision(st, technique, embedder)
	}
	if err := st.SetTechniqueStatus(techniqueID, status, models.Now()); err != nil {
		return models.Technique{}, false, err
	}
	if status == "stable" {
		noteApproved(st, technique)
	}
	technique, _, err = st.GetTechnique(techniqueID)
	return technique, err == nil, err
}

// noteApproved records that a PERSON accepted this technique.
//
// It exists because nothing else distinguishes a reviewer's promotion from the
// auto-promote gate's: both leave a technique at `stable`, and only the gate wrote a
// lifecycle event, so a machine-authored technique that graduated with no manual review
// was indistinguishable from one a reviewer read line by line. That difference
// does not matter much while a technique only serves this org — and matters entirely
// at the moment the technique can be published to the internet under the org's own
// signing key, which is what the Public channel does
// (docs/distribution/global-access-plan.md).
//
// Recorded as an event rather than a field on the technique because approval is
// something that HAPPENED, at a time, and the lifecycle log is where this
// codebase already keeps those. It also means human promotions finally appear in
// the Events view, which until now showed only the machine's half of curation.
func noteApproved(st techniqueStore, technique models.Technique) {
	at := models.Now()
	_, _ = st.AppendLifecycleEvent(models.LifecycleEvent{
		EventID:       contracts.DeterministicLifecycleEventID(technique.ID, "approved", at),
		TechniqueID:   technique.ID,
		TechniqueName: technique.Name,
		Kind:          "approved",
		Provenance:    technique.Provenance,
		Reason:        "a reviewer promoted it to serving",
		CreatedAt:     at,
	})
}

// applyRevision lands an approved revision draft on its base technique.
func applyRevision(st techniqueStore, draft models.Technique, embedder embed.Embedder) (models.Technique, bool, error) {
	base, ok, err := st.GetTechnique(draft.Supersedes)
	if err != nil {
		return models.Technique{}, false, err
	}
	if !ok {
		return models.Technique{}, false, fmt.Errorf(
			"base technique %s no longer exists. Reject this revision", draft.Supersedes)
	}
	if base.Version != draft.BaseVersion {
		return models.Technique{}, false, fmt.Errorf(
			"stale revision: %s is at version %d. This revision uses version %d. Create a revision from the current technique",
			base.ID, base.Version, draft.BaseVersion)
	}
	// Snapshot the outgoing version first: prior versions stay viewable.
	if err := st.ArchiveTechniqueVersion(base); err != nil {
		return models.Technique{}, false, err
	}
	base.Name = draft.Name
	base.Description = draft.Description
	base.Recipe = draft.Recipe
	base.AppliesWhen = draft.AppliesWhen
	base.NotWhen = draft.NotWhen
	base.BeforeAfter = draft.BeforeAfter
	base.Tags = draft.Tags
	// The support matrix travels with a revision. Without this a revision whose
	// whole content is a support row — which is what a measured proposal is —
	// promotes to nothing, silently.
	base.SupportMatrix = draft.SupportMatrix
	base.Origin = draft.Origin
	base.Version++
	base.UpdatedAt = models.Now()
	if err := st.UpsertTechnique(base); err != nil {
		return models.Technique{}, false, err
	}
	if err := st.SetTechniqueOrigin(base.ID, draft.Origin); err != nil {
		return models.Technique{}, false, err
	}
	// Re-embed: the fit conditions are retrieval inputs.
	vec := embedder.Embed([]string{embed.TechniqueText(base)})[0]
	if err := st.SetTechniqueEmbedding(base.ID, vec, embedder.ModelID(), embedder.Dim()); err != nil {
		return models.Technique{}, false, err
	}
	if _, err := st.DeleteTechnique(draft.ID); err != nil {
		return models.Technique{}, false, err
	}
	got, _, err := st.GetTechnique(base.ID)
	return got, err == nil, err
}

// RevertTo makes a technique's current content equal to a prior version n by landing
// a NEW version whose fields are copied from that archived version. History is
// append-only, exactly as a promote or edit leaves it: the outgoing version is
// archived first, so the id, created_at, outcome history, and lineage are kept
// and the revert is itself reversible (a later revert can undo it). It is not a
// rollback of the version counter — reverting v5 to v2 produces v6 with v2's
// content, and the record of v3–v5 stays viewable in the history.
//
// Errors (leaving the technique untouched) if the technique is a pre-review draft, if n is
// the current version, or if n is not among the technique's archived versions — so a
// drive-by request cannot point the revert at an arbitrary version number.
func RevertTo(st techniqueStore, techniqueID string, n int, embedder embed.Embedder) (models.Technique, bool, error) {
	technique, ok, err := st.GetTechnique(techniqueID)
	if err != nil || !ok {
		return models.Technique{}, false, err
	}
	if technique.Status == "draft" {
		return models.Technique{}, false, fmt.Errorf("technique %s is a draft: draft edits keep no version history to revert", techniqueID)
	}
	if n == technique.Version {
		return models.Technique{}, false, fmt.Errorf("technique %s is already at version %d", techniqueID, n)
	}
	versions, err := st.TechniqueVersions(techniqueID)
	if err != nil {
		return models.Technique{}, false, err
	}
	var target models.Technique
	found := false
	for _, v := range versions {
		if v.Version == n {
			target, found = v, true
			break
		}
	}
	if !found {
		return models.Technique{}, false, fmt.Errorf("technique %s has no archived version %d to revert to", techniqueID, n)
	}
	// Snapshot the outgoing version first: the revert stays reversible, and the
	// version it replaced keeps its place in the history.
	if err := st.ArchiveTechniqueVersion(technique); err != nil {
		return models.Technique{}, false, err
	}
	// The same seven content fields a promoted revision lands (applyRevision);
	// id, status, created_at, decay, and outcome lineage are deliberately left.
	technique.Name = target.Name
	technique.Description = target.Description
	technique.Recipe = target.Recipe
	technique.AppliesWhen = target.AppliesWhen
	technique.NotWhen = target.NotWhen
	technique.BeforeAfter = target.BeforeAfter
	technique.Tags = target.Tags
	technique.Version++
	technique.UpdatedAt = models.Now()
	if err := st.UpsertTechnique(technique); err != nil {
		return models.Technique{}, false, err
	}
	// Re-embed: the fit conditions are retrieval inputs.
	vec := embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	if err := st.SetTechniqueEmbedding(technique.ID, vec, embedder.ModelID(), embedder.Dim()); err != nil {
		return models.Technique{}, false, err
	}
	got, _, err := st.GetTechnique(technique.ID)
	return got, true, err
}
