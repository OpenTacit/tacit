// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package contribute

import (
	"strings"

	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/screen"
)

// Provenance values for the two machine lanes: research suggestions and techniques
// distilled from observed usage.
//
// These strings reach further than this package. The Public channel asks
// federation.HumanOrigin whether a person wrote a technique, and only `curated` and
// `contributed` answer yes, so a technique carrying either value here stays out of
// the commons until someone records an approval. Change one of these and
// machine-written text starts passing a gate meant for human-written text.
const (
	ProvenanceSuggested = "suggested"
	ProvenanceObserved  = "observed"
)

// ProposedTechnique is one machine-written technique proposal, before it is screened,
// deduped, or stored. Both machine lanes produce this shape: the researcher parses
// it out of the model's JSON, and the distiller fills it in from a cluster of
// observed sketches.
type ProposedTechnique struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Recipe      string   `json:"recipe"`
	AppliesWhen string   `json:"applies_when"`
	NotWhen     string   `json:"not_when"`
	Tags        []string `json:"tags"`
	// SourceURL becomes the technique's Source: the primary source a research
	// suggestion cites, or the count of observations a distilled technique came from.
	SourceURL string `json:"source_url"`
}

// landingStore is the storage one landing pass needs.
type landingStore interface {
	GetTechnique(string) (models.Technique, bool, error)
	SetTechniqueEmbedding(string, []float32, string, int) error
	UpsertTechnique(models.Technique) error
}

// EntryStatus is the status a machine-written technique enters at: shadow when the
// registry runs automated review, the ordinary draft lane otherwise.
func EntryStatus(enterShadow bool) string {
	if enterShadow {
		return "shadow"
	}
	return "draft"
}

// Landing files the proposals of one pass. It holds the serving set the pass started
// from and grows it as techniques land, so a batch dedupes against itself as well as
// against the store.
type Landing struct {
	store      landingStore
	embedder   embed.Embedder
	status     string
	provenance string
	now        string
	existing   []models.Technique
}

// NewLanding starts a landing pass. status comes from EntryStatus; provenance is
// ProvenanceSuggested or ProvenanceObserved and reaches the stored technique
// unchanged. existing is the serving set to dedupe against. Every technique the pass
// files carries the same timestamp, so one pass reads as one event.
func NewLanding(st landingStore, embedder embed.Embedder, existing []models.Technique, status, provenance string) *Landing {
	return &Landing{
		store: st, embedder: embedder, status: status, provenance: provenance,
		now: models.Now(), existing: existing,
	}
}

// LandDraft screens one proposal, checks it is not a technique the registry already
// has, and stores it with its embedding. It reports the filed technique and true, or
// the zero technique and false when the proposal was dropped: unusable, blocked by the
// safety screen, a slug already taken, or too close to a technique in the serving set.
// A dropped proposal is not an error — a pass keeps going through the rest of its
// batch.
func (l *Landing) LandDraft(p ProposedTechnique) (models.Technique, bool, error) {
	name := strings.TrimSpace(p.Name)
	description := strings.TrimSpace(p.Description)
	recipe := strings.TrimSpace(p.Recipe)
	appliesWhen := strings.TrimSpace(p.AppliesWhen)
	notWhen := strings.TrimSpace(p.NotWhen)
	if name == "" || recipe == "" || description == "" {
		return models.Technique{}, false, nil // unusable proposal
	}
	if fs := screen.Scan(screen.Input{
		Name:        name,
		Description: description,
		Recipe:      recipe,
		Extra:       appliesWhen + "\n" + notWhen,
	}); screen.Blocks(fs) {
		return models.Technique{}, false, nil // a machine proposal is what the screen is for
	}
	technique := models.Technique{
		ID:          models.Slugify(name),
		Name:        name,
		Description: description,
		Scope:       "general",
		Status:      l.status,
		Provenance:  l.provenance,
		Version:     1,
		Recipe:      recipe,
		AppliesWhen: appliesWhen,
		NotWhen:     notWhen,
		Tags:        p.Tags,
		Source:      strings.TrimSpace(p.SourceURL),
		CreatedAt:   l.now,
		UpdatedAt:   l.now,
	}
	if _, taken, err := l.store.GetTechnique(technique.ID); err != nil {
		return models.Technique{}, false, err
	} else if taken {
		return models.Technique{}, false, nil // same slug = same move; a rephrase is not a new technique
	}
	vec := l.embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	if embed.TooSimilar(vec, l.existing) {
		return models.Technique{}, false, nil // already a technique in the serving set
	}
	if err := l.store.UpsertTechnique(technique); err != nil {
		return models.Technique{}, false, err
	}
	if err := l.store.SetTechniqueEmbedding(technique.ID, vec, l.embedder.ModelID(), l.embedder.Dim()); err != nil {
		return models.Technique{}, false, err
	}
	technique.Embedding = vec
	l.existing = append(l.existing, technique) // dedupe within the batch too
	return technique, true, nil
}
