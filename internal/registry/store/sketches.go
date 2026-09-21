// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Adoption sketches (docs/mining/mining-design.md source 2) — the org's own
// zero-effort knowledge inflow. Same shape as every other log here: an
// append-only JSONL file replayed into memory at startup, idempotent on the
// record id.

package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/opentacit/tacit/internal/registry/models"
)

func (s *Store) sketchesPath() string { return filepath.Join(s.dir, "sketches.jsonl") }

func (s *Store) loadSketches() error {
	f, err := os.Open(s.sketchesPath())
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
		var sk models.Sketch
		if err := json.Unmarshal(line, &sk); err != nil {
			return errors.New("corrupt sketches.jsonl: " + err.Error())
		}
		if sk.SketchID != "" && !s.sketchIDs[sk.SketchID] {
			s.sketchIDs[sk.SketchID] = true
			s.sketches = append(s.sketches, sk)
		}
	}
	return sc.Err()
}

// InsertSketch appends one sketch; false for a replayed sketch_id.
func (s *Store) InsertSketch(sk models.Sketch) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sk.SketchID == "" || s.sketchIDs[sk.SketchID] {
		return false, nil
	}
	raw, err := json.Marshal(sk)
	if err != nil {
		return false, err
	}
	fh, err := os.OpenFile(s.sketchesPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	if _, err := fh.Write(append(raw, '\n')); err != nil {
		return false, err
	}
	s.sketchIDs[sk.SketchID] = true
	s.sketches = append(s.sketches, sk)
	return true, nil
}

// SketchesForTechnique returns a technique's sketches, newest first.
func (s *Store) SketchesForTechnique(techniqueID string) ([]models.Sketch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Sketch
	for _, sk := range s.sketches {
		if sk.TechniqueID == techniqueID {
			out = append(out, sk)
		}
	}
	sortNewestFirst(out)
	return out, nil
}

// TechniquelessSketches returns sketches with no technique_id, newest first,
// up to limit.
func (s *Store) TechniquelessSketches(limit int) ([]models.Sketch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []models.Sketch
	for _, sk := range s.sketches {
		if sk.TechniqueID == "" {
			out = append(out, sk)
		}
	}
	sortNewestFirst(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// sortNewestFirst orders sketches by created_at descending with the sketch id
// as the tie-break, matching the SQL backends. Sketches arrive in batches and
// often share a timestamp to the second, so without the tie-break a limited
// read returns a different set each call.
func sortNewestFirst(out []models.Sketch) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].SketchID > out[j].SketchID
	})
}
