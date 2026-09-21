// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// Sketches (models.Sketch) — the scrubbed situation-and-move records the
// mining layer clusters (docs/mining/mining-design.md source 2).

import (
	"encoding/json"

	"github.com/opentacit/tacit/internal/registry/models"
)

const sketchColumns = `sketch_id, technique_id, session_hash, trigger_text, move,
	harness, task_type, segment, tools, created_at`

// InsertSketch records one adoption sketch; false for a replayed sketch_id.
func (s *Store) InsertSketch(sk models.Sketch) (bool, error) {
	segment, err := JSON(sk.Segment)
	if err != nil {
		return false, err
	}
	tools, err := JSON(sk.Tools)
	if err != nil {
		return false, err
	}
	if sk.SketchID == "" {
		return false, nil
	}
	res, err := s.exec(`
		INSERT INTO sketches (`+sketchColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (sketch_id) DO NOTHING`,
		sk.SketchID, sk.TechniqueID, sk.SessionHash, sk.Trigger,
		sk.Move, sk.Harness, sk.TaskType, segment, tools, sk.CreatedAt)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// SketchesForTechnique returns a technique's sketches, newest first.
func (s *Store) SketchesForTechnique(techniqueID string) ([]models.Sketch, error) {
	return s.scanSketches(`SELECT `+sketchColumns+`
		FROM sketches WHERE technique_id = ? ORDER BY created_at DESC, sketch_id DESC`, techniqueID)
}

// TechniquelessSketches returns sketches with no technique_id, newest first.
func (s *Store) TechniquelessSketches(limit int) ([]models.Sketch, error) {
	q := `SELECT ` + sketchColumns + `
		FROM sketches WHERE technique_id = '' ORDER BY created_at DESC, sketch_id DESC`
	if limit > 0 {
		return s.scanSketches(q+` LIMIT ?`, limit)
	}
	return s.scanSketches(q)
}

func (s *Store) scanSketches(query string, args ...any) ([]models.Sketch, error) {
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Sketch
	for rows.Next() {
		var sk models.Sketch
		var segment, tools []byte
		if err := rows.Scan(&sk.SketchID, &sk.TechniqueID, &sk.SessionHash,
			&sk.Trigger, &sk.Move, &sk.Harness, &sk.TaskType, &segment, &tools,
			&sk.CreatedAt); err != nil {
			return nil, err
		}
		// Unreadable JSON leaves the field zero rather than failing the read:
		// a sketch is evidence, and a malformed column must not hide the rest.
		if len(segment) > 0 {
			_ = json.Unmarshal(segment, &sk.Segment)
		}
		if len(tools) > 0 {
			_ = json.Unmarshal(tools, &sk.Tools)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}
