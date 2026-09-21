// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package storage defines the registry's data-layer seam.
//
// Store is the contract every backend satisfies (docs/design/postgres-plan.md). The
// embedded file store (internal/registry/store) is the zero-dependency
// default; a Postgres backend implements the same interface for shared /
// multi-instance deployments. Retrieval, feedback, contribution, jobs, and the
// web surface depend only on this interface, so a backend swap is a config
// change, not a redesign — the same migration posture docs/design/architecture.md
// always documented.
//
// Behavioral contract (enforced by storage/conformance):
//   - UpsertTechnique preserves runtime state on conflict: created_at, decay
//     signal/checked, and the embedding survive a content re-sync.
//   - InsertEvent is idempotent on event_id; false means a replay.
//   - AppendAuditFact is idempotent on audit_id; false means a replay.
//   - SetDecay flips status only between stable/decayed (drafts and retired
//     techniques keep their status; the signal still records).
//   - CandidateTechniques returns stable|mined, non-decayed, embedded techniques.
//   - ShadowCandidateTechniques returns shadow, non-decayed, embedded techniques only.
//   - OutcomesForSegment orders by adopted descending.
//   - SetTechniqueEmbedding, SetTechniqueStatus, and SetDecay refuse an unknown
//     id with an error wrapping ErrNotFound.
package storage

import (
	"errors"

	"github.com/opentacit/tacit/internal/registry/models"
)

// ErrNotFound is what a write against an id the store has never seen wraps.
// Callers test it with errors.Is instead of reading the message, so "the
// technique was deleted under me" reads apart from a backend failure. Only the
// three technique-state writers below report it; the credential writers treat
// an unknown id as a no-op by design.
var ErrNotFound = errors.New("not found")

// Store is the one queryable source of truth for registry state. It is the
// union of the seven role interfaces below, so every backend still implements
// exactly the same set of methods; the roles exist so that a consumer needing
// one part of the store can ask for that part alone (jobs.RunEmbed takes a
// TechniqueStore, not the whole thing) and a test double has less to fake.
type Store interface {
	TechniqueStore
	EventStore
	AuditStore
	SketchStore
	OutcomeStore
	CredentialStore
	Health
}

// TechniqueStore is the playbook itself: the techniques, their retrieval
// state, and the archive of what earlier versions said.
type TechniqueStore interface {
	// UpsertTechnique inserts or updates a technique by id. On update the content
	// fields change but created_at, decay state, and the embedding are
	// preserved, so a git re-sync can't clobber runtime state.
	UpsertTechnique(c models.Technique) error
	// SetTechniqueOrigin sets or clears local federation lineage explicitly.
	SetTechniqueOrigin(id string, origin *models.FederationOrigin) error
	// GetTechnique returns a technique by id; ok is false when it doesn't exist.
	GetTechnique(id string) (c models.Technique, ok bool, err error)
	// DeleteTechnique removes a technique and its outcome rollups; ok is false when it
	// didn't exist. The event log is append-only and keeps its history.
	DeleteTechnique(id string) (ok bool, err error)
	// ListTechniques returns techniques ordered by id, optionally filtered by status
	// and limited (limit<=0 means no limit).
	ListTechniques(statuses []string, limit int) ([]models.Technique, error)
	// CandidateTechniques returns the techniques eligible for retrieval.
	CandidateTechniques() ([]models.Technique, error)
	// ShadowCandidateTechniques returns techniques under evaluation (status=shadow) with
	// an embedding — scored for relevance but never surfaced
	// (docs/learning/validation-without-review.md). Kept separate from
	// CandidateTechniques so the serving set and the shadow set can never mix.
	ShadowCandidateTechniques() ([]models.Technique, error)
	// SetTechniqueEmbedding stores a technique's vector and the model that produced it.
	// An unknown id is an error wrapping ErrNotFound, never a silent no-op.
	SetTechniqueEmbedding(id string, vec []float32, model string, dim int) error
	// TechniquesNeedingEmbedding returns techniques missing a vector or embedded with a
	// different (stale) model.
	TechniquesNeedingEmbedding(model string) ([]models.Technique, error)
	// SetTechniqueStatus flips a technique's lifecycle status.
	// An unknown id is an error wrapping ErrNotFound, never a silent no-op.
	SetTechniqueStatus(id, status, updatedAt string) error
	// SetDecay flags (or clears) a technique's decay signal.
	// An unknown id is an error wrapping ErrNotFound, never a silent no-op.
	SetDecay(id string, decayed bool, checkedAt string) error

	// ArchiveTechniqueVersion snapshots a technique's pre-change state so prior
	// versions stay viewable on demand (docs/design/revision-design.md). Append-only;
	// the embedding is dropped from the snapshot (history answers "what did
	// it say", not retrieval).
	ArchiveTechniqueVersion(c models.Technique) error
	// TechniqueVersions returns a technique's archived prior versions, newest first.
	TechniqueVersions(id string) ([]models.Technique, error)
}

