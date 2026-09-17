// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// POST /v1/sketches — the registry's first-party adoption-sketch intake.
//
// The sketch channel (docs/mining/mining-design.md source 2) was designed for
// an external miner and had never fired because no org configures one. This
// endpoint makes the member's own registry the default sink: at adoption
// moments the hook agent posts a scrubbed {situation, move} pair, the sketch
// lands as evidence attached to the technique, and once enough distinct adoption
// contexts accumulate the registry files a MECHANICAL revision draft — the
// contexts appended to the technique's description — through the ordinary review
// lane. No LLM (the registry never calls one), nothing auto-promotes; a
// reviewer still owns what goes live. Consent semantics are unchanged:
// no TACIT_SKETCH_URL on the member's machine, no sketches.
package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/registry/contribute"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/pkg/scrub"
)

// sketchRevisionThreshold is how many adoption contexts, newer than the
// technique's last update, earn an auto-filed revision draft. Small on purpose:
// three distinct real-world uses is already more grounding than most techniques'
// original authoring.
const sketchRevisionThreshold = 3

func (s *Server) handleSketchIntake(w http.ResponseWriter, r *http.Request) {
	// Accept the key either way the producers send it: X-Tacit-Key (the
	// registry convention) or Authorization: Bearer (the sketch sink's shape,
	// kept for miner-intake compatibility).
	key := r.Header.Get("X-Tacit-Key")
	if key == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimPrefix(h, "Bearer ")
		}
	}
	if !s.keyOK(key) {
		s.sendError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var sk models.Sketch
	if err := readJSONBody(r, &sk); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	// Scrub again at the boundary — the agent scrubs before sending, but the
	// registry must not trust producers with its own privacy posture.
	sk.Trigger, _ = scrub.Redact(strings.TrimSpace(sk.Trigger))
	sk.Move, _ = scrub.Redact(strings.TrimSpace(sk.Move))
	if sk.Trigger == "" || sk.Move == "" {
		s.sendError(w, 400, "trigger and move are required")
		return
	}
	if sk.SketchID == "" {
		sk.SketchID = models.NewID("skt_")
	}
	if sk.CreatedAt == "" {
		sk.CreatedAt = models.Now()
	}
	inserted, err := s.Store.InsertSketch(sk)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	resp := map[string]any{"sketch_id": sk.SketchID, "inserted": inserted}
	if inserted && sk.TechniqueID != "" {
		if filed := s.maybeFileSketchRevision(sk.TechniqueID); filed != "" {
			resp["revision_filed"] = filed
		}
	}
	s.sendJSON(w, http.StatusCreated, resp)
}

// maybeFileSketchRevision files one mechanical revision draft when a technique has
// accumulated sketchRevisionThreshold adoption contexts newer than its last
// update, and no revision draft for it is already open. Returns the draft id
// ("" when nothing was filed). Best-effort: a failure here must never fail
// the intake — the sketch itself is already recorded.
func (s *Server) maybeFileSketchRevision(techniqueID string) string {
	base, ok, err := s.Store.GetTechnique(techniqueID)
	if err != nil || !ok || base.Status != "stable" {
		return ""
	}
	sketches, err := s.Store.SketchesForTechnique(techniqueID)
	if err != nil {
		return ""
	}
	fresh := make([]models.Sketch, 0, len(sketches))
	for _, sk := range sketches {
		if base.UpdatedAt == "" || sk.CreatedAt > base.UpdatedAt {
			fresh = append(fresh, sk)
		}
	}
	if len(fresh) < sketchRevisionThreshold {
		return ""
	}
	// One open auto-revision per technique: a queue of near-identical drafts is
	// reviewer spam, the thing the review lane must never become.
	drafts, err := s.Store.ListTechniques([]string{"draft"}, 0)
	if err != nil {
		return ""
	}
	for _, d := range drafts {
		if d.Supersedes == techniqueID {
			return ""
		}
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(base.Description, "\n"))
	b.WriteString("\n\nAdoption contexts:\n")
	max := len(fresh)
	if max > 5 {
		max = 5
	}
	for _, sk := range fresh[:max] { // newest first, capped: evidence, not a log dump
		fmt.Fprintf(&b, "- %s\n", sk.Trigger)
	}
	body := map[string]any{
		"description": b.String(),
		"note": fmt.Sprintf("created automatically from %d member adoption contexts recorded since the last update",
			len(fresh)),
	}
	draft, _, err := contribute.Revise(s.Store, techniqueID, body, s.Embedder)
	if err != nil {
		return ""
	}
	return draft.ID
}
