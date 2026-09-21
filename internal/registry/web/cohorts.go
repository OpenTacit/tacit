// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// GET /v1/cohorts — the cohort directory a joining member's machine reads
// before that member types a cohort of their own (insights.CohortDirectory
// explains why they need it).
//
// Key-authed, like every other member-facing read. It carries less than the
// endpoints beside it: /v1/events already hands out the same segment maps
// attached to individual events, and this returns only the distinct values and
// how much each is used.

import (
	"net/http"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/insights"
)

// handleCohortDirectory lists the cohorts already in use, so a member can join
// one instead of inventing a near-duplicate.
func (s *Server) handleCohortDirectory(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.AllEvents("")
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	facts, err := s.Store.AuditFacts("")
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	dims := insights.CohortDirectory(events, facts)
	if dims == nil {
		dims = []insights.CohortDimensionUse{}
	}
	s.sendJSON(w, 200, map[string]any{
		"dimensions": dims,
		// The dimensions a member may type, whether or not anyone has yet —
		// so a first-ever joiner is offered the vocabulary rather than a blank
		// page, and clients need not hardcode the list.
		"settable": config.MemberDimensions,
	})
}
