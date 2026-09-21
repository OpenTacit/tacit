// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// Outcome rollups (models.Outcome) — the recomputed funnel per
// (technique, segment) — and the table counts the health probe reads.

import (
	"database/sql"
	"errors"

	"github.com/opentacit/tacit/internal/registry/models"
)

// ReplaceOutcomes swaps in a recomputed rollup set in one transaction, so
// readers never see a half-replaced set. Dialect.PreReplaceOutcomes runs first
// where a backend needs one: Postgres takes an EXCLUSIVE table lock, which
// serializes concurrent replacers (two uncoordinated recomputes would
// otherwise risk a delete/insert deadlock) while still allowing reads — the
// scheduler already elects one runner via the advisory lock, but manual
// recomputes can race. SQLite needs no lock: its one-connection pool
// serializes every writer in this process, and that backend is single-instance
// by contract.
func (s *Store) ReplaceOutcomes(rows []models.Outcome) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if s.d.PreReplaceOutcomes != "" {
		if _, err := s.txExec(tx, s.d.PreReplaceOutcomes); err != nil {
			return err
		}
	}
	if _, err := s.txExec(tx, `DELETE FROM technique_outcomes`); err != nil {
		return err
	}
	stmt, err := s.txPrepare(tx, `INSERT INTO technique_outcomes (technique_id, segment_key, shown,
		adopted, helped, dismissed, weighted_adopted, weighted_helped, weighted_dismissed,
		adoption_rate, helped_rate, sample_size, last_updated)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, o := range rows {
		if _, err := stmt.Exec(o.TechniqueID, o.SegmentKey, o.Shown, o.Adopted, o.Helped,
			o.Dismissed, o.WeightedAdopted, o.WeightedHelped, o.WeightedDismissed,
			nullFloat(o.AdoptionRate), nullFloat(o.HelpedRate),
			o.SampleSize, o.LastUpdated); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}

const outcomeColumns = `technique_id, segment_key, shown, adopted, helped, dismissed,
	weighted_adopted, weighted_helped, weighted_dismissed,
	adoption_rate, helped_rate, sample_size, last_updated`

func scanOutcome(row Scanner) (models.Outcome, error) {
	var o models.Outcome
	var ar, hr sql.NullFloat64
	err := row.Scan(&o.TechniqueID, &o.SegmentKey, &o.Shown, &o.Adopted, &o.Helped,
		&o.Dismissed, &o.WeightedAdopted, &o.WeightedHelped, &o.WeightedDismissed,
		&ar, &hr, &o.SampleSize, &o.LastUpdated)
	if err != nil {
		return models.Outcome{}, err
	}
	if ar.Valid {
		o.AdoptionRate = &ar.Float64
	}
	if hr.Valid {
		o.HelpedRate = &hr.Float64
	}
	return o, nil
}

// GetOutcome returns the rollup for (technique, segmentKey), or false.
func (s *Store) GetOutcome(techniqueID, segmentKey string) (models.Outcome, bool, error) {
	row := s.queryRow(`SELECT `+outcomeColumns+` FROM technique_outcomes
		WHERE technique_id=? AND segment_key=?`, techniqueID, segmentKey)
	o, err := scanOutcome(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Outcome{}, false, nil
	}
	if err != nil {
		return models.Outcome{}, false, err
	}
	return o, true, nil
}

// OutcomesForSegment returns a segment's rollups, adopted descending.
func (s *Store) OutcomesForSegment(segmentKey string) ([]models.Outcome, error) {
	rows, err := s.query(`SELECT `+outcomeColumns+` FROM technique_outcomes
		WHERE segment_key=? ORDER BY adopted DESC, technique_id`, segmentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Outcome
	for rows.Next() {
		o, err := scanOutcome(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Counts reports table sizes for /v1/health and the dashboard (and doubles as
// the DB liveness probe: any failure surfaces as a health 503). One SELECT of
// scalar subqueries, so a health probe costs a single round trip and every
// count is read at the same instant. "drafts" is review-queue depth, for
// status lines.
func (s *Store) Counts() (map[string]int, error) {
	var techniques, drafts, events, outcomes, facts, lifecycle int
	if err := s.queryRow(`SELECT
		(SELECT COUNT(*) FROM techniques),
		(SELECT COUNT(*) FROM techniques WHERE status = 'draft'),
		(SELECT COUNT(*) FROM feedback_events),
		(SELECT COUNT(*) FROM technique_outcomes),
		(SELECT COUNT(*) FROM audit_facts),
		(SELECT COUNT(*) FROM lifecycle_events)`).Scan(
		&techniques, &drafts, &events, &outcomes, &facts, &lifecycle); err != nil {
		return nil, err
	}
	return map[string]int{
		"techniques":  techniques,
		"drafts":      drafts,
		"events":      events,
		"outcomes":    outcomes,
		"audit_facts": facts,
		"lifecycle":   lifecycle,
	}, nil
}
