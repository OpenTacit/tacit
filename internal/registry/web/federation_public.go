// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/ui"
)

// The Federation view's Public-channel section and feed-token surface
// (docs/distribution/global-access-plan.md).
//
// The Public channel is rendered as a COMPUTED object, visibly not the same kind
// of thing as a hand-curated channel: it has no Unpublish button, and that
// absence is explained in place rather than left to be noticed as a missing
// control. The ranking columns are the argument for the ranking made visible —
// helped and n beside the bound that actually orders them — so an operator can
// see why a 72%-of-9 technique sits below an 84%-of-40 one without reading the design.

// publicSectionHTML renders the commons: its state, its members with their
// evidence, and why each near-miss technique is not in it.
func (s *Server) publicSectionHTML() string {
	var b strings.Builder
	b.WriteString(`<h2 class="fed-h" id="public">Public</h2>`)

	if !s.cfg().GlobalAccess {
		b.WriteString(`<p class="empty">Global Access is off, so this registry contributes nothing to the ` +
			`Public pool and has no public address. Both are one setting in ` +
			`<a href="/settings">Settings</a>.</p>`)
		return b.String()
	}

	members, ranked, exclusions := s.PublicStatus()
	served := 0
	for _, m := range members {
		if m.Served {
			served++
		}
	}

	state := `<span class="badge">⟳ computed</span>`
	note := `Your top ` + fmt.Sprint(federation.PublicSize) + ` most helpful general techniques; ` + productHTML() + ` recomputes ` +
		`the set each cycle. Membership follows evidence, not curation—there is nothing to publish here by hand, and ` +
		`a technique leaves when it is no longer a general technique.`
	if s.GlobalAccessStaged() {
		state = `<span class="status serious">not served—confirmation needed</span>`
		note = `This is the list that Global Access will contribute. Nothing left the registry: a proxy was not consent to share ` +
			`your techniques, so the feed waits for an administrator to agree in <a href="/settings">Settings</a>.`
	}

	fmt.Fprintf(&b, `<div class="fed-list"><div class="fed-item fed-computed"><div class="fed-main">`+
		`<div class="fed-title">Public %s <span class="badge">%d technique%s</span></div>`+
		`<p class="fed-note">%s</p>`,
		state, served, plural(served), note)
	if s.GlobalAccessServing() {
		b.WriteString(`<div class="fed-url"><a href="/f/public/feed.json"><code>/f/public/feed.json</code></a>` +
			` <span class="gen">· open by design: subscribers fetch the pool anonymously</span></div>`)
	}

	// Where the near misses are, and why — the scrub case especially, which is
	// unguessable from "not eligible".
	var reasons []string
	counts := map[string]int{}
	for _, e := range exclusions {
		counts[e.Reason]++
	}
	for _, reason := range []string{
		federation.ReasonThinEvidence, federation.ReasonNoEvidence, federation.ReasonUnapproved,
		federation.ReasonSecrets, federation.ReasonOrgScoped, federation.ReasonImported,
	} {
		if n := counts[reason]; n > 0 {
			reasons = append(reasons, fmt.Sprintf("%d %s", n, html.EscapeString(reason)))
		}
	}
	if len(reasons) > 0 {
		b.WriteString(`<div class="fed-sub"><span>` + strings.Join(reasons, `</span><span>`) + `</span></div>`)
	}
	b.WriteString(`</div></div></div>`)

	if len(ranked) == 0 {
		fmt.Fprintf(&b, `<p class="empty">No technique qualifies yet. A technique needs to be general, serving, `+
			`approved by a person, free of anything the secret scanner flags, and measured over at least `+
			`n=%d.</p>`, s.publicMinNOrDefault())
		return b.String()
	}

	// The ranking, with the de-listing threshold drawn in — hysteresis is
	// otherwise invisible and a held technique looks like a bug.
	entered := map[string]string{}
	isMember := map[string]bool{}
	for _, m := range members {
		entered[m.TechniqueID], isMember[m.TechniqueID] = m.EnteredAt, true
	}
	rows := make([]tableRow, 0, len(ranked)+1)
	for _, c := range ranked {
		if c.Rank > federation.PublicDelistRank {
			break
		}
		state := ""
		switch {
		case !isMember[c.TechniqueID]:
			state = ` <span class="gen">(waiting)</span>`
		case c.Rank > federation.PublicSize:
			state = ` <span class="gen">(held, not served)</span>`
		}
		arrived := `<span class="muted">—</span>`
		if at := entered[c.TechniqueID]; at != "" {
			arrived = html.EscapeString(shortStamp(at))
		}
		rows = append(rows, tableRow{
			Cells: []string{
				fmt.Sprint(c.Rank),
				`<a href="/techniques/` + html.EscapeString(c.TechniqueID) + `">` + html.EscapeString(c.Name) + `</a>` + state,
				fmt.Sprintf("%.0f%%", c.Helped*100),
				fmt.Sprint(c.N),
				fmt.Sprintf("%.2f", c.Bound),
				arrived,
			},
		})
	}
	b.WriteString(ui.Sub("", "by lower bound, not rate"))
	fmt.Fprintf(&b, ui.Fine(`The order is by the lower bound rather than the rate: a technique that `+
		`helped once out of once must not outrank one that helped 40 times out of 50.`,
		`The feed serves the top %d. A technique that falls below keeps its place until rank %d, so `+
			`a dip does not tell every subscriber to re-review a technique that is fine.`),
		federation.PublicSize, federation.PublicDelistRank)
	cols := []tableCol{
		{Label: "rank", Num: true}, {Label: "technique"},
		{Label: "helped&#8202;%", Num: true}, {Label: "n", Num: true},
		{Label: "bound", Num: true}, {Label: "entered", Num: true},
	}
	b.WriteString(dataTable(cols, rows))
	return b.String()
}

