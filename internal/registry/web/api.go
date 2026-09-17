// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/contribute"
	"github.com/opentacit/tacit/internal/registry/feedback"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/retrieval"
)

// --- JSON API handlers ---------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	c := s.cfg()
	// Liveness is never cached. A monitor asking "is this up now" must not be
	// answered from an edge copy of a minute ago, and the counts here are the
	// org's, not the public's.
	cachepolicy.MarkPrivate(w, r, "liveness")
	counts, err := s.Store.Counts()
	if err != nil {
		s.sendJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	body := map[string]any{"ok": true, "techniques": counts["techniques"],
		"events": counts["events"], "outcomes": counts["outcomes"], "drafts": counts["drafts"],
		"audit_facts": counts["audit_facts"]}
	// The embedder that is ACTUALLY serving retrieval — a misconfigured onnx
	// setup degrades to hashing-v1 at startup rather than failing, and this is
	// where that otherwise-silent fallback becomes observable (tacit doctor
	// checks it).
	if s.Embedder != nil {
		body["embed_model"] = s.Embedder.ModelID()
		if s.EmbedWanted != "" && s.EmbedWanted != s.Embedder.ModelID() {
			body["embed_degraded"] = true
			body["embed_wanted"] = s.EmbedWanted
		}
	}
	if s.Version != "" {
		body["version"] = s.Version
	}
	// The externally-reachable dashboard origin, when the operator configured
	// one. Members' tools reach the API by whatever address works for them
	// (often loopback), but human-facing links they print — techniques, insights —
	// should use the address a browser anywhere can open. Health is the open,
	// unauthenticated place to advertise it.
	// Published, the proxy address is that origin — the same answer join links,
	// share URLs and sign-in now use. Advertising the configured value here
	// while every link used the proxy would send `tacit doctor` and every tool
	// reading this endpoint to an address the registry no longer answers on.
	if base := s.PublishedBase(); base != "" {
		body["external_url"] = base
	} else if c.ExternalURL != "" {
		body["external_url"] = strings.TrimRight(c.ExternalURL, "/")
	}
	// Which kind of instance this is, so a harness can say so on every turn
	// (docs/distribution/global-access-plan.md, the CLI marker). The registry is
	// the authority deliberately: a member on the office network reaches this
	// registry at a private address while the registry itself may be globally
	// proxied, so sniffing the URL a tool happens to dial would tell two members
	// of one org two different stories. Open and keyless like the rest of this
	// endpoint — it is a mode, not a secret, and the public address it corresponds
	// to is already here.
	body["access"] = s.accessMode()
	if c.GlobalAccess {
		st := s.PublishState()
		if st.Connected {
			body["tunnel"] = "up"
		} else {
			body["tunnel"] = "down"
			// Why, in the ingress's own words. A registry it refused — an
			// enrolment ceiling, a rejected key — reports that only in its own
			// log, so every tool waiting on a public address polled until it
			// timed out and then said "not yet" about something that was never
			// going to happen.
			if st.LastError != "" {
				body["tunnel_error"] = st.LastError
			}
		}
	}
	// How this instance has been classifying its responses, since it started
	// (cachepolicy). Behind Cloudflare the interesting failure is silent — every
	// response private, every request a cache MISS, nobody the wiser — and these
	// counts are where it becomes visible from the origin's own side. Totals only:
	// no path, no host, nothing attributable to a reader.
	body["cache"] = s.cacheCounters().Snapshot()
	s.sendJSON(w, 200, body)
}

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	c := s.cfg()
	var body map[string]any
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	ch, err := models.ParseCharacterization(body)
	if err != nil {
		s.sendError(w, 400, err.Error())
		return
	}
	block, err := retrieval.BuildEvidence(s.Store, ch, s.Embedder, retrieval.AutonomyGate{
		Enabled:       c.AutonomyEnabled,
		MinHelpedRate: c.AutonomyMinHelpedRate,
		MinN:          c.AutonomyMinN,
	})
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	s.recordAuditFact(ch, block)
	s.sendJSON(w, 200, block)
}

// handleEnrichAuditFact accepts the model-inferred half of an audit fact
// (tools_absent), posted after the turn was delivered. It cannot be captured at
// evidence time because it is a judgment, not an observation, and the hook agent
// characterizes without an LLM to stay off the same-turn latency budget.
func (s *Server) handleEnrichAuditFact(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	e, err := models.ParseAuditFactEnrichment(body)
	if err != nil {
		s.sendError(w, 400, err.Error())
		return
	}
	found, err := s.Store.EnrichAuditFact(e)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !found {
		// Unknown audit: the producer is enriching something we never recorded.
		// Say so rather than silently inventing a headless fact.
		s.sendError(w, 404, "unknown audit_id")
		return
	}
	s.sendJSON(w, 200, map[string]any{"audit_id": e.AuditID, "enriched": true})
}

