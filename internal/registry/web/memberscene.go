// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"strings"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/ui"
)

// The Members picture: who is actually in, and who was invited and never came.
//
// Once a registry is opened up, this page stops being about a decision and
// becomes the founder's daily one — and the question it is opened with is not
// "how many keys exist" but "did they actually join?" The page said "3 machines
// are connected to this playbook" and listed them; whether a key had ever been
// used was a timestamp in a column.
//
// So the picture draws the distinction and nothing else. A member who has signed
// in has a live line to the playbook with traffic on it. A key that was minted
// and never used has a DASHED line and a hollow end — somebody is expected there
// and has not arrived. One glance answers the question the table takes a read to
// answer.
//
// The owner keeps the west slot in every arrangement, because they are the one
// person who is always there and the only admin; the rest are placed from a
// table that keeps the ring balanced at any count rather than filling clockwise
// and leaving a gap.
//
// Seven members and no more. Past that the census below carries the rest and a
// line says how many — a ring of twenty dots answers nothing, and one that
// quietly drew seven of twenty would be answering it wrongly.

// memberSlot is one position on the ring: how far from the playbook, and at what
// angle. The ring is an ELLIPSE, because the scene is twice as wide as it is
// tall and a circle would leave the sides empty and crowd the top.
type memberSlot string

const (
	slotE  memberSlot = "e"
	slotNE memberSlot = "ne"
	slotN  memberSlot = "n"
	slotNW memberSlot = "nw"
	slotW  memberSlot = "w"
	slotSW memberSlot = "sw"
	slotS  memberSlot = "s"
	slotSE memberSlot = "se"
)

// memberRing keeps the arrangement balanced at every count. Filling the slots in
// a fixed order would put two people side by side and leave the other half of
// the ring bare, which reads as a picture that failed to finish.
//
// WEST IS THE OWNER'S, so these are the other seven slots. Every row is
// symmetric about the horizontal, with the ring's open side facing the one
// person the picture always draws there.
var memberRing = map[int][]memberSlot{
	1: {slotE},
	2: {slotNE, slotSE},
	3: {slotNE, slotE, slotSE},
	4: {slotN, slotNE, slotSE, slotS},
	5: {slotN, slotNE, slotE, slotSE, slotS},
	6: {slotNW, slotN, slotNE, slotSE, slotS, slotSW},
	7: {slotNW, slotN, slotNE, slotE, slotSE, slotS, slotSW},
}

// memberRingWhole is the same table for a registry with NO OWNER, where west is
// nobody's and the members have the whole ring.
//
// The two are not one table. An organization's registry has no owner link, so
// memberRing's arrangements — which are balanced AROUND a west anchor — drew
// five people bunched into the eastern half with the other half bare, and the
// thing that would have explained the gap was the one node the picture does not
// draw there. Balanced on its own, the ring closes.
var memberRingWhole = map[int][]memberSlot{
	1: {slotW},
	2: {slotW, slotE},
	3: {slotW, slotNE, slotSE},
	4: {slotNW, slotNE, slotSE, slotSW},
	5: {slotW, slotNW, slotNE, slotSE, slotSW},
	6: {slotW, slotNW, slotNE, slotE, slotSE, slotSW},
	7: {slotW, slotNW, slotN, slotNE, slotSE, slotS, slotSW},
}

const memberSceneMax = 7

// memberScene draws the ring. It takes the live keys — revoked ones are not
// people who are here — the playbook's technique count, and whether to draw the
// owner.
//
// owner is FALSE on an organization's registry, and that is not a detail. There
// the way in is the identity provider, nobody holds an owner link, and a node
// labelled "you · owner link" would be describing a credential that does not
// exist. The ring draws member keys, which is what a member key registry has.
func memberScene(live []models.MemberKey, techniques int, owner bool) string {
	shown := live
	if len(shown) > memberSceneMax {
		shown = shown[:memberSceneMax]
	}
	joined, waiting := 0, 0
	for _, k := range live {
		if k.LastSeen != "" {
			joined++
		} else {
			waiting++
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="dgm mbr-stage" aria-hidden="true"><div class="dgm-scene mbr-scene">`)

	// The owner's line first, where there is one. It is never pending: they are
	// here, and they are the reason the registry exists.
	if owner {
		b.WriteString(`<span class="dgm-beam dgm-beam-in mbr-owner"></span>`)
	}

	ring := memberRing
	if !owner {
		ring = memberRingWhole
	}
	for i, k := range shown {
		slot := ring[len(shown)][i]
		kind := "dgm-beam-in"
		if k.LastSeen == "" {
			// Minted and never used. No traffic, because none has run.
			kind = "dgm-beam-pending"
		}
		fmt.Fprintf(&b, `<span class="dgm-beam %s mbr-at mbr-%s"></span>`, kind, slot)
	}

	b.WriteString(`<div class="dgm-node dgm-lit mbr-book">` +
		`<span class="dgm-slab dgm-slab1"></span>` +
		`<span class="dgm-slab dgm-slab2"></span>` +
		`<span class="dgm-slab dgm-slab3"></span></div>` +
		`<span class="dgm-mark mbr-mark">` + ui.MarkSVG + `</span>`)

	book := "Your team’s playbook"
	if techniques > 0 {
		book = fmt.Sprintf("%d technique%s", techniques, plural(techniques))
	}
	fmt.Fprintf(&b, `<span class="dgm-tag mbr-tag-book"><b>%s</b><i>one playbook, everybody’s outcomes</i></span>`, book)
	if owner {
		b.WriteString(`<span class="dgm-tag mbr-tag-owner"><b>You</b><i>owner link · administrator</i></span>`)
	}
	fmt.Fprintf(&b, `<span class="dgm-tag mbr-tag-count"><b>%s</b><i>%s</i></span>`,
		joinedPhrase(joined), waitingPhrase(len(live), waiting))

	if n := len(live) - len(shown); n > 0 {
		fmt.Fprintf(&b, `<span class="dgm-tag mbr-tag-more">and %d more, listed below</span>`, n)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// joinedPhrase counts the keys that have been used.
//
// NOT "signed in". What the store records is when a key was last seen, and a
// member key is carried by a harness — the same key on two machines is one key,
// and a key used by an agent is not somebody at a screen. The count is keys,
// which is what it counts, and the page says so.
func joinedPhrase(n int) string {
	switch n {
	case 0:
		return "No keys used yet"
	case 1:
		return "1 key in use"
	}
	return fmt.Sprintf("%d keys in use", n)
}

// waitingPhrase counts the invitations nobody has spent. It is the actionable
// half: a key that was never used is somebody to go and ask.
//
// With no keys at all there is nothing to have used, and the picture said "every
// invitation used" — a claim about a set with nothing in it, on the one registry
// where the next move is obvious. It says there are none, and the invite form
// sits beside it.
func waitingPhrase(keys, n int) string {
	if keys == 0 {
		return "no invitations yet"
	}
	switch n {
	case 0:
		return "every invitation used"
	case 1:
		return "1 invitation not used yet"
	}
	return fmt.Sprintf("%d invitations not used yet", n)
}
