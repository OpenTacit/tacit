// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/schemas"
)

func (s *Server) mountAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/evidence", s.keyAuthed(s.handleEvidence))
	mux.HandleFunc("POST /v1/feedback", s.keyAuthed(s.handleFeedback))
	mux.HandleFunc("POST /v1/audit-facts/enrich", s.keyAuthed(s.handleEnrichAuditFact))
	mux.HandleFunc("POST /v1/contribute", s.keyAuthed(s.handleContribute))
	// The sealed usage ledger (ledger.go). The write is key-authed like the
	// rest of /v1 — a member key proves the caller is one of this registry's
	// machines — and the read is gated on a signed-in session instead, because
	// browsers cannot send X-Tacit-Key.
	mux.HandleFunc("PUT /v1/ledger/{id}/{machine}", s.keyAuthed(s.handleLedgerPut))
	mux.HandleFunc("GET /v1/ledger/{id}", s.handleLedgerGet)
	mux.HandleFunc("POST /v1/sketches", s.handleSketchIntake)
	mux.HandleFunc("POST /v1/admin/promote", s.rootKeyAuthed(s.handlePromote))
	mux.HandleFunc("POST /v1/admin/sync-techniques", s.rootKeyAuthed(s.handleSyncTechniques))
	mux.HandleFunc("POST /v1/admin/recompute", s.rootKeyAuthed(s.handleRecompute))
	mux.HandleFunc("GET /v1/admin/usage-profile", s.keyAuthed(s.handleUsageProfile))
	mux.HandleFunc("POST /v1/admin/suggest", s.rootKeyAuthed(s.handleSuggest))
	mux.HandleFunc("GET /v1/techniques", s.keyAuthed(s.handleListTechniques))
	mux.HandleFunc("GET /v1/events", s.keyAuthed(s.handleListEvents))
	mux.HandleFunc("GET /v1/cohorts", s.keyAuthed(s.handleCohortDirectory))
	mux.HandleFunc("GET /v1/insights/app", s.keyAuthed(s.handleInsightsApp))
	mux.HandleFunc("GET /v1/map/app", s.keyAuthed(s.handleMapApp))
	mux.HandleFunc("GET /v1/organization/app", s.keyAuthed(s.handleOrganizationApp))
	mux.HandleFunc("GET /v1/review/app", s.keyAuthed(s.handleReviewApp))

	if s.MCP != nil {
		mux.HandleFunc("POST /mcp", s.mcpAuthed(s.MCP.ServeHTTP))
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.handleOAuthPRM)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.handleOAuthPRM)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleOAuthASMeta)
	mux.HandleFunc("POST /oauth/register", s.handleOAuthRegister)
	mux.HandleFunc("GET /oauth/authorize", s.handleOAuthAuthorize)
	mux.HandleFunc("POST /oauth/token", s.handleOAuthToken)

	mux.HandleFunc("GET /v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		cachepolicy.MarkPublicFor(w, r, 3600, "protocol description")
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(schemas.OpenAPI())
	})
	mux.HandleFunc("GET /v1/schemas/{name}", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := schemas.Get(r.PathValue("name"))
		if !ok {
			s.sendJSON(w, 404, map[string]any{"error": "unknown schema", "available": schemas.Names()})
			return
		}
		cachepolicy.MarkPublicFor(w, r, 3600, "protocol description")
		w.Header().Set("Content-Type", "application/schema+json")
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("GET /v1/techniques/{id...}", s.keyAuthed(s.handleGetTechnique))
	mux.HandleFunc("DELETE /v1/techniques/{id...}", s.rootKeyAuthed(s.handleDeleteTechnique))
	mux.HandleFunc("POST /v1/techniques/revise/{id...}", s.keyAuthed(s.handleRevise))
	mux.HandleFunc("GET /v1/techniques/versions/{id...}", s.keyAuthed(s.handleTechniqueVersions))
	mux.HandleFunc("POST /v1/admin/techniques/{id...}", s.rootKeyAuthed(s.handleTechniqueEdit))
	mux.HandleFunc("POST /v1/admin/publish", s.rootKeyAuthed(s.handlePublish))
	mux.HandleFunc("GET /v1/admin/subscriptions", s.keyAuthed(s.handleSubscriptionsList))
	mux.HandleFunc("POST /v1/admin/subscriptions", s.rootKeyAuthed(s.handleSubscriptionPut))
	mux.HandleFunc("DELETE /v1/admin/subscriptions/{id}", s.rootKeyAuthed(s.handleSubscriptionDelete))
	mux.HandleFunc("POST /v1/admin/poll-feeds", s.rootKeyAuthed(s.handlePollFeeds))
}
