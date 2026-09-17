// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"regexp"
	"strings"
	"testing"
)

// The bundle is a second copy of the project page, and a second copy of a page
// is the thing this codebase spends the most effort not having. These tests are
// the price of the copy: they hold it to the Go source every other surface
// renders from, so the static host cannot publish a document that no longer
// exists in the repository.

func bundle(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := StaticSite()
	if err != nil {
		t.Fatal(err)
	}
	m := make(map[string][]byte, len(files))
	for _, f := range files {
		if _, dup := m[f.Path]; dup {
			t.Fatalf("two files at %s", f.Path)
		}
		m[f.Path] = f.Data
	}
	return m
}

// The whole point: what publishes is what SiteHTML renders, byte for byte. A
// generator that reformatted, minified or templated anything would break this,
// and it should — the page has one home.
func TestBundledPageIsTheRenderedPage(t *testing.T) {
	got := string(bundle(t)["index.html"])
	if want := SiteHTML(""); got != want {
		t.Fatalf("index.html is not SiteHTML(\"\"): %d bytes vs %d", len(got), len(want))
	}
}

// The visitor's document and nothing else. An operator's copy carries a
// sign-out route; if one ever reached the bundle it would ship to the world.
func TestBundledPageCarriesNoOperatorChrome(t *testing.T) {
	page := string(bundle(t)["index.html"])
	for _, forbidden := range []string{"/auth/login", "/auth/logout", "/auth/callback"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("published page references %s", forbidden)
		}
	}
}

var (
	htmlRefRe = regexp.MustCompile(`(?:href|src)="(/[^"]*)"`)
	cssRefRe  = regexp.MustCompile(`url\("(/[^"]+)"\)`)
)

// Every absolute reference the page or the stylesheet makes has to resolve
// inside the bundle. On the ingress a missing asset is a 404 nobody sees until a
// typeface fails to load; here it is a file that was never copied, which is the
// same symptom with one more way to happen.
func TestBundleCarriesEverythingThePageAsksFor(t *testing.T) {
	files := bundle(t)
	refs := map[string]string{} // reference -> where it was found
	for _, m := range htmlRefRe.FindAllStringSubmatch(string(files["index.html"]), -1) {
		refs[m[1]] = "index.html"
	}
	for _, m := range cssRefRe.FindAllStringSubmatch(string(files["assets/app.css"]), -1) {
		refs[m[1]] = "app.css"
	}
	// The backdrop is injected by script rather than linked, so no regexp over
	// the markup would find it. It is named here for the same reason it is
	// injected there: the page only asks for it when there is a WebGL context.
	refs[BackdropURL()] = "the backdrop injector"

	for ref, where := range refs {
		p := strings.TrimPrefix(strings.SplitN(ref, "?", 2)[0], "/")
		if p == "" {
			continue // the page linking its own root
		}
		if _, ok := files[p]; !ok {
			t.Errorf("%s references /%s, which the bundle does not carry", where, p)
		}
	}
}

// The availability property, stated as a test. A bundle whose page could not be
// served stale through an origin failure would still work and would have lost
// the reason it exists.
func TestHeadersKeepThePageServableThroughAnOutage(t *testing.T) {
	headers := string(bundle(t)["_headers"])
	if !strings.Contains(headers, "stale-if-error=") {
		t.Fatal("_headers does not let the edge serve the page through an origin failure")
	}
	// The favicon is SVG under an .ico name. Without this line a static host
	// types it by extension and browsers reject it.
	if !strings.Contains(headers, "Content-Type: image/svg+xml") {
		t.Fatal("_headers does not correct the favicon's content type")
	}
}