// recordAuditFact persists what this interaction WAS, so the learning layer can
// later join it to how it turned OUT (docs/learning/synthesis-design.md). Called after
// the evidence block is built and deliberately best-effort: a member's
// retrieval must never fail — or even slow — because bookkeeping did. A
// producer that sends no audit_id simply gets no fact recorded.
func (s *Server) recordAuditFact(ch models.Characterization, block models.EvidenceBlock) {
	f, ok := models.AuditFactFrom(ch, block, time.Now())
	if !ok {
		return
	}
	if _, err := s.Store.AppendAuditFact(f); err != nil {
		log.Printf("audit fact %s: %v", f.AuditID, err)
	}
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := readJSONBody(r, &raw); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		var one map[string]any
		if err := json.Unmarshal(raw, &one); err != nil {
			s.sendError(w, 400, "invalid JSON")
			return
		}
		items = []map[string]any{one}
	}
	accepted, duplicates, rejected := 0, 0, 0
	for _, item := range items {
		e, err := models.ParseFeedbackEvent(item)
		if err != nil {
			s.sendError(w, 400, err.Error())
			return
		}
		id, err := feedback.Ingest(s.Store, e)
		// A `shown` event the fact log contradicts is dropped, not fatal: the
		// rest of the batch is legitimate, and a producer old enough to send one
		// is old enough to retry the whole batch forever if we 500. It is
		// counted in the reply and logged, so a stale client is visible rather
		// than silently ignored.
		if errors.Is(err, feedback.ErrRetrievalNotExposure) {
			log.Printf("feedback: rejected shown for %s (audit %s): retrieval is not exposure",
				e.TechniqueID, e.AuditID)
			rejected++
			continue
		}
		if err != nil {
			s.sendError(w, 500, err.Error())
			return
		}
		if id == "" {
			duplicates++ // replayed event_id: already ingested
		} else {
			accepted++
		}
	}
	resp := map[string]any{"accepted": accepted}
	if duplicates > 0 {
		resp["duplicates"] = duplicates
	}
	if rejected > 0 {
		resp["rejected"] = rejected
	}
	s.sendJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleContribute(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	technique, err := contribute.Create(s.Store, body, s.Embedder)
	if err != nil {
		code := 400
		var verr *models.ValidationError
		if !errors.As(err, &verr) {
			code = 500
		}
		s.sendError(w, code, err.Error())
		return
	}
	s.sendJSON(w, http.StatusCreated, map[string]string{
		"id": technique.ID, "status": technique.Status, "scope": technique.Scope, "provenance": technique.Provenance})
}

func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	id, _ := body["id"].(string)
	status, _ := body["status"].(string)
	technique, found, err := contribute.Promote(s.Store, id, status, s.Embedder)
	if err != nil {
		s.sendError(w, 400, err.Error())
		return
	}
	if !found {
		s.sendError(w, 404, "technique not found")
		return
	}
	s.sendJSON(w, 200, map[string]string{"id": technique.ID, "status": technique.Status})
}

func (s *Server) handleSyncTechniques(w http.ResponseWriter, r *http.Request) {
	synced, err := jobs.RunSync(s.Store, s.cfg().TechniquesDir)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	embedded, err := jobs.RunEmbed(s.Store, s.Embedder)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	s.sendJSON(w, 200, map[string]int{"loaded": synced, "embedded": embedded})
}

// autoPromoteGate builds the automated-review gate from live config, so a
// settings change hot-applies to the next recompute (docs/learning/validation-without-review.md).
func (s *Server) autoPromoteGate() feedback.AutoPromoteGate {
	c := s.cfg()
	return feedback.AutoPromoteGate{
		Enabled:   c.AutoPromoteEnabled,
		MinFit:    c.AutoPromoteMinFit,
		MinJudged: c.AutoPromoteMinJudged,
	}
}

func (s *Server) handleRecompute(w http.ResponseWriter, r *http.Request) {
	n, err := jobs.RunRecompute(s.Store, s.autoPromoteGate())
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	// Re-rank the Public channel on the same fresh rollups, exactly as the
	// scheduler's tick does. Without this an operator who recomputes on demand is
	// shown a channel built from the previous cycle's evidence — and in the staged
	// state, is asked to consent to a stale list of what will be published.
	s.RecomputePublicChannel()
	s.sendJSON(w, 200, map[string]int{"techniques": n})
}

// handleListEvents pages through the append-only event log (the miner's
// event-pattern source; docs/mining/mining-design.md). Cursor: created_at >= since,
// lexicographic RFC3339; the response's next_since resumes after the last
// returned event when the page was full.
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	events, err := s.Store.AllEvents(since)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	next := ""
	if len(events) > limit {
		events = events[:limit]
		// resume strictly after the last event on this page: its created_at
		// plus the smallest lexicographic suffix (avoids re-fetching ties
		// except exact-timestamp duplicates, which idempotent ingest tolerates)
		next = events[len(events)-1].CreatedAt
	}
	if events == nil {
		events = []models.FeedbackEvent{}
	}
	s.sendJSON(w, 200, map[string]any{"events": events, "next_since": next})
}

// handleDeleteTechnique permanently removes a technique and its rollups — the admin
// verb for malformed imports and mistakes. Retire remains the right verb for
// techniques with real history; the append-only event log keeps its history
// either way.
func (s *Server) handleDeleteTechnique(w http.ResponseWriter, r *http.Request) {
	id, _ := url.PathUnescape(r.PathValue("id"))
	ok, err := s.Store.DeleteTechnique(id)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !ok {
		s.sendError(w, 404, "no technique with that ID")
		return
	}
	s.sendJSON(w, 200, map[string]any{"deleted": id})
}

func (s *Server) handleListTechniques(w http.ResponseWriter, r *http.Request) {
	statuses := r.URL.Query()["status"]
	// The default ceiling is a page size for anything browsing. A caller that
	// has to see EVERY technique — `tacit merge`, which contributes a whole
	// registry — asks for a higher one, because a merge that silently stopped
	// at 200 would leave work behind and say it was done.
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	techniques, err := s.Store.ListTechniques(statuses, limit)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(techniques))
	for _, c := range techniques {
		out = append(out, publicTechnique(c))
	}
	s.sendJSON(w, 200, map[string]any{"techniques": out})
}

func (s *Server) handleGetTechnique(w http.ResponseWriter, r *http.Request) {
	technique, ok, err := s.Store.GetTechnique(r.PathValue("id"))
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !ok {
		s.sendError(w, 404, "not found")
		return
	}
	s.sendJSON(w, 200, publicTechnique(technique))
}
