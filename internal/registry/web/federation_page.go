// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The Federation dashboard page.
//
// INTENT — the operator's control panel for technique flow across the org
// boundary (docs/federation/federation-design.md, docs/federation/federation-guide.md). Two
// directions, one invariant repeated everywhere: publication is explicit,
// imports are review-gated, and local evidence always outranks imported
// attestations.
//
// The full interaction inventory this page serves:
//
//	Outbound (what this registry offers):
//	  · see the provider identity — id, descriptor URL, signing key
//	  · see each published channel: title, technique count, feed URL to hand out
//	  · see exactly which techniques are published where, and UNPUBLISH one
//	    (publishing itself starts on the technique's own page — this is the exit)
//	Inbound (what this registry imports):
//	  · judge each subscription's health at a glance: trust level, imported
//	    count, last poll, ok/error status
//	  · drill into a feed (local vs provider-attested performance)
//	  · see a pending retraction ATTACHED to the subscription it came from
//	  · UNSUBSCRIBE (imported techniques stay; confirmed)
//	  · POLL NOW — the browser counterpart of POST /v1/admin/poll-feeds,
//	    which previously existed only for key-authed automation
//	  · add a feed (name, URL, trust, prefix, auth token)
//	  · inspect imported attestations (display-only; collapsed by default)
//
// FORM — TWO DIRECTIONS, drawn and then listed.
//
// The page opens with the picture (federationscene.go): this registry inside its
// boundary, its channels leaving through their ports, the feeds arriving from
// other registries, and — the thing worth the space — a hold on each feed that
// still waits for a person. Then two plates, one per direction, each holding
// everything about that direction and nothing about the other.
//
// It used to be six sibling headings in source order: identity, Public,
// Publishing, published techniques, tokens, Subscriptions, attestations, and an
// "Add a feed" form at the very bottom, four sections below the subscriptions it
// adds to. Nothing said that half of those were one direction and half the
// other, and the invariant — local evidence outranks imported evidence — was
// repeated in three paragraphs and shown nowhere. A form now sits with what it
// changes, and the direction is the structure rather than a word in a sentence.
//
// Flat and phone-first inside each plate. The layout before that wrapped
// everything in two mega-panels with tables, boxed forms, and sub-headers nested
// inside them: boxes in boxes, and a seven-column subscriptions table that was
// unusable on a phone. Now each entity (channel, published technique, subscription) is one
// .fed-item row that reads top-to-bottom — title line, URL line, meta line,
// actions — and simply stacks on narrow screens. Sections are plain h2s with
// hairline rules. Tables remain only where data is genuinely tabular (the
// feed-detail comparison). The old Unpublish/Unsubscribe buttons also used
// classes styled only inside .techniqueblock, so they rendered as unstyled browser
// defaults — they now use the shared .btn family.
package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/pkg/feed"
)

