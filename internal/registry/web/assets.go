// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/ui"
)

// appCSS is the dashboard stylesheet. It lives in internal/ui, because the
// ingress console (docs/distribution/ingress.md) serves the same bytes and
// two consoles that merely resembled each other would drift within a release.
// Embedded at build time either way, so the binary stays a single file with no
// asset directory to deploy.
//
// It is served at /assets/app.css to the browser, and inlined verbatim by the
// MCP apps, which render outside our origin and cannot fetch it.
var appCSS = ui.CSS()

// appCSSHash is the content fingerprint cssLink carries.
var appCSSHash = ui.CSSHash()

func (s *Server) handleAppCSS(w http.ResponseWriter, r *http.Request) {
	ui.ServeCSS(w, r)
}

// cssLink is the stylesheet reference every browser-facing document carries.
// The URL carries the content hash so a registry upgrade changes it and every
// client re-fetches immediately — including installed home-screen web apps,
// which skip revalidation and offer no way to hard-refresh (the same
// self-healing the doc screenshots get from ?v=<mtime>).
// The onload is what reveals the page: bootHideScript hides content until the
// stylesheet has applied, and this is the earliest honest signal that it has.
// A timer in that script is the backstop for a stylesheet that never arrives.
var cssLink = `<link rel="stylesheet" href="/assets/app.css?v=` + appCSSHash[:12] +
	`" onload="document.documentElement.classList.remove('booting')">`

// The ambient WebGL scene moved to internal/ui, because the project page at the
// zone's apex renders the same field and the ingress serves that page. This route
// stays: the front door's URL is fingerprinted and cached, and the bytes are the
// same bytes.

// techniqueMapJS is the playbook map's client: the controller, the WebGL field and
// the 2-D canvas fallback in one file (see the header comment there). Like the
// backdrop it is an asset rather than an inline script because two documents
// share it — but only one of them can fetch it. The /techniques/map page loads it by
// hashed URL and the browser caches it; the MCP map app inlines these same bytes,
// because a sandboxed frame cannot reach back to the origin.
//
//go:embed assets/techniquemap.js
var techniqueMapJS string

var techniqueMapJSHash = func() string {
	sum := sha256.Sum256([]byte(techniqueMapJS))
	return hex.EncodeToString(sum[:16])
}()

var techniqueMapJSETag = `"` + techniqueMapJSHash + `"`

// techniqueMapScriptTag is what the page emits: the renderer, deferred so it runs
// after the graph's <script type="application/json"> data is in the document.
var techniqueMapScriptTag = `<script src="/assets/techniquemap.js?v=` + techniqueMapJSHash[:12] + `" defer></script>`

// usageJS is the Usage page's client (see the header comment there). An asset
// for the same reason the map renderer is one — three thousand lines that no
// browser could cache while they were pasted into every response — and, unlike
// the map, it has no second document inlining it: the page is the only reader.
//
//go:embed assets/usage.js
var usageJS string

var usageJSHash = func() string {
	sum := sha256.Sum256([]byte(usageJS))
	return hex.EncodeToString(sum[:16])
}()

var usageJSETag = `"` + usageJSHash + `"`

// Deferred, so it runs after the <script type="application/json"> configuration
// it reads is in the document.
var usageScriptTag = `<script src="/assets/usage.js?v=` + usageJSHash[:12] + `" defer></script>`

func (s *Server) handleUsageJS(w http.ResponseWriter, r *http.Request) {
	serveHashedAsset(w, r, "text/javascript; charset=utf-8", usageJSETag, "usage renderer", usageJS)
}

func (s *Server) handleTechniqueMapJS(w http.ResponseWriter, r *http.Request) {
	serveHashedAsset(w, r, "text/javascript; charset=utf-8", techniqueMapJSETag, "map renderer", techniqueMapJS)
}

func (s *Server) handleBackdropJS(w http.ResponseWriter, r *http.Request) {
	ui.ServeBackdrop(w, r)
}

// serveHashedAsset answers a request for an embedded script.
//
// The ?v= is the whole bargain: a URL carrying the content hash is a promise
// about the bytes, so it is bucket C — cached for a year and never revalidated.
// A bare URL makes no such promise, so it is bucket A with the default short
// browser TTL and the stale directives behind it. It used to answer no-cache,
// which is now forbidden on a public response: "store but always revalidate" is
// a worse small max-age, and it cancels the stale-while-revalidate that keeps an
// expiring edge entry from making somebody wait for the origin. The ETag is the
// same either way, so an unchanged asset still costs one 304.
func serveHashedAsset(w http.ResponseWriter, r *http.Request, contentType, etag, reason, body string) {
	if r.URL.Query().Get("v") != "" {
		cachepolicy.MarkImmutable(w, r, reason+", fingerprinted")
	} else {
		cachepolicy.MarkPublic(w, r, reason)
	}
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write([]byte(body))
}
