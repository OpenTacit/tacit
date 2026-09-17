// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package store is the registry's embedded, dependency-free data layer.
//
// This project eliminates third-party dependencies (docs/design/architecture.md),
// so instead of an embedded database the three "tables" live behind this
// package: techniques (JSON file), feedback events (append-only JSONL — the
// source of truth for outcomes), and outcome rollups (derived, held in memory
// only and recomputed from the event log). Semantics match the SQL backends
// exactly (enforced by storage/conformance): technique upsert preserves
// created_at/decay/embedding fields, event insert is idempotent on event_id,
// rollups are replace-all.
//
// At pilot scale (hundreds–few thousand rows) this is comfortably fast; the
// thin interface keeps a later swap to SQLite/Postgres an engine change, not a
// redesign.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/embed"
)

// Store satisfies storage.Store (checked at compile time).
var _ storage.Store = (*Store)(nil)

// Store is the one queryable source of truth.
type Store struct {
	mu           sync.RWMutex
	dir          string
	techniques   map[string]models.Technique
	events       []models.FeedbackEvent
	eventIDs     map[string]bool
	outcomes     map[string]models.Outcome // key: techniqueID + "\x00" + segmentKey
	facts        []models.AuditFact
	factIdx      map[string]int // audit_id -> position in facts
	keys         map[string]models.MemberKey
	feedTokens   map[string]models.FeedToken
	sketches     []models.Sketch
	sketchIDs    map[string]bool
	lifecycle    []models.LifecycleEvent
	lifecycleIDs map[string]bool
}

func outcomeKey(techniqueID, segmentKey string) string { return techniqueID + "\x00" + segmentKey }

// Open loads (or initializes) a store under dir.
func Open(dir string) (*Store, error) {
	s := &Store{
		dir:          dir,
		techniques:   map[string]models.Technique{},
		eventIDs:     map[string]bool{},
		outcomes:     map[string]models.Outcome{},
		factIdx:      map[string]int{},
		keys:         map[string]models.MemberKey{},
		feedTokens:   map[string]models.FeedToken{},
		sketchIDs:    map[string]bool{},
		lifecycleIDs: map[string]bool{},
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := s.loadTechniques(); err != nil {
		return nil, err
	}
	if err := s.loadEvents(); err != nil {
		return nil, err
	}
	if err := s.loadFacts(); err != nil {
		return nil, err
	}
	if err := s.loadEnrichments(); err != nil {
		return nil, err
	}
	if err := s.loadFeedTokens(); err != nil {
		return nil, err
	}
	if err := s.loadKeys(); err != nil {
		return nil, err
	}
	if err := s.loadSketches(); err != nil {
		return nil, err
	}
	if err := s.loadLifecycle(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) techniquesPath() string { return filepath.Join(s.dir, "techniques.json") }
func (s *Store) eventsPath() string     { return filepath.Join(s.dir, "events.jsonl") }
func (s *Store) versionsPath() string   { return filepath.Join(s.dir, "technique_versions.jsonl") }
func (s *Store) factsPath() string      { return filepath.Join(s.dir, "audit_facts.jsonl") }
func (s *Store) enrichPath() string     { return filepath.Join(s.dir, "audit_fact_enrichments.jsonl") }
func (s *Store) lifecyclePath() string  { return filepath.Join(s.dir, "lifecycle_events.jsonl") }

func (s *Store) loadTechniques() error {
	raw, err := os.ReadFile(s.techniquesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var techniques []models.Technique
	if err := json.Unmarshal(raw, &techniques); err != nil {
		return fmt.Errorf("corrupt techniques.json: %w", err)
	}
	for _, c := range techniques {
		s.techniques[c.ID] = c
	}
	return nil
}

func (s *Store) loadEvents() error {
	f, err := os.Open(s.eventsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e models.FeedbackEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("corrupt events.jsonl: %w", err)
		}
		if !s.eventIDs[e.EventID] {
			s.eventIDs[e.EventID] = true
			s.events = append(s.events, e)
		}
	}
	return sc.Err()
}

func (s *Store) loadFacts() error {
	f, err := os.Open(s.factsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var fact models.AuditFact
		if err := json.Unmarshal(line, &fact); err != nil {
			return fmt.Errorf("corrupt audit_facts.jsonl: %w", err)
		}
		if _, seen := s.factIdx[fact.AuditID]; !seen {
			s.factIdx[fact.AuditID] = len(s.facts)
			s.facts = append(s.facts, fact)
		}
	}
	return sc.Err()
}

// loadEnrichments replays the inferred-field log over the facts, in order. The
// fact log stays the untouched record of what was observed; this is what a model
// later said about it.
func (s *Store) loadEnrichments() error {
	f, err := os.Open(s.enrichPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e models.AuditFactEnrichment
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("corrupt audit_fact_enrichments.jsonl: %w", err)
		}
		if i, ok := s.factIdx[e.AuditID]; ok {
			applyEnrichment(&s.facts[i], e)
		}
	}
	return sc.Err()
}

func (s *Store) loadLifecycle() error {
	f, err := os.Open(s.lifecyclePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e models.LifecycleEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("corrupt lifecycle_events.jsonl: %w", err)
		}
		if !s.lifecycleIDs[e.EventID] {
			s.lifecycleIDs[e.EventID] = true
			s.lifecycle = append(s.lifecycle, e)
		}
	}
	return sc.Err()
}