func (s *Server) pageFederation(r *http.Request, user oidc.Claims) page {
	var b strings.Builder
	b.WriteString(`<div class="page-head"><p class="sub">Manage imported and published techniques. Feed trust controls imports, and local evidence takes priority over imported claims.</p></div>`)

	// ---- identity: a flat meta strip, not a box ----
	pub, err := s.publisher()
	if err != nil {
		b.WriteString(`<p class="empty">publisher unavailable: ` + html.EscapeString(err.Error()) + `</p>`)
		return page{active: "federation", content: b.String(),
			crumbs: s.registryCrumbs("Federation"), viewMenu: s.registryViews("federation")}
	}
	desc, err := pub.Descriptor()
	if err != nil {
		b.WriteString(`<p class="empty">` + html.EscapeString(err.Error()) + `</p>`)
		return page{active: "federation", content: b.String(),
			crumbs: s.registryCrumbs("Federation"), viewMenu: s.registryViews("federation")}
	}
	fmt.Fprintf(&b, `<p class="fed-id">provider <code>%s</code> · descriptor <a href="/.well-known/tacit.json"><code>/.well-known/tacit.json</code></a> · signing key <code>%s…</code></p>`,
		html.EscapeString(desc.Provider.ID), html.EscapeString(head24(desc.PublicKey)))

	// ---- the picture, before either direction is listed ----
	//
	// It is built from what the two sections below are about to render, so a
	// channel that appears in the drawing appears in the list and vice versa;
	// there is one source for both and nothing to keep in step by hand.
	subsForScene, _ := s.subscriptions().List()
	if scene := federationScene(s.sceneOutbound(desc), s.sceneInbound(subsForScene)); scene != "" {
		b.WriteString(`<section class="panel fed-hero">` + scene + `</section>`)
	}

	// ---- the two directions, side by side ----
	//
	// They are the page's whole subject. Stacked full-width they were two plates
	// a reader scrolled between, which is the arrangement the six sibling
	// headings had; side by side, the difference between them IS the layout.
	//
	// ARRIVES FIRST, BECAUSE THE PICTURE ABOVE PUTS IT ON THE LEFT. The columns
	// read the other way round at first, so a reader who had just followed a feed
	// in from the left edge of the drawing found its list on the right and the
	// channels on the left. Same order in the DOM, so the tab order and a screen
	// reader follow the drawing too.
	b.WriteString(`<div class="fed-cols">`)

	// ---- what arrives ----
	b.WriteString(`<section class="panel fed-plate"><h2>What arrives from others</h2>`)
	b.WriteString(`<p class="fed-lede">Imported techniques include evidence from another registry. Local evidence sets the ranking; imported evidence appears for reference. Feed trust controls whether entries arrive as drafts or become active at once.</p>`)

	// ---- subscriptions ----
	b.WriteString(`<h2 class="fed-h">Subscriptions` +
		`<form class="fed-inline" method="post" action="/admin/poll-feeds">` +
		`<button class="btn" type="submit" title="Fetch every subscribed feed now (they also poll on a schedule)">Poll now</button></form></h2>`)
	subs, err := s.subscriptions().List()
	if err != nil {
		b.WriteString(`<p class="empty">` + html.EscapeString(err.Error()) + `</p>`)
		return page{active: "federation", content: b.String(),
			crumbs: s.registryCrumbs("Federation"), viewMenu: s.registryViews("federation")}
	}
	if len(subs) == 0 {
		b.WriteString(`<p class="empty">No subscriptions. Add a feed below to import techniques from another registry.</p>`)
	} else {
		b.WriteString(`<div class="fed-list">`)
		for _, sub := range subs {
			status := `<span class="badge">ok</span>`
			if sub.LastError != "" {
				status = `<span class="status serious">▲ ` + html.EscapeString(sub.LastError) + `</span>`
			}
			lastPoll := "no poll yet"
			if sub.LastPoll != "" {
				lastPoll = sub.LastPoll
				if len(lastPoll) > 19 {
					lastPoll = strings.ReplaceAll(lastPoll[:19], "T", " ")
				}
			}
			// Retractions belong to the subscription they arrived on — attach
			// them to its row instead of aggregating them below the fold.
			var alerts strings.Builder
			for _, note := range sub.Retractions {
				fmt.Fprintf(&alerts, `<div class="fed-alert">▲ retraction pending review: %s</div>`,
					html.EscapeString(note))
			}
			fmt.Fprintf(&b, `<div class="fed-item"><div class="fed-main">`+
				`<div class="fed-title"><a href="/federation/feed/%s">%s</a> <span class="badge">%s</span> %s</div>`+
				`<div class="fed-url"><code>%s</code></div>`+
				`<div class="fed-meta"><span>%d imported</span><span>prefix <code>%s</code></span><span>last poll %s</span></div>%s`+
				`</div><div class="fed-actions">`+
				`<form method="post" action="/admin/subscriptions/delete/%s" onsubmit="return confirm('Unsubscribe from this feed? Imported techniques stay.')">`+
				`<button class="danger" type="submit">Unsubscribe</button></form>`+
				`</div></div>`,
				url.PathEscape(sub.ID), html.EscapeString(sub.DisplayName()),
				html.EscapeString(sub.Trust), status,
				html.EscapeString(sub.FeedURL),
				sub.Imported, html.EscapeString(sub.Prefix), html.EscapeString(lastPoll),
				alerts.String(), url.PathEscape(sub.ID))
		}
		b.WriteString(`</div>`)

		// imported attestations: display-only context, collapsed by default —
		// it is reference material, not a decision surface.
		var att []string
		for _, sub := range subs {
			for id, a := range sub.Attestations {
				if a != nil {
					att = append(att, fmt.Sprintf(`<li><a href="/techniques/%s">%s</a> · provider-measured: helped %.0f%% · n=%d <span class="gen">(local evidence governs ranking)</span></li>`,
						url.PathEscape(id), html.EscapeString(id),
						a.Outcomes.HelpedRate*100, a.Outcomes.SampleSize))
				}
			}
		}
		if len(att) > 0 {
			fmt.Fprintf(&b, `<details class="fed-more"><summary>Imported attestations (%d)</summary><ul class="fresh">%s</ul></details>`,
				len(att), strings.Join(att, ""))
		}
	}

	// ---- add a feed: with the subscriptions it adds to, not four sections below ----
	b.WriteString(subscribeForm())
	b.WriteString(`</section>`)

	// ---- what leaves ----
	b.WriteString(`<section class="panel fed-plate"><h2>What leaves this registry</h2>`)
	b.WriteString(`<p class="fed-lede">Channels publish selected techniques to other registries. The Public channel selects techniques from evidence and allows access without a token.</p>`)

	// ---- the computed Public channel ----
	b.WriteString(s.publicSectionHTML())

	// ---- publishing ----
	b.WriteString(`<h2 class="fed-h">Channels</h2>`)
	curated := 0
	for _, ch := range desc.Channels {
		if ch.ID != federation.PublicChannel {
			curated++
		}
	}
	if curated == 0 {
		b.WriteString(`<p class="empty">No published channels. Publish a technique from its <a href="/techniques">technique page</a> (Federation control), or <code>POST /v1/admin/publish {"technique_id": "…", "channels": ["general"]}</code>.</p>`)
	} else {
		channels, _ := pub.Channels()
		rejected := s.RejectedFeedReads()
		tokens, _ := s.Store.ListFeedTokens()
		b.WriteString(`<div class="fed-list">`)
		for _, ch := range desc.Channels {
			if ch.ID == federation.PublicChannel {
				continue // has its own section above; it is a different kind of object
			}
			// The lock marker on every non-public channel, and its absence on
			// Public, is where an operator learns the rule without being taught it:
			// the two kinds of channel now differ in what they cost to reach, and
			// that belongs on the object rather than in a document.
			access := `<span class="badge">token needed</span>`
			if containsString(s.cfg().OpenFeedChannels, ch.ID) {
				access = `<span class="status serious">open—readable by anyone</span>`
			}
			fmt.Fprintf(&b, `<div class="fed-item"><div class="fed-main">`+
				`<div class="fed-title">%s <span class="badge">%d technique%s</span> %s</div>`+
				`<div class="fed-url"><a href="/f/%s/feed.json"><code>/f/%s/feed.json</code></a></div>`,
				html.EscapeString(ch.Title), channels[ch.ID], plural(channels[ch.ID]), access,
				url.PathEscape(ch.ID), html.EscapeString(ch.ID))
			// "Is the peer we set this up for still reading?" and, for the 404
			// policy, "is somebody failing to authenticate?" — the publisher's side
			// of a refusal the subscriber cannot diagnose.
			var meta []string
			if n := countTokensFor(tokens, ch.ID); n > 0 {
				meta = append(meta, fmt.Sprintf("%d token%s", n, plural(n)))
			}
			if last := lastReadFor(tokens, ch.ID); last != "" {
				meta = append(meta, "last read "+shortStamp(last))
			}
			if n := rejected[ch.ID]; n > 0 {
				meta = append(meta, fmt.Sprintf("feed refused %d request%s (no token)", n, plural(n)))
			}
			if len(meta) > 0 {
				fmt.Fprintf(&b, `<div class="fed-meta"><span>%s</span></div>`,
					html.EscapeString(strings.Join(meta, " · ")))
			}
			b.WriteString(`</div></div>`)
		}
		b.WriteString(`</div>`)
	}
	// per-technique publication list: what exactly is in the feeds, with the exit
	if techniques, err := s.Store.ListTechniques(nil, 0); err == nil {
		var rows strings.Builder
		for _, c := range techniques {
			if len(c.Channels) == 0 {
				continue
			}
			cid := html.EscapeString(c.ID)
			fmt.Fprintf(&rows, `<div class="fed-item"><div class="fed-main">`+
				`<div class="fed-title"><a href="/techniques/%s">%s</a></div>`+
				`<div class="fed-meta"><code>%s</code><span>channels: %s</span></div>`+
				`</div><div class="fed-actions">`+
				`<form method="post" action="/admin/techniques/channels/%s">`+
				`<input type="hidden" name="next" value="/federation">`+
				`<button class="danger" type="submit">Unpublish</button></form>`+
				`</div></div>`,
				cid, html.EscapeString(c.Name), cid,
				html.EscapeString(strings.Join(c.Channels, ", ")), cid)
		}
		if rows.Len() > 0 {
			b.WriteString(`<h3 class="fed-sh">Published techniques</h3><div class="fed-list">` +
				rows.String() + `</div>` +
				`<p class="sub">Publish or change channels from each technique’s page (Federation control).</p>`)
		}
	}

	// Feed tokens sit with Publishing, because that is what they authorize — and
	// they are rendered whatever Global Access is doing, since gating a private
	// channel has nothing to do with the commons. A registry with a `partners`
	// channel and Global Access off still needs somewhere to mint one.
	b.WriteString(s.feedTokenSectionHTML())
	b.WriteString(`</section></div>`)
	return page{active: "federation", content: b.String(),
		crumbs: s.registryCrumbs("Federation"), viewMenu: s.registryViews("federation")}
}

