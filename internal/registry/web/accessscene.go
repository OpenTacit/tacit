// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/ui"
)

// The Access picture: what the switch above it just decided, drawn.
//
// The Access plate asks one question and shows one row, so on a wide window it
// stood beside a taller neighbour with most of a plate of empty board under it.
// The space is now the answer: a registry, the world, and whether the two are
// joined.
//
// TWO STATES, ONE SCENE. Every part is in both positions and MOVES between them
// — the clients travel from a local cluster out onto the globe and onto the
// cities they are in, their beams stretch to follow, the world comes up out of
// the dark, and the operator's boundary draws in around the registry alone. The
// ingress is the one thing that is not in both, and it does not appear: it GROWS
// OUT OF THE REGISTRY along the link the registry opens to it, because that is
// what happens. Two pictures swapped would say Own address and Global Access
// were two settings.
//
// WHAT GLOBAL ACCESS ACTUALLY IS, and the reason the picture is worth drawing:
// the registry does not open a port. It dials OUT to an ingress, and the world
// reaches the ingress. So the traffic on the uplink runs away from the registry
// — that is the connection being established, by the only end that can — and the
// traffic on the client beams runs in to the ingress, which is where the two
// meet. The ingress stands OUTSIDE the boundary — which is the point of keeping
// that boundary drawn in both positions. Global Access does not dissolve the
// operator's perimeter; it runs one line out through it.
//
// THE WORLD IS THE WORLD, AND IT IS A SPHERE. It was a wireframe globe — six
// meridians and three parallels — which is a drawing of a sphere and not of
// anywhere; then it was the real coastlines on a flat map rolling behind a round
// window, which reads as exactly that, since a continent at the rim stayed its
// full width where a sphere squeezes it away. It is now an orthographic
// projection, worked out per frame: the coastlines are in worldpath.go and the
// projection in globe.go, the only numbers this picture does not measure in its
// own grid.
//
// The two machines are DIFFERENT SHAPES, because they are different things. The
// registry is flat plates lying down, stacked: something that holds what it is
// given. The ingress is an open tube standing on edge, and every line in the
// picture runs in one mouth and out the other: something traffic goes THROUGH.
// Drawn as a smaller plate it read as a lesser registry, which is the one thing
// it is not.
//
// And the registry wears the mark, on the top plate, at the angle the plate is
// seen — printed on the machine rather than beside it. It is the same MarkSVG
// the header uses, so there is one drawing of it. It answers the question the
// picture otherwise leaves open, which is which of these two boxes is yours.
//
// It is GEOMETRY ONLY. This file emits the skeleton and nothing else: no
// coordinate, no angle, no colour. All of that is in the stylesheet with the
// rest of the .set block, so the picture stays where the rest of the look lives
// and a theme, a hairline or a token still has one home. The two drawings it
// carries — the mark, and the world's coastlines — are shapes rather than
// placements, and each has a file of its own. So does the one piece of
// arithmetic no stylesheet can do, which is turning a sphere.
//
// The state is an ATTRIBUTE, not a hidden/shown pair. Every other row on this
// page belongs to one position of the switch and is hidden in the other
// (onlyWhenSwitch); this one belongs to both, so switchScript reflects the
// switch onto data-on and the CSS transitions between them. Rendered server-side
// from the saved setting, it is correct before the script runs and correct if it
// never does.
//
// aria-hidden, because it is the switch said again in a picture. A screen reader
// gets "Global Access, checked" from the control itself and the address from the
// row beside it; a second, wordless copy of the same fact is noise.
//
// The spinning globe and the beams' traffic are the one piece of ambient motion
// in the registry, and they exist because the operator asked for the world to
// turn. Both stop dead under prefers-reduced-motion, which leaves the same
// diagram without the animation — the picture carries its meaning in position
// and shape, so nothing it says is lost when it holds still.
func accessScene(on bool) string {
	state := "0"
	if on {
		state = "1"
	}
	var b strings.Builder
	// TWO STATES, AND THEY ARE NOT THE SAME STATE. data-on follows the control and
	// switchScript moves it the moment the switch does; data-saved is the setting
	// the server rendered and only a save changes it. While they differ the
	// picture is a preview of something that has not happened, and the picture
	// says so rather than drawing a tunnel the registry has not opened.
	fmt.Fprintf(&b, `<div class="dgm acc" data-switch-state="publish" data-on="%[1]s" `+
		`data-saved="%[1]s" aria-hidden="true"><div class="dgm-scene acc-scene">`, state)

	// The operator's boundary. It holds this machine's clients on Own address and
	// contracts to the registry alone on Global Access, leaving the ingress
	// outside it.
	b.WriteString(`<span class="dgm-fence acc-fence"></span>`)

	// The world: the actual one, turning. Sea, the land, and the limb closing
	// over both.
	//
	// The path carries BOTH the earth and one view of it. data-world is the
	// coastlines in degrees, which is what the script projects each frame; d is
	// that same projection worked out here, so the page shows a globe before any
	// script runs and the same globe if none ever does.
	b.WriteString(`<div class="acc-globe"><svg class="acc-earth" viewBox="-2 -2 184 184">` +
		`<circle class="acc-sea" cx="90" cy="90" r="90"/>` +
		`<path class="acc-land" data-world="` + worldLandPath + `" d="` +
		globeAt(worldRings(), globeFace) + `"/>` +
		`<circle class="acc-limb" cx="90" cy="90" r="90"/>` +
		`</svg></div>`)

	// The link the registry opens. On Own address it has no length, so the ingress
	// at the end of it has nowhere to be.
	//
	// IT CARRIES AN ARROWHEAD, NOT TRAFFIC. Everything else in this picture is a
	// line something travels along; this one is the registry DIALLING — an act,
	// done once, in the one direction that matters, and the whole point of the
	// drawing is which end does it. A dot going round it for ever said the
	// opposite: a channel with something on it.
	b.WriteString(`<span class="dgm-beam dgm-beam-bare dgm-beam-arrow acc-uplink"></span>`)

	// A reach for each client, carrying it on the far end and its own traffic
	// along it — so a client is a length and an angle from whatever the
	// beams start at, and cannot come off its line while the two positions swap.
	// What they start at is the thing that changes: the registry itself on Own
	// address, the ingress on Global Access.
	//
	// AND OUT THERE A CLIENT IS A PLACE. On Global Access they land on San
	// Francisco, São Paulo, London, Tokyo and Sydney, and they stay on those
	// cities while the world turns — which means each one goes dark for half of
	// every revolution, on the far side of a sphere, where there is nothing to
	// draw. globe.go works out where a city has turned to; the stylesheet carries
	// the same positions at the still's angle, so the picture is right before any
	// script runs.
	//
	// One reach per city, counted off globeCities rather than written out here, so
	// a city added there arrives with a reach to travel on.
	for i := 1; i <= len(globeCities); i++ {
		fmt.Fprintf(&b, `<span class="dgm-beam dgm-beam-in acc-beam acc-beam%d"></span>`, i)
	}

	// The ingress, over the beams that land on it: an open tube on edge — two rims
	// and the four edges between them — with everything running through it.
	// The cage is not decoration: the outer element moves and fades, the inner one
	// turns, because an element that fades is rendered as a group and a group has
	// no 3D (see the stylesheet). The turn is the arrival — the tube comes round
	// from edge-on to face the world as it grows.
	b.WriteString(`<div class="dgm-port acc-ingress"><div class="dgm-cage">` +
		`<span class="dgm-tube dgm-tube1"></span>` +
		`<span class="dgm-tube dgm-tube2"></span>` +
		`<span class="dgm-gate dgm-gate1"></span>` +
		`<span class="dgm-gate dgm-gate2"></span></div></div>`)

	// This registry: three plates stacked, seen from the corner, the top one
	// wearing the mark. Last, so it sits over the beams that end on it.
	b.WriteString(`<div class="dgm-node acc-node">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3 acc-slab3"></span></div>` +
		`<span class="dgm-mark acc-mark">` + ui.MarkSVG + `</span>`)

	// And the word for the state the scene cannot draw: what the switch says is
	// not what the registry does until the form is saved.
	b.WriteString(`<span class="dgm-tag acc-tag-preview"><b>Preview — not saved</b></span>`)

	b.WriteString(`</div></div>`)

	// The one script in any of these pictures, and it is here because a
	// projection is not a transform: the globe is redrawn as it turns, not moved.
	// It ships with the scene rather than with the page's other scripts, which
	// are admin-only — a reader who cannot change a setting still gets the world.
	b.WriteString(globeScript)
	return b.String()
}
