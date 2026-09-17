// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Setup picture: what `serve` is about to stand up.
//
// First-run setup is a single narrow column of fields with a wide empty margin
// either side, and the person reading it has never seen this product's shape.
// The form asks for a key, an address and a place to keep data without ever
// saying what those three things are to each other.
//
// So the picture says it: agents reach the registry and authenticate with the
// key the form just generated, and the registry keeps what it learns in the
// store named in the field below. Nothing else — the wizard has enough on its
// hands, and a diagram of everything OpenTacit does would be a second thing to read
// rather than a way of reading the first.
//
// It is drawn from the values the form is ABOUT to write: the address from the
// host and port fields, and the store from whichever of the two storage fields
// is filled. So it is a picture of this registry rather than of the idea of one,
// and it is server-rendered, since the wizard runs before anything else exists.
func setupScene(addr, store string) string {
	var b strings.Builder
	b.WriteString(`<div class="dgm stp-stage" aria-hidden="true"><div class="dgm-scene stp-scene">`)

	// Three agents reaching in. The traffic runs toward the registry, which is
	// the direction a session's queries travel.
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&b, `<span class="dgm-beam dgm-beam-in stp-agent stp-a%d"></span>`, i)
	}
	// And down to the store. No end dot: the store is drawn there.
	b.WriteString(`<span class="dgm-beam dgm-beam-bare stp-store"></span>`)
	b.WriteString(`<div class="dgm-peer stp-disk"><span></span></div>`)

	b.WriteString(`<div class="dgm-node dgm-lit stp-reg">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3"></span></div>` +
		`<span class="dgm-mark stp-mark">` + ui.MarkSVG + `</span>`)

	// The address is machine text — it is typed into a client, not read aloud —
	// so it wears the mono face inside an ordinary label. It arrives raw and is
	// escaped HERE, once: escaped by the caller as well, a host with an ampersand
	// in it came out carrying its own entity.
	fmt.Fprintf(&b, `<span class="dgm-tag stp-tag-reg"><b>This registry</b><i><code>%s</code></i></span>`,
		html.EscapeString(addr))
	b.WriteString(`<span class="dgm-tag stp-tag-agents"><b>Your agents</b><i>authenticate with the API key</i></span>`)
	fmt.Fprintf(&b, `<span class="dgm-tag stp-tag-store"><b>Stored data</b><i>%s</i></span>`,
		html.EscapeString(store))

	b.WriteString(`</div></div>`)
	return b.String()
}
