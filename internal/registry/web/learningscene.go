// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Learning picture: which capabilities are switched on, and what is holding
// the rest shut.
//
// Learning readiness is four tiles and two tables of "0 of 14", "met", "0 of 3".
// Every number on it is a gate — a detector runs or it does not — and reading
// which ones are open meant comparing a have against a need across six rows.
//
// So each capability is drawn behind a gate, and the gate is BARRED until its
// evidence arrives. That is the same shape the Review lane and the Federation
// channels use, and it means the same thing in all three: something is required
// to pass. An open gate means the plan's floors are cleared — every one of them,
// not the one the caller had to hand — and it stops there, because whether the
// detector behind it is at work is a question about a worker that this page
// cannot see.
//
// The registry feeds all three, so the evidence beam is live whenever facts are
// arriving at all and dashed when none are — which is itself the answer on a
// registry that has just been stood up.

// learningNeed is one requirement: what it counts, how much there is, and how
// much it takes. The count is always "have of need", so the noun agrees with the
// need and no phrase has to be pluralised.
type learningNeed struct {
	What       string // "days of facts", "techniques with outcomes"
	Have, Need int
	Note       string // the table's last column, where there is something true to add
}

func (n learningNeed) met() bool { return n.Have >= n.Need }

// learningGate is one capability and EVERY requirement that opens it.
//
// It was one have/need pair, and the caller passed whichever of a compound
// trigger's two halves it had to hand. That is how twenty helped events from a
// SINGLE technique came to read as a running Experiments detector, when the plan
// wants them across three — the untested half could not fail. A gate that tests
// one of two requirements is not a gate.
type learningGate struct {
	Name  string
	Needs []learningNeed
}

// Open reports whether every requirement is met — and nothing beyond that. It
// does not report that a detector is running: that is a question about a worker,
// and this page has no source for the answer.
func (g learningGate) Open() bool {
	for _, n := range g.Needs {
		if !n.met() {
			return false
		}
	}
	return len(g.Needs) > 0
}

func learningScene(facts int, gates []learningGate) string {
	var b strings.Builder
	b.WriteString(`<div class="dgm lrn-stage" aria-hidden="true"><div class="dgm-scene lrn-scene">`)

	// The evidence, and the three ways it is used. Dashed while nothing is
	// arriving: a registry with no facts is not feeding anything, and drawing
	// traffic on it would be drawing traffic that does not exist.
	// Bare in both: the gate is drawn at the end of each, so a beam wants no end
	// dot of its own. Pending adds the dash and takes the traffic off.
	feed := "dgm-beam-bare"
	if facts == 0 {
		feed = "dgm-beam-bare dgm-beam-pending"
	}
	// ONE FEED PER GATE. The beams are bare — the gate at the end of each is
	// their end marker — so a feed drawn past the last gate is a line that stops
	// in mid-air and points at nothing. Three is the frame's own limit, and the
	// only number the plan has ever asked for.
	for i := 1; i <= len(gates) && i <= 3; i++ {
		fmt.Fprintf(&b, `<span class="dgm-beam %s lrn-feed lrn-f%d"></span>`, feed, i)
	}

	for i, g := range gates {
		bars := ""
		if !g.Open() {
			bars = `<span class="dgm-mesh dgm-bars"></span>`
		}
		fmt.Fprintf(&b, `<div class="dgm-port lrn-g%d"><div class="dgm-cage">`+
			`<span class="dgm-tube dgm-tube1"></span><span class="dgm-tube dgm-tube2"></span>`+
			`<span class="dgm-gate dgm-gate1"></span><span class="dgm-gate dgm-gate2"></span>`+
			`%s</div></div>`, i+1, bars)
	}

	// Lit only while evidence is actually arriving. A cold registry drawn live
	// says the feed is running before anything has been collected.
	node := "dgm-node lrn-reg"
	if facts > 0 {
		node += " dgm-lit"
	}
	b.WriteString(`<div class="` + node + `">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3"></span></div>` +
		`<span class="dgm-mark lrn-mark">` + ui.MarkSVG + `</span>`)

	fmt.Fprintf(&b, `<span class="dgm-tag lrn-tag-reg"><b>%s</b><i>evidence collected from member sessions</i></span>`,
		factsPhrase(facts))
	for i, g := range gates {
		fmt.Fprintf(&b, `<span class="dgm-tag lrn-tag-g%d"><b>%s</b>`, i+1, html.EscapeString(g.Name))
		for _, line := range gateStateLines(g) {
			b.WriteString(`<i>` + html.EscapeString(line) + `</i>`)
		}
		b.WriteString(`</span>`)
	}

	b.WriteString(`</div></div>`)
	return b.String()
}

// factsPhrase counts the corpus. Nothing yet is a state a new registry is
// legitimately in, and it says so rather than reporting a zero.
func factsPhrase(n int) string {
	if n == 0 {
		return "No evidence yet"
	}
	return fmt.Sprintf("%s audit fact%s", fmtCount(n), plural(n))
}

// gateStateLines says what the evidence proves, and stops there.
//
// "Requirements met" is the truthful ceiling: it says the floors in the plan are
// cleared, which is all this page can see. The word it replaces was "running",
// which claimed a detector was at work — a claim about a worker whose state
// nothing here reads.
//
// Short of that, EVERY outstanding requirement is named. Showing only the
// nearest one leaves an operator working on a number that opens nothing.
func gateStateLines(g learningGate) []string {
	if g.Open() {
		return []string{"Requirements met"}
	}
	var out []string
	for _, n := range g.Needs {
		if !n.met() {
			out = append(out, fmt.Sprintf("%d of %d %s", n.Have, n.Need, n.What))
		}
	}
	return out
}
