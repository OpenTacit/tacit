// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// The three append-only logs: feedback events (the per-interaction funnel),
// audit facts (what one interaction WAS), and lifecycle events (curation).

import (
	"database/sql"
	"errors"

	"github.com/opentacit/tacit/internal/registry/models"
)

// InsertEvent appends one event; false for a replayed event_id. The conflict
// target makes idempotency correct across instances sharing one database.
func (s *Store) InsertEvent(e models.FeedbackEvent) (bool, error) {
	value, err := JSON(e.Value)
	if err != nil {
		return false, err
	}
	segment, err := JSON(e.Segment)
	if err != nil {
		return false, err
	}
	res, err := s.exec(`
		INSERT INTO feedback_events (event_id, audit_id, technique_id, technique_version,
			stage, value, segment, task_type, rank_shown, confidence, delivery,
			source, similarity, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (event_id) DO NOTHING`,
		e.EventID, e.AuditID, e.TechniqueID, e.TechniqueVersion, e.Stage,
		value, segment, e.TaskType, e.RankShown, e.Confidence, e.Delivery,
		e.Source, e.Similarity, e.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// AllEvents returns the feedback event log, oldest first — the whole log, or
// from an RFC3339 timestamp when since is non-empty.
func (s *Store) AllEvents(since string) ([]models.FeedbackEvent, error) {
	query := `SELECT event_id, audit_id, technique_id, technique_version, stage,
		value, segment, task_type, rank_shown, confidence, delivery,
		source, similarity, created_at
		FROM feedback_events`
	var args []any
	if since != "" {
		query += ` WHERE created_at >= ?`
		args = append(args, since)
	}
	query += ` ORDER BY created_at, event_id`
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.FeedbackEvent
	for rows.Next() {
		var e models.FeedbackEvent
		var value, segment []byte
		if err := rows.Scan(&e.EventID, &e.AuditID, &e.TechniqueID, &e.TechniqueVersion,
			&e.Stage, &value, &segment, &e.TaskType, &e.RankShown, &e.Confidence,
			&e.Delivery, &e.Source, &e.Similarity, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err := ReadJSON(value, &e.Value); err != nil {
			return nil, err
		}
		if err := ReadJSON(segment, &e.Segment); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- audit facts --------------------------------------------------------------

// AppendAuditFact records one interaction's context, keyed by audit_id.
// Idempotent on the primary key, so a retried evidence request can't
// double-count one interaction — the same contract InsertEvent honours.
func (s *Store) AppendAuditFact(f models.AuditFact) (bool, error) {
	segment, err := JSON(f.Segment)
	if err != nil {
		return false, err
	}
	toolsUsed, err := JSON(f.ToolsUsed)
	if err != nil {
		return false, err
	}
	toolsAbsent, err := JSON(f.ToolsAbsent)
	if err != nil {
		return false, err
	}
	resources, err := JSON(f.Resources)
	if err != nil {
		return false, err
	}
	techniquesOffered, err := JSON(f.TechniquesOffered)
	if err != nil {
		return false, err
	}
	res, err := s.exec(`
		INSERT INTO audit_facts (audit_id, session_hash, created_at, segment, task_type, model, domain,
			harness, surface, skill_level, tools_used, tools_absent, resources,
			techniques_offered, thin)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (audit_id) DO NOTHING`,
		f.AuditID, f.SessionHash, f.CreatedAt, segment, f.TaskType, f.Model, f.Domain, f.Harness, f.Surface,
		f.SkillLevel, toolsUsed, toolsAbsent, resources, techniquesOffered, f.Thin)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// EnrichAuditFact fills in what could not be observed (tools_absent), keyed by
// audit id. found is false for an unknown audit — an enrichment with nothing to
// attach to is dropped rather than creating a headless fact. Only the inferred
// column is written; an enrichment can never rewrite what was observed.
func (s *Store) EnrichAuditFact(e models.AuditFactEnrichment) (bool, error) {
	if len(e.ToolsAbsent) == 0 {
		return false, nil // nothing to add
	}
	toolsAbsent, err := JSON(e.ToolsAbsent)
	if err != nil {
		return false, err
	}
	res, err := s.exec(
		`UPDATE audit_facts SET tools_absent = ? WHERE audit_id = ?`,
		toolsAbsent, e.AuditID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// auditFactCols is the column list both fact reads select, in the order
// scanAuditFact expects — one place to change when the fact grows a field.
const auditFactCols = `audit_id, session_hash, created_at, segment, task_type, model, domain, harness,
	surface, skill_level, tools_used, tools_absent, resources, techniques_offered, thin`

// scanAuditFact reads one row and rehydrates its JSON columns.
func scanAuditFact(sc Scanner) (models.AuditFact, error) {
	var f models.AuditFact
	var segment, toolsUsed, toolsAbsent, resources, techniquesOffered []byte
	if err := sc.Scan(&f.AuditID, &f.SessionHash, &f.CreatedAt, &segment, &f.TaskType, &f.Model, &f.Domain,
		&f.Harness, &f.Surface, &f.SkillLevel, &toolsUsed, &toolsAbsent, &resources,
		&techniquesOffered, &f.Thin); err != nil {
		return f, err
	}
	for _, j := range []struct {
		raw []byte
		dst any
	}{{segment, &f.Segment}, {toolsUsed, &f.ToolsUsed}, {toolsAbsent, &f.ToolsAbsent},
		{resources, &f.Resources}, {techniquesOffered, &f.TechniquesOffered}} {
		if err := ReadJSON(j.raw, j.dst); err != nil {
			return f, err
		}
	}
	return f, nil
}

// AuditFact returns one fact by audit id; found is false for an unknown audit.
func (s *Store) AuditFact(auditID string) (models.AuditFact, bool, error) {
	f, err := scanAuditFact(s.queryRow(
		`SELECT `+auditFactCols+` FROM audit_facts WHERE audit_id = ?`, auditID))
	if errors.Is(err, sql.ErrNoRows) {
		return models.AuditFact{}, false, nil
	}
	if err != nil {
		return models.AuditFact{}, false, err
	}
	return f, true, nil
}

// AuditFacts returns the fact log, optionally filtered to created_at >= since.
func (s *Store) AuditFacts(since string) ([]models.AuditFact, error) {
	query := `SELECT ` + auditFactCols + ` FROM audit_facts`
	var args []any
	if since != "" {
		query += ` WHERE created_at >= ?`
		args = append(args, since)
	}
	query += ` ORDER BY created_at, audit_id`
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.AuditFact
	for rows.Next() {
		f, err := scanAuditFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// --- lifecycle events --------------------------------------------------------

// AppendLifecycleEvent records one curation moment, keyed by event_id.
// Idempotent on the primary key — a recompute cycle re-observing a transition
// returns false rather than double-logging it.
func (s *Store) AppendLifecycleEvent(e models.LifecycleEvent) (bool, error) {
	res, err := s.exec(`
		INSERT INTO lifecycle_events (event_id, technique_id, technique_name, kind, provenance, reason, created_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (event_id) DO NOTHING`,
		e.EventID, e.TechniqueID, e.TechniqueName, e.Kind, e.Provenance, e.Reason, e.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// LifecycleEvents returns the curation log, optionally filtered to
// created_at >= since.
func (s *Store) LifecycleEvents(since string) ([]models.LifecycleEvent, error) {
	query := `SELECT event_id, technique_id, technique_name, kind, provenance, reason, created_at
		FROM lifecycle_events`
	var args []any
	if since != "" {
		query += ` WHERE created_at >= ?`
		args = append(args, since)
	}
	query += ` ORDER BY created_at, event_id`
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.LifecycleEvent
	for rows.Next() {
		var e models.LifecycleEvent
		if err := rows.Scan(&e.EventID, &e.TechniqueID, &e.TechniqueName, &e.Kind,
			&e.Provenance, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