// EventStore is the two append-only logs of what happened: the per-interaction
// funnel and the curation moments in a technique's own life.
type EventStore interface {
	// InsertEvent appends one feedback event; inserted is false for a
	// replayed event_id (idempotent ingest).
	InsertEvent(e models.FeedbackEvent) (inserted bool, err error)
	// AllEvents returns the event log, optionally filtered to
	// created_at >= since (RFC3339 strings order lexicographically).
	AllEvents(since string) ([]models.FeedbackEvent, error)

	// AppendLifecycleEvent records one curation moment about a technique —
	// discovered, promoted, retired, or decayed (models.LifecycleKinds) — the
	// org-level counterpart to InsertEvent's per-interaction funnel. Cohort-free
	// by construction: a technique's lifecycle carries no member, cohort, or session.
	// Idempotent on event_id: inserted is false for a replay, so a recompute
	// cycle that re-observes a transition can't double-log it.
	AppendLifecycleEvent(e models.LifecycleEvent) (inserted bool, err error)
	// LifecycleEvents returns the curation log, optionally filtered to
	// created_at >= since — the same cursor contract as AllEvents.
	LifecycleEvents(since string) ([]models.LifecycleEvent, error)
}

// AuditStore is the fact log: what each interaction WAS, which the event log
// joins to for how it turned out.
type AuditStore interface {
	// AppendAuditFact records what one interaction was, keyed by audit_id —
	// the join to the feedback events that audit emits (the learning layer's
	// substrate, docs/learning/synthesis-design.md). Idempotent on audit_id: inserted
	// is false when the audit was already recorded, so a retried evidence
	// request can't double-count one interaction.
	AppendAuditFact(f models.AuditFact) (inserted bool, err error)
	// AuditFacts returns the fact log, optionally filtered to
	// created_at >= since (RFC3339 strings order lexicographically) — the same
	// cursor contract as AllEvents.
	AuditFacts(since string) ([]models.AuditFact, error)
	// AuditFact returns one fact by audit id. found is false for an unknown
	// audit, which is not an error: a producer may post events for a retrieval
	// this registry never recorded. Ingest uses it to check an incoming event
	// against what the interaction actually WAS (feedback.Ingest).
	AuditFact(auditID string) (f models.AuditFact, found bool, err error)
	// EnrichAuditFact adds model-inferred fields to a fact already recorded.
	// found is false when the audit is unknown — an enrichment with nothing to
	// attach to is dropped rather than creating a headless fact. It never
	// overwrites an observed field; it only fills what could not be observed.
	EnrichAuditFact(e models.AuditFactEnrichment) (found bool, err error)
}

