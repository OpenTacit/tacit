// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// Where a member stands in the move from their own registry to their team's
// (docs/design/browser-led-team-transition.md, M5).
//
// The transition used to live in a panel under the playbook, which is where a
// member's techniques are rather than where the question is. It is a place now,
// and what it renders is the answer to "where am I in this":
//
//	alone                 — invite somebody, or accept a link you were sent
//	opened to a team      — who is here, and how the next person joins
//	contributed elsewhere — what became of the work (the standings)
//	both                  — still your registry, tools pointed at theirs (M9)
//
// It is one page rather than four because a member does not know which of those
// they are in; that is the thing they came to find out.

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/merge"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

func (s *Server) pageTeam(r *http.Request, user oidc.Claims) page {
	// NO BANNER AFTER THE SWITCH. There was one, and it said what the panel
	// under it already says: the registry is the team's, send somebody the join
	// link below. Two boxes making the same point, the top one gone on the next
	// visit — the page itself is the confirmation. Where the founder left "You
	// are the only one here" they arrive at "Your team's playbook", with the
	// join link under it.
	var b strings.Builder
	keys, _ := s.Store.ListMemberKeys()
	switch {
	case s.cfg().SingleMember():
		// Alone: the state, then the decision that changes it. The members
		// surface is not rendered — there are no members, and a census of one
		// absent person is furniture.
		b.WriteString(s.alonePanel(r))
		b.WriteString(s.openToTeamPanel(r))
	case s.cfg().TeamEnabled():
		// Opened: who is here, then the whole members surface — the join link,
		// the mint, the coverage, the census. On a team this small that is one
		// page, not two menu items.
		b.WriteString(s.teamHerePanel(r))
		b.WriteString(s.memberSections(r, keys))
	default:
		// An organization's registry, or a pilot with no sign-in at all: this
		// page is not about either. It is reachable by typing the address, so it
		// says which door it is not rather than rendering an empty column — but
		// nothing links here from such a registry (chrome.go, accountMenu).
		b.WriteString(`<section class="panel"><h2>This is an organization’s registry</h2>` +
			`<p class="hint">This registry already belongs to a team. Use <a href="/members">Members</a> ` +
			`to add people and view current members.</p></section>`)
	}
	// The standings, the progress of a run, and the offer to finish: all of them
	// belong to a member who has contributed somewhere, whatever else is true of
	// this registry (contributed.go).
	b.WriteString(s.contributedPanel(r))
	// Full width, like every other view in this shell. It was narrow — 880px
	// against the 1320 Outcomes, Playbook, Review and Members all take — which
	// made it the odd page out to look at AND squeezed its picture: the scene is
	// a size container, so a third off the column is a third off the unit every
	// coordinate in that drawing is measured in, and the labels landed on the
	// plates. Narrow belongs to the invitation pages, which are one column of
	// prose for one reader; this is a dashboard view.
	return page{active: "team", content: b.String(),
		crumbs: s.registryCrumbs("Team"), viewMenu: s.registryViews("people")}
}

// alonePanel is the first state, and the one the whole plan is about: a member
// with a registry of their own and nobody in it.
//
// A lede and nothing else. The two ways of sharing are the two panels below it,
// each with the control that performs it — opening this registry to a team, and
// contributing to an organization that already has one. An earlier cut put a
// card and an "Invite someone" button here as well, which described the panel
// directly underneath and linked to a page that now redirects back to this one.
func (s *Server) alonePanel(r *http.Request) string {
	if !s.isAdmin(s.sessionUser(r)) {
		return ""
	}
	techniques, _ := s.Store.ListTechniques(servingStatuses, 0)
	var b strings.Builder
	b.WriteString(`<section class="panel"><h2>Single-member registry</h2>`)
	if n := len(techniques); n > 0 {
		fmt.Fprintf(&b, `<p class="hint">%d technique%s, measured on your own work.</p>`, n, plural(n))
	}
	// Both ways, named, so the panels below read as a choice rather than as a
	// sequence. The second is only offered while it is actually below: once a
	// member has contributed, the standings sit there instead, and inviting them
	// to do it again reads as though the first time had not happened.
	if l, err := merge.OpenLedger(ledgerPath()); err != nil || l.Destination == "" || len(l.Sent) == 0 {
		b.WriteString(`<p class="hint">You can open this registry to your team or contribute techniques ` +
			`to an existing organization. Your history stays in this registry.</p>`)
	} else {
		b.WriteString(`<p class="hint">Opening this registry to your team keeps its address, ` +
			`its techniques and its figures.</p>`)
	}
	b.WriteString(`</section>`)
	return b.String()
}