// sceneOutbound is what the picture draws on the right: the channels this
// registry offers, Public first because it is the one that needs no token and
// the only one evidence fills by itself.
//
// It reads the same descriptor the Channels list below it does, so the drawing
// and the list cannot disagree.
func (s *Server) sceneOutbound(desc feed.Descriptor) []fedOut {
	var out []fedOut
	if s.GlobalAccessServing() {
		members, _, _ := s.PublicStatus()
		served := 0
		for _, m := range members {
			if m.Served {
				served++
			}
		}
		out = append(out, fedOut{Title: "Public", Count: served, Open: true})
	}
	counts, _ := s.publisher2Channels()
	tokens, _ := s.Store.ListFeedTokens()
	for _, ch := range desc.Channels {
		if ch.ID == federation.PublicChannel {
			continue
		}
		out = append(out, fedOut{
			Title: ch.Title,
			Count: counts[ch.ID],
			Open:  containsString(s.cfg().OpenFeedChannels, ch.ID),
			// The same reading the Channels list below shows in its meta line, so
			// the picture and the list cannot disagree about who has fetched what.
			LastRead: lastReadFor(tokens, ch.ID),
		})
	}
	return out
}

// sceneInbound is what the picture draws on the left: one peer per subscription,
// and whether its entries stop for a person on the way in.
func (s *Server) sceneInbound(subs []federation.Subscription) []fedIn {
	in := make([]fedIn, 0, len(subs))
	for _, sub := range subs {
		in = append(in, fedIn{
			Name:   sub.DisplayName(),
			Count:  sub.Imported,
			Review: sub.Trust != "auto-accept",
			// The error the subscription row below shows in red. A feed whose
			// last poll failed is not delivering anything, whatever it delivered
			// before.
			Failing: sub.LastError != "",
		})
	}
	return in
}

