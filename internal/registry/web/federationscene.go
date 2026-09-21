// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Federation picture: two directions across one boundary, drawn.
//
// Federation is the page in this registry that is entirely about SHAPE — what
// this registry offers outward, what it takes in, and where a person still
// stands between the two. As sections of prose it was three headings and a
// paragraph repeating the invariant, and an operator could not see from the page
// that they publish two channels and import from one, let alone which of those
// imports walks straight in.
//
// It is a LIVE view, not an illustration. Every port, peer and label is one real
// channel or one real subscription carrying its own count, and a registry with
// nothing published and nothing subscribed draws neither side — the page says so
// in words instead, which is the honest thing for a picture with nothing to
// show.
//
// THE ONE THING WORTH LOOKING AT is the hold: a feed on review trust stops
// outside the boundary until a person accepts it, and a feed on auto-accept does
// not stop at all. Drawn per feed, on its own line, just short of the perimeter.
// That is the page's most consequential per-feed setting and it was a word in a
// badge.
//
// Three rows a side and no more. Past that the lists below carry the rest and a
// line under the picture says how many were left out: a diagram of eleven feeds
// is a hairball, and one that quietly drew nine of them would be a lie.
//
// Everything it is built from — plates, boundary, beams, ports — is the shared
// diagram vocabulary in the stylesheet, the same parts the Access picture uses.
// This file places them and labels them; it sets no colour and no coordinate.

// fedOut is one thing this registry offers: a channel, its size, whether
// anything is needed to read it, and when somebody last did.
//
// LastRead is only knowable for a gated channel, because a token is what leaves
// a trace. Public is fetched anonymously by design, so an empty LastRead there
// means "we cannot see", not "nobody came" — which is why Open decides how the
// picture draws it rather than the timestamp.
type fedOut struct {
	Title    string
	Count    int
	Open     bool
	LastRead string
}

// fedIn is one feed this registry imports: who from, how much has arrived,
// whether a person sees it before it is live, and whether the last poll worked.
type fedIn struct {
	Name    string
	Count   int
	Review  bool
	Failing bool
}

// rowsFor decides which of the three rows a side uses. One item takes the MIDDLE
// row rather than the top, so a single channel does not read as the first of
// three that failed to render.
func rowsFor(n int) []int {
	switch n {
	case 1:
		return []int{2}
	case 2:
		return []int{1, 3}
	default:
		return []int{1, 2, 3}
	}
}

const fedSceneMax = 3

