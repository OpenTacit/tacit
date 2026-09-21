// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/techmerge"
	"github.com/opentacit/tacit/internal/ui"
	"github.com/opentacit/tacit/pkg/contracts"
)

// Consolidating the playbook, on the bargain package tagmerge struck for the
// tag vocabulary: the machine proposes and a person disposes.
//
// The novelty gate keeps restatements out. Nothing kept them out before it
// existed, and nothing in the lifecycle compares one technique to another — so
// the groups already inside are found here, ranked on the outcomes the registry
// has measured, and put to a reviewer. Retiring a technique members use is a
// decision about what an organization is told, and a similarity score is not
// entitled to take it.

// consolidation is one round of proposals awaiting a verdict. Held in memory
// like the tag one, and for the same reason: a proposal is a suggestion, not
// registry state, and a restart losing it costs one cheap pass over the corpus.
type consolidation struct {
	Groups []techmerge.Group
	Err    string // a failed run is shown rather than swallowed
}

// lane is the two consolidations, each with its own page, its own slot and its
// own words. They are separate because the decisions are: one retires something
// members use, the other drops a candidate nobody has seen.
type lane struct {
	key string // the URL segment and the map key
	techmerge.Lane
	page     string // where its panel lives, for the redirect back
	heading  string
	standing string // what the panel says before a pass has been run
	invite   string // the button that runs one
	verdict  string // the button that acts on the result
	empty    string // what it says when it finds nothing
}

var lanes = map[string]lane{
	"playbook": {
		key: "playbook", Lane: techmerge.PlaybookLane, page: "/techniques",
		heading:  "Duplicate techniques",
		standing: "Techniques that say the same thing split their evidence between them: retrieval shows one at a time, so a move that would clear the ranking bar as one technique may never clear it as any of its copies.",
		invite:   "Find duplicates",
		verdict:  "Retire selected techniques",
		empty:    "No two serving techniques say the same thing.",
	},
	"queue": {
		key: "queue", Lane: techmerge.QueueLane, page: "/review",
		heading:  "Duplicate candidates",
		standing: "Candidates under evaluation, checked against each other and against the techniques you already publish. Dropping one costs nothing a member has seen.",
		invite:   "Find duplicate candidates",
		verdict:  "Drop selected candidates",
		empty:    "No candidate restates another, or anything already published.",
	},
}

// comparingNote says what the run is about to do, in the count it already has.
// A technique with no recipe is not compared (embed.MoveText), so it is not
// counted either — a number that includes what the pass will skip is a worse
// answer than no number.
func (s *Server) comparingNote(l lane) string {
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		return "Comparing every technique…"
	}
	n := 0
	for _, t := range techniques {
		if embed.MoveText(t) == "" {
			continue
		}
		if t.Status == "stable" || (l.Lane == techmerge.QueueLane && t.Status == "shadow") {
			n++
		}
	}
	return fmt.Sprintf("Comparing %d %s — this takes a few seconds.", n, "technique"+plural(n))
}

func laneOf(r *http.Request) (lane, bool) {
	l, ok := lanes[r.PathValue("lane")]
	return l, ok
}

func (s *Server) handleConsolidatePropose(w http.ResponseWriter, r *http.Request) {
	l, ok := laneOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.signedInOrRedirect(w, r, l.page) {
		return
	}
	prop := &consolidation{}
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		prop.Err = err.Error()
	} else if s.Embedder == nil {
		prop.Err = "no embedder is configured, so nothing can be compared"
	} else {
		prop.Groups = techmerge.Propose(techniques, s.overallOutcomes(techniques), s.Embedder, l.Lane)
	}
	s.setConsolidation(l.key, prop)
	http.Redirect(w, r, l.page+"#consolidate", http.StatusFound)
}

func (s *Server) setConsolidation(key string, prop *consolidation) {
	s.consolidateMu.Lock()
	defer s.consolidateMu.Unlock()
	if s.consolidate == nil {
		s.consolidate = map[string]*consolidation{}
	}
	if prop == nil {
		delete(s.consolidate, key)
		return
	}
	s.consolidate[key] = prop
}

func (s *Server) consolidation(key string) *consolidation {
	s.consolidateMu.Lock()
	defer s.consolidateMu.Unlock()
	return s.consolidate[key]
}

