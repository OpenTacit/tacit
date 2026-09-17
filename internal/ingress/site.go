// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// The product's domain — https://opentacit.com/ — and the project page it serves
// (internal/ui/site.go holds the page itself and the reasoning about its copy).
//
// **Being replaced.** The page is a static bundle now (internal/ui/static.go,
// written by hack/sitegen), published to a host whose job is handing out files,
// with access decided in front of it rather than by the switch below. The reason
// is that none of this ever needed a process, and while it had one, restarting
// the ingress was a landing-page outage. What is left here answers the apex only
// until DNS moves. Both copies render from ui.SiteHTML, so they cannot disagree
// about the page in the meantime.
//
// The apex names no instance and never can: routing wants one label under the
// zone, so before this file existed the apex fell through to "no registry is
// published at this address". That is a true sentence about instances and a
// wasted first impression for anyone who typed the name they had been given.
//
// **The page is only ever served from the apex.** It was briefly previewed on the
// console instead, at ingress.<zone>/project, because the apex cannot hold a
// console session — and that was the wrong trade. A page reviewed at one address
// and published at another has not been reviewed: the hostname is in the links,
// the cookie scope, the cache key and the first line of the sign-in door. So the
// apex signs an operator in itself, with its own provider and its own callback
// (Config.SiteRedirectURI), and there is one address for this page in every state.
//
// Three states, one switch (Config.SitePublish):
//
//   - OFF, no session: the sign-in door. Nothing of the page is served.
//   - OFF, signed in: the page, exactly as it will publish. The escape key signs
//     the operator out — the one difference from a visitor's copy, and an
//     invisible one, so what is being reviewed is the published composition and
//     not a variant of it.
//   - ON: the page, to anybody, cacheable at the edge. A signed-in operator's
//     copy still carries the key, and nothing else of theirs.
//
// Turning it on is one environment variable and a restart. Deploying a binary
// publishes nothing.
//
// The cost of the apex having its own sign-in is one more callback registered at
// the identity provider. `serve` logs the exact address at startup while the page
// is gated, so it is a copy and paste rather than a thing to work out.

// siteHandler is what the apex answers. The page needs the stylesheet and the
// typefaces it links, and those live on the console mux — which this host never
// reaches — so the apex serves the same shared handlers itself. It signs people in
// for the same reason. Everything else is a 404: the apex is a single page, not a
// tree.
func (s *Server) siteHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /assets/app.css", ui.ServeCSS)
	mux.HandleFunc("GET /assets/backdrop.js", ui.ServeBackdrop)
	mux.HandleFunc("GET /assets/fonts/{name}", ui.ServeFont)
	mux.HandleFunc("GET /assets/shots/{name}", ui.ServeShot)
	mux.HandleFunc("GET /favicon.ico", ui.ServeFavicon)
	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.HandleFunc("GET /auth/login", s.handleLogin(s.SiteOIDC))
	mux.HandleFunc("GET /auth/callback", s.handleCallback(s.SiteOIDC))
	mux.HandleFunc("GET /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.handleSite)
	mux.HandleFunc("/", s.handleSiteNotFound)
	// robots.txt is absent deliberately: this zone has Cloudflare's managed
	// robots.txt switched on, which replaces the origin's file rather than
	// appending to it, so anything written here would never reach a client
	// (docs/distribution/cloudflare-caching.md).
	return s.classified(mux)
}

// handleInstallScript is the short address for the installer: <product>/install.sh
// redirects to where the bytes actually are (ui.InstallScriptURL). It exists so the
// line a person reads off a screen and retypes is 29 characters rather than 62, and
// so that address is the project's rather than a code host's — the script can move,
// or be served from here instead of redirected to, without the command changing.
//
// A redirect rather than the file: the ingress embeds nothing and retrieves nothing
// (see the Makefile), and a copy of the script inside this binary would version
// independently of the script in the repository. That is a drift with a shell
// command at the end of it.
//
// NOT gated on SitePublish, unlike the page. The console hands this same command to
// every new member of every registry (internal/registry/web/members.go), so it has
// to answer whether or not the project page is published — and it holds nothing of
// anybody's: one fixed address, the same for every caller.
//
// The classifier will make this private, because only a 200 or a 304 may be
// cacheable at the origin here and redirect caching belongs in a reviewed edge rule.
// Declared anyway, so the operations log says which response this was rather than
// "undeclared" (cachepolicy).
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPrivate(w, r, "the installer's short address; a fixed redirect")
	http.Redirect(w, r, ui.InstallScriptURL, http.StatusFound)
}

