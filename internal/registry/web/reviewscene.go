// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Review picture: the two ways into the playbook, and who stands where.
//
// IT IS A FORK, NOT A LANE, and the first version of this drawing got that
// wrong. It showed drafts flowing into evaluation and evaluation into serving,
// with a gate between each — which says two false things: that a draft can
// progress without a person, and that a promoted draft goes on to be evaluated.
//
// Neither is true. contribute.EntryStatus decides at CREATION: a machine-written
// technique is filed as a draft or as under-evaluation, and that is the whole of
// what the auto-evaluate switch does. Nothing ever moves from the draft queue
// into evaluation. And a draft a person promotes goes straight into service —
// the Review page says so in as many words ("Promoted; now in service").
//
// So: one source, two ways in, both ending at the same place. The draft gate is
// ALWAYS barred, because a draft always waits for somebody; only the evaluation
// gate opens, and only when the operator has said the fit evidence may decide.
//
// The exit at the end is drawn dashed while nothing is leaving — a lane that
// exists and is carrying nothing. It goes live the moment a technique's evidence
// turns, which is the one thing here that happens without anybody asking.

// reviewCounts is what the picture draws, all of it already computed by the page.
type reviewCounts struct {
	Drafts, Shadow, Serving, Decayed int
	AutoShadow, AutoPromote          bool
}

func reviewScene(c reviewCounts) string {
	var b strings.Builder
	b.WriteString(`<div class="dgm rev-stage" aria-hidden="true"><div class="dgm-scene rev-scene">`)

	// The fork, leaving the mark that stands where a technique is made.
	b.WriteString(`<span class="dgm-beam dgm-beam-bare rev-fork-up"></span>`)
	b.WriteString(`<span class="dgm-beam dgm-beam-bare rev-fork-down"></span>`)
	// Out of each holding place, through its gate, into service.
	b.WriteString(`<span class="dgm-beam dgm-beam-bare rev-to-g1"></span>`)
	b.WriteString(`<span class="dgm-beam dgm-beam-bare rev-to-g2"></span>`)
	b.WriteString(`<span class="dgm-beam dgm-beam-bare rev-serve-up"></span>`)
	b.WriteString(`<span class="dgm-beam dgm-beam-bare rev-serve-down"></span>`)

	// The draft gate never opens: a draft always waits for somebody. The
	// evaluation gate opens when the operator lets the fit evidence decide.
	b.WriteString(gateHTML("rev-g1", true))
	b.WriteString(gateHTML("rev-g2", !c.AutoPromote))

	exit := "dgm-beam-pending"
	if c.Decayed > 0 {
		exit = "dgm-beam"
	}
	fmt.Fprintf(&b, `<span class="dgm-beam %s rev-out"></span>`, exit)

	// The mark at the branch point, which is where a new technique is made. It
	// is the same badge the registry wears in every other picture, and it is what
	// the picture had instead of a source: a bare beam arriving from off-scene
	// said something turns up, and not what makes it. Last, so it sits over the
	// two beams that leave it.
	b.WriteString(`<span class="dgm-mark rev-mark">` + ui.MarkSVG + `</span>`)

	for _, n := range []struct {
		cls string
		lit bool
	}{{"rev-a", false}, {"rev-b", false}, {"rev-c", true}} {
		lit := ""
		if n.lit {
			lit = " dgm-lit"
		}
		fmt.Fprintf(&b, `<div class="dgm-node %s%s">`+
			`<span class="dgm-slab dgm-slab1"></span>`+
			`<span class="dgm-slab dgm-slab2"></span>`+
			`<span class="dgm-slab dgm-slab3"></span></div>`, n.cls, lit)
	}

	// The labels carry the counts, in the names the rest of the page uses.
	fmt.Fprintf(&b, `<span class="dgm-tag rev-tag-in"><b>A new technique</b><i>%s</i></span>`,
		intakePhrase(c.AutoShadow))
	fmt.Fprintf(&b, `<span class="dgm-tag rev-tag-a"><b>%s</b><i>requires review</i></span>`,
		lanePhrase(c.Drafts, "draft"))
	fmt.Fprintf(&b, `<span class="dgm-tag rev-tag-b"><b>%s</b><i>under evaluation · shown to nobody</i></span>`,
		lanePhrase(c.Shadow, "technique"))
	fmt.Fprintf(&b, `<span class="dgm-tag rev-tag-c"><b>%s</b><i>available in retrieval</i></span>`,
		lanePhrase(c.Serving, "technique"))
	b.WriteString(`<span class="dgm-tag rev-tag-g1"><b>you decide</b></span>`)
	fmt.Fprintf(&b, `<span class="dgm-tag rev-tag-g2"><b>%s</b></span>`, gatePhrase(c.AutoPromote))
	fmt.Fprintf(&b, `<span class="dgm-tag rev-tag-out"><i>%s</i></span>`, decayPhrase(c.Decayed))

	b.WriteString(`</div></div>`)
	return b.String()
}

// gateHTML is one gate. Barred means a technique cannot pass without somebody;
// open means it can.
func gateHTML(cls string, barred bool) string {
	bars := ""
	if barred {
		bars = `<span class="dgm-mesh dgm-bars"></span>`
	}
	return fmt.Sprintf(`<div class="dgm-port %s"><div class="dgm-cage">`+
		`<span class="dgm-tube dgm-tube1"></span><span class="dgm-tube dgm-tube2"></span>`+
		`<span class="dgm-gate dgm-gate1"></span><span class="dgm-gate dgm-gate2"></span>`+
		`%s</div></div>`, cls, bars)
}

// intakePhrase says what the fork actually decides, which is where a new
// MACHINE-written technique is filed the moment it is made. One a person wrote
// is a draft either way, so the sentence says whose it is.
func intakePhrase(autoShadow bool) string {
	if autoShadow {
		return "machine-written ones start under evaluation"
	}
	// Three words shorter than "machine-written ones start as drafts too",
	// which is the difference between three lines and two in a label seven ems
	// wide — and widening the label instead puts it on the drafts stack.
	return "machine-written ones as well"
}

// lanePhrase counts what is in a lane. An empty lane says it is empty rather
// than reporting a zero, because a lane at rest is a state and not a
// measurement of nothing.
func lanePhrase(n int, noun string) string {
	if n == 0 {
		return "Empty"
	}
	return fmt.Sprintf("%d %s%s", n, noun, plural(n))
}

// gatePhrase says who is standing at a gate. Named for the person, because the
// question an operator brings to this page is which gates still need them.
func gatePhrase(automatic bool) string {
	if automatic {
		return "automatic"
	}
	return "you decide"
}

// decayPhrase is the exit. It is the one lane nobody asks for, so it says what
// it is doing rather than carrying a count nobody set.
func decayPhrase(n int) string {
	if n == 0 {
		return "retires automatically when its helped rate declines"
	}
	return fmt.Sprintf("%d retiring due to declining helped rates", n)
}
