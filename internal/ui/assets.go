// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package ui holds the chrome two binaries share: the stylesheet, the
// typefaces, the brand mark, and the pre-paint scripts that stop a flash of the
// wrong colour. The registry dashboard was here first and is still the reason
// every value looks the way it does; the ingress console
// (docs/distribution/ingress.md) is the second consumer, and the reason
// any of it moved out of internal/registry/web.
//
// The rule this package exists to keep: there is ONE stylesheet. A second
// console that merely resembles the first would drift within a release, and the
// house style is explicit that reuse is the discipline. So app.css lives here,
// both binaries serve the same bytes, and a change to the look lands in both at
// once whether or not anyone remembered the other one.
package ui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"path"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
)

// The typefaces, embedded so each binary stays a single file and an instance on
// an isolated network renders exactly like one on the open internet. Mona Sans
// (GitHub, OFL) carries the full 200–900 range this design needs for its
// hairline figures; IBM Plex Mono (IBM, OFL) is confined to literal machine
// text. Licences ship beside them and are served, because the OFL asks that the
// licence travel with the font.
//
//go:embed assets/fonts
var fontFS embed.FS

//go:embed assets/app.css
var cssRaw string

// The ambient WebGL scene — the emergent knowledge graph flown as a corridor —
// documented at the top of the file itself. It lives here rather than in the
// registry because three documents render it now: the registry's front door, the
// project page at the zone's apex (site.go), and whatever comes next. Thirty
// kilobytes, which is why it is an asset and not an inline script — the browser
// caches it across the session, and a page that has no canvas never asks for it.
//
// Served without a session: the front door and the project page both need it
// before anyone has one.
//
//go:embed assets/backdrop.js
var backdropJS string

var backdropHash = func() string {
	sum := sha256.Sum256([]byte(backdropJS))
	return hex.EncodeToString(sum[:16])
}()

var backdropETag = `"` + backdropHash + `"`

// BackdropURL is the src a document injects. Neither document ships a static
// <script> tag: each probes for WebGL first and builds the element only if there
// is a context to render into.
func BackdropURL() string { return "/assets/backdrop.js?v=" + backdropHash[:12] }

