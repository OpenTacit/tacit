// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Members page: the admin's view of who (which machines) can reach this
// registry, and the lever to disallow one without rotating everyone
// (docs/design/enterprise-readiness.md). This is ACCESS management, not
// analytics — a key shows label, created, last-seen, and status; it is never
// joined to feedback events, so "who has access" is answerable while
// individual performance stays unaskable.
package web

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/ingress"
	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// The copy glyph is shared with the project page, which puts the same control on
// the install command in its hero (internal/ui/chrome.go). So is the command
// itself: what this panel hands a new member and what the project page shows a
// stranger are one string, quoted from one place.
const copyIcon = ui.CopyIcon

// copyablePre renders a command panel with the copy icon embedded in its
// top-right corner (the .copyable styles and the shared data-copy script in
// shell.go); the icon acknowledges with a "Copied" tooltip.
func copyablePre(text string) string {
	esc := html.EscapeString(text)
	return `<div class="copyable"><pre><code>` + esc + `</code></pre>` +
		`<button type="button" class="copy-btn" data-copy="` + esc + `" aria-label="Copy to clipboard">` +
		copyIcon + `</button></div>`
}

// pageMembers has two jobs, in reading order: hand a new machine in (the two
// add-member cards) and account for the machines already in (the table). Everything
// secondary — CLI variants, the root key, troubleshooting — folds into one
// disclosure so the working surfaces stay quiet.
// memberActiveWindow is how recently a member machine must have been seen to
// count as active. A week absorbs a holiday weekend without pretending someone
// who left three months ago is still here.
const memberActiveWindow = 7 * 24 * time.Hour

// activeMachines counts the member machines seen inside the window: unrevoked
// keys with a last-seen that recent.
//
// It has one implementation because it has two readers. The Members page shows
// it to the organization's own administrator, and — when the registry publishes
// through the shared proxy — it is the single figure sent to whoever runs that
// proxy, so they can see how much each tenant is worth carrying (publish.go).
// Two counters would eventually disagree, and the disagreement would be between
// what a tenant sees about itself and what its host sees about it.
func activeMachines(keys []models.MemberKey, window time.Duration) int {
	now, n := time.Now(), 0
	for _, k := range keys {
		if k.RevokedAt != "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, k.LastSeen); err == nil && now.Sub(t) <= window {
			n++
		}
	}
	return n
}

// ActiveMemberMachines is activeMachines over the store, for callers outside
// this file. It answers 0 when the store cannot be read: a figure the registry
// is unsure of is not one to report.
func (s *Server) ActiveMemberMachines() int {
	keys, err := s.Store.ListMemberKeys()
	if err != nil {
		return 0
	}
	return activeMachines(keys, memberActiveWindow)
}

// MemberActivity is the same count over the four windows the shared proxy is
// told about (publish.go): a day, a week, a month, a year.
//
// Every one is a ROLLING window ending now, which is the only shape this data
// supports. A member key holds one last-seen timestamp and it is overwritten on
// every use, so the registry can say how many machines have been active since a
// moment, and cannot say how many were active on a Tuesday in March. Anything
// that looks like history has to be accumulated by whoever keeps the samples.
//
// One read of the key list answers all four, so the reporting path costs the
// same as it did when it reported one.
func (s *Server) MemberActivity() ingress.Stats {
	keys, err := s.Store.ListMemberKeys()
	if err != nil {
		return ingress.Stats{}
	}
	return ingress.Stats{
		Day:   activeMachines(keys, 24*time.Hour),
		Week:  activeMachines(keys, memberActiveWindow),
		Month: activeMachines(keys, 30*24*time.Hour),
		Year:  activeMachines(keys, 365*24*time.Hour),
	}
}