// feedTokenSectionHTML is the mint/revoke surface for channel access.
func (s *Server) feedTokenSectionHTML() string {
	tokens, err := s.Store.ListFeedTokens()
	if err != nil {
		return `<h2 class="fed-h">Feed tokens</h2><p class="empty">` + html.EscapeString(err.Error()) + `</p>`
	}
	var b strings.Builder
	b.WriteString(`<h2 class="fed-h" id="tokens">Feed tokens</h2>`)
	b.WriteString(`<p class="sub">Each private channel needs a feed token. The Public channel allows anonymous access.</p>`)

	if len(tokens) == 0 {
		b.WriteString(`<p class="empty">No tokens exist. Currently only this registry’s own operator ` +
			`can reach the non-public channels.</p>`)
	} else {
		b.WriteString(`<div class="fed-list">`)
		for _, t := range tokens {
			status := ``
			if t.RevokedAt != "" {
				status = ` <span class="status serious">revoked</span>`
			}
			last := "never used"
			if t.LastUsed != "" {
				last = "last used " + shortStamp(t.LastUsed)
			}
			scope := strings.Join(t.Channels, ", ")
			if scope == "" {
				scope = "no channels"
			}
			fmt.Fprintf(&b, `<div class="fed-item"><div class="fed-main">`+
				`<div class="fed-title">%s%s</div>`+
				`<div class="fed-meta"><span>%s</span><span>%s</span></div>`+
				`</div><div class="fed-actions">`,
				html.EscapeString(t.Label), status,
				html.EscapeString(scope), html.EscapeString(last))
			if t.RevokedAt == "" {
				fmt.Fprintf(&b, `<form method="post" action="/admin/feed-tokens/revoke/%s" `+
					`onsubmit="return confirm('Revoke this token? The holder then cannot read those channels.')">`+
					`<button class="danger" type="submit">Revoke</button></form>`,
					html.EscapeString(t.ID))
			}
			b.WriteString(`</div></div>`)
		}
		b.WriteString(`</div>`)
	}

	b.WriteString(`<h3 class="fed-sh">Mint a token</h3>` +
		`<form class="form-grid" method="post" action="/admin/feed-tokens">` +
		`<label>For whom<input type="text" name="label" required placeholder="for example, Acme Corp"></label>` +
		`<label>Channels<input type="text" name="channels" required placeholder="partners, vendor-pack"></label>` +
		`<div class="full"><button type="submit" class="btn btn-primary">Mint</button></div></form>` +
		`<p class="sub">Copy the secret now. The registry stores only its hash and cannot show it again.</p>`)
	return b.String()
}

// publicStandingFor is one technique's standing in the computed Public channel, for
// its own page.
//
// Membership is a different kind of fact from the channels beside it: nobody
// chose it, and it can change without anyone acting. So a technique says where it
// stands — in the feed at rank 7, held, waiting on evidence, or ineligible and
// why — rather than making an operator infer it from the aggregate view.
func (s *Server) publicStandingFor(technique models.Technique) string {
	if !s.cfg().GlobalAccess || technique.Status == "draft" {
		return ""
	}
	members, ranked, exclusions := s.PublicStatus()
	line := func(cls, text string) string {
		return `<p class="sub ` + cls + `">` + text + `</p>`
	}
	served := map[string]bool{}
	for _, m := range members {
		if m.Served {
			served[m.TechniqueID] = true
		}
	}
	for _, c := range ranked {
		if c.TechniqueID != technique.ID {
			continue
		}
		where := fmt.Sprintf("rank %d of the Public feed", c.Rank)
		switch {
		case !s.GlobalAccessServing():
			return line("", fmt.Sprintf("Eligible at %s. An administrator must enable the feed in "+
				`<a href="/settings">Settings</a> before it is published.`, where))
		case served[technique.ID]:
			return line("", fmt.Sprintf(`In the <a href="/federation#public">Public feed</a> at %s `+
				`(helped %.0f%% over n=%d, bound %.2f)—every registry in the pool receives it.`,
				where, c.Helped*100, c.N, c.Bound))
		default:
			return line("", fmt.Sprintf("Eligible at %s, but outside the %d in service—it keeps its place "+
				"until rank %d.", where, federation.PublicSize, federation.PublicDelistRank))
		}
	}
	for _, e := range exclusions {
		if e.TechniqueID != technique.ID {
			continue
		}
		if e.Reason == federation.ReasonThinEvidence {
			return line("", fmt.Sprintf("Not in the Public feed: %s (n=%d of %d).",
				html.EscapeString(e.Reason), e.N, s.publicMinNOrDefault()))
		}
		return line("", "Not in the Public feed: "+html.EscapeString(e.Reason)+".")
	}
	return ""
}

