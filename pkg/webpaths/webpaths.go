// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package webpaths is the registry's public surface, described once: which
// request paths an anonymous visitor may be served from a shared cache, which
// must never be, and which cookies make a response one reader's rather than
// everybody's.
//
// It is a package rather than a list inside the server because two kinds of
// code need the same answer and they do not run in the same place. The registry
// consults it at request time — `internal/cachepolicy` decides each response's
// headers, and the session middleware needs the cookie name. A CDN in front of
// the registry needs it at configuration time, to decide which requests the
// edge may even consider caching. Whoever operates that edge is not
// necessarily us, which is why this is under `pkg/` and importable: an OpenTacit
// fleet behind any CDN needs to know these paths, and the alternative is
// everybody keeping their own copy and finding out it drifted from a hit-ratio
// graph.
//
// Nothing here is protection. The bytes are kept out of a shared cache by the
// origin's own headers, on every request; an edge rule is an optimization on
// top of that, and deleting every one of them costs a cold cache and nothing
// else.
package webpaths

import "strings"

// PrivatePathPrefixes are the paths that must never be cached under any
// circumstances, expressed as an allowlist of what to bypass rather than as an
// exception to a cache-everything rule.
//
// /v1/ is here whole, including the two genuinely public documents under it
// (/v1/openapi.yaml and /v1/schemas/…). The origin still marks those public, so a
// later rule could pick them up — but /v1/ is the credentialed surface, they are a
// few kilobytes fetched rarely, and one misordered exception carved into the
// credentialed prefix would cost more than the hit ratio it bought.
var PrivatePathPrefixes = []string{
	"/v1/",          // the whole JSON API, credentialed
	"/mcp",          // the MCP session endpoint, streaming
	"/oauth/",       // the MCP-client OAuth broker
	"/.well-known/", // OAuth discovery and the token-filtered provider descriptor
	"/auth/",        // sign-in, callback, sign-out
	"/admin/",       // every dashboard mutation
	"/settings",     // registry configuration
	"/setup",        // the first-run wizard
	"/members",      // member access management
	"/join/",        // an invite's install script
	"/usage",        // a member's own activity
	"/learning",     // the evidence pipeline
	"/review",       // the review queue
	"/drafts",       // draft detail
	"/federation",   // published feeds and subscriptions
	"/demo/",        // demonstration-dataset switching
	"/outcomes",     // the working dashboard
	"/techniques",   // the playbook
}

// PublicPaths are the reviewed cacheable paths: an exact path, or a prefix ending
// in "/" or matched by prefix where the route itself is a tree.
//
// Every entry has been checked against the question that matters — could a tenant
// setting make this private? — and every one of them answers no. The dashboard's
// own routes are absent even though an anonymous visitor gets the front door at
// each of them, and so is "/", which used to be here for exactly that reason.
//
// The front door came off the list because it is not the only answer at any of
// those addresses: they serve the member's own dashboard to a request carrying a
// session. The origin downgrades such a request, and a well-built edge rule
// bypasses on the cookie, so the shared cache was always safe — but a browser is
// a cache too, and it keys on neither. One that stored the front door went on
// serving it to the reader after they signed in. Nothing cacheable may live at
// an address that answers two ways.
var PublicPaths = []PublicPath{
	{Path: "/robots.txt", Exact: true, Note: "crawler policy"},
	{Path: "/favicon.ico", Exact: true, Note: "brand mark"},
	{Path: "/apple-touch-icon.png", Exact: true, Note: "home-screen icon"},
	{Path: "/apple-touch-icon-precomposed.png", Exact: true, Note: "home-screen icon, older iOS"},
	{Path: "/manifest.webmanifest", Exact: true, Note: "web app manifest"},
	{Path: "/assets/", Note: "fingerprinted stylesheet, scripts and typefaces"},
	{Path: "/docs/user-guide", Note: "the public member guide and its illustrations"},
	{Path: "/f/public/feed.json", Exact: true, Note: "the commons feed"},
	{Path: "/f/techniques/", Note: "published techniques; gated ones answer private and bypass"},
}

// PublicPath is one reviewed entry in the allowlist. The note is the artefact of
// the review: an entry without one is an entry nobody checked.
type PublicPath struct {
	Path  string
	Exact bool
	Note  string
}

// SessionCookieName is the one cookie name the whole fleet uses for a signed-in
// reader. It is here rather than in the registry package because both kinds of
// consumer need it: the middleware, to recognize an authenticated request, and
// an edge rule, to bypass one.
const SessionCookieName = "tacit_session"

// IngressSessionCookieName is the ingress console's own session cookie
// (internal/ingress signs its operators in separately from the registries).
// Console hosts are normally excluded from caching wholesale, so bypassing on
// this cookie is defense in depth: if a console response ever escaped onto a
// cacheable host, a signed-in operator's request would still reach the origin.
const IngressSessionCookieName = "tacit_ingress_session"

// RoutingCookies are cookies that change WHICH content answers a URL rather than
// who is reading it.
//
// The distinction decides whether an edge rule is optional. A private response
// header stops a demonstration response being stored; it cannot stop an
// already-cached production page being served to a demonstration reader, and
// only a bypass can. So a routing cookie has to reach the edge configuration,
// while an identity cookie is already handled at the origin.
var RoutingCookies = []string{"tacit_demo"}

// PersonalCookies are every cookie that makes a response unshareable: the two
// session cookies and every routing cookie. An edge that bypasses on these, and
// an origin that marks the same requests private, agree by construction.
func PersonalCookies() []string {
	return append([]string{SessionCookieName, IngressSessionCookieName}, RoutingCookies...)
}

// IsPublic reports whether the allowlist makes a path eligible for caching.
//
// Nothing at runtime consults it: at runtime the origin's own headers are the
// answer, and this is the model an edge is configured from. It exists so a test
// can hold the two to each other.
func IsPublic(path string) bool {
	for _, p := range PublicPaths {
		if p.Exact && path == p.Path {
			return true
		}
		if !p.Exact && strings.HasPrefix(path, p.Path) {
			return true
		}
	}
	return false
}

// IsPrivate reports whether a path is on the never-cache list.
func IsPrivate(path string) bool {
	for _, p := range PrivatePathPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
