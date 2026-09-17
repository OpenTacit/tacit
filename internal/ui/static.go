// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/cachepolicy"
)

// The project page as a directory of files: the same document the apex serves,
// plus every asset it links, written out so a static host can answer
// https://tacit.zone/ with no OpenTacit process behind it at all.
//
// Why this exists. The page is a pure function of the binary — SiteHTML reads no
// request, no session and no route table, and two builds of the same commit
// produce identical bytes. Nothing about it needed a server; what needed the
// server was everything around it, because the assets live in embed.FS and only
// the ingress could open them. So the apex's availability was the ingress's
// availability, and `systemctl restart tacit-ingress` was a landing-page outage.
// This file is the whole of the fix: the same bytes, on a host whose job is to
// hand out files.
//
// The bundle is generated, never edited (hack/sitegen), and a test holds
// index.html byte-identical to SiteHTML("") — the static copy cannot drift from
// the Go source that every other surface renders from, which is the one failure
// mode a second copy of a page introduces.
//
// What it deliberately does NOT carry:
//
//   - The operator's chrome. SiteHTML("") is the visitor's document, and the
//     empty string is load-bearing: access is decided in front of these files
//     now (Cloudflare Access, docs/distribution/site-static.md), not inside them,
//     so the reviewed bytes and the published bytes are the same bytes. That is
//     stricter than the ingress ever managed — its operator copy carried the
//     sign-out hotkey, so what an operator reviewed was never quite what
//     published.
//   - install.sh. The short address has to answer while the page is still gated,
//     because the console hands `curl -fsSL https://opentacit.com/install.sh | sh`
//     to every new member of every registry. A file in this bundle would sit
//     behind the same gate as the page. It is a redirect on the zone instead,
//     which runs before any gate in front of these files — configured from
//     pkg/installaddr, so the address the rule serves and the address every
//     document quotes cannot drift.

// StaticFile is one file in the bundle, at a slash-separated path relative to
// its root.
type StaticFile struct {
	Path string
	Data []byte
}

// SiteMaxAge is the browser TTL on the project page, and it is a constant
// because two things now send it: the ingress while it still serves the apex,
// and the bundle's _headers file once a static host does. An hour, so a copy
// edit is visible the same afternoon.
const SiteMaxAge = 3600

// siteAssetMaxAge is the TTL on a bare, unfingerprinted asset request. Every
// reference the page makes carries ?v=, so this is only ever what somebody
// typing a URL by hand gets.
const siteAssetMaxAge = 86400

// StaticSite renders the whole bundle, ordered by path so a build is
// reproducible and a diff of two builds is readable.
func StaticSite() ([]StaticFile, error) {
	files := []StaticFile{
		{Path: "index.html", Data: []byte(SiteHTML(""))},
		{Path: "assets/app.css", Data: []byte(CSS())},
		{Path: "assets/backdrop.js", Data: []byte(backdropJS)},
		// Named .ico because that is what a client asks for unprompted, and
		// carrying SVG because that is what the bytes are — the same trade
		// ServeFavicon makes. A static host would type it by extension and get
		// it wrong, so _headers overrides it below.
		{Path: "favicon.ico", Data: []byte(FaviconSVG)},
	}
	for _, dir := range []struct {
		fsys embed.FS
		name string
	}{{fontFS, "assets/fonts"}, {shotFS, "assets/shots"}} {
		entries, err := dir.fsys.ReadDir(dir.name)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", dir.name, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := path.Join(dir.name, e.Name())
			b, err := dir.fsys.ReadFile(name)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", name, err)
			}
			files = append(files, StaticFile{Path: name, Data: b})
		}
	}
	files = append(files, StaticFile{Path: "_headers", Data: []byte(siteHeaders())})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// siteHeaders is the bundle's _headers file — the static host's version of what
// cachepolicy declares at the origin. It is built from the same constants for
// the same reason the Cloudflare ruleset is generated: a policy written twice is
// a policy that is true once.
//
// The line that matters most is stale-if-error on the page itself. It is why a
// static bundle is an availability improvement twice over: the files answer
// without an OpenTacit process, and if the host that holds them ever fails, the edge
// keeps serving the last good copy for a day rather than an error.
//
// /assets/* is immutable, which is safe only because every reference to those
// files carries a ?v= fingerprint and a browser caches by whole URL: a
// regenerated stylesheet is a different URL, so nobody is holding a year-old
// copy of one they will ask for again. A bare /assets/app.css — hand-typed,
// linked from nothing — gets the year too. That is the one place this is looser
// than the origin, which downgrades a bare request to a short TTL, and it is
// loose about a URL the product never emits.
func siteHeaders() string {
	var b strings.Builder
	b.WriteString("# Generated by hack/sitegen from internal/ui/static.go. Do not edit.\n")
	for _, h := range []struct {
		pattern string
		headers []string
	}{
		// The request path is what matches, and a browser asking for the page
		// asks for "/". index.html is named too, for a client that followed a
		// link to the file itself.
		{"/", []string{"Cache-Control: " + cachepolicy.PublicControl(SiteMaxAge)}},
		{"/index.html", []string{"Cache-Control: " + cachepolicy.PublicControl(SiteMaxAge)}},
		{"/assets/*", []string{"Cache-Control: " + cachepolicy.ImmutableControl}},
		{"/favicon.ico", []string{
			"Content-Type: image/svg+xml",
			"Cache-Control: " + cachepolicy.PublicControl(siteAssetMaxAge),
		}},
	} {
		b.WriteString("\n" + h.pattern + "\n")
		for _, line := range h.headers {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}