// saveTechniques writes the full technique set atomically (temp file + rename).
// Callers hold s.mu.
func (s *Store) saveTechniques() error {
	techniques := make([]models.Technique, 0, len(s.techniques))
	for _, c := range s.techniques {
		techniques = append(techniques, c)
	}
	sort.Slice(techniques, func(i, j int) bool { return techniques[i].ID < techniques[j].ID })
	raw, err := json.MarshalIndent(techniques, "", " ")
	if err != nil {
		return err
	}
	// 0600, like keys.json and feed_tokens.json beside it. This file holds the
	// org's recipes — the playbook itself — and the registry is the only thing
	// that reads it. It was 0644, which made it readable by every account on the
	// host for no reason anyone chose.
	//
	// A file written before this narrows on its next write, since the atomic
	// replace applies the mode to the new file. A backup or sync tool running as
	// another user is the one thing this breaks, and such a tool should be
	// reading the registry's API or running as its user anyway.
	return fsx.WriteFileAtomic(s.techniquesPath(), raw, 0o600)
}

func (s *Store) appendEvent(e models.FeedbackEvent) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.eventsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// --- techniques -----------------------------------------------------------------

// UpsertTechnique inserts or updates a technique. On update the content fields change
// but created_at, decay state, and the embedding are preserved, so a git
// re-sync can't clobber runtime state (enforced by storage/conformance).
func (s *Store) UpsertTechnique(c models.Technique) error {
	// The one tag gate. Every technique write — curated sync, contribution, LLM
	// suggestion, federated import, admin edit — lands here, so this is where
	// the vocabulary is kept from growing variant spellings of tags it already
	// has (models.NormalizeTags). Enforced for every backend by the storage
	// conformance suite.
	c.Tags = models.NormalizeTags(c.Tags)
	if c.Scope == "" {
		// Scope decides who a technique is offered to, so an unset one is not a
		// third option. models.ParseTechnique defaults it; the store defaults it
		// again for the write paths that build a Technique directly (federated
		// import, admin edit, mining), the same way the SQL backends do.
		c.Scope = "general"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.techniques[c.ID]; ok {
		if c.Status == "" {
			// lifecycle state survives content syncs that don't declare one —
			// an API retirement must not be resurrected by the next boot's
			// techniques/*.md sync (same posture as Channels below)
			c.Status = old.Status
		}
		c.CreatedAt = old.CreatedAt
		c.DecaySignal = old.DecaySignal
		c.DecayChecked = old.DecayChecked
		// The vector survives a sync, but only while it still describes the
		// technique. Carrying it across a text change made the registry serve
		// one wording and match on another — invisibly, because nothing errors
		// and the served text looks right. embed.TechniqueText is the single
		// definition of what gets embedded, so asking it both times means a
		// field added there is covered without touching this.
		if embed.TechniqueText(old) == embed.TechniqueText(c) {
			c.Embedding = old.Embedding
			c.EmbeddingModel = old.EmbeddingModel
			c.EmbeddingDim = old.EmbeddingDim
		} else {
			// Left empty on purpose: TechniquesNeedingEmbedding picks it up on
			// the next embed pass, which is the one place that knows the model.
			c.Embedding, c.EmbeddingModel, c.EmbeddingDim = nil, "", 0
		}
		if c.Channels == nil {
			// publication state survives content syncs that don't mention it;
			// unpublish explicitly with an empty non-nil slice (SetChannels)
			c.Channels = old.Channels
		}
		if c.Origin == nil {
			c.Origin = old.Origin
		}
	}
	if c.Status == "" {
		c.Status = "stable" // a brand-new technique with no declared status serves
	}
	s.techniques[c.ID] = c
	return s.saveTechniques()
}

// SetTechniqueOrigin makes a lineage change explicit; nil clears it.
func (s *Store) SetTechniqueOrigin(id string, origin *models.FederationOrigin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.techniques[id]
	if !ok {
		return fmt.Errorf("set technique origin %s: %w", id, storage.ErrNotFound)
	}
	c.Origin = origin
	s.techniques[id] = c
	return s.saveTechniques()
}

// ArchiveTechniqueVersion snapshots a technique's pre-change state (append-only JSONL,
// like the event log). The embedding is dropped: history answers "what did
// it say", not retrieval.
func (s *Store) ArchiveTechniqueVersion(c models.Technique) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Embedding, c.EmbeddingModel, c.EmbeddingDim = nil, "", 0
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.versionsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// TechniqueVersions returns a technique's archived prior versions, newest first. A
// linear file scan — history is an on-demand view, not a hot path, and the
// file only grows by one line per reviewed change.
func (s *Store) TechniqueVersions(id string) ([]models.Technique, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, err := os.Open(s.versionsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []models.Technique
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var c models.Technique
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("corrupt technique_versions.jsonl: %w", err)
		}
		if c.ID == id {
			out = append(out, c)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // appended oldest-first
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// DeleteTechnique removes a technique and its outcome rollups (events stay: the log
// is append-only history).
func (s *Store) DeleteTechnique(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.techniques[id]; !ok {
		return false, nil
	}
	delete(s.techniques, id)
	for key := range s.outcomes {
		if strings.HasPrefix(key, id+"\x00") {
			delete(s.outcomes, key)
		}
	}
	return true, s.saveTechniques()
}

// GetTechnique returns a technique by id, or false.
func (s *Store) GetTechnique(id string) (models.Technique, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.techniques[id]
	return c, ok, nil
}

// ListTechniques returns techniques ordered by id, optionally filtered by status and
// limited.
func (s *Store) ListTechniques(statuses []string, limit int) ([]models.Technique, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	want := map[string]bool{}
	for _, st := range statuses {
		want[st] = true
	}
	var out []models.Technique
	for _, c := range s.techniques {
		if len(want) > 0 && !want[c.Status] {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CandidateTechniques returns the techniques eligible for retrieval: live status, not
// decayed, with an embedding.
func (s *Store) CandidateTechniques() ([]models.Technique, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Technique
	for _, c := range s.techniques {
		if (c.Status == "stable" || c.Status == "mined") && c.DecaySignal == 0 && len(c.Embedding) > 0 {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ShadowCandidateTechniques returns techniques under evaluation (status=shadow), not
// decayed, with an embedding — scored for relevance but never surfaced
// (docs/learning/validation-without-review.md). Held separate from
// CandidateTechniques so the serving set and the shadow set can never mix.
func (s *Store) ShadowCandidateTechniques() ([]models.Technique, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Technique
	for _, c := range s.techniques {
		if c.Status == "shadow" && c.DecaySignal == 0 && len(c.Embedding) > 0 {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetTechniqueEmbedding stores a technique's vector and the model that produced it.
func (s *Store) SetTechniqueEmbedding(id string, vec []float32, model string, dim int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.techniques[id]
	if !ok {
		return fmt.Errorf("no technique %q: %w", id, storage.ErrNotFound)
	}
	c.Embedding = vec
	c.EmbeddingModel = model
	c.EmbeddingDim = dim
	s.techniques[id] = c
	return s.saveTechniques()
}

// TechniquesNeedingEmbedding returns techniques missing a vector or embedded with a
// different (stale) model.
func (s *Store) TechniquesNeedingEmbedding(model string) ([]models.Technique, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Technique
	for _, c := range s.techniques {
		if len(c.Embedding) == 0 || c.EmbeddingModel != model {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SetTechniqueStatus flips a technique's lifecycle status.
func (s *Store) SetTechniqueStatus(id, status, updatedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.techniques[id]
	if !ok {
		return fmt.Errorf("no technique %q: %w", id, storage.ErrNotFound)
	}
	c.Status = status
	c.UpdatedAt = updatedAt
	s.techniques[id] = c
	return s.saveTechniques()
}

// SetDecay flags (or clears) a technique's decay signal; status flips between
// stable/decayed only when it currently holds one of those (a draft or retired
// technique is left alone) — the same rule as the SQL backends' CASE.
func (s *Store) SetDecay(id string, decayed bool, checkedAt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.techniques[id]
	if !ok {
		return fmt.Errorf("no technique %q: %w", id, storage.ErrNotFound)
	}
	if decayed {
		c.DecaySignal = 1
	} else {
		c.DecaySignal = 0
	}
	c.DecayChecked = checkedAt
	if c.Status == "stable" || c.Status == "decayed" {
		if decayed {
			c.Status = "decayed"
		} else {
			c.Status = "stable"
		}
	}
	s.techniques[id] = c
	return s.saveTechniques()
}

// --- feedback events ---------------------------------------------------------

// InsertEvent appends one feedback event; returns false for a replayed
// event_id, so an at-least-once client retrying a batch is idempotent.
func (s *Store) InsertEvent(e models.FeedbackEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.eventIDs[e.EventID] {
		return false, nil
	}
	if err := s.appendEvent(e); err != nil {
		return false, err
	}
	s.eventIDs[e.EventID] = true
	s.events = append(s.events, e)
	return true, nil
}

// AppendAuditFact records one interaction's context, keyed by audit_id.
// Idempotent: a replayed audit (the hook agent retrying an evidence request)
// returns false rather than logging the same interaction twice — the same
// contract InsertEvent honours for event_id.
func (s *Store) AppendAuditFact(f models.AuditFact) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.factIdx[f.AuditID]; seen {
		return false, nil
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return false, err
	}
	fh, err := os.OpenFile(s.factsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	if _, err := fh.Write(append(raw, '\n')); err != nil {
		return false, err
	}
	s.factIdx[f.AuditID] = len(s.facts)
	s.facts = append(s.facts, f)
	return true, nil
}

// EnrichAuditFact fills in what could not be observed (tools_absent), keyed by
// audit id. Enrichments live in their OWN append-only log rather than rewriting
// audit_facts.jsonl: the fact log is the record of what we saw, and a model's
// later opinion does not get to edit history in place. Both logs are replayed
// and merged at startup, in order.
func (s *Store) EnrichAuditFact(e models.AuditFactEnrichment) (bool, error) {
	if len(e.ToolsAbsent) == 0 {
		// Nothing to add, so nothing found: the caller reads the bool as "a
		// fact changed", and writing an empty line to the log would only make
		// the replay do the same no-op again (applyEnrichment ignores it).
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.factIdx[e.AuditID]
	if !ok {
		return false, nil // no fact to attach to; dropping it beats a headless one
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	fh, err := os.OpenFile(s.enrichPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	if _, err := fh.Write(append(raw, '\n')); err != nil {
		return false, err
	}
	applyEnrichment(&s.facts[i], e)
	return true, nil
}

// applyEnrichment merges inferred fields onto a fact. It only ever fills the
// fields a model may speak to — an enrichment can never rewrite what was
// observed.
func applyEnrichment(f *models.AuditFact, e models.AuditFactEnrichment) {
	if len(e.ToolsAbsent) > 0 {
		f.ToolsAbsent = e.ToolsAbsent
	}
}

// AuditFact returns one fact by audit id, through the same index AppendAuditFact
// keeps for idempotency — so the ingest-time check costs a map hit, not a scan.
func (s *Store) AuditFact(auditID string) (models.AuditFact, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.factIdx[auditID]
	if !ok {
		return models.AuditFact{}, false, nil
	}
	return s.facts[i], true, nil
}

// AuditFacts returns the fact log ordered by (created_at, audit_id), optionally
// filtered to created_at >= since.
func (s *Store) AuditFacts(since string) ([]models.AuditFact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.AuditFact
	for _, f := range s.facts {
		if since == "" || f.CreatedAt >= since {
			out = append(out, f)
		}
	}
	sortLog(out, func(f models.AuditFact) (string, string) { return f.CreatedAt, f.AuditID })
	return out, nil
}

// AllEvents returns the event log ordered by (created_at, event_id), optionally
// filtered to created_at >= since.
func (s *Store) AllEvents(since string) ([]models.FeedbackEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.FeedbackEvent
	for _, e := range s.events {
		if since == "" || e.CreatedAt >= since {
			out = append(out, e)
		}
	}
	sortLog(out, func(e models.FeedbackEvent) (string, string) { return e.CreatedAt, e.EventID })
	return out, nil
}

// sortLog puts an append-only log in the order every backend reads it:
// created_at ascending, then the record id. Arrival order is not that order —
// a producer replaying a backlog writes older rows last — and a caller paging
// with a `since` cursor would skip whatever arrived out of sequence. RFC3339
// timestamps compare as strings, which is also how the SQL backends' TEXT
// columns collate.
func sortLog[T any](rows []T, key func(T) (createdAt, id string)) {
	sort.Slice(rows, func(i, j int) bool {
		ai, ii := key(rows[i])
		aj, ij := key(rows[j])
		if ai != aj {
			return ai < aj
		}
		return ii < ij
	})
}

// --- lifecycle events --------------------------------------------------------

// AppendLifecycleEvent records one curation moment, keyed by event_id.
// Idempotent: a replayed id (a recompute cycle re-observing the same
// transition) returns false rather than logging it twice.
func (s *Store) AppendLifecycleEvent(e models.LifecycleEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lifecycleIDs[e.EventID] {
		return false, nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	fh, err := os.OpenFile(s.lifecyclePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	if _, err := fh.Write(append(raw, '\n')); err != nil {
		return false, err
	}
	s.lifecycleIDs[e.EventID] = true
	s.lifecycle = append(s.lifecycle, e)
	return true, nil
}

// LifecycleEvents returns the curation log ordered by (created_at, event_id),
// optionally filtered to created_at >= since.
func (s *Store) LifecycleEvents(since string) ([]models.LifecycleEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.LifecycleEvent
	for _, e := range s.lifecycle {
		if since == "" || e.CreatedAt >= since {
			out = append(out, e)
		}
	}
	sortLog(out, func(e models.LifecycleEvent) (string, string) { return e.CreatedAt, e.EventID })
	return out, nil
}

// --- outcome rollups ---------------------------------------------------------

// ReplaceOutcomes swaps in a freshly recomputed rollup set (derived data —
// in memory only; recomputed from the event log at startup).
func (s *Store) ReplaceOutcomes(rows []models.Outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes = make(map[string]models.Outcome, len(rows))
	for _, o := range rows {
		s.outcomes[outcomeKey(o.TechniqueID, o.SegmentKey)] = o
	}
	return nil
}

// GetOutcome returns the rollup for (technique, segmentKey), or false.
func (s *Store) GetOutcome(techniqueID, segmentKey string) (models.Outcome, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.outcomes[outcomeKey(techniqueID, segmentKey)]
	return o, ok, nil
}

// OutcomesForSegment returns a segment's rollups ordered by adopted desc.
func (s *Store) OutcomesForSegment(segmentKey string) ([]models.Outcome, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Outcome
	for _, o := range s.outcomes {
		if o.SegmentKey == segmentKey {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Adopted != out[j].Adopted {
			return out[i].Adopted > out[j].Adopted
		}
		return out[i].TechniqueID < out[j].TechniqueID
	})
	return out, nil
}

// Counts reports table sizes for /v1/health and the dashboard. "drafts" is the
// review-queue depth (techniques awaiting promotion), surfaced in status lines.
func (s *Store) Counts() (map[string]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	drafts := 0
	for _, c := range s.techniques {
		if c.Status == "draft" {
			drafts++
		}
	}
	return map[string]int{
		"techniques":  len(s.techniques),
		"events":      len(s.events),
		"outcomes":    len(s.outcomes),
		"drafts":      drafts,
		"audit_facts": len(s.facts),
		"lifecycle":   len(s.lifecycle),
	}, nil
}

// Close releases the store. The file store holds no open handles between
// operations, so this is a no-op; it exists to satisfy storage.Store.
func (s *Store) Close() error { return nil }
