// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"bytes"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every embedded screenshot is the user guide's own file, and every one of them
// is accounted for.
//
// That is the whole arrangement: regenerating the guide's screenshots is the one
// workflow, and the front page rides along — the hero and the carousel both show
// captures from it. A copy that drifts shows a stranger a dashboard the product
// no longer draws, silently, because nothing else reads both files.
//
// The completeness half matters as much as the comparison. A file in here that
// no view claims is a capture nobody is checking, and this page has already
// carried one of those: a scrubbed screenshot of somebody's own desktop, which
// was correct only for as long as the person who scrubbed it was looking.
func TestEveryEmbeddedScreenshotIsTheGuidesOwn(t *testing.T) {
	claimed := map[string]bool{}
	for _, v := range siteSeeViews {
		claimed[v.Shot], claimed[v.Dark] = true, true
	}
	heroShot, heroDark, _, _, _ := siteHeroShot()
	if !claimed[heroShot] || !claimed[heroDark] {
		t.Errorf("the hero's pair (%s, %s) is not one of the carousel's, so it is a "+
			"second copy to keep current", heroShot, heroDark)
	}

	names, err := shotFS.ReadDir("assets/shots")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded screenshots: %v", err)
	}
	seen := map[string]bool{}
	for _, n := range names {
		name := n.Name()
		seen[name] = true
		if !claimed[name] {
			t.Errorf("embedded screenshot %s is shown by no view. Every capture in "+
				"here has to be accounted for: one nothing displays is one nothing "+
				"checks", name)
			continue
		}
		embedded, err := shotFS.ReadFile("assets/shots/" + name)
		if err != nil {
			t.Fatalf("reading embedded %s: %v", name, err)
		}
		source := filepath.Join("..", "..", "docs", "user-guide", "images", name)
		disk, err := os.ReadFile(source)
		if err != nil {
			t.Errorf("embedded screenshot %s has no source in the guide: %v", name, err)
			continue
		}
		if !bytes.Equal(embedded, disk) {
			t.Errorf("embedded %s differs from the guide's copy — after regenerating "+
				"the guide's screenshots, run: cp docs/user-guide/images/%s "+
				"internal/ui/assets/shots/", name, name)
		}
	}
	for name := range claimed {
		if !seen[name] {
			t.Errorf("a view shows %s and it is not embedded", name)
		}
	}
}

// The page is theme-aware and the screenshot ships as a light/dark pair; both
// have to be in the document, at their fingerprinted URLs, each describing
// itself to a reader who cannot see it. Losing one variant shows a dark reader
// a glowing white plate, which is exactly the mismatch the pair exists to avoid.
func TestSiteShowsTheDashboardInBothSchemes(t *testing.T) {
	page := SiteHTML("")
	for _, v := range siteSeeViews {
		for _, want := range []string{
			`<img class="theme-light" src="` + SiteShotURL(v.Shot) + `"`,
			`<img class="theme-dark" src="` + SiteShotURL(v.Dark) + `"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("the %s view does not carry %s", v.Key, want)
			}
		}
		if !strings.Contains(page, siteProduct(html.EscapeString(v.Alt))) {
			t.Errorf("the %s screenshot carries no description — the section goes "+
				"silent for a reader who cannot see it", v.Key)
		}
	}
}

// The section is one slice told twice: a slide per view, and a paragraph per view
// in the column beside it. Losing either half leaves a screen nobody explains, or
// a sentence about a screen that is not there — and the second failure is
// invisible until somebody swipes.
func TestSiteSeeTellsEveryViewTwice(t *testing.T) {
	page := SiteHTML("")
	if len(siteSeeViews) < 2 {
		t.Fatal("the carousel has one view, so it is not a carousel — the dots and the " +
			"track are machinery with nothing to do")
	}
	for i, v := range siteSeeViews {
		for _, want := range []string{
			`<li class="site-see-slide" data-see="` + v.Key + `"`,
			`<button type="button" class="site-see-dot" data-see-to="` + v.Key + `"`,
			`<div class="site-see-view" data-see-text="` + v.Key + `"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("the %s view is missing %s", v.Key, want)
			}
		}
		// Title, deck and paragraph are one block per view. A view that lost any of
		// the three would leave the column describing the screen beside it with
		// somebody else's words.
		for what, line := range map[string]string{
			"title":    siteProduct(`<h2>` + html.EscapeString(v.Title) + `</h2>`),
			"subtitle": siteProduct(`<p class="hint">` + html.EscapeString(v.Subtitle) + `</p>`),
			"desc":     siteProduct(`<p class="site-see-sub">` + html.EscapeString(v.Desc) + `</p>`),
		} {
			if !strings.Contains(page, line) {
				t.Errorf("the %s view has no %s in the column", v.Key, what)
			}
		}
		// The heading is the screen's, not the section's. Two views sharing one
		// makes the carousel look broken to anybody who swipes and sees the same
		// line stay put.
		for _, other := range siteSeeViews {
			if other.Key != v.Key && other.Title == v.Title {
				t.Errorf("the %s and %s views share the title %q", v.Key, other.Key, v.Title)
			}
		}
		// Everything past the first starts hidden, so the page without script is
		// one screenshot and one paragraph rather than three of each stacked up.
		// Both halves of a view — its slide and its block of column — start hidden
		// together past the first, or the base page pairs one screenshot with three
		// stacked descriptions.
		for _, attr := range []string{`data-see="`, `data-see-text="`} {
			marker := attr + v.Key + `" hidden`
			if got := strings.Contains(page, marker); got != (i > 0) {
				t.Errorf("the %s view's hidden state on %s is %v, want %v — the base page "+
					"shows the first view alone", v.Key, attr, got, i > 0)
			}
		}
	}
}