// ServeBackdrop answers a request for the scene. Fingerprinted in its URL, so the
// request that matters is immutable; a bare one is a client asking by hand.
func ServeBackdrop(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("v") != "" {
		cachepolicy.MarkImmutable(w, r, "ambient backdrop, fingerprinted")
	} else {
		cachepolicy.MarkPublic(w, r, "ambient backdrop")
	}
	w.Header().Set("ETag", backdropETag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, backdropETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write([]byte(backdropJS))
}

// The project page's session frames used to live here too, as PNGs captured
// from the terminal that ran the session. They are text now, held in the page
// itself (siteframes.go). The one image the page does carry is the dashboard,
// and it is deliberately not a capture of its own: it is the user guide's
// screenshot pair (docs/user-guide/images/outcomes.png and its -dark sibling),
// embedded because the apex serves nothing from disk. A test holds these
// copies byte-identical to the guide's files, so regenerating the guide's
// screenshots is the one workflow that refreshes the front page too — there is
// no second capture to age separately.
//
//go:embed assets/shots
var shotFS embed.FS

// shotVer fingerprints the embedded screenshots the way fontVer does the
// typefaces: one hash over all of them, carried as ?v= on each URL, so a
// binary that ships a regenerated screenshot busts the browser's copy.
var shotVer = func() string {
	sum := sha256.New()
	names, _ := shotFS.ReadDir("assets/shots")
	for _, n := range names {
		b, err := shotFS.ReadFile("assets/shots/" + n.Name())
		if err != nil {
			continue
		}
		sum.Write([]byte(n.Name()))
		sum.Write(b)
	}
	return hex.EncodeToString(sum.Sum(nil)[:8])[:12]
}()

// SiteShotURL is the src the project page writes for an embedded screenshot.
func SiteShotURL(name string) string {
	return "/assets/shots/" + name + "?v=" + shotVer
}

// ServeShot answers a screenshot request by basename. The page's URLs carry
// the fingerprint, so the request that matters is immutable; a bare one is a
// client asking by hand.
func ServeShot(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	b, err := shotFS.ReadFile("assets/shots/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("v") != "" {
		cachepolicy.MarkImmutable(w, r, "site screenshot, fingerprinted")
	} else {
		cachepolicy.MarkPublicFor(w, r, 86400, "site screenshot")
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(b)
}

// fontVer fingerprints every embedded font at once. The stylesheet's @font-face
// URLs carry it, so a binary upgrade that changes a face busts the browser's
// copy — and because the URL then uniquely identifies the bytes, the files can
// be cached hard.
var fontVer = func() string {
	sum := sha256.New()
	names, _ := fontFS.ReadDir("assets/fonts")
	for _, n := range names {
		b, err := fontFS.ReadFile("assets/fonts/" + n.Name())
		if err != nil {
			continue
		}
		sum.Write([]byte(n.Name()))
		sum.Write(b)
	}
	return hex.EncodeToString(sum.Sum(nil)[:8])[:12]
}()

// css is the stylesheet with the font fingerprint substituted into its
// @font-face URLs. Keeping a placeholder in the file rather than a version
// number means app.css stays a plain stylesheet you can lint and edit.
var css = strings.ReplaceAll(cssRaw, "FONTVER", fontVer)

var cssHash = func() string {
	sum := sha256.Sum256([]byte(css))
	return hex.EncodeToString(sum[:16])
}()

var cssETag = `"` + cssHash + `"`

// CSS returns the stylesheet source. The registry inlines it into the MCP apps,
// which render outside our origin and cannot fetch it.
func CSS() string { return css }

// CSSHash is the content fingerprint, used as the ?v= on the stylesheet link so
// an upgrade busts the browser's copy.
func CSSHash() string { return cssHash }

// FontVer is the fingerprint stamped into the @font-face URLs.
func FontVer() string { return fontVer }

// ServeCSS answers a stylesheet request. The stylesheet is immutable for the
// life of a binary, so a matching If-None-Match gets a 304 and the browser
// reuses its copy across every page in the session.
func ServeCSS(w http.ResponseWriter, r *http.Request) {
	// The link carries the content hash, so a request that has it is asking for
	// bytes that cannot change and can be cached hard (bucket C). Without the
	// parameter the URL is not a promise about content, so it is bucket A with a
	// short browser TTL and the edge revalidates for everyone.
	if r.URL.Query().Get("v") != "" {
		cachepolicy.MarkImmutable(w, r, "stylesheet, fingerprinted")
	} else {
		cachepolicy.MarkPublicFor(w, r, 300, "stylesheet")
	}
	w.Header().Set("ETag", cssETag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, cssETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(css))
}

// ServeFavicon answers /favicon.ico with FaviconSVG. The name is what clients
// ask for; the content type is what the bytes actually are, which every current
// browser honours — and a client too old to render SVG was going to ignore the
// answer either way. Both consoles serve it: plenty of clients ask for this path
// regardless of the icon a page declares inline, and every one of those asks used
// to be a 404 in the operations log.
func ServeFavicon(w http.ResponseWriter, r *http.Request) {
	cachepolicy.MarkPublicFor(w, r, 86400, "favicon")
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte(FaviconSVG))
}

// ServeFont answers a font (or licence) request by basename.
func ServeFont(w http.ResponseWriter, r *http.Request) {
	name := path.Base(r.URL.Path)
	b, err := fontFS.ReadFile("assets/fonts/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	case strings.HasSuffix(name, ".txt"):
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	// The stylesheet's @font-face URLs carry the fingerprint, so the request that
	// matters is bucket C. A bare one is a client asking by hand.
	if r.URL.Query().Get("v") != "" {
		cachepolicy.MarkImmutable(w, r, "typeface, fingerprinted")
	} else {
		cachepolicy.MarkPublicFor(w, r, 86400, "typeface")
	}
	_, _ = w.Write(b)
}