// handleApexElsewhere is what the zone's apex answers once the product has a
// domain of its own. It is a permanent redirect that keeps the path, so
// the zone's own /install.sh still reaches an installer for everybody who wrote that
// command down — and the command carries -L for a redirect it already had.
//
// A redirect rather than a second copy of the page, deliberately. Two addresses
// serving the same document is two things to review, two cache keys, and an
// answer to "where does this live" that depends on who you ask. With no product
// domain configured this never runs: the apex IS the page's address.
//
// Public and cacheable: it holds nothing of anybody's and is the same answer for
// every caller, so the edge can absorb the traffic that follows an address people
// keep in their shell history.
func (s *Server) handleApexElsewhere(w http.ResponseWriter, r *http.Request) {
	if !s.Cfg.SiteElsewhere() {
		s.siteHandler().ServeHTTP(w, r)
		return
	}
	target := s.Cfg.SiteURL() + r.URL.RequestURI()
	cachepolicy.MarkPublicFor(w, r, apexRedirectTTL, "the zone's apex, moved to the product's domain")
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// apexRedirectTTL is how long the edge may keep that redirect. A day: the
// destination is a decision, not a deployment detail, and it does not change
// between restarts.
const apexRedirectTTL = 86400

// handleSite serves the project page at the apex, in whichever of the three
// states applies.
func (s *Server) handleSite(w http.ResponseWriter, r *http.Request) {
	p := s.providerFor(r, s.SiteOIDC)
	user := s.userOf(p, r)
	admitted := s.admittedBy(p, user)

	if !s.Cfg.SitePublish && !admitted {
		// The door, and nothing of the page behind it. Private rather than
		// cacheable, unlike a tenant's front door: this one exists only until the
		// switch is thrown, and a cached copy would outlive the state it
		// describes. The apex sees no crawler traffic worth optimizing for while
		// it is answering this.
		cachepolicy.MarkPrivate(w, r, "project page not published; sign-in required")
		s.sendHTML(w, http.StatusOK, s.renderSiteSignin(r, user))
		return
	}
	if admitted && s.OIDC != nil {
		// A signed-in operator gets the page a visitor gets, plus one invisible
		// thing: the escape key signs them out, so the round trip — sign out, ask
		// again — shows what a stranger sees. Private: this response was
		// authorized and carries a route a visitor's does not.
		cachepolicy.MarkPrivate(w, r, "project page, with an operator's sign-out key")
		s.sendHTML(w, http.StatusOK, ui.SiteHTMLFor(hostOnly(r.Host), ui.SiteSignOutHotkey("/auth/logout")))
		return
	}
	// The page as the world gets it. No identity in it, nothing a cookie changes,
	// and it is the most-requested single document on the zone the moment it goes
	// live. The TTL is ui.SiteMaxAge because the static bundle's _headers file
	// sends the same number (internal/ui/static.go), and during the cutover both
	// of them are answering this address.
	// Cacheable, and the install address is part of what is cached — so the host
	// has to be in the cache key. It already is: the edge keys on hostname, and
	// each apex is its own hostname.
	cachepolicy.MarkPublicFor(w, r, ui.SiteMaxAge, "the project page")
	s.sendHTML(w, http.StatusOK, ui.SiteHTMLFor(hostOnly(r.Host), ""))
}

// renderSiteSignin is the door in front of the unpublished page. It is the shared
// front door every other console wears (internal/ui/signin.go), so a stranger who
// stumbles onto the apex early sees something that belongs to this product rather
// than an error — and an operator sees the same panel they know from the console.
func (s *Server) renderSiteSignin(r *http.Request, user oidc.Claims) string {
	page := ui.SignIn{
		Brand:       product.Name(),
		Eyebrow:     "",
		Lede:        "",
		ActionLabel: "Sign in",
		ActionHref:  "/auth/login",
		Title:       "Sign in",
	}
	if user != nil {
		// Authenticated, and refused: saying "sign in" again would send them
		// round the same loop to the same answer. What they do need is the way
		// back out — signed in as the wrong account, on a page with no
		// navigation, the only exit was clearing a cookie by hand. The console's
		// refusal has carried this since it was written (renderSignin); this one
		// did not, which is the whole difference between a closed door and a
		// locked room.
		//
		// The address is named for the same reason it is named there: "not an
		// operator" is unactionable until you know WHICH account you arrived as,
		// and on a browser signed into several Google accounts that is the
		// question being asked.
		who := "an address"
		if email, _ := user["email"].(string); email != "" {
			who = email
		}
		page.Lede = "This page is not published yet."
		page.ActionLabel, page.ActionHref = "", ""
		page.Note = "You are signed in as " + who + ", which this ingress does not list " +
			"as an operator, so there is nothing here for you yet."
		page.Links = [][2]string{{"Sign out", "/auth/logout"}}
	}
	return page.Render()
}

// handleSiteNotFound answers everything else on the apex. Like the unknown-host
// refusal, it is a fixed sentence with nothing of anybody's in it, so the edge
// may keep it and absorb the path sweeps a public apex attracts.
func (s *Server) handleSiteNotFound(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPublicRefusal(w, r, unknownHostTTL, "nothing at this path on the apex")
	http.Error(w, "not found", http.StatusNotFound)
}

// localPath keeps a redirect on this host. Anything that is not a single-slash
// absolute path — an off-site url, a protocol-relative "//host" — becomes "/", so
// a ?next= cannot be used to bounce somebody somewhere else.
func localPath(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		return next
	}
	return "/"
}