// overallOutcomes reads the rollup row each technique is ranked on. A technique
// with no row keeps a zero Outcome, which the ranking reads as "nothing
// measured" rather than as a score of nought.
func (s *Server) overallOutcomes(techniques []models.Technique) map[string]models.Outcome {
	out := make(map[string]models.Outcome, len(techniques))
	for _, t := range techniques {
		if o, ok, err := s.Store.GetOutcome(t.ID, config.OverallKey); err == nil && ok {
			out[t.ID] = o
		}
	}
	return out
}

// consolidateWaiting is how many groups are awaiting a verdict, for the review
// queue. Zero when no pass has been run, which is the usual state: this one is
// started by hand rather than on a schedule.
func (s *Server) consolidateWaiting(key string) int {
	if prop := s.consolidation(key); prop != nil {
		return len(prop.Groups)
	}
	return 0
}

func (s *Server) handleConsolidateDiscard(w http.ResponseWriter, r *http.Request) {
	l, ok := laneOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.signedInOrRedirect(w, r, l.page) {
		return
	}
	s.setConsolidation(l.key, nil)
	http.Redirect(w, r, l.page, http.StatusFound)
}

// handleConsolidateApply retires what the reviewer left ticked.
//
// It reads the FORM rather than the stored proposal, the way the tag merge
// does: a reviewer may have spared a technique, and what they saw on screen is
// what must happen. Every id is checked back against the stored proposal, so
// this cannot be driven to retire something that was never proposed, and the
// technique chosen to keep can never be among those retired.
func (s *Server) handleConsolidateApply(w http.ResponseWriter, r *http.Request) {
	l, ok := laneOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.signedInOrRedirect(w, r, l.page) {
		return
	}
	_ = r.ParseForm()
	prop := s.consolidation(l.key)
	if prop == nil {
		http.Redirect(w, r, l.page, http.StatusFound)
		return
	}

	now := models.Now()
	for i, group := range prop.Groups {
		keep := group.Keep.Technique
		// Only what THIS lane offered. Group.Alongside is deliberately absent:
		// a serving technique shown beside a candidate is context, and the form
		// must not become a way to retire it from the wrong lane on the wrong
		// evidence.
		offered := map[string]models.Technique{}
		for _, m := range group.Retire {
			offered[m.Technique.ID] = m.Technique
		}
		for _, id := range r.Form[fmt.Sprintf("retire_%d", i)] {
			retiring, ok := offered[id]
			if !ok || id == keep.ID {
				continue // not offered in this group, or the one being kept
			}
			// Retired AND pointed at the survivor. The pointer is what makes
			// the merge additive: every fold over the event log redirects
			// through it, so the technique that was kept is judged on what the
			// whole group learned rather than on the fragment it happened to
			// hold. Without it a merge keeps one copy's evidence and discards
			// the rest — which is the problem consolidation exists to fix.
			retiring.MergedInto = keep.ID
			retiring.Status = "retired"
			retiring.UpdatedAt = now
			if err := s.Store.UpsertTechnique(retiring); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			// The reason names the survivor, because "retired" on its own leaves
			// anybody reading the feed later unable to answer the only question
			// they will have, which is what to use instead.
			_, _ = s.Store.AppendLifecycleEvent(models.LifecycleEvent{
				EventID:       contracts.DeterministicLifecycleEventID(id, "retired", now),
				TechniqueID:   id,
				TechniqueName: retiring.Name,
				Kind:          "retired",
				Provenance:    retiring.Provenance,
				Reason:        "says the same as " + keep.Name + " (" + keep.ID + "), which is kept",
				CreatedAt:     now,
			})
		}
	}
	s.setConsolidation(l.key, nil)
	http.Redirect(w, r, l.page, http.StatusFound)
}

