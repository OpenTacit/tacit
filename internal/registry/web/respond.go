// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
)

// --- helpers -----------------------------------------------------------------

func (s *Server) sendJSON(w http.ResponseWriter, code int, obj any) {
	body, _ := json.Marshal(obj)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// sendError answers with the one error shape every JSON endpoint here uses:
// a single-key object, {"error": "..."}. It is a wire surface, so the shape is
// fixed and the helper exists to keep it that way rather than to save typing.
// Callers that need a second key (the OAuth token endpoint adds
// error_description) build the map themselves and go through sendJSON.
func (s *Server) sendError(w http.ResponseWriter, code int, msg string) {
	s.sendJSON(w, code, map[string]string{"error": msg})
}

// sendAppJSON answers one of the /v1/*/app endpoints. The body is the same
// {window, html, text, summary} shape as before, except that `?format=text`
// drops the `html` — which is most of the payload and all of the problem for
// the caller these endpoints are the fallback FOR.
//
// A skill reaches for them when the MCP tool is unavailable, and it reaches
// with curl, from an agent whose transcript the response lands in. The insights
// body is ~198KB of embedded stylesheet wrapping a 1KB text summary, so the
// documented fallback drowned the answer it was supposed to rescue. A caller
// that can render the panel simply does not pass the parameter.
func (s *Server) sendAppJSON(w http.ResponseWriter, r *http.Request, body map[string]any) {
	if strings.EqualFold(r.URL.Query().Get("format"), "text") {
		delete(body, "html")
	}
	s.sendJSON(w, 200, body)
}

func (s *Server) sendHTML(w http.ResponseWriter, code int, markup string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The stylesheet, announced in a header rather than only in the markup. The
	// browser starts fetching it before it has parsed anything, which matters more
	// here than usual: bootHideScript keeps the page hidden until the stylesheet
	// has applied, so this link is on the critical path to the first pixel. On a
	// cacheable response Cloudflare also replays it as a 103 Early Hints, which
	// means the fetch can begin before the origin has answered at all.
	w.Header().Set("Link", s.cssPreloadLink())
	w.WriteHeader(code)
	_, _ = w.Write([]byte(s.rebase(markup)))
}

// cssPreloadLink is the preload header, carrying the same fingerprinted URL the
// document's <link> does so the two resolve to one request.
func (s *Server) cssPreloadLink() string {
	s.preloadOnce.Do(func() {
		s.preload = "<" + s.cfg().BasePath + "/assets/app.css?v=" + appCSSHash[:12] +
			">; rel=preload; as=style"
	})
	return s.preload
}

// rebase maps the root-absolute URLs handlers emit — href="/techniques",
// src="/assets/app.css", form actions, data-href rows — onto the configured
// base path. Every HTML response funnels through sendHTML, so this one rewrite
// keeps the ~hundreds of emission sites (and future ones) prefix-correct
// without threading the prefix through each of them. It relies on the
// codebase's uniform double-quoted attribute style; TestBasePathPagesRebased
// sweeps the rendered pages for stragglers.
func (s *Server) rebase(markup string) string {
	base := s.cfg().BasePath
	if base == "" {
		return markup
	}
	s.rebaseOnce.Do(func() {
		s.rebaser = strings.NewReplacer(
			`href="/`, `href="`+base+`/`,
			`src="/`, `src="`+base+`/`,
			`action="/`, `action="`+base+`/`,
			`data-href="/`, `data-href="`+base+`/`,
		)
	})
	return s.rebaser.Replace(markup)
}

func readJSONBody(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	return dec.Decode(into)
}

func publicTechnique(c models.Technique) map[string]any {
	// A technique safe to JSON-serialize over the API: drop the internal embedding.
	raw, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	delete(m, "embedding")
	return m
}
