// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package sqlstore

// Row and value codecs: the column order every technique query shares, the
// packed-float32 embedding encoding, and the JSON round-trip for the list and
// map columns.

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"math"

	"github.com/opentacit/tacit/internal/registry/models"
)

// TechniqueColumns is the fixed order used by each technique query and scan.
const TechniqueColumns = `id, name, description, scope, status, provenance, version, recipe,
	before_after, tags, task_types, triggers, applies_when, not_when, support_matrix,
	shipped, decay_signal, decay_checked, embedding, embedding_model, embedding_dim,
	channels, source, created_at, updated_at, supersedes, base_version, revision_note, origin,
	merged_into`

// Scanner is met by sql.Row and sql.Rows.
type Scanner interface {
	Scan(...any) error
}

func PackVector(vec []float32) []byte {
	if len(vec) == 0 {
		return nil
	}
	out := make([]byte, 4*len(vec))
	for i, f := range vec {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(f))
	}
	return out
}

func unpackVector(raw []byte) []float32 {
	if len(raw) < 4 {
		return nil
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

func JSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func ReadJSON(raw []byte, into any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func ScanTechnique(row Scanner) (models.Technique, error) {
	var c models.Technique
	var tags, taskTypes, triggers, supportMatrix, embedding, channels, origin []byte
	var mergedInto sql.NullString
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Scope, &c.Status, &c.Provenance,
		&c.Version, &c.Recipe, &c.BeforeAfter, &tags, &taskTypes, &triggers,
		&c.AppliesWhen, &c.NotWhen, &supportMatrix, &c.Shipped, &c.DecaySignal,
		&c.DecayChecked, &embedding, &c.EmbeddingModel, &c.EmbeddingDim, &channels, &c.Source,
		&c.CreatedAt, &c.UpdatedAt, &c.Supersedes, &c.BaseVersion, &c.RevisionNote, &origin,
		&mergedInto)
	if err != nil {
		return models.Technique{}, err
	}
	for _, item := range []struct {
		raw  []byte
		into any
	}{
		{tags, &c.Tags},
		{taskTypes, &c.TaskTypes},
		{triggers, &c.Triggers},
		{supportMatrix, &c.SupportMatrix},
		{channels, &c.Channels},
		{origin, &c.Origin},
	} {
		if err := ReadJSON(item.raw, item.into); err != nil {
			return models.Technique{}, err
		}
	}
	c.Embedding = unpackVector(embedding)
	c.MergedInto = mergedInto.String
	return c, nil
}