// teamHerePanel is what the founder sees once they have opened the registry up:
// who is in, and the door for the next person.
func (s *Server) teamHerePanel(r *http.Request) string {
	keys, err := s.Store.ListMemberKeys()
	if err != nil {
		return ""
	}
	live := liveKeys(keys)
	var b strings.Builder
	b.WriteString(`<section class="panel"><h2>Your team’s playbook</h2>`)
	// The join link, the mint and the census are all directly below this on the
	// same page now, so this says "below" where it used to link to Members —
	// which would send a reader back to the page they are already reading.
	switch len(live) {
	case 0:
		b.WriteString(`<p class="hint">This registry is open to your team, and nobody has joined yet. ` +
			`The join link below mints a key that signs its holder in here, and their outcomes ` +
			`join yours in the figures.</p>`)
	case 1:
		fmt.Fprintf(&b, `<p class="hint">One colleague has joined: <strong>%s</strong>. `+
			`Their outcomes count toward this playbook’s figures alongside yours.</p>`,
			html.EscapeString(live[0].Label))
	default:
		fmt.Fprintf(&b, `<p class="hint"><strong>%d machines</strong> are connected to this playbook, `+
			`and every one of them is listed below.</p>`, len(live))
	}
	b.WriteString(`</section>`)
	return b.String()
}

// liveKeys drops the revoked, so a count of "who is here" does not include
// people who were shut out.
func liveKeys(keys []models.MemberKey) []models.MemberKey {
	out := make([]models.MemberKey, 0, len(keys))
	for _, k := range keys {
		if k.RevokedAt == "" {
			out = append(out, k)
		}
	}
	return out
}

// teamPointer is the one line the Playbook page keeps.
//
// The panel moved here, and a member who was reading their standings above their
// own techniques should not have to discover that. It renders only when there is
// something to point at, so a registry with nothing going on gains no furniture.
func (s *Server) teamPointer(r *http.Request) string {
	if !s.cfg().SingleMember() || !s.isAdmin(s.sessionUser(r)) {
		return ""
	}
	if running, _, _, unread := s.takeMergeReport(); running || unread {
		return `<p class="sub"><a href="/team">A contribution is in progress →</a></p>`
	}
	l, err := merge.OpenLedger(ledgerPath())
	if err != nil || l.Destination == "" || len(l.Sent) == 0 {
		return `<p class="sub"><a href="/team">Share this playbook with your team →</a></p>`
	}
	return fmt.Sprintf(`<p class="sub"><a href="/team">What became of the %d technique%s you contributed to %s →</a></p>`,
		len(l.SentTo(l.Destination)), plural(len(l.SentTo(l.Destination))),
		html.EscapeString(displayHost(l.Destination)))
}

// stillYoursBanner is M9: the member who contributed and has not finished.
//
// `tacit merge` ends in an irreversible step behind a confirmation, which is
// right for somebody who has decided and wrong for somebody who has not. Nothing
// forces the decision, so the page stops presenting it as the next thing to do
// and says what is actually true instead.
func (s *Server) stillYoursBanner(l *merge.Ledger) string {
	if l == nil || l.Destination == "" || len(l.Sent) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="notice"><p><strong>This registry remains available.</strong> `)
	fmt.Fprintf(&b, `Your AI tools use %s. This registry keeps `,
		html.EscapeString(displayHost(l.Destination)))
	b.WriteString(`its techniques, history, and address, and this dashboard remains available.</p>`)
	// The honest limit, said once. A kept registry is a place to look at what
	// you built; it is not a second source of suggestions, because a machine
	// points at one registry and this one is no longer it.
	b.WriteString(`<p class="muted">It no longer answers your agents because each machine uses one ` +
		`registry, and yours now uses the organization’s registry. This dashboard still shows your work ` +
		`and the review status of each contribution.`)
	if since := firstSentAt(l); since != "" {
		fmt.Fprintf(&b, ` Contributed %s.`, html.EscapeString(since))
	}
	b.WriteString(`</p></div>`)
	return b.String()
}

// firstSentAt is when this member contributed, in the words a person uses.
func firstSentAt(l *merge.Ledger) string {
	earliest := ""
	for _, sent := range l.SentTo(l.Destination) {
		if earliest == "" || sent.SentAt < earliest {
			earliest = sent.SentAt
		}
	}
	if earliest == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, earliest)
	if err != nil {
		return ""
	}
	return agoWords(time.Since(t))
}