// handleMintFeedToken is the Federation page's Mint button. It renders the secret
// on its own page rather than redirecting, because a redirect would lose the one
// and only chance to read it.
func (s *Server) handleMintFeedToken(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/federation") {
		return
	}
	_ = r.ParseForm()
	label := strings.TrimSpace(r.PostFormValue("label"))
	var channels []string
	for _, ch := range strings.Split(r.PostFormValue("channels"), ",") {
		if ch = strings.TrimSpace(ch); ch != "" {
			channels = append(channels, ch)
		}
	}
	fail := func(msg string) {
		s.sendHTML(w, 400, s.renderShell(page{status: 400, active: "federation",
			crumbs: []crumb{{label: "Federation", href: "/federation"}, {label: "Mint a token", href: ""}},
			content: `<p>` + html.EscapeString(msg) +
				`</p><p class="sub"><a href="/federation#tokens">← back to federation</a></p>`}, s.sessionUser(r)))
	}
	if label == "" {
		fail("Say who the token is for—it is the only way to know later whose access you revoke.")
		return
	}
	if len(channels) == 0 {
		fail("Name at least one channel. Public is open and has no gate, so a token for it means nothing.")
		return
	}
	t, secret, err := s.mintFeedToken(label, channels)
	if err != nil {
		fail(err.Error())
		return
	}
	if len(t.Channels) == 0 {
		fail("That leaves no channels to authorize: Public is open and has no gate.")
		return
	}
	s.sendHTML(w, 200, s.renderShell(page{active: "federation",
		crumbs:  []crumb{{label: "Federation", href: "/federation"}, {label: "Token minted", href: ""}},
		content: mintedSecretHTML(t, secret) + `<p class="sub"><a href="/federation#tokens">← back to federation</a></p>`},
		s.sessionUser(r)))
}

// handleRevokeFeedToken is the Revoke button. The token is kept, not deleted, so
// the record of who once had access survives — and so the gate can still tell a
// revoked credential from an unknown one.
func (s *Server) handleRevokeFeedToken(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/federation") {
		return
	}
	id := r.PathValue("id")
	if err := s.Store.SetFeedTokenRevoked(id, models.Now()); err != nil {
		s.sendHTML(w, 500, s.renderShell(page{status: 500, active: "federation",
			content: `<p>` + html.EscapeString(err.Error()) + `</p>`}, s.sessionUser(r)))
		return
	}
	http.Redirect(w, r, "/federation#tokens", http.StatusFound)
}

// mintedSecretHTML shows a freshly minted secret exactly once.
func mintedSecretHTML(t models.FeedToken, secret string) string {
	return `<div class="fed-secret"><p class="lbl">Token for ` + html.EscapeString(t.Label) + `</p>` +
		`<pre><code>` + html.EscapeString(secret) + `</code></pre>` +
		`<p class="hint">Copy it now: the registry keeps only its hash, so the secret appears only this one time. ` +
		`The holder presents it as <code>Authorization: Bearer &lt;token&gt;</code> and can read ` +
		html.EscapeString(strings.Join(t.Channels, ", ")) + `.</p></div>`
}

// countTokensFor counts active tokens scoped to a channel.
func countTokensFor(tokens []models.FeedToken, channel string) int {
	n := 0
	for _, t := range tokens {
		if t.Allows(channel) {
			n++
		}
	}
	return n
}

// lastReadFor is the most recent use of any token for a channel.
func lastReadFor(tokens []models.FeedToken, channel string) string {
	best := ""
	for _, t := range tokens {
		if t.Allows(channel) && t.LastUsed > best {
			best = t.LastUsed
		}
	}
	return best
}

// shortStamp trims an RFC3339 timestamp to minutes for display, matching the
// subscriptions list's treatment of last-poll.
func shortStamp(ts string) string {
	if len(ts) > 16 {
		return strings.ReplaceAll(ts[:16], "T", " ")
	}
	return ts
}
