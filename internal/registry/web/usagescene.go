// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Usage picture: where your usage lives, and the one thing that leaves.
//
// The Usage page carries the product's trust precondition — personal usage
// stays on the machine where the agent runs, and the registry never collects it
// — and carried it as one sentence on a page that was otherwise empty board. A
// promise about where data lives is a promise about SHAPE, and shape is the one
// thing prose is worst at. This draws it.
//
// The claim is made STRUCTURALLY, not by a crossed-out arrow. Everything
// personal is inside the dashed line: the sessions, and the log they write to.
// Exactly one line crosses it, it leaves through a port in the wall, and it is
// labelled for what it carries — counts, with nobody's name on them. There is no
// blocked path drawn, because nothing is blocked; there is simply nothing else
// that crosses, and the absence is the guarantee. A barred arrow would have
// invented a thing that tries and fails.
//
// It takes NO ARGUMENTS and reads nothing, which is the point twice over: the
// registry has no per-member usage to draw, and a picture of this claim that
// varied with data would be claiming something narrower than the promise. It is
// also SERVER-RENDERED, unlike the rest of this page — so the page says what it
// is about before any script runs, and still says it if none ever does.
func usageScene() string {
	var b strings.Builder
	b.WriteString(`<div class="dgm usg-stage" aria-hidden="true"><div class="dgm-scene usg-scene">`)

	// The perimeter of the member's own machine, and the one opening in it. The
	// wall is cut where the port stands, because a wall that runs on through the
	// port is a wall nothing gets past — and one line does.
	b.WriteString(`<span class="dgm-fence usg-fence"></span>`)
	b.WriteString(`<span class="usg-gap"></span>`)

	// Three sessions writing to the log. They carry their own end dot and their
	// own traffic, and the traffic runs toward the log because that is the
	// direction a session's usage travels.
	for i := 1; i <= 3; i++ {
		b.WriteString(`<span class="dgm-beam dgm-beam-in usg-write usg-w` + string(rune('0'+i)) + `"></span>`)
	}

	// The one line out, in two halves: to the wall, and on to the registry. It
	// passes through an OPEN port — nothing is needed to send a count, and
	// nothing about the port is gated, so it wears no bars.
	b.WriteString(`<span class="dgm-beam dgm-beam-bare usg-out1"></span>`)
	b.WriteString(`<span class="dgm-beam dgm-beam-bare usg-out2"></span>`)
	b.WriteString(`<div class="dgm-port usg-port"><div class="dgm-cage">` +
		`<span class="dgm-tube dgm-tube1"></span><span class="dgm-tube dgm-tube2"></span>` +
		`<span class="dgm-gate dgm-gate1"></span><span class="dgm-gate dgm-gate2"></span>` +
		`</div></div>`)

	// The log: a live store like the registry, and deliberately WITHOUT the mark.
	// The mark says whose machine a thing is, and this one is the member's.
	b.WriteString(`<div class="dgm-node dgm-lit usg-log">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3"></span></div>`)

	// The registry, wearing the mark, outside the perimeter.
	b.WriteString(`<div class="dgm-node dgm-lit usg-reg">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3"></span></div>` +
		`<span class="dgm-mark usg-mark">` + ui.MarkSVG + `</span>`)

	// The labels. Each names the thing under it, and the one on the crossing line
	// is the only sentence on the page a reader has to take on trust.
	b.WriteString(`<span class="dgm-tag usg-tag-machine"><b>Your machine</b></span>`)
	b.WriteString(`<span class="dgm-tag usg-tag-sessions"><b>Your sessions</b><i>queries and suggestions</i></span>`)
	// "stays here" was true of every deployment the scene was drawn for, and the
	// sealed ledger made it false in one of them: a copy does cross, and the
	// registry cannot read it (ledger.go). "readable only here" is true in both
	// — the log is plaintext on this side of the wall and ciphertext anywhere
	// else — and it keeps the claim the diagram is actually making.
	b.WriteString(`<span class="dgm-tag usg-tag-log"><b>Usage log</b><i>decrypted on this machine</i></span>`)
	b.WriteString(`<span class="dgm-tag usg-tag-out"><b>Outcomes</b><i>aggregate counts without identity</i></span>`)
	b.WriteString(`<span class="dgm-tag usg-tag-reg"><b>The registry</b><i>cohort data only</i></span>`)

	b.WriteString(`</div></div>`)
	return b.String()
}
