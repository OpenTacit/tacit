// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The suggestion surface (docs/mining/suggest-design.md): discover
// generally-applicable best-practice techniques matched to this org's
// observed usage and land them as drafts.
//
//	GET  /v1/admin/usage-profile   the aggregate usage profile (key-authed)
//	POST /v1/admin/suggest         run one research pass (key-authed)
//	POST /admin/suggest            the Drafts page button (session-gated)
package web

import (
	"errors"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/observe"
	"github.com/opentacit/tacit/internal/registry/suggest"
)

// suggestGate serializes research passes: a run costs real API spend and a
// minute of wall clock, so a double-click (or a second reviewer) must join the
// queue behind a 409, not double the spend.
//
// It remembers WHEN the in-flight run started, not just that one exists,
// because the refusal is only half the job. The Drafts page's progress bar is
// client state: reload the page — or let a phone background the tab and drop
// the fetch — and the button is armed again with no sign that a run is still
// going. The reviewer clicks, the gate refuses, and a healthy run that is about
// to file ten drafts reads as a failure. Serving the elapsed time lets the page
// come back showing the run it would otherwise invite a second click on.
type suggestGate struct {
	mu      sync.Mutex
	started time.Time // zero when idle
}

var suggestRuns suggestGate

// tryStart claims the slot, or reports false when a run is already in flight.
func (g *suggestGate) tryStart() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.started.IsZero() {
		return false
	}
	g.started = time.Now()
	return true
}

// done releases the slot. Always call it deferred: a research pass that panics
// with the slot held would wedge every later run behind a permanent refusal.
func (g *suggestGate) done() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.started = time.Time{}
}

// elapsed reports how long the in-flight run has been going, and whether there
// is one at all.
func (g *suggestGate) elapsed() (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.started.IsZero() {
		return 0, false
	}
	return time.Since(g.started), true
}

// researcher returns the injected researcher (tests) or one built from the
// environment — resolved per call so a key added later is picked up live.
func (s *Server) researcher() (suggest.Researcher, error) {
	if s.Suggest != nil {
		return s.Suggest, nil
	}
	c, err := suggest.FromEnv()
	if err != nil {
		return nil, err
	}
	// A provider with no server-side web search cannot run the registry-side
	// research pass; refuse honestly so the /tacit:suggest skill falls back to
	// the harness's own web access rather than landing a search-less guess.
	if !c.CanSearch() {
		return nil, suggest.ErrNoSearch
	}
	return c, nil
}

func (s *Server) handleUsageProfile(w http.ResponseWriter, r *http.Request) {
	p, err := suggest.BuildProfile(s.Store, 90)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	s.sendJSON(w, 200, p)
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Count int `json:"count"`
	}
	if r.ContentLength > 0 {
		if err := readJSONBody(r, &body); err != nil {
			s.sendError(w, 400, "invalid JSON")
			return
		}
	}
	res, err := s.researcher()
	if errors.Is(err, suggest.ErrNoResearcher) || errors.Is(err, suggest.ErrNoSearch) {
		s.sendError(w, 503, err.Error())
		return
	} else if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !suggestRuns.tryStart() {
		s.sendError(w, http.StatusConflict, "a suggestion run is already in progress")
		return
	}
	defer suggestRuns.done()
	start := time.Now()
	techniques, err := suggest.Run(s.Store, s.Embedder, res, body.Count, s.cfg().AutoShadow)
	if err != nil {
		s.sendError(w, 502, err.Error())
		return
	}
	s.suggestTimes.record(time.Since(start))
	ids := make([]string, 0, len(techniques))
	for _, c := range techniques {
		ids = append(ids, c.ID)
	}
	s.sendJSON(w, 200, map[string]any{"created": ids, "count": len(ids)})
}

