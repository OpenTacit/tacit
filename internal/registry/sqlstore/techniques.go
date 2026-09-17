// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// Techniques (models.Technique) — the playbook itself, plus its version archive.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/storage"
	"github.com/opentacit/tacit/pkg/embed"
)

// UpsertTechnique inserts or updates a technique. On conflict the content fields update
// but created_at, decay state, and the embedding are preserved — a git
// re-sync can't clobber runtime state (enforced by storage/conformance).
// Placeholders are positional, so the status the CASE tests and the status it
// assigns are two separate arguments carrying the same value.
func (s *Store) UpsertTechnique(c models.Technique) error {
	// The one tag gate — see store.Store.UpsertTechnique and models.NormalizeTags.
	// Enforced for every backend by the storage conformance suite.
	c.Tags = models.NormalizeTags(c.Tags)
	tags, err := JSON(c.Tags)
	if err != nil {
		return err
	}
	taskTypes, err := JSON(c.TaskTypes)
	if err != nil {
		return err
	}
	triggers, err := JSON(c.Triggers)
	if err != nil {
		return err
	}
	supportMatrix, err := JSON(c.SupportMatrix)
	if err != nil {
		return err
	}
	// A nil Channels means "this sync doesn't mention publication state", and the
	// COALESCE below must then keep what's stored. It has to arrive as an SQL
	// NULL to do that: json.Marshal of a nil slice yields the four bytes `null`,
	// which is a *JSON* null and satisfies COALESCE — silently wiping a technique's
	// federation channels on every git re-sync. Unpublishing is an empty non-nil
	// slice (SetChannels), which marshals to `[]` and correctly overwrites.
	var channels []byte
	if c.Channels != nil {
		var err error
		if channels, err = JSON(c.Channels); err != nil {
			return err
		}
	}
	var origin []byte
	if c.Origin != nil {
		if origin, err = JSON(c.Origin); err != nil {
			return err
		}
	}
	// Does this write change what was embedded? The vector is kept across a
	// sync, but only while it still describes the technique: carrying it over a
	// text change made the registry serve one wording and match on another,
	// with nothing in any log to say so. The comparison asks embed.TechniqueText
	// both times rather than listing columns here, so a field added there is
	// covered without a second edit in SQL.
	//
	// The extra read is cheap and rare — upserts happen on a sync of a few
	// dozen files, or on one person saving one edit.
	keepEmbedding := false
	if stored, ok, err := s.GetTechnique(c.ID); err != nil {
		return err
	} else if ok {
		keepEmbedding = embed.TechniqueText(stored) == embed.TechniqueText(c)
	}
	_, err = s.exec(`
		INSERT INTO techniques (`+TechniqueColumns+`)
		VALUES (?,?,?,?,COALESCE(NULLIF(?,''),'stable'),?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET
			name=EXCLUDED.name, description=EXCLUDED.description, scope=EXCLUDED.scope,
			status=CASE WHEN ? = '' THEN techniques.status ELSE ? END,
			provenance=EXCLUDED.provenance, version=EXCLUDED.version,
			recipe=EXCLUDED.recipe, before_after=EXCLUDED.before_after, tags=EXCLUDED.tags,
			task_types=EXCLUDED.task_types, triggers=EXCLUDED.triggers,
			applies_when=EXCLUDED.applies_when, not_when=EXCLUDED.not_when,
			support_matrix=EXCLUDED.support_matrix, shipped=EXCLUDED.shipped,
			channels=COALESCE(EXCLUDED.channels, techniques.channels),
			origin=COALESCE(EXCLUDED.origin, techniques.origin),
			source=EXCLUDED.source, updated_at=EXCLUDED.updated_at,
			supersedes=EXCLUDED.supersedes, base_version=EXCLUDED.base_version,
			revision_note=EXCLUDED.revision_note,
			embedding=CASE WHEN ? THEN techniques.embedding ELSE NULL END,
			embedding_model=CASE WHEN ? THEN techniques.embedding_model ELSE '' END,
			embedding_dim=CASE WHEN ? THEN techniques.embedding_dim ELSE 0 END`,
		c.ID, c.Name, c.Description, orDefault(c.Scope, "general"), c.Status, c.Provenance,
		c.Version, c.Recipe, c.BeforeAfter, tags, taskTypes, triggers, c.AppliesWhen,
		c.NotWhen, supportMatrix, c.Shipped, c.DecaySignal, c.DecayChecked,
		PackVector(c.Embedding), c.EmbeddingModel, c.EmbeddingDim, channels, c.Source,
		c.CreatedAt, c.UpdatedAt, c.Supersedes, c.BaseVersion, c.RevisionNote, origin,
		c.Status, c.Status,
		keepEmbedding, keepEmbedding, keepEmbedding)
	return err
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// SetTechniqueOrigin makes a lineage change explicit; nil clears it.
func (s *Store) SetTechniqueOrigin(id string, origin *models.FederationOrigin) error {
	raw, err := JSON(origin)
	if err != nil {
		return err
	}
	res, err := s.exec(`UPDATE techniques SET origin=? WHERE id=?`, raw, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("set technique origin %s: %w", id, storage.ErrNotFound)
	}
	return nil
}

// ArchiveTechniqueVersion snapshots a technique's pre-change state. The embedding is
// dropped: history answers "what did it say", not retrieval.
func (s *Store) ArchiveTechniqueVersion(c models.Technique) error {
	c.Embedding, c.EmbeddingModel, c.EmbeddingDim = nil, "", 0
	snap, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.exec(`INSERT INTO technique_versions (technique_id, version, snapshot, archived_at)
		VALUES (?,?,?,?)`, c.ID, c.Version, snap, models.Now())
	return err
}

// TechniqueVersions returns a technique's archived prior versions, newest first.
func (s *Store) TechniqueVersions(id string) ([]models.Technique, error) {
	rows, err := s.query(`SELECT snapshot FROM technique_versions
		WHERE technique_id=? ORDER BY archived_at DESC, version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Technique
	for rows.Next() {
		var snap []byte
		if err := rows.Scan(&snap); err != nil {
			return nil, err
		}
		var c models.Technique
		if err := json.Unmarshal(snap, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteTechnique removes a technique and its outcome rollups (events stay: the log
// is append-only history).
func (s *Store) DeleteTechnique(id string) (bool, error) {
	if _, err := s.exec(`DELETE FROM technique_outcomes WHERE technique_id=?`, id); err != nil {
		return false, err
	}
	res, err := s.exec(`DELETE FROM techniques WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// GetTechnique returns a technique by id, or false.
func (s *Store) GetTechnique(id string) (models.Technique, bool, error) {
	row := s.queryRow(`SELECT `+TechniqueColumns+` FROM techniques WHERE id=?`, id)
	c, err := ScanTechnique(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Technique{}, false, nil
	}
	if err != nil {
		return models.Technique{}, false, err
	}
	return c, true, nil
}

func (s *Store) queryTechniques(query string, args ...any) ([]models.Technique, error) {
	rows, err := s.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Technique
	for rows.Next() {
		c, err := ScanTechnique(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListTechniques returns techniques ordered by id, optionally filtered and limited.
func (s *Store) ListTechniques(statuses []string, limit int) ([]models.Technique, error) {
	query := `SELECT ` + TechniqueColumns + ` FROM techniques`
	var args []any
	if len(statuses) > 0 {
		placeholders := make([]string, len(statuses))
		for i, st := range statuses {
			placeholders[i] = "?"
			args = append(args, st)
		}
		query += ` WHERE status IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.queryTechniques(query, args...)
}

// CandidateTechniques returns retrieval-eligible techniques: live status, not decayed,
// with an embedding.
func (s *Store) CandidateTechniques() ([]models.Technique, error) {
	return s.queryTechniques(`SELECT ` + TechniqueColumns + ` FROM techniques
		WHERE status IN ('stable','mined') AND decay_signal=0 AND embedding IS NOT NULL
		ORDER BY id`)
}

// ShadowCandidateTechniques returns techniques under evaluation (status=shadow), not
// decayed, with an embedding — scored for relevance but never surfaced
// (docs/learning/validation-without-review.md).
func (s *Store) ShadowCandidateTechniques() ([]models.Technique, error) {
	return s.queryTechniques(`SELECT ` + TechniqueColumns + ` FROM techniques
		WHERE status='shadow' AND decay_signal=0 AND embedding IS NOT NULL
		ORDER BY id`)
}

// SetTechniqueEmbedding stores a technique's vector and the model that produced it.
func (s *Store) SetTechniqueEmbedding(id string, vec []float32, model string, dim int) error {
	res, err := s.exec(
		`UPDATE techniques SET embedding=?, embedding_model=?, embedding_dim=? WHERE id=?`,
		PackVector(vec), model, dim, id)
	return oneRow(res, err, id)
}

// TechniquesNeedingEmbedding returns techniques missing a vector or embedded with a
// stale model.
func (s *Store) TechniquesNeedingEmbedding(model string) ([]models.Technique, error) {
	return s.queryTechniques(`SELECT `+TechniqueColumns+` FROM techniques
		WHERE embedding IS NULL OR embedding_model<>? ORDER BY id`, model)
}

// SetTechniqueStatus flips a technique's lifecycle status.
func (s *Store) SetTechniqueStatus(id, status, updatedAt string) error {
	res, err := s.exec(`UPDATE techniques SET status=?, updated_at=? WHERE id=?`,
		status, updatedAt, id)
	return oneRow(res, err, id)
}

// SetDecay flags (or clears) decay; status flips only between stable/decayed
// — a draft or retired technique is left alone (the CASE below).
func (s *Store) SetDecay(id string, decayed bool, checkedAt string) error {
	signal, status := 0, "stable"
	if decayed {
		signal, status = 1, "decayed"
	}
	res, err := s.exec(`UPDATE techniques SET decay_signal=?, decay_checked=?,
		status = CASE WHEN status IN ('stable','decayed') THEN ? ELSE status END
		WHERE id=?`, signal, checkedAt, status, id)
	return oneRow(res, err, id)
}

// oneRow turns an UPDATE that matched nothing into storage.ErrNotFound, so the
// three technique-state writers refuse an id the database has never seen rather
// than reporting a silent no-op as success.
func oneRow(res sql.Result, err error, id string) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no technique %q: %w", id, storage.ErrNotFound)
	}
	return nil
}
