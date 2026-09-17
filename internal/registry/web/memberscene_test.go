// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

// memberSectionsHTML renders the members surface of a team-mode registry.
func memberSectionsHTML(t *testing.T) string {
	t.Helper()
	srv, _ := newServer(t)
	srv.Cfg.AuthMode = "team"
	srv.Cfg.OwnerSecret = "s"
	// A census with nobody in it renders no table, so seed one.
	if err := srv.Store.InsertMemberKey(mk("ada@example.com", true)); err != nil {
		t.Fatal(err)
	}
	keys, _ := srv.Store.ListMemberKeys()
	return srv.memberSections(ownerRequest(srv), keys)
}

func mk(label string, seen bool) models.MemberKey {
	k := models.MemberKey{ID: label, Label: label}
	if seen {
		k.LastSeen = "2026-09-01T00:00:00Z"
	}
	return k
}

// The question this page is opened with is "did they actually join?", and the
// answer is a SHAPE: a solid line where a key has been used, a dashed line with
// a hollow end where one was minted and never used. It was a timestamp in a
// column.
func TestMemberSceneTellsJoinedFromInvited(t *testing.T) {
	s := memberScene([]models.MemberKey{mk("a", true), mk("b", false), mk("c", true)}, 12, true)
	if n := strings.Count(s, "dgm-beam-pending"); n != 1 {
		t.Errorf("%d pending reaches; one key here has never been used", n)
	}
	if n := strings.Count(s, "dgm-beam-in mbr-at"); n != 2 {
		t.Errorf("%d live reaches; two of these keys have been used", n)
	}
	if !strings.Contains(s, "2 keys in use") || !strings.Contains(s, "1 invitation not used yet") {
		t.Error("the summary does not count what the ring draws")
	}
	// KEYS, not people. The store records when a key was last seen, and a member
	// key is carried by a harness — the same key on two machines is one key.
	if strings.Contains(s, "signed in") {
		t.Error("the picture calls a used key a person who signed in")
	}
}

// THREE empty states, and the third was being told as the second. A key nobody
// has used says so; no outstanding invitations says so; and a registry with no
// keys at all has nothing to have used — it said "every invitation used", which
// is a claim about a set with nothing in it.
func TestMemberSceneNamesTheEmptyStates(t *testing.T) {
	none := memberScene([]models.MemberKey{mk("a", false)}, 3, true)
	if !strings.Contains(none, "No keys used yet") {
		t.Error("a registry whose keys are all unused reports a count instead of saying so")
	}
	all := memberScene([]models.MemberKey{mk("a", true)}, 3, true)
	if !strings.Contains(all, "every invitation used") {
		t.Error("no outstanding invitations should be said, not left as a zero")
	}
	empty := memberScene(nil, 3, true)
	if !strings.Contains(empty, "no invitations yet") {
		t.Error("a registry with no keys at all does not say so")
	}
	if strings.Contains(empty, "every invitation used") {
		t.Error("a registry nobody has been invited to claims every invitation was used")
	}
	if strings.Contains(all, "0 invitation") || strings.Contains(none, "0 keys") {
		t.Error("the picture reports a zero where it has something truer to say")
	}
}

// The ring stays BALANCED at every count. Filling slots in a fixed order would
// put two people side by side and leave the other half bare, which reads as a
// picture that failed to finish rather than as a team of two.
func TestMemberSceneRingIsBalancedAtEveryCount(t *testing.T) {
	for n := 1; n <= memberSceneMax; n++ {
		slots, ok := memberRing[n]
		if !ok {
			t.Fatalf("no arrangement for %d members", n)
		}
		if len(slots) != n {
			t.Errorf("%d members take %d slots", n, len(slots))
		}
		seen := map[memberSlot]bool{}
		for _, s := range slots {
			if seen[s] {
				t.Errorf("arrangement for %d puts two people in slot %s", n, s)
			}
			seen[s] = true
		}
		// The owner keeps the west slot in every arrangement.
		if seen["w"] {
			t.Errorf("arrangement for %d takes the owner's slot", n)
		}
	}
}