// publisher2Channels is the per-channel technique count, or an empty map if the
// publisher cannot answer. The picture draws "none yet" either way rather than a
// zero it did not measure.
func (s *Server) publisher2Channels() (map[string]int, error) {
	pub, err := s.publisher()
	if err != nil {
		return map[string]int{}, err
	}
	c, err := pub.Channels()
	if err != nil {
		return map[string]int{}, err
	}
	return c, nil
}

// subscribeForm is the browser UI for adding an imported feed — the counterpart
// to the key-authed POST /v1/admin/subscriptions used by automation. Flat:
// the page section provides the rhythm, so the form carries no box of its own.
func subscribeForm() string {
	return `<h2 class="fed-h">Add a feed</h2>` +
		`<form class="form-grid" method="post" action="/admin/subscriptions">` +
		`<label class="full">Feed URL<input type="url" name="feed_url" required placeholder="https://…/f/general/feed.json"></label>` +
		`<label>Name<input type="text" name="name" placeholder="for example, Platform team feed (defaults from the URL)"></label>` +
		`<label>Trust<select name="trust">` +
		`<option value="review">review: entries become drafts (default)</option>` +
		`<option value="auto-accept">auto-accept: entries become live immediately</option>` +
		`</select></label>` +
		`<label>Prefix<input type="text" name="prefix" placeholder="local id namespace (defaults from the feed host)"></label>` +
		`<label>Auth token<input type="text" name="auth_token" placeholder="Bearer token, if the feed needs one"></label>` +
		`<div class="full"><button type="submit" class="btn btn-primary">Subscribe</button></div>` +
		`</form>` +
		`<p class="sub">Local evidence takes priority over imported evidence. Automation can use ` +
		`<code>POST /v1/admin/subscriptions</code>.</p>`
}

