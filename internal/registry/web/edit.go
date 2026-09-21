// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Reviewer editing — "approve with edits" is the most common real verdict in
// any review lane. Two surfaces over one implementation:
//
//	POST /v1/admin/techniques/{id...}     partial JSON update (key-authed, any technique)
//	POST /admin/techniques/edit/{id...}   the draft page's edit form (session-gated,
//	                                 drafts only, like promote/reject)
//
// Content edits bump updated_at and re-embed immediately so retrieval sees
// the refined fit conditions, not the stale vector.
package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/opentacit/tacit/internal/registry/contribute"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
)

func (s *Server) saveTechniqueEdit(technique models.Technique) error {
	technique.UpdatedAt = models.Now()
	// A direct edit of a serving technique bumps Version so open revision drafts
	// pinned to the old version go stale instead of silently applying over
	// the edit (docs/design/revision-design.md), and archives the outgoing version
	// so it stays viewable. Draft edits are pre-review churn: no version
	// bump, no archive.
	if technique.Status != "draft" {
		if old, ok, err := s.Store.GetTechnique(technique.ID); err == nil && ok {
			if err := s.Store.ArchiveTechniqueVersion(old); err != nil {
				return err
			}
		}
		technique.Version++
	}
	if err := s.Store.UpsertTechnique(technique); err != nil {
		return err
	}
	// re-embed now: the fit conditions are retrieval inputs
	vec := s.Embedder.Embed([]string{embed.TechniqueText(technique)})[0]
	return s.Store.SetTechniqueEmbedding(technique.ID, vec, s.Embedder.ModelID(), s.Embedder.Dim())
}

// handleTechniqueEdit is the key-authed partial update.
func (s *Server) handleTechniqueEdit(w http.ResponseWriter, r *http.Request) {
	id, _ := url.PathUnescape(r.PathValue("id"))
	technique, ok, err := s.Store.GetTechnique(id)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !ok {
		s.sendError(w, 404, "no technique with that ID")
		return
	}
	var body map[string]any
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	if !models.ApplyTechniqueEditFromMap(&technique, body) {
		s.sendJSON(w, 200, map[string]any{"id": technique.ID, "changed": false})
		return
	}
	if err := s.saveTechniqueEdit(technique); err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	s.sendJSON(w, 200, map[string]any{"id": technique.ID, "changed": true})
}

// handleRevise proposes a reviewed update to an existing technique
// (docs/design/revision-design.md): contributor-grade, like /v1/contribute — the
// change lands as a revision draft in the review lane while the base technique
// keeps serving. Body: any subset of the reviewable fields, plus "note".
func (s *Server) handleRevise(w http.ResponseWriter, r *http.Request) {
	id, _ := url.PathUnescape(r.PathValue("id"))
	var body map[string]any
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	draft, found, err := contribute.Revise(s.Store, id, body, s.Embedder)
	if err != nil {
		code := 500
		var verr *models.ValidationError
		if errors.As(err, &verr) {
			code = 400
		}
		s.sendError(w, code, err.Error())
		return
	}
	if !found {
		s.sendError(w, 404, "no technique with that ID")
		return
	}
	s.sendJSON(w, http.StatusCreated, map[string]any{
		"id": draft.ID, "supersedes": draft.Supersedes, "base_version": draft.BaseVersion,
		"status": draft.Status})
}

// handleDraftEditForm is the browser edit action (drafts only, session-gated
// when OIDC is on — same posture as promote/reject).
func (s *Server) handleDraftEditForm(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/review") {
		return
	}
	id, _ := url.PathUnescape(r.PathValue("id"))
	technique, ok, err := s.Store.GetTechnique(id)
	if err != nil || !ok || technique.Status != "draft" {
		http.Redirect(w, r, "/review", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err == nil {
		get := func(key string) (string, bool) {
			// Tags post as a SET — the chips that survived plus whatever is in
			// the add field — not as one comma-separated box (tags.go).
			if key == "tags" {
				return formTags(r.PostForm)
			}
			if !r.PostForm.Has(key) {
				return "", false
			}
			return r.PostFormValue(key), true
		}
		if models.ApplyTechniqueEdit(&technique, get) {
			_ = s.saveTechniqueEdit(technique)
		}
	}
	http.Redirect(w, r, "/drafts/"+technique.ID, http.StatusFound)
}

// draftEditForm renders the edit fields on the draft detail page. vocab is the
// live tag vocabulary, offered as autocomplete on the tag field: this is the one
// moment a human decides what a new technique is called in the org's language,
// and it used to be a bare text box with the existing tags nowhere in sight —
// which made coining a near-synonym the path of least resistance.
func draftEditForm(technique models.Technique, vocab []tagStat) string {
	esc := func(v string) string {
		return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(v)
	}
	var b strings.Builder
	b.WriteString(`<form class="edit-form" method="post" action="/admin/techniques/edit/` + esc(technique.ID) + `">`)
	b.WriteString(`<h3>Edit draft</h3>`)
	field := func(label, name, value string) {
		b.WriteString(`<label>` + label + `<input type="text" name="` + name + `" value="` + esc(value) + `"></label>`)
	}
	area := func(label, name, value string, rows string) {
		b.WriteString(`<label>` + label + `<textarea name="` + name + `" rows="` + rows + `">` +
			esc(value) + `</textarea></label>`)
	}
	field("Name", "name", technique.Name)
	area("Description", "description", technique.Description, "3")
	area("Recipe", "recipe", technique.Recipe, "6")
	area("Applies when", "applies_when", technique.AppliesWhen, "2")
	area("Not when", "not_when", technique.NotWhen, "2")
	b.WriteString(tagField(technique.Tags, vocab))
	b.WriteString(`<button type="submit" class="promote">Save edits</button>`)
	b.WriteString(`</form>`)
	return b.String()
}