// A REGISTRY WITH NO OWNER GETS THE WHOLE RING. memberRing's arrangements are
// balanced around the west anchor the owner stands on, so used on an
// organization's registry — where nobody holds an owner link and that node is
// never drawn — they put five people in the eastern half with the other half
// bare, and the thing that would have explained the gap is the one node the
// picture does not draw.
func TestMemberSceneClosesTheRingWhenThereIsNoOwner(t *testing.T) {
	for n := 1; n <= memberSceneMax; n++ {
		slots, ok := memberRingWhole[n]
		if !ok {
			t.Fatalf("no ownerless arrangement for %d members", n)
		}
		if len(slots) != n {
			t.Errorf("%d members take %d slots", n, len(slots))
		}
		seen := map[memberSlot]bool{}
		for _, s := range slots {
			if seen[s] {
				t.Errorf("arrangement for %d puts two people in slot %s", n, s)
			}
			seen[s] = true
		}
		// Every arrangement is symmetric about the horizontal, which is what
		// "balanced" means here — and the half the owner used to fill is in play.
		for a, b := range map[memberSlot]memberSlot{slotNE: slotSE, slotNW: slotSW} {
			if seen[a] != seen[b] {
				t.Errorf("arrangement for %d uses %s without %s; the ring is lopsided", n, a, b)
			}
		}
	}
	if !strings.Contains(memberSceneOrg(5), "mbr-w") {
		t.Error("five members and nobody in the west; the ring is open where the owner is not")
	}
	// And the owner's own arrangements still leave west alone.
	if strings.Contains(memberScene([]models.MemberKey{mk("a", true)}, 3, true), "mbr-at mbr-w") {
		t.Error("a member took the owner's slot on a registry that has an owner")
	}
}

// memberSceneOrg is the picture an organization's registry draws: n used keys
// and no owner link.
func memberSceneOrg(n int) string {
	var keys []models.MemberKey
	for i := 0; i < n; i++ {
		keys = append(keys, mk("m", true))
	}
	return memberScene(keys, 59, false)
}

// Seven and no more, and what is left out is said. A ring of twenty dots answers
// nothing, and one that quietly drew seven of twenty would answer it wrongly.
func TestMemberSceneSaysWhatItLeftOut(t *testing.T) {
	var keys []models.MemberKey
	for i := 0; i < 11; i++ {
		keys = append(keys, mk("m", true))
	}
	s := memberScene(keys, 5, true)
	if n := strings.Count(s, "mbr-at"); n != memberSceneMax {
		t.Errorf("drew %d members; the cap is %d", n, memberSceneMax)
	}
	if !strings.Contains(s, "and 4 more, listed below") {
		t.Error("the ring drops members without saying how many")
	}
	// The counts still describe EVERYBODY, not just the seven drawn.
	if !strings.Contains(s, "11 keys in use") {
		t.Error("the summary counts only what fitted; it must count the whole team")
	}
}

// The census is five columns and the page must never scroll sideways, so the
// table does — inside its own frame, the way every other wide table in this UI
// already does. And a join link is one unbroken 90-character token that pushed a
// 390px phone out to 874. Measured before and after: 442/390, then 390/390.
func TestMembersSurfaceFitsAPhone(t *testing.T) {
	if !strings.Contains(appCSS, ".feed code,li>code{overflow-wrap:anywhere}") {
		t.Error("a join link cannot break; it will push the page sideways")
	}
	panel := memberSectionsHTML(t)
	i := strings.Index(panel, "<table>")
	if i < 0 {
		t.Fatal("no census table")
	}
	if !strings.Contains(panel[max(0, i-40):i], `class="table-wrap"`) {
		t.Error("the census table is not in a scroll frame")
	}
}