// consolidatePanel renders the verdict form: each group, what the evidence said,
// the technique it would keep, and the rest as checkboxes. Untick to spare one.
func (s *Server) consolidatePanel(key string) string {
	l, ok := lanes[key]
	if !ok {
		return ""
	}
	prop := s.consolidation(key)

	var b strings.Builder
	fmt.Fprintf(&b, `<section class="panel" id="consolidate"><h2>%s</h2>`, html.EscapeString(l.heading))
	if prop == nil {
		// Every recipe in the lane has to be embedded to compare it, which on a
		// library this size is seconds rather than milliseconds — long enough
		// that a click with no answer reads as a click that missed. The count is
		// what the reader can use: it says what is being worked on and roughly
		// how much of it, without inventing a deadline.
		fmt.Fprintf(&b, `%s<div class="toolbar">`+
			`<form method="post" action="/admin/techniques/consolidate/%s/propose" data-busy="%s">`+
			`<button type="submit" data-busy-label="Comparing…">%s</button></form></div></section>`,
			ui.Fine(html.EscapeString(l.standing)), l.key,
			html.EscapeString(s.comparingNote(l)), html.EscapeString(l.invite))
		return b.String()
	}
	switch {
	case prop.Err != "":
		fmt.Fprintf(&b, `<p class="hint">The comparison could not run: %s</p>`+
			`<form method="post" action="/admin/techniques/consolidate/%s/discard">`+
			`<button type="submit">Dismiss</button></form></section>`, html.EscapeString(prop.Err), l.key)
		return b.String()
	case len(prop.Groups) == 0:
		fmt.Fprintf(&b, `<p class="empty">%s</p>`+
			`<form method="post" action="/admin/techniques/consolidate/%s/discard">`+
			`<button type="submit">Dismiss</button></form></section>`, html.EscapeString(l.empty), l.key)
		return b.String()
	}

	fmt.Fprintf(&b, `<p class="hint">Nothing has been %s. Each group keeps the technique the evidence favours; `+
		`untick any you would rather keep as well. A retired technique stops being served and keeps its record.</p>`,
		map[bool]string{true: "dropped", false: "retired"}[l.Lane == techmerge.QueueLane])
	fmt.Fprintf(&b, `<form method="post" action="/admin/techniques/consolidate/%s/apply">`, l.key)
	for i, g := range prop.Groups {
		b.WriteString(`<div class="merge-row">`)
		fmt.Fprintf(&b, `<p class="merge-why">%s</p>`, html.EscapeString(g.Why))
		fmt.Fprintf(&b, `<p class="merge-keep"><b>Keep</b> <a href="/techniques/%s">%s</a> <span class="muted">%s</span></p>`,
			html.EscapeString(g.Keep.Technique.ID), html.EscapeString(g.Keep.Technique.Name),
			html.EscapeString(evidence(g.Keep)))
		// What this lane may not touch, shown so the reviewer can see what a
		// candidate restates without being offered the chance to retire it here.
		for _, m := range g.Alongside {
			fmt.Fprintf(&b, `<p class="merge-alongside"><span class="muted">also published:</span> `+
				`<a href="/techniques/%s">%s</a> <span class="muted">%s · %.0f%% alike</span></p>`,
				html.EscapeString(m.Technique.ID), html.EscapeString(m.Technique.Name),
				html.EscapeString(evidence(m)), m.Similarity*100)
		}
		for _, m := range g.Retire {
			fmt.Fprintf(&b, `<label class="merge-src"><input type="checkbox" name="retire_%d" value="%s" checked> `+
				`<span>%s</span> <span class="muted">%s · %.0f%% alike</span></label>`,
				i, html.EscapeString(m.Technique.ID), html.EscapeString(m.Technique.Name),
				html.EscapeString(evidence(m)), m.Similarity*100)
		}
		b.WriteString(`</div>`)
	}
	fmt.Fprintf(&b, `<div class="merge-actions">`+
		`<button type="submit">%s</button>`+
		`<button class="quiet" type="submit" form="consolidate-discard">Keep them all</button>`+
		`</div></form>`+
		`<form id="consolidate-discard" method="post" action="/admin/techniques/consolidate/%s/discard"></form>`,
		html.EscapeString(l.verdict), l.key)
	b.WriteString(`</section>`)
	return b.String()
}

// evidence is what a technique has to show for itself, in the counts the
// registry already has. A rate with no n behind it is not reported as a rate.
func evidence(m techmerge.Member) string {
	// Only a rate the ranking would act on is shown as a rate. Below that floor
	// the count is the honest thing to report — a helped rate off one adoption
	// reads as a measurement and is not one, and the panel would then contradict
	// its own reason line, which says "too few to rate".
	if rate, ok := m.Measured(); ok {
		return fmt.Sprintf("%.0f%% helped over %.0f adoptions", rate*100, m.Sample())
	}
	if m.Adopted() > 0 {
		return strconv.Itoa(m.Adopted()) + " adopted, too few to rate"
	}
	if m.Shown() > 0 {
		return strconv.Itoa(m.Shown()) + " shown, never adopted"
	}
	return "never shown"
}
