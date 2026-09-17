// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The registry's federation HTTP surface (docs/federation/federation-design.md):
//
//	GET  /.well-known/tacit.json   provider descriptor (channels filtered)
//	GET  /f/public/feed.json           the commons: always open, never gateable
//	GET  /f/{channel}/feed.json        channel feed, ?page= archive (token)
//	GET  /f/techniques/{id}.md              canonical published technique (token, by channel)
//	POST /v1/admin/publish             set a technique's channels (key-authed)
//
// The read surface used to be open throughout, on the reasoning that a channel an
// org wants private simply isn't published from a reachable registry. Global
// Access retires that reasoning: it gives every instance a public address and a
// reason to have channels at the same time, so "not reachable" stopped being a
// privacy mechanism. Now only the computed `public` channel is open — the commons
// depends on anonymous fetches — and everything else needs a feed token
// (feedtokens.go, docs/distribution/global-access-plan.md).
package web

import (
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/federation"
	"github.com/opentacit/tacit/pkg/feed"
)

// The descriptor is filtered by the caller's token, which makes its body depend
// on a request header — and a header absent from the cache key must never change
// a cacheable response. So it stays private however open the surface looks. The
// two visible channel lists (with a token, without one) would otherwise be one
// cache entry, and whichever arrived first would be served to the other.
func (s *Server) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPrivate(w, r, "token-filtered descriptor")
	pub, err := s.publisher()
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	d, err := pub.Descriptor()
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	// The descriptor lists channels with their sizes and URLs, so served
	// unfiltered it is a public directory of an org's private channel names.
	// Callers see `public` plus whatever their token authorizes.
	visible := d.Channels[:0:0]
	for _, ch := range d.Channels {
		if s.feedAuthorized(r, ch.ID) {
			visible = append(visible, ch)
		}
	}
	d.Channels = visible
	if d.Channels == nil {
		d.Channels = []feed.ChannelInfo{}
	}
	s.sendJSON(w, 200, d)
}