// handleDiscover runs one observed-technique discovery pass: cluster the org's own
// technique-less "worked move" sketches into candidates and file them as
// provenance=observed (docs/learning/observed-technique-discovery.md). Distillation
// needs only a plain completion, so it uses FromEnv directly (no web search
// required); a missing model returns 503, like suggest. Synchronous, admin-gated.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/review") {
		return
	}
	model, err := suggest.FromEnv()
	if err == nil {
		_, err = observe.Run(s.Store, s.Embedder, model, observe.Config{
			MinSessions:   config.ObserveMinSessions,
			MinCohorts:    config.ObserveMinCohorts,
			SimThreshold:  config.ObserveSim,
			MaxTechniques: config.ObserveMaxTechniques,
			EnterShadow:   s.cfg().AutoShadow,
		})
	}
	if err != nil {
		s.sendHTML(w, 503, s.renderShell(page{status: 503, active: "review",
			crumbs: []crumb{{label: "Review", href: "/review"}, {label: "Discover", href: ""}},
			content: `<p>` + html.EscapeString(err.Error()) +
				`</p><p class="sub"><a href="/review">← back to the review queue</a></p>`}, s.sessionUser(r)))
		return
	}
	http.Redirect(w, r, "/review", http.StatusFound)
}

// handleSuggestForm is the Drafts page's "Suggest techniques" button —
// session-gated when OIDC is on, same posture as promote/reject. The run is
// synchronous: the reviewer clicked to get drafts, so they wait for them.
//
// Every outcome carries its own status, because the page has to tell them
// apart: 409 means a run is already going (wait for it — nothing failed), 503
// means no researcher is configured (a real outage the operator must fix), and
// 502 means the research pass itself broke. Collapsing all three into 503, as
// this did, reported the healthy case as the broken one.
func (s *Server) handleSuggestForm(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/review") {
		return
	}
	res, err := s.researcher()
	if err != nil {
		s.suggestFailed(w, r, http.StatusServiceUnavailable, err)
		return
	}
	if !suggestRuns.tryStart() {
		s.suggestFailed(w, r, http.StatusConflict, errors.New(
			"a suggestion run is already in progress; wait for it to finish, then reload the review page"))
		return
	}
	defer suggestRuns.done()
	start := time.Now()
	if _, err := suggest.Run(s.Store, s.Embedder, res, suggest.MaxBatch, s.cfg().AutoShadow); err != nil {
		s.suggestFailed(w, r, http.StatusBadGateway, err)
		return
	}
	s.suggestTimes.record(time.Since(start))
	if wantsJSON(r) {
		s.sendJSON(w, 200, map[string]string{"status": "done"})
		return
	}
	http.Redirect(w, r, "/review", http.StatusFound)
}

// suggestFailed answers a refused or failed run. The page's button posts with
// fetch and Accept: application/json, so it gets the reason as data and can say
// it out loud; a no-JS plain POST still gets the ordinary error page.
func (s *Server) suggestFailed(w http.ResponseWriter, r *http.Request, status int, err error) {
	if wantsJSON(r) {
		s.sendError(w, status, err.Error())
		return
	}
	s.sendHTML(w, status, s.renderShell(page{status: status, active: "review",
		crumbs: []crumb{{label: "Review", href: "/review"}, {label: "Suggest", href: ""}},
		content: `<p>` + html.EscapeString(err.Error()) +
			`</p><p class="sub"><a href="/review">← back to the review queue</a></p>`}, s.sessionUser(r)))
}

// wantsJSON reports whether the caller asked for data rather than a page.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// handleSuggestStatus reports whether a research pass is in flight and how long
// it has been going, so the Drafts page can show a run started by an earlier
// page load — or by a colleague — instead of an armed button.
func (s *Server) handleSuggestStatus(w http.ResponseWriter, r *http.Request) {
	elapsed, running := suggestRuns.elapsed()
	s.sendJSON(w, 200, map[string]any{
		"running": running,
		"elapsed": int(elapsed.Seconds()),
		"est":     s.suggestTimes.estimateSecs(),
	})
}
