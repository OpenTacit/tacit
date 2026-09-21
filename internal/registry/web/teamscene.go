// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Team picture: what opening this registry to a team does, and what it
// leaves alone.
//
// This page exists to be read BEFORE a decision — the whole justification for
// keeping a member's evidence when they open up is that they were told first
// (docs/design/browser-led-team-transition.md, M7). It made that case in
// four bullets, three of which are claims about shape: your techniques become
// theirs, nothing moves, the address does not change. Prose is the worst
// available way to say "this does not move".
//
// So the reader MOVES IT THEMSELVES. The switch above the picture is the same
// two-position control the Settings page uses, driven by the same script, and it
// saves nothing — it previews. Flip it and four more people arrive around the
// playbook, each with their own way in. The playbook does not move a pixel while
// they do, and that is the claim: the registry the member already has is the
// registry their team gets.
//
// It carries the REAL technique count, because "your 30 techniques become theirs
// to read and use" is a promise about a number the member can check. The count
// after is the same count — there is no second number, and inventing one would
// be inventing the size of a team that does not exist yet.
//
// Rendered in the "Only you" position server-side, so a page with no script
// shows today's truth rather than a preview of tomorrow's.
func teamScene(techniques int) string {
	var b strings.Builder
	b.WriteString(`<div class="dgm tm-stage" data-switch-state="teampreview" data-on="0" aria-hidden="true">` +
		`<div class="dgm-scene tm-scene">`)

	// Who can sign in. It is the one thing that actually widens.
	b.WriteString(`<span class="dgm-fence tm-fence"></span>`)

	// The four who are not here yet. They have no length in the first position,
	// so they grow out of the playbook rather than appearing beside it — the
	// order of events when somebody accepts an invitation.
	for i := 1; i <= 4; i++ {
		fmt.Fprintf(&b, `<span class="dgm-beam dgm-beam-in tm-mate tm-m%d"></span>`, i)
	}
	// And the one who is. This beam does not change: the member reaches the
	// registry the same way afterwards.
	b.WriteString(`<span class="dgm-beam dgm-beam-in tm-you"></span>`)

	// The playbook, fixed. Every coordinate it has is the same in both positions,
	// which is the whole argument and the reason it is drawn at all.
	b.WriteString(`<div class="dgm-node dgm-lit tm-book">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3"></span></div>` +
		`<span class="dgm-mark tm-mark">` + ui.MarkSVG + `</span>`)

	count := "Your playbook"
	if techniques > 0 {
		count = fmt.Sprintf("%d technique%s", techniques, plural(techniques))
	}
	fmt.Fprintf(&b, `<span class="dgm-tag tm-tag-book"><b>%s</b><i>same address, same figures</i></span>`, count)
	b.WriteString(`<span class="dgm-tag tm-tag-you"><b>You</b><i>still the only administrator</i></span>`)
	b.WriteString(`<span class="dgm-tag tm-tag-team"><b>Your team</b><i>an invitation each · readers</i></span>`)

	b.WriteString(`</div></div>`)
	return b.String()
}