// handleFederationTree dispatches /f/techniques/{id}.md and /f/{channel}/feed.json.
func (s *Server) handleFederationTree(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/f/"), "/")
	parts := strings.Split(rest, "/")
	switch {
	// technique ids may contain slashes (federated/mined imports): everything
	// after "techniques/" up to ".md" is the id
	case parts[0] == "techniques" && strings.HasSuffix(rest, ".md") && len(parts) >= 2:
		s.handleCanonicalTechnique(w, r, strings.TrimSuffix(strings.TrimPrefix(rest, "techniques/"), ".md"))
	case len(parts) == 2 && parts[1] == "feed.json":
		s.handleFeed(w, r, parts[0])
	default:
		s.sendError(w, 404, "not found")
	}
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request, channel string) {
	if !s.feedAuthorized(r, channel) {
		s.refuseFeed(w, channel)
		return
	}
	pub, err := s.publisher()
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	doc, err := pub.Feed(channel, r.URL.Query().Get("page"))
	if err != nil {
		s.sendError(w, 404, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// A shared cache in front of this — Cloudflare now, and the proxy terminates
	// TLS besides — must never hand a gated feed to an unauthenticated client. The
	// commons is the one channel that is the same document for everybody who asks,
	// and it is the one that needs caching most: it exists to be polled hourly by
	// every subscriber there is.
	if channel == federation.PublicChannel && s.GlobalAccessServing() {
		cachepolicy.MarkPublic(w, r, "commons feed")
	} else {
		cachepolicy.MarkPrivate(w, r, "gated channel")
	}
	s.sendJSON(w, 200, doc)
}

// refuseFeed answers an unauthorized read with 404, not 401.
//
// A gated channel and a channel that does not exist answer identically, so the
// surface is not an oracle for private channel names — an org's
// `acme-migration` channel should not be discoverable by guessing. The cost is
// real and deliberate: a subscriber whose token was revoked or mistyped sees "not
// found" and cannot tell it from a rename. That information lives on the
// publisher's side instead, in the per-channel rejection counters the Federation
// view shows, where the operator can actually act on it.
func (s *Server) refuseFeed(w http.ResponseWriter, channel string) {
	s.feedRejects.note(channel)
	s.sendError(w, 404, "not found")
}

func (s *Server) handleCanonicalTechnique(w http.ResponseWriter, r *http.Request, id string) {
	pub, err := s.publisher()
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	// This path carries no channel, so authorization comes from the technique's own
	// channel set. It is the bypass that matters: gate the feeds and leave this
	// open, and a gated channel's entire payload is still readable one id at a
	// time by anyone who can guess an id.
	technique, found, err := s.Store.GetTechnique(id)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !found {
		s.sendError(w, 404, "not found")
		return
	}
	channels := pub.TechniqueChannels(technique)
	if len(channels) == 0 {
		s.sendError(w, 404, "not published")
		return
	}
	if !s.feedAuthorizedForAny(r, channels) {
		s.refuseFeed(w, strings.Join(channels, ","))
		return
	}
	doc, ok, err := pub.CanonicalTechnique(id)
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !ok {
		s.sendError(w, 404, "not published")
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	// A technique in the commons is a published document with a permanent id: the same
	// markdown for every reader, and the thing a subscriber fetches after the feed
	// tells it what changed. A technique in any other channel is somebody's.
	if containsString(channels, federation.PublicChannel) && s.GlobalAccessServing() {
		cachepolicy.MarkPublic(w, r, "published technique in the commons")
	} else {
		cachepolicy.MarkPrivate(w, r, "technique in a gated channel")
	}
	_, _ = w.Write([]byte(doc))
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TechniqueID string   `json:"technique_id"`
		Channels    []string `json:"channels"`
	}
	if err := readJSONBody(r, &body); err != nil || body.TechniqueID == "" {
		s.sendError(w, 400, "technique_id and channels required")
		return
	}
	if body.Channels == nil {
		body.Channels = []string{} // explicit unpublish
	}
	technique, err := federation.SetChannels(s.Store, body.TechniqueID, body.Channels)
	if err != nil {
		s.sendError(w, 400, err.Error())
		return
	}
	s.sendJSON(w, 200, map[string]any{"id": technique.ID, "channels": technique.Channels})
}

// handleTechniqueChannels is the browser publish/unpublish action — the UI twin
// of the key-authed /v1/admin/publish, gated by a signed-in session when
// OIDC is on (same posture as promote/reject). do=publish sets the submitted
// comma-separated channels; anything else (the Unpublish button) clears them.
func (s *Server) handleTechniqueChannels(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/federation") {
		return
	}
	id, _ := url.PathUnescape(r.PathValue("id"))
	_ = r.ParseForm()
	channels := []string{} // empty non-nil = explicit unpublish
	if r.PostFormValue("do") == "publish" {
		for _, ch := range strings.Split(r.PostFormValue("channels"), ",") {
			if ch = strings.TrimSpace(ch); ch != "" {
				channels = append(channels, ch)
			}
		}
	}
	if _, err := federation.SetChannels(s.Store, id, channels); err != nil {
		s.sendHTML(w, 400, s.renderShell(page{status: 400, active: "techniques",
			crumbs: []crumb{{label: "Playbook", href: playbookHome}, {label: "Publication", href: ""}},
			content: "<p>" + html.EscapeString(err.Error()) +
				`</p><p class="sub"><a href="/techniques/` + html.EscapeString(id) + `">← back to the technique</a></p>`}, s.sessionUser(r)))
		return
	}
	next := r.PostFormValue("next")
	if !strings.HasPrefix(next, "/") {
		next = "/techniques/" + id
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// --- subscriptions (admin) ---------------------------------------------------

func (s *Server) subscriptions() *federation.Subscriptions {
	return federation.OpenSubscriptions(s.cfg().DataDir)
}

// Poller builds a poller for one pass. The serve loop and the admin "poll now"
// button each get their own — this said they shared one, which they never did —
// so nothing about a poll can be serialized by holding state on the value.
// Concurrent polls of the same subscription are kept apart in the federation
// package instead, keyed by the subscriptions file and the subscription id.
func (s *Server) Poller() *federation.Poller {
	return &federation.Poller{Store: s.Store, Subs: s.subscriptions()}
}

func (s *Server) handleSubscriptionsList(w http.ResponseWriter, r *http.Request) {
	subs, err := s.subscriptions().List()
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	// tokens never round-trip
	for i := range subs {
		if subs[i].AuthToken != "" {
			subs[i].AuthToken = "(set)"
		}
	}
	if subs == nil {
		subs = []federation.Subscription{}
	}
	s.sendJSON(w, 200, map[string]any{"subscriptions": subs})
}

func (s *Server) handleSubscriptionPut(w http.ResponseWriter, r *http.Request) {
	var body federation.Subscription
	if err := readJSONBody(r, &body); err != nil {
		s.sendError(w, 400, "invalid JSON")
		return
	}
	sub, err := s.subscriptions().Put(body)
	if err != nil {
		s.sendError(w, 400, err.Error())
		return
	}
	s.sendJSON(w, 200, map[string]any{"id": sub.ID, "trust": sub.Trust,
		"prefix": sub.Prefix, "poll_seconds": sub.PollSeconds})
}

func (s *Server) handleSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	ok, err := s.subscriptions().Delete(r.PathValue("id"))
	if err != nil {
		s.sendError(w, 500, err.Error())
		return
	}
	if !ok {
		s.sendError(w, 404, "unknown subscription")
		return
	}
	s.sendJSON(w, 200, map[string]any{"deleted": true,
		"note": "imported techniques remain (provenance: federated)"})
}

// handleSubscriptionForm is the browser "Add a feed" form on the Federation
// page — the session-gated counterpart to the key-authed JSON put (same posture
// as promote/reject). Bad input re-renders with the error; success redirects.
func (s *Server) handleSubscriptionForm(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/federation") {
		return
	}
	_ = r.ParseForm()
	sub := federation.Subscription{
		Name:      strings.TrimSpace(r.PostFormValue("name")),
		FeedURL:   strings.TrimSpace(r.PostFormValue("feed_url")),
		Trust:     strings.TrimSpace(r.PostFormValue("trust")),
		Prefix:    strings.TrimSpace(r.PostFormValue("prefix")),
		AuthToken: strings.TrimSpace(r.PostFormValue("auth_token")),
	}
	if _, err := s.subscriptions().Put(sub); err != nil {
		s.sendHTML(w, 400, s.renderShell(page{status: 400, active: "federation",
			crumbs:  []crumb{{label: "Federation", href: "/federation"}, {label: "Add a feed", href: ""}},
			content: `<p>` + html.EscapeString(err.Error()) + `</p><p class="sub"><a href="/federation">← back to federation</a></p>`}, s.sessionUser(r)))
		return
	}
	http.Redirect(w, r, "/federation", http.StatusFound)
}

// handleSubscriptionDeleteForm is the Federation page's Unsubscribe button.
// Imported techniques remain (flagged federated); only the feed config is removed.
func (s *Server) handleSubscriptionDeleteForm(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/federation") {
		return
	}
	id, _ := url.PathUnescape(r.PathValue("id"))
	_, _ = s.subscriptions().Delete(id)
	http.Redirect(w, r, "/federation", http.StatusFound)
}

// handlePollFeedsForm is the Federation page's "Poll now" button — the browser
// counterpart of the key-authed POST /v1/admin/poll-feeds, which was the only
// way to trigger a manual poll before. Session-gated like every browser admin
// action; feeds also poll on their own schedule, so this is a convenience, not
// a requirement.
func (s *Server) handlePollFeedsForm(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrRedirect(w, r, "/federation") {
		return
	}
	s.Poller().PollAll()
	http.Redirect(w, r, "/federation", http.StatusFound)
}

func (s *Server) handlePollFeeds(w http.ResponseWriter, r *http.Request) {
	s.sendJSON(w, 200, map[string]any{"results": s.Poller().PollAll()})
}