// SketchStore is the adoption-sketch intake and the reads the mining side
// makes of it.
type SketchStore interface {
	// InsertSketch records one adoption sketch (scrubbed situation + move at
	// a first-adoption moment — docs/mining/mining-design.md source 2, landed
	// via the registry's own /v1/sketches intake). Idempotent on sketch_id:
	// inserted is false for a replay.
	InsertSketch(sk models.Sketch) (inserted bool, err error)
	// SketchesForTechnique returns a technique's sketches, newest first.
	SketchesForTechnique(techniqueID string) ([]models.Sketch, error)
	// TechniquelessSketches returns sketches with no technique_id — novel observed
	// moves not yet tied to a technique — newest first, up to limit (<=0 = no limit).
	// The clustering input for observed-technique discovery
	// (docs/learning/observed-technique-discovery.md).
	TechniquelessSketches(limit int) ([]models.Sketch, error)
}

// OutcomeStore is the recomputed funnel per (technique, segment) — written
// whole by the recompute job, read by everything that ranks.
type OutcomeStore interface {
	// ReplaceOutcomes swaps in a freshly recomputed rollup set atomically.
	ReplaceOutcomes(rows []models.Outcome) error
	// GetOutcome returns the rollup for (technique, segmentKey).
	GetOutcome(techniqueID, segmentKey string) (o models.Outcome, ok bool, err error)
	// OutcomesForSegment returns a segment's rollups, adopted descending.
	OutcomesForSegment(segmentKey string) ([]models.Outcome, error)
}

// CredentialStore is the two credential namespaces — member keys and
// federation feed tokens — kept apart on purpose: they authorize disjoint
// surfaces. Only hashes are stored; a secret never is.
type CredentialStore interface {
	// InsertMemberKey records a minted access credential (hash only; the
	// secret is never stored — models.MemberKey).
	InsertMemberKey(k models.MemberKey) error
	// MemberKeyByHash resolves an authenticating secret's hash; ok is false
	// when no key matches. Revoked keys ARE returned — the auth layer decides.
	MemberKeyByHash(hash string) (k models.MemberKey, ok bool, err error)
	// ListMemberKeys returns every key, newest first.
	ListMemberKeys() ([]models.MemberKey, error)
	// SetMemberKeyRevoked stamps (or, with "", clears) a key's revocation.
	SetMemberKeyRevoked(id, revokedAt string) error
	// TouchMemberKey updates a key's last-seen timestamp.
	TouchMemberKey(id, lastSeen string) error
	// InsertFeedToken records a minted federation-feed credential (hash only;
	// models.FeedToken). A separate namespace from member keys by design.
	InsertFeedToken(t models.FeedToken) error
	// FeedTokenByHash resolves a presented feed secret's hash.
	FeedTokenByHash(hash string) (t models.FeedToken, ok bool, err error)
	// ListFeedTokens returns every feed token, newest first.
	ListFeedTokens() ([]models.FeedToken, error)
	// SetFeedTokenRevoked stamps (or, with "", clears) a token's revocation.
	SetFeedTokenRevoked(id, revokedAt string) error
	// TouchFeedToken updates a token's last-used timestamp — access metadata for
	// the admin ("is this peer still reading?"), never analytics.
	TouchFeedToken(id, lastUsed string) error

	// DeleteMemberKey removes a key outright (no error for unknown ids). The
	// web layer only offers this for already-revoked keys — deletion is the
	// cleanup step, revocation the safety step.
	DeleteMemberKey(id string) error
}

// Health is what a caller asks of the store as a whole rather than of any one
// table: is it answering, and let it go.
type Health interface {
	// Counts reports table sizes for /v1/health and the dashboard.
	Counts() (map[string]int, error)
	// Close releases the backend's resources.
	Close() error
}

// RecomputeLocker is optionally implemented by shared backends (Postgres) so
// that when N instances share one database, only one runs the periodic rollup
// recompute at a time. The jobs scheduler probes for it; single-instance
// backends (the file store) simply don't implement it.
type RecomputeLocker interface {
	// TryRecomputeLock returns ok=false without blocking when another
	// instance holds the lock; on ok=true the caller must call release.
	TryRecomputeLock() (release func(), ok bool, err error)
}
