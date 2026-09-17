// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"

	"github.com/opentacit/tacit/internal/ui"
)

func (s *Server) mountServiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /assets/app.css", s.handleAppCSS)
	mux.HandleFunc("GET /assets/backdrop.js", s.handleBackdropJS)
	mux.HandleFunc("GET /assets/techniquemap.js", s.handleTechniqueMapJS)
	mux.HandleFunc("GET /assets/usage.js", s.handleUsageJS)
	mux.HandleFunc("GET /assets/fonts/{name}", s.handleFont)
	mux.HandleFunc("GET /apple-touch-icon.png", s.handleAppleTouchIcon)
	mux.HandleFunc("GET /apple-touch-icon-precomposed.png", s.handleAppleTouchIcon)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("GET /favicon.ico", ui.ServeFavicon)
	mux.HandleFunc("GET /robots.txt", s.handleRobots)
	mux.HandleFunc("GET /.well-known/tacit.json", s.handleWellKnown)
	mux.HandleFunc("GET /f/", s.handleFederationTree)
	mux.HandleFunc("GET /federation/feed/{id}", s.htmlView(s.pageFeedDetail))
	// Demonstration mode only (demo.go). Without TACIT_DEMO_DIR there are no
	// datasets to switch between, so the route does not exist rather than
	// standing there answering 404.
	if s.cfg().DemoDir != "" {
		mux.HandleFunc("POST /demo/switch", s.handleDemoSwitch)
	}

	mux.HandleFunc("GET /auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	mux.HandleFunc("GET /auth/logout", s.handleAuthLogout)
	// Owner mode's sign-in: a one-time link minted on the machine that runs
	// this registry, exchanged here for the same session cookie OIDC issues.
	mux.HandleFunc("GET /auth/owner", s.handleOwnerSignIn)
	// The second door (M6): a member key, on a registry its owner has opened to
	// a team. POST because it carries a credential in a form body.
	mux.HandleFunc("POST /auth/key", s.handleMemberSignIn)

	mux.HandleFunc("POST /admin/techniques/edit/{id...}", s.adminOnly(s.handleDraftEditForm))
	mux.HandleFunc("POST /admin/techniques/channels/{id...}", s.adminOnly(s.handleTechniqueChannels))
	mux.HandleFunc("POST /admin/techniques/bulk-delete", s.adminOnly(s.handleBulkDeleteDrafts))
	mux.HandleFunc("POST /admin/techniques/revert/{version}/{id...}", s.adminOnly(s.handleTechniqueRevert))
	mux.HandleFunc("POST /admin/techniques/{action}/{id...}", s.adminOnly(s.handleAdminTechniqueAction))
	mux.HandleFunc("POST /admin/suggest", s.adminOnly(s.handleSuggestForm))
	mux.HandleFunc("POST /members/open", s.adminOnly(s.handleOpenTeam))
	mux.HandleFunc("POST /admin/merge", s.adminOnly(s.handleMergeStart))
	mux.HandleFunc("POST /admin/merge/contribute", s.adminOnly(s.handleMergeContribute))
	mux.HandleFunc("POST /admin/merge/finish", s.adminOnly(s.handleMergeFinish))
	mux.HandleFunc("GET /admin/suggest/status", s.adminOnly(s.handleSuggestStatus))
	mux.HandleFunc("POST /admin/discover", s.adminOnly(s.handleDiscover))
	mux.HandleFunc("POST /admin/subscriptions", s.adminOnly(s.handleSubscriptionForm))
	mux.HandleFunc("POST /admin/poll-feeds", s.adminOnly(s.handlePollFeedsForm))
	mux.HandleFunc("POST /admin/subscriptions/delete/{id}", s.adminOnly(s.handleSubscriptionDeleteForm))
	mux.HandleFunc("POST /admin/feed-tokens", s.adminOnly(s.handleMintFeedToken))
	mux.HandleFunc("POST /admin/feed-tokens/revoke/{id}", s.adminOnly(s.handleRevokeFeedToken))
}