// pageMembers is an organization's member list. On a registry that is one
// member's, or one they have opened to their team, this page is not reached at
// all: /members redirects to /team, which carries these same sections under the
// heading that actually describes them (routes_pages.go, team.go).
func (s *Server) pageMembers(r *http.Request, _ oidc.Claims) page {
	keys, err := s.Store.ListMemberKeys()
	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Who can reach this registry, and how someone new joins.</p></div>`)
	if err != nil {
		b.WriteString(`<p>could not list member keys: ` + html.EscapeString(err.Error()) + `</p>`)
		return page{active: "members", crumbs: s.registryCrumbs("Members"),
			viewMenu: s.registryViews("people"), content: b.String()}
	}
	b.WriteString(s.memberSections(r, keys))
	return page{active: "members", crumbs: s.registryCrumbs("Members"),
		viewMenu: s.registryViews("people"), content: b.String()}
}

// memberSections is the whole members surface — the minted-key receipt, the two
// ways to add somebody, the coverage line and the census — as one block that
// two pages render.
//
// One implementation because it is one subject. The split that used to exist
// was between two DESTINATIONS ("Team" and "Members" in the same menu) for the
// same question, which is what a reader had to resolve before clicking.
func (s *Server) memberSections(r *http.Request, keys []models.MemberKey) string {
	var b strings.Builder
	base := s.externalBaseFor(r)

	if code := r.URL.Query().Get("code"); code != "" {
		// A code, not the key. The key used to ride this redirect in the query
		// string, which put a credential that never expires into the browser's
		// history and into whatever the operator pasted it into. The code
		// stands for the key for fifteen minutes and works once (handoff.go).
		//
		// This is also the way back for somebody whose earlier code expired:
		// mint another here rather than asking a colleague for a whole new
		// invitation.
		b.WriteString(`<div class="notice"><p><strong>Give the member this command.</strong> ` +
			`The code works once and expires in 15 minutes; mint another here if it lapses:</p>` +
			copyablePre(fmt.Sprintf("tacit connect --registry %s --code %s", base, code)) +
			`</div>`)
	}

	// --- Who is here, and the two doors for the next person, SIDE BY SIDE.
	//
	// The picture answers "who has actually joined" and the block beside it is
	// what a founder does about the answer, so they are one band rather than a
	// full-width picture with a metre of empty board either side of it and the
	// action scrolled below. On a narrow window they stack, in that order.
	//
	// The picture belongs on this SURFACE rather than on either page that renders
	// it, because the question is the same on both. Put on the team page alone it
	// missed /members entirely, which is the door an organization's registry uses.
	// BOTH HALVES ARE CAPTIONED, and that is not decoration. One column opened
	// with a plate and the other with a heading, so the two columns' first box
	// edges started eighty pixels apart and the band read as two things that had
	// failed to line up. A title on each puts every edge on the same line.
	//
	// The two captions are the page's own lede said as two halves: who can reach
	// this registry, and how someone new joins.
	techniques, _ := s.Store.ListTechniques(servingStatuses, 0)
	b.WriteString(`<div class="mbr-band"><div class="mbr-who"><h3>Who is here</h3>` +
		`<section class="panel mbr-hero">` +
		memberScene(liveKeys(keys), len(techniques), s.cfg().OwnerEnabled()) +
		`</section></div><div class="mbr-add">`)

	token := mintJoinToken(s.cfg().APIKey, joinTTLDefault)
	joinLine := fmt.Sprintf("curl -fsSL %s/join/%s | sh", base, token)
	// A wall-clock expiry is only useful against the clock the reader is
	// looking at, so this one carries its zone and is rendered in theirs.
	expires := ui.LocalTime(time.Now().Add(joinTTLDefault), ui.LTStamp)
	b.WriteString(`<h3>Add a member</h3><div class="add-grid">`)
	fmt.Fprintf(&b, `<div class="add-technique"><h4>Share a join link</h4>`+
		`<p class="muted">This command installs tacit, creates a member key, and connects supported harnesses.</p>%s`+
		`<p class="muted">It expires %s. Keep it secret; it gives access.</p></div>`,
		copyablePre(joinLine), expires)
	b.WriteString(`<div class="add-technique"><h4>Mint a key by hand</h4>` +
		`<p class="muted">For a member who must get credentials directly from you.</p>` +
		`<form method="post" action="/members/mint" class="inline-form">` +
		`<input name="label" placeholder="who or what it’s for, for example dana@laptop" maxlength="80" required aria-label="Key label"> ` +
		`<button type="submit">Mint</button></form>` +
		`<p class="muted">You get a command carrying a short code, safe to paste into a message: ` +
		`it works once and expires in 15 minutes, and the key itself never leaves this registry ` +
		`until their machine asks for it.</p></div>`)
	b.WriteString(`</div>`)

	fmt.Fprintf(&b, `<details class="fineprint"><summary>More options and notes</summary><ul>`+
		`<li>Member already has tacit installed: <code>tacit join %s/join/%s</code></li>`+
		`<li>Any member can mint a link from their terminal: <code>tacit invite</code></li>`+
		`<li>Manual install without a link: <code>%s</code></li>`+
		`<li>The org root key in registry.env continues to work and does not appear below. If you rotate it, outstanding invite links become void.</li>`+
		`<li>If a member machine has a problem: <code>tacit doctor</code></li>`+
		`</ul></details>`,
		html.EscapeString(base), html.EscapeString(token),
		html.EscapeString(ui.InstallCommandAt(s.requestBase(r))))
	b.WriteString(`</div></div>`)
	// The caution is about an UNGATED dashboard, not about the absence of an
	// identity provider: a single-member registry has a sign-in, and telling
	// its owner that anyone who opens the page can mint access is false.
	if !s.authRequired() {
		b.WriteString(`<p><strong>Caution:</strong> Do not expose the dashboard beyond a trusted network before you configure a sign-in. ` +
			`This registry has none, so each person who can open this page can mint access.</p>`)
	}

	// --- Coverage: the number the first enthusiast moves (growth-plan.md
	// mechanism 4, the honest subset). Derived from member keys alone —
	// created_at and last_seen are access metadata; no feedback event carries
	// an identity, so there is no person-level funnel here by design.
	if len(keys) > 0 {
		now := time.Now()
		active, joined30d, quiet := activeMachines(keys, memberActiveWindow), 0, 0
		for _, k := range keys {
			if k.RevokedAt != "" {
				continue
			}
			if t, err := time.Parse(time.RFC3339, k.CreatedAt); err == nil && now.Sub(t) <= 30*24*time.Hour {
				joined30d++
			}
			if t, err := time.Parse(time.RFC3339, k.LastSeen); err != nil && k.LastSeen != "" {
				quiet++
			} else if err == nil && now.Sub(t) > memberActiveWindow {
				quiet++
			}
		}
		fmt.Fprintf(&b, `<h3>Coverage</h3><p class="sub"><strong>%d</strong> member machine%s active this week · `+
			`<strong>%d</strong> joined in the last 30 days · <strong>%d</strong> now silent</p>`,
			active, plural(active), joined30d, quiet)
	}

	// --- The census: who holds access now.
	b.WriteString(`<h3>Access</h3>`)
	if len(keys) == 0 {
		b.WriteString(`<p class="muted">No member keys yet. The first appears when someone joins or when you mint a key.</p>`)
		return b.String()
	}
	b.WriteString(`<p class="muted">Revoke blocks the machine’s access on its next request; reinstate restores it.</p>`)
	// Five columns do not fit a phone, and the page must never scroll sideways —
	// so the table does, inside its own frame, the way every other wide table in
	// this UI already does (dataTable).
	b.WriteString(`<div class="table-wrap"><table><thead><tr><th>Member</th><th>Created</th><th>Last seen</th><th>Status</th><th></th></tr></thead><tbody>`)
	for _, k := range keys {
		// status is MARKUP from here down, not text: a revoked key carries a
		// date, and a date is a <time> the reader's browser re-dates into its
		// own zone (ui.LocalTimeScript). Everything text-shaped that goes into
		// it is escaped at the point it is added.
		status := "active"
		// Revoke is the one-click safety step; Delete only appears once a key
		// is already revoked, so removing a credential entirely always takes
		// two deliberate actions.
		actions := fmt.Sprintf(`<form method="post" action="/members/revoke/%s"><button class="danger" type="submit">Revoke</button></form>`,
			html.EscapeString(k.ID))
		if k.RevokedAt != "" {
			status = "revoked " + ui.LocalTimeISO(k.RevokedAt, ui.LTYMD)
			actions = fmt.Sprintf(
				`<form method="post" action="/members/reinstate/%s"><button type="submit">Reinstate</button></form> `+
					`<form method="post" action="/members/delete/%s"><button class="danger" type="submit">Delete</button></form>`,
				html.EscapeString(k.ID), html.EscapeString(k.ID))
		}
		// Last-seen is rendered as an age, and a member whose credential has
		// gone quiet is SAID to be quiet: hooks fail silently by design on the
		// member's machine, so this column is where the org notices a
		// disconnected member (rotated key, moved binary) before the member
		// does.
		//
		// An age is a duration between two instants, so it reads the same in
		// every timezone and stays server-rendered. Only the fall-back — a
		// stored value that would not parse — is an absolute date, and that one
		// is localised like any other.
		lastSeen := "never"
		if k.LastSeen != "" {
			lastSeen = ui.LocalTimeISO(k.LastSeen, ui.LTYMD)
			if t, err := time.Parse(time.RFC3339, k.LastSeen); err == nil {
				age := time.Since(t)
				switch {
				case age < 48*time.Hour:
					lastSeen = fmt.Sprintf("%dh ago", int(age.Hours()))
				default:
					lastSeen = fmt.Sprintf("%dd ago", int(age.Hours()/24))
				}
				if age > 7*24*time.Hour && k.RevokedAt == "" {
					status = html.EscapeString(fmt.Sprintf("silent for %d days; tell the member to run `tacit doctor`", int(age.Hours()/24)))
				}
			}
		}
		fmt.Fprintf(&b,
			`<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td class="row-action">%s</td></tr>`,
			html.EscapeString(k.Label), ui.LocalTimeISO(k.CreatedAt, ui.LTYMD), lastSeen,
			status, actions)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// memberActionAllowed is the guard on every key mutation: session-gated when
// OIDC is on (same posture as promote/reject/edit — adminOnly alone is a
// no-op until TACIT_ADMIN_EMAILS is set, and these endpoints mint and revoke
// CREDENTIALS; they must never be open just because the admin list is empty
// on an internet-reachable registry).
func (s *Server) memberActionAllowed(w http.ResponseWriter, r *http.Request) bool {
	return s.signedInOrRedirect(w, r, s.membersHome())
}

// handleMemberAction is the form target for revoke/reinstate/delete. Admin
// action posture matches the technique-review actions: signed-in members during
// the pilot, the TACIT_ADMIN_EMAILS list once set (adminOnly).
func (s *Server) handleMemberAction(w http.ResponseWriter, r *http.Request) {
	if !s.memberActionAllowed(w, r) {
		return
	}
	switch r.PathValue("action") {
	case "revoke":
		_ = s.Store.SetMemberKeyRevoked(r.PathValue("id"), time.Now().UTC().Format(time.RFC3339))
	case "reinstate":
		_ = s.Store.SetMemberKeyRevoked(r.PathValue("id"), "")
	case "delete":
		// Server-side too, not just in the UI: only a revoked key may be
		// deleted, so an active credential can never vanish in one step.
		if k, ok := s.memberKeyByID(r.PathValue("id")); ok && k.RevokedAt != "" {
			_ = s.Store.DeleteMemberKey(k.ID)
		}
	default:
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, s.membersHome(), http.StatusFound)
}

func (s *Server) memberKeyByID(id string) (models.MemberKey, bool) {
	keys, err := s.Store.ListMemberKeys()
	if err != nil {
		return models.MemberKey{}, false
	}
	for _, k := range keys {
		if k.ID == id {
			return k, true
		}
	}
	return models.MemberKey{}, false
}

func (s *Server) handleMemberMint(w http.ResponseWriter, r *http.Request) {
	if !s.memberActionAllowed(w, r) {
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Redirect(w, r, s.membersHome(), http.StatusFound)
		return
	}
	if len(label) > 80 {
		label = label[:80]
	}
	secret, key := NewMemberKey(label)
	if err := s.Store.InsertMemberKey(key); err != nil {
		http.Error(w, "could not mint: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// The code rides the redirect, never the secret: a query string lands in
	// browser history, and a member key does not expire.
	http.Redirect(w, r, s.membersHome()+"?code="+s.newHandoff(secret, s.externalBaseFor(r)), http.StatusFound)
}

// --- M7: inviting is what founds the organization -------------------------
//
// The missing half of the transition. `tacit merge` folds a personal registry
// into an organization that already exists and has already invited you; nothing
// covered the member whose team has no registry at all, which is the common
// case and the one where the enthusiast is standing.
//
// The act is not called "promote" and it is not a mode the member has to learn.
// It is "invite a teammate", and the registry noticing what that implies —
// which is the same move `tacit init` makes when it serves the registry it
// has just set up.
//
// It changes the registry in place: same address, same instance key, same
// techniques, same history. Nothing is copied anywhere and nothing crosses a
// registry boundary, which is why the evidence question below is answered the
// way it is.

// openToTeamPanel is what a registry that is still one person's offers its
// owner: what changes, and then the button. It is a section of /team rather
// than a page of its own — inviting the first person and seeing who is here are
// the same question asked at two moments.
func (s *Server) openToTeamPanel(r *http.Request) string {
	var b strings.Builder
	if !s.isAdmin(s.sessionUser(r)) {
		// The control hands this registry's playbook, and its figures, to
		// whoever the poster names. It is not something to offer a visitor.
		b.WriteString(`<p class="hint">Only this registry’s owner can invite anybody. ` +
			`Run <code>tacit dashboard</code> on the machine it runs on.</p>`)
		return b.String()
	}
	techniques, _ := s.Store.ListTechniques(servingStatuses, 0)

	// THE CONTROL SITS ON THE HEADING LINE, at the panel's top right. It used
	// to close the panel, under a switch, a drawing and five bullets — so the
	// one thing this section exists to let an owner do was the last thing they
	// reached, and on a short window it was below the fold of a panel that
	// reads as explanation. On the title line it is visible the moment the
	// panel is, and the heading asks the question the button answers.
	//
	// Solid rather than outlined: every other control on /team is a hairline
	// plate, and this is the page's one irreversible move. The fill says which
	// of them the panel is for.
	b.WriteString(`<section class="panel"><div class="section-head head-act">` +
		`<h2>What changes when you invite somebody</h2>` +
		`<form method="post" action="/members/open">` +
		fmt.Sprintf(`<input type="hidden" name="csrf" value="%s">`, html.EscapeString(s.csrfToken(r))) +
		`<button class="btn btn-primary" type="submit">Open this registry to my team</button>` +
		`</form></div>`)
	// The picture first, with the control that moves it. A member deciding this
	// is being asked to believe three things about shape — their techniques
	// become the team's, nothing moves, the address holds — and moving the switch
	// answers all three faster than the sentences below can.
	b.WriteString(setSwitchPreview("teampreview", "Your team", "Only you",
		"the playbook has not moved—the same address, and the same figures",
		"today: one owner link, and nobody else can sign in"))
	b.WriteString(teamScene(len(techniques)))
	b.WriteString(`<ul class="feed">`)
	if n := len(techniques); n > 0 {
		fmt.Fprintf(&b, `<li><strong>Your %d technique%s become theirs to read and use.</strong> `+
			`Their agents retrieve from this playbook the way yours do.</li>`, n, plural(n))
	} else {
		b.WriteString(`<li><strong>New techniques and outcomes are shared with the team.</strong></li>`)
	}
	// The evidence sentence. Decided 2026-09-01 and written out here rather than
	// summarised, because the whole justification for keeping it is that the
	// member was told before they chose (browser-led-team-transition.md, M7).
	b.WriteString(`<li><strong>Your existing outcomes become part of the organization’s figures.</strong> ` +
		`The data stays in this registry. Until more members add outcomes, your work will make up most ` +
		`of the sample.</li>`)
	b.WriteString(`<li><strong>The address does not change.</strong> Same link, same bookmarks, ` +
		`same instance key.</li>`)
	b.WriteString(`<li><strong>Your colleagues can sign in.</strong> Invitations create member keys ` +
		`with read access to this dashboard. You keep control of curation, settings, and invitations.</li>`)
	b.WriteString(`</ul>`)
	// The switch is inert without this, and the picture then simply shows today —
	// which is the honest fallback, since today is what is true.
	b.WriteString(switchScript)
	b.WriteString(`</section>`)
	return b.String()
}

// setSwitchPreview is a two-position switch that PREVIEWS rather than saves. It
// is the Settings control (setSwitch) exactly, and what makes it a preview is
// where it sits: outside this panel's one <form>, so an unchecked box posts
// nothing and a checked one posts nothing either. Nothing on this page is saved
// until the button below is pressed.
//
// The two answers beside it are what each position means, in the same place the
// Settings switch puts the address it selects — so a reader who has met one has
// met both.
func setSwitchPreview(key, label, offLabel, whenOn, whenOff string) string {
	return setSwitch(key, label, offLabel, false, false,
		`<span class="gen">`+html.EscapeString(whenOn)+`</span>`,
		`<span class="gen">`+html.EscapeString(whenOff)+`</span>`)
}

// handleOpenTeam performs the change: one setting, written and applied.
//
// Hot-applied rather than left for a restart, for the same reason a settings
// save applies its own: a member who presses a button expects the next page to
// be different. The mode lives in registry.env so it survives one, and the
// owner secret it depends on is already there.
func (s *Server) handleOpenTeam(w http.ResponseWriter, r *http.Request) {
	user := s.sessionUser(r)
	if !s.isAdmin(user) {
		http.Error(w, "only this registry’s owner can open it to a team", http.StatusForbidden)
		return
	}
	_ = r.ParseForm()
	if !s.checkCSRF(r) {
		http.Error(w, "stale form; reload the page and try again", http.StatusForbidden)
		return
	}
	// Refuse anything that is not the case this was built for. An organization's
	// registry is already open, and one with no owner secret has no door to
	// widen — opening it would leave a dashboard anybody could read.
	if !s.cfg().SingleMember() {
		http.Error(w, "this registry is not a single member’s", http.StatusConflict)
		return
	}
	if err := config.PatchEnv(config.RegistryEnvPath(), map[string]string{
		"TACIT_AUTH_MODE": config.AuthTeam,
	}); err != nil {
		http.Error(w, "could not write the setting: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.mutateCfg(func(c *config.Config) { c.AuthMode = config.AuthTeam })
	log.Printf("[members] registry opened to a team by its owner; member keys now sign in")
	// No flag on the address. It used to carry ?opened=1 for a banner saying
	// what had just happened, and the page it lands on says that already.
	http.Redirect(w, r, s.cfg().BasePath+s.membersHome(), http.StatusSeeOther)
}

// membersHome is where the members surface lives on THIS registry.
//
// One subject, one destination: an organization keeps /members, and a registry
// that is one member's or one they opened to their team keeps everything about
// who is here on /team, where the rest of that story already is. Every handler
// that finishes by sending the reader back to "the members page" asks this, so
// a mint or a revoke returns to the page the button was on.
func (s *Server) membersHome() string {
	if s.cfg().SingleMember() || s.cfg().TeamEnabled() {
		return "/team"
	}
	return "/members"
}