// On an organization's registry the way in is the identity provider: nobody
// holds an owner link, and a node labelled "you · owner link" would be drawing a
// credential that does not exist.
func TestMemberSceneDrawsNoOwnerWhereThereIsNone(t *testing.T) {
	org := memberScene([]models.MemberKey{mk("a", true)}, 4, false)
	if strings.Contains(org, "mbr-owner") || strings.Contains(org, "owner link") {
		t.Error("an OIDC registry is drawn with an owner link nobody holds")
	}
	own := memberScene([]models.MemberKey{mk("a", true)}, 4, true)
	if !strings.Contains(own, "mbr-owner") {
		t.Error("an owner-mode registry drops the one person who is always there")
	}
}

// It lives on the SHARED members surface, so both doors to it get it: /members on
// an organization's registry, /team on one that was opened to a team. Put on one
// page it missed the other entirely.
func TestMemberSceneIsOnTheSurfaceBothPagesRender(t *testing.T) {
	if !strings.Contains(memberSectionsHTML(t), "mbr-scene") {
		t.Error("the members surface does not carry the ring")
	}
}

// BOTH HALVES ARE CAPTIONED, and that is not decoration. One column opened with
// a plate and the other with a heading, so the two columns' first box edges
// started eighty pixels apart and the band read as two things that had failed to
// line up. And the captions take no size of their own: Coverage and Access
// further down the page are h3s too, and a heading two pixels smaller than the
// ones under it reads as a different kind of heading rather than the same one.
func TestMembersBandCaptionsBothHalves(t *testing.T) {
	panel := memberSectionsHTML(t)
	if !strings.Contains(panel, `<div class="mbr-who"><h3>Who is here</h3>`) {
		t.Error("the picture's half has no caption, so its plate starts above the one beside it")
	}
	if !strings.Contains(panel, `<div class="mbr-add"><h3>Add a member</h3>`) {
		t.Error("the add half's caption is not inside the column")
	}
	if !strings.Contains(appCSS, ".mbr-who>h3,.mbr-add>h3{margin:.1rem 0 .55rem}") {
		t.Error("the band's captions are sized apart from the page's other h3s")
	}
}

// THE MARK IS NEVER DISTORTED, in any picture. A logo is a fixed drawing;
// squashed to a plate's angle it is a different shape, and every picture here
// would be showing a slightly different one.
//
// The way that is guaranteed is structural: the mark is drawn OUTSIDE the plate
// stack, so no 3D can reach it. It was first tried as a child of the top plate
// carrying the exact inverse of the node's rotation, which cannot work — a
// perspective projects an element's children before the element's own transform
// is applied, so the inverse composes against an already-projected drawing and
// comes out a quarter turn round. Measured in Chromium, every scene's mark is
// now exactly square.
func TestTheMarkIsNeverInsideThePlateStack(t *testing.T) {
	scenes := map[string]string{
		"access":     accessScene(true),
		"federation": federationScene([]fedOut{{Title: "c", Open: true}}, nil),
		"usage":      usageScene(),
		"team":       teamScene(3),
		"members":    memberScene([]models.MemberKey{mk("a", true)}, 3, true),
		"setup":      setupScene("h:1", "a file store"),
		"learning":   learningScene(9, []learningGate{{Name: "A", Needs: []learningNeed{{What: "facts", Have: 1, Need: 2}}}}),
	}
	for name, s := range scenes {
		if !strings.Contains(s, "dgm-mark") {
			t.Errorf("%s draws no mark", name)
			continue
		}
		// A mark inside a slab is a mark inside the node's 3D context.
		i := strings.Index(s, "dgm-mark")
		before := s[:i]
		if k := strings.LastIndex(before, "dgm-slab"); k >= 0 {
			if !strings.Contains(before[k:], "</span>") {
				t.Errorf("%s draws its mark inside a plate; the plate's angle will squash it", name)
			}
		}
	}
	// And the primitive carries no rotation of its own to be got wrong.
	rule := setRule(t, ".dgm-mark")
	if strings.Contains(rule, "rotate") {
		t.Error(".dgm-mark rotates; outside the node there is nothing to undo")
	}
	if !strings.Contains(rule, "transform:translate(-50%,-50%)") {
		t.Error(".dgm-mark is not centred on the point its scene places it at")
	}
}