// pageFeedDetail is one subscription's drill-down: the techniques imported
// from that feed with their LOCAL performance beside the provider's attested
// rate — the trust question federation raises. Reached from a feed row.
func (s *Server) pageFeedDetail(r *http.Request, user oidc.Claims) page {
	fid, _ := url.PathUnescape(r.PathValue("id"))
	subs, err := s.subscriptions().List()
	if err != nil {
		return page{status: 500, active: "federation", content: `<p>` + html.EscapeString(err.Error()) + `</p>`}
	}
	idx := -1
	for i := range subs {
		if subs[i].ID == fid {
			idx = i
			break
		}
	}
	if idx < 0 {
		return page{status: 404, active: "federation",
			crumbs:  []crumb{{label: "Federation", href: "/federation"}},
			content: `<p>No subscription with that ID. <a href="/federation">All feeds.</a></p>`}
	}
	sub := subs[idx]

	events, err := s.Store.AllEvents("")
	if err != nil {
		return page{status: 500, active: "federation", content: `<p>` + html.EscapeString(err.Error()) + `</p>`}
	}
	techniques, _ := s.Store.ListTechniques(nil, 0)
	now := time.Now().UTC()
	w := insights.WindowByKey(r.URL.Query().Get("w"), now, insights.Earliest(events))
	o := insights.Compute(techniques, events, now, w)
	stat := map[string]insights.TechniqueStat{}
	for _, c := range o.Techniques {
		stat[c.Technique.ID] = c
	}
	crumbs := []crumb{{label: "Federation", href: "/federation"}, {label: sub.DisplayName(), href: ""}}

	var b strings.Builder
	fmt.Fprintf(&b, `<div class="page-head"><p class="sub">Imported from <code>%s</code> · trust <b>%s</b> · prefix <code>%s</code> · %d imported. Your own evidence sets the ranking; an imported feed’s claims appear only for reference.</p></div>`,
		html.EscapeString(sub.FeedURL), html.EscapeString(sub.Trust), html.EscapeString(sub.Prefix), sub.Imported)
	b.WriteString(windowSelect("/federation/feed/"+url.PathEscape(sub.ID), w.Key, now, insights.Earliest(events)))

	type frow struct {
		id, name, scope string
		f               insights.Funnel
		att             string
	}
	seen := map[string]bool{}
	var rows []frow
	add := func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		cs := stat[id]
		name := cs.Technique.Name
		if name == "" {
			name = id // technique since removed — show its id
		}
		att := `<span class="muted">—</span>`
		if a := sub.Attestations[id]; a != nil {
			att = fmt.Sprintf(`%.0f%% <span class="muted">n=%d</span>`, a.Outcomes.HelpedRate*100, a.Outcomes.SampleSize)
		}
		rows = append(rows, frow{id, name, cs.Technique.Scope, cs.Funnel, att})
	}
	for id := range sub.Attestations {
		add(id)
	}
	if sub.Prefix != "" {
		for _, c := range techniques {
			if strings.HasPrefix(c.ID, sub.Prefix) {
				add(c.ID)
			}
		}
	}
	if len(rows) == 0 {
		b.WriteString(`<p class="empty">No imported techniques from this feed yet.</p>`)
		return page{active: "federation", crumbs: crumbs, content: b.String()}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].f.Adopted != rows[j].f.Adopted {
			return rows[i].f.Adopted > rows[j].f.Adopted
		}
		return rows[i].name < rows[j].name
	})

	var t strings.Builder
	out := make([]tableRow, 0, len(rows))
	for _, rw := range rows {
		local := `<span class="muted">—</span>`
		if hr, ok := rw.f.HelpedRate(); ok {
			local = fmt.Sprintf("%.0f%%", hr*100)
		}
		out = append(out, tableRow{
			Cells: []string{techniqueCell(rw.scope, rw.name), fmtCount(rw.f.Shown), fmtCount(rw.f.Adopted), local, rw.att},
			Href:  techniqueHref(rw.id, w.Key),
		})
	}
	t.WriteString(dataTable(numCols("technique", "shown", "adopted", "local helped&#8202;%", "attested helped&#8202;%"), out))
	b.WriteString(`<p class="hint">Click a row for that technique’s full local insights.</p>`)
	b.WriteString(t.String())
	return page{active: "federation", crumbs: crumbs, content: b.String()}
}

func head24(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}