// federationScene draws what leaves and what arrives. It returns "" when there
// is neither, because a picture of an empty registry is a grid with nothing in
// it — the page's own copy is better at saying "nothing yet, here is the move".
func federationScene(out []fedOut, in []fedIn) string {
	if len(out) == 0 && len(in) == 0 {
		return ""
	}
	shownOut, shownIn := out, in
	if len(shownOut) > fedSceneMax {
		shownOut = shownOut[:fedSceneMax]
	}
	if len(shownIn) > fedSceneMax {
		shownIn = shownIn[:fedSceneMax]
	}

	var b strings.Builder
	b.WriteString(`<div class="dgm fed-stage" aria-hidden="true"><div class="dgm-scene fed-scene">`)
	b.WriteString(`<span class="dgm-fence fed-fence"></span>`)

	// What arrives, drawn first so the registry sits over the lines that reach it.
	rows := rowsFor(len(shownIn))
	for i, f := range shownIn {
		r := rows[i]
		// A FEED THAT IS NOT ARRIVING IS NOT DRAWN ARRIVING. The last poll's
		// error is on the subscription and the picture used to ignore it, so a
		// feed whose host does not resolve still ran traffic down its line —
		// exactly the fault the learning feed lines were fixed for.
		kind := "dgm-beam-bare"
		if f.Failing {
			kind = "dgm-beam-bare dgm-beam-pending"
		}
		fmt.Fprintf(&b, `<span class="dgm-beam %s fed-in fed-r%d"></span>`, kind, r)
		fmt.Fprintf(&b, `<div class="dgm-peer fed-peer fed-r%d"><span></span></div>`, r)
		// The gate, and only where there is one. A mesh says something is
		// required to pass; the word beside it says what — so a feed that is not
		// arriving does not draw one. Its label says "not arriving" instead of
		// naming the gate, and a barred shape with nothing to explain it is a
		// symbol on its own. What is wrong with that feed is the fetch.
		if f.Review && !f.Failing {
			fmt.Fprintf(&b, `<span class="dgm-mesh fed-hold fed-r%d"></span>`, r)
			fmt.Fprintf(&b, `<span class="dgm-tag fed-tag-hold fed-r%d"><b>Review</b></span>`, r)
		}
	}

	// What leaves.
	rows = rowsFor(len(shownOut))
	for i, c := range shownOut {
		r := rows[i]
		fmt.Fprintf(&b, `<span class="dgm-beam dgm-beam-bare fed-lead fed-r%d"></span>`, r)
		// AND A CHANNEL NOBODY HAS READ IS NOT DRAWN BEING READ. "Is the peer we
		// set this up for still fetching?" is the question the channel's own meta
		// line exists to answer, and the picture can answer it at a glance: a
		// live line to a solid reader where somebody has, a dashed one to a
		// hollow end where the token was minted and never used.
		//
		// AND OPEN IS NOT READ. A public channel has no reader to draw: nothing
		// observes who fetches it, so the line leaves the port and stops there.
		// A solid dot on the end of it is a person this registry has never seen,
		// and it wore one on every open channel. The label says "open to anyone",
		// which is the whole of what is known.
		serve := "dgm-beam"
		switch {
		case c.Open:
			serve = "dgm-beam dgm-beam-bare"
		case c.LastRead == "":
			serve = "dgm-beam dgm-beam-pending"
		}
		fmt.Fprintf(&b, `<span class="%s fed-serve fed-r%d"></span>`, serve, r)
		bars := ""
		if !c.Open {
			bars = `<span class="dgm-mesh dgm-bars"></span>`
		}
		fmt.Fprintf(&b, `<div class="dgm-port fed-port fed-r%d"><div class="dgm-cage">`+
			`<span class="dgm-tube dgm-tube1"></span><span class="dgm-tube dgm-tube2"></span>`+
			`<span class="dgm-gate dgm-gate1"></span><span class="dgm-gate dgm-gate2"></span>`+
			`%s</div></div>`, r, bars)
	}

	// This registry, over everything that lands on it. Lit only when it is
	// actually offering something — an unlit plate is a registry that publishes
	// nothing, which is true and worth seeing.
	lit := ""
	if len(shownOut) > 0 {
		lit = " dgm-lit"
	}
	fmt.Fprintf(&b, `<div class="dgm-node fed-node%s">`+
		`<span class="dgm-slab dgm-slab1"></span>`+
		`<span class="dgm-slab dgm-slab2"></span>`+
		`<span class="dgm-slab dgm-slab3"></span></div>`+
		`<span class="dgm-mark fed-mark">%s</span>`, lit, ui.MarkSVG)

	// The labels last, so nothing draws over them.
	rows = rowsFor(len(shownIn))
	for i, f := range shownIn {
		gate := "walks straight in"
		if f.Review {
			gate = "waits for review"
		}
		if f.Failing {
			// The trust only matters to something that arrives.
			gate = "not arriving"
		}
		fmt.Fprintf(&b, `<span class="dgm-tag fed-tag-in fed-r%d"><b>%s</b><i>%s · %s</i></span>`,
			rows[i], html.EscapeString(clip(f.Name, 22)),
			html.EscapeString(importPhrase(f.Count)), gate)
	}
	rows = rowsFor(len(shownOut))
	for i, c := range shownOut {
		gate := "token needed"
		switch {
		case c.Open:
			gate = "open to anyone"
		case c.LastRead == "":
			gate = "no reader yet"
		default:
			gate = "read " + shortStamp(c.LastRead)
		}
		fmt.Fprintf(&b, `<span class="dgm-tag fed-tag-out fed-r%d"><b>%s</b><i>%s · %s</i></span>`,
			rows[i], html.EscapeString(clip(c.Title, 20)),
			html.EscapeString(countPhrase(c.Count, "technique")), gate)
	}

	// What the picture left out, said rather than dropped.
	if n := (len(out) - len(shownOut)) + (len(in) - len(shownIn)); n > 0 {
		var parts []string
		if k := len(out) - len(shownOut); k > 0 {
			parts = append(parts, fmt.Sprintf("%d more channel%s", k, plural(k)))
		}
		if k := len(in) - len(shownIn); k > 0 {
			parts = append(parts, fmt.Sprintf("%d more feed%s", k, plural(k)))
		}
		fmt.Fprintf(&b, `<span class="dgm-tag fed-tag-more">%s, listed below</span>`,
			html.EscapeString(strings.Join(parts, " and ")))
	}

	b.WriteString(`</div></div>`)
	return b.String()
}

// countPhrase writes a count with its noun, and says "none" rather than "0" —
// a zero beside a name reads as a measurement that came back empty, and this is
// a channel nobody has published to yet.
func countPhrase(n int, noun string) string {
	if n == 0 {
		return "none yet"
	}
	return fmt.Sprintf("%d %s%s", n, noun, plural(n))
}

// importPhrase counts what has arrived. "imported" is already the participle, so
// it takes no plural — countPhrase would write "7 importeds".
func importPhrase(n int) string {
	if n == 0 {
		return "none yet"
	}
	return fmt.Sprintf("%d imported", n)
}

// clip keeps a label to one line. The full name is in the list below it.
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
