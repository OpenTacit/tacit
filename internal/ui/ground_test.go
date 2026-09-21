// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// GroundCSS restates three tokens from assets/app.css so the first paint is the
// right colour before the stylesheet lands. A duplicated palette in this package
// has gone stale unnoticed before — a whole second copy of the dark tokens sat
// here through a redesign — so this asserts the copy still matches the original
// rather than trusting anyone to remember.
func TestGroundCSSMatchesStylesheet(t *testing.T) {
	// Checked BOTH ways. Only asserting that each stylesheet palette appears
	// somewhere in GroundCSS is too weak: GroundCSS states the dark trio twice
	// (once under the media query, once under data-theme="dark"), so one copy
	// could drift while the other kept the test green.
	css := regexp.MustCompile(`--plane:(#[0-9a-f]{6}); --surface:(#[0-9a-f]{6}); --ink:(#[0-9a-f]{6})`)
	ground := regexp.MustCompile(`--plane:(#[0-9a-f]{6});--surface:(#[0-9a-f]{6});--ink:(#[0-9a-f]{6})`)
	trio := func(m []string) string { return m[1] + " " + m[2] + " " + m[3] }

	inCSS := map[string]bool{}
	for _, m := range css.FindAllStringSubmatch(cssRaw, -1) {
		inCSS[trio(m)] = true
	}
	if len(inCSS) != 2 {
		t.Fatalf("expected two distinct ground palettes in app.css (light and dark), got %d", len(inCSS))
	}

	inGround := map[string]bool{}
	copies := ground.FindAllStringSubmatch(GroundCSS, -1)
	if len(copies) < 3 {
		t.Fatalf("expected at least three ground declarations in GroundCSS "+
			"(light, dark-by-preference, dark-by-attribute), got %d", len(copies))
	}
	for _, m := range copies {
		inGround[trio(m)] = true
		if !inCSS[trio(m)] {
			t.Errorf("GroundCSS declares %s, which app.css does not. "+
				"A copy has drifted — the pre-stylesheet paint will not match the stylesheet.", trio(m))
		}
	}
	for want := range inCSS {
		if !inGround[want] {
			t.Errorf("app.css declares %s, which GroundCSS does not carry. "+
				"That mode will paint the browser default before the stylesheet lands.", want)
		}
	}
}

// The mark exists twice as a favicon: percent-encoded into a data URI for the
// page's own <link>, and as a plain SVG document for /favicon.ico. One drawing in
// two encodings is the shape of thing that drifts — and a drift here is silent,
// since each is only ever seen on its own. So the two are checked against each
// other: decode the data URI and it must BE the served document.
func TestFaviconEncodingsAreOneDrawing(t *testing.T) {
	const prefix = `<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,`
	href := strings.TrimPrefix(FaviconLink, prefix)
	if href == FaviconLink {
		t.Fatalf("FaviconLink no longer starts with the data-URI prefix this test decodes:\n%s", FaviconLink)
	}
	href = strings.TrimSuffix(href, `">`)
	decoded, err := url.PathUnescape(href)
	if err != nil {
		t.Fatalf("the data URI does not decode: %v", err)
	}
	if decoded != FaviconSVG {
		t.Errorf("the inline icon and the served icon have drifted apart.\n"+
			"data URI decodes to:\n%s\n/favicon.ico serves:\n%s", decoded, FaviconSVG)
	}
}

// The front-door fallback is allowed to exist only because it cannot become a
// second design. Two properties enforce that, and this is where they are kept
// honest — a palette duplicated into this package has gone stale before.
func TestFrontDoorFallbackDeclaresNoColoursOfItsOwn(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	if found := hex.FindAllString(FrontDoorCSS, -1); len(found) > 0 {
		t.Errorf("the front-door fallback declares colours of its own (%v). "+
			"It must draw everything from the tokens GroundCSS already carries, "+
			"or it becomes a second palette that drifts from the stylesheet.", found)
	}
	// Every custom property it reads has to be one GroundCSS defines, since the
	// fallback's whole point is to work when app.css has not arrived.
	for _, ref := range regexp.MustCompile(`var\((--[a-z0-9-]+)\)`).FindAllStringSubmatch(FrontDoorCSS, -1) {
		if !strings.Contains(GroundCSS, ref[1]+":") {
			t.Errorf("the fallback reads %s, which GroundCSS does not define — "+
				"it would be empty in exactly the case the fallback exists for", ref[1])
		}
	}
}

// A floor, not a facsimile. The ceiling is the discipline: without it this grows
// one rule at a time into the copy of the stylesheet the comment promises it is
// not.
func TestFrontDoorFallbackStaysAFloor(t *testing.T) {
	const ceiling = 1200
	if len(FrontDoorCSS) > ceiling {
		t.Errorf("the front-door fallback is %d bytes, over the %d-byte ceiling. "+
			"If the front door needs this much style, the argument for inlining it "+
			"needs remaking rather than the ceiling raising.", len(FrontDoorCSS), ceiling)
	}
}

// The guard is worthless if it probes a property the stylesheet does not define:
// it would fire on every load and re-fetch the stylesheet for nothing.
func TestStylesheetGuardProbesAPropertyOnlyTheStylesheetDefines(t *testing.T) {
	if !strings.Contains(cssRaw, GuardProperty+":") {
		t.Fatalf("the guard probes %s, which app.css does not define on :root — "+
			"every page load would think the stylesheet had failed", GuardProperty)
	}
	if strings.Contains(GroundCSS, GuardProperty+":") {
		t.Fatalf("the guard probes %s, which GroundCSS also defines — "+
			"the probe would pass on a page whose stylesheet never arrived", GuardProperty)
	}
	if !strings.Contains(StylesheetGuardScript, GuardProperty) {
		t.Fatal("the guard script no longer reads GuardProperty")
	}
}

// An element toggled with the `hidden` attribute needs its own
// [hidden]{display:none}, because an author display value beats the browser's
// built-in rule for it. This is not theoretical: .set-addr is inline-flex, so
// unticking Global Access on the settings page left its public address on
// screen — still badged "connected" — while the setting underneath it was off.
func TestElementsToggledWithHiddenRestoreTheBuiltInRule(t *testing.T) {
	// The settings grid covers its own rows with one blanket rule, because they
	// are many and the next one to be hidden would forget a per-row rule.
	if !strings.Contains(cssRaw, ".set-grid [hidden]{display:none}") {
		t.Error("settings rows hidden by a script would stay on screen: .set-row is flex, .set-note is block")
	}
	// Elsewhere it is one rule per element. Adding one to the stylesheet means
	// adding it here.
	for _, sel := range []string{".suggest-status", ".account-menu"} {
		display := regexp.MustCompile(regexp.QuoteMeta(sel) + `\{[^}]*display:`)
		if !display.MatchString(cssRaw) {
			continue // no author display; the built-in rule already wins
		}
		if !regexp.MustCompile(regexp.QuoteMeta(sel) + `\[hidden\][^{]*\{display:none\}`).MatchString(cssRaw) {
			t.Errorf("%s sets its own display but has no %s[hidden] rule, so hiding it does nothing",
				sel, sel)
		}
	}
}

// A two-position switch has to be idempotent: clicking the position already in
// force does nothing. It is one checkbox inside a <label>, and a label toggles on
// ANY click within it — so without making the in-force half inert, clicking the
// word you are already on jumps you to the opposite position. That is the one
// thing such a control must never do, and it is a stylesheet rule with no other
// guard: the markup and the Go tests look identical either way.
func TestTheSettingsSwitchTakesClicksOnlyOnThePositionNotInForce(t *testing.T) {
	if !strings.Contains(cssRaw, ".set-seg-track{pointer-events:none}") {
		t.Error("the switch's track takes clicks, so clicking the position in force flips it")
	}
	// And the half that would move you gets its pointer back, or the switch
	// cannot be clicked at all.
	for _, sel := range []string{
		`.set-seg input:not(:checked)~.set-seg-track .set-seg-on`,
		`.set-seg input:checked~.set-seg-track .set-seg-off`,
	} {
		if !strings.Contains(cssRaw, sel+",") && !strings.Contains(cssRaw, sel+"{") {
			t.Errorf("%s never gets its pointer back, so that position is unreachable by mouse", sel)
		}
	}
}

// The thumb slides one cell; the reversed-out copy of the words it clips slides
// back by the same distance, so it stays registered over the originals and every
// frame of the travel is legible. The two transforms are a PAIR and are easy to
// change one at a time: the thumb moves 100% of itself (one cell), the copy is
// two cells wide and so moves 50% of itself. Get one wrong and the words double
// or smear for 180ms — which a still of the finished states never shows.
func TestTheSwitchThumbAndItsLabelCopyTravelTogether(t *testing.T) {
	for _, want := range []string{
		`.set-seg input:checked~.set-seg-track .set-seg-thumb{transform:translateX(100%)}`,
		`.set-seg input:checked~.set-seg-track .set-seg-mask{transform:translateX(-50%)}`,
	} {
		if !strings.Contains(cssRaw, want) {
			t.Errorf("missing %q; the thumb and its label copy no longer travel together", want)
		}
	}
	// The copy spans both cells, or it cannot cover the word being uncovered.
	if !regexp.MustCompile(`\.set-seg-mask\{[^}]*width:200%`).MatchString(cssRaw) {
		t.Error(".set-seg-mask is not two cells wide, so half the travel shows no reversed text")
	}
	// The thumb clips it, or the copy hangs outside the accent.
	if !regexp.MustCompile(`\.set-seg-thumb\{[^}]*overflow:hidden`).MatchString(cssRaw) {
		t.Error(".set-seg-thumb does not clip its label copy, so both words render twice")
	}
	// Asked for less motion, the switch arrives without the travel — but it still
	// has to arrive, so only the transitions are dropped.
	if !strings.Contains(cssRaw, ".set-seg-thumb,.set-seg-mask{transition:none}") {
		t.Error("the switch animates regardless of prefers-reduced-motion")
	}
}

// Every glyph painted with background-clip:text needs room under its last line,
// because that paint stops at the element's box. These are set large with a
// leading tighter than the font's own — 1.02 on the headline and on the hero
// figure — so the line box ends above the bottom of a g, a y or a comma, and the
// part hanging below it is never painted. On screen it reads as a glyph with its
// tail cut off, and it is invisible in a diff: the markup is right, the colour is
// right, and the letter is wrong.
//
// The room is padding, taken back out of the margin so the page's spacing does
// not move. Both halves are asserted: padding alone would push everything under
// the heading down by a fifth of a line.
func TestClippedTextLeavesRoomForDescenders(t *testing.T) {
	// Comments first. A comment mentioning the treatment reads as a selector to
	// anything matching on braces, and the first version of this test reported
	// three paragraphs of prose as unpadded headings.
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(cssRaw, "")
	rules := regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1)
	if len(rules) == 0 {
		t.Fatal("could not read any rules out of app.css")
	}

	padded := map[string]bool{}
	compensated := map[string]bool{}
	var clipped []string
	for _, r := range rules {
		body := r[2]
		for _, sel := range strings.Split(r[1], ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			if strings.Contains(body, "background-clip:text") {
				clipped = append(clipped, sel)
			}
			if strings.Contains(body, "padding-bottom:.18em") {
				padded[sel] = true
			}
			if strings.Contains(body, "margin-bottom:-.18em") ||
				strings.Contains(body, "- .18em)") {
				compensated[sel] = true
			}
		}
	}
	if len(clipped) == 0 {
		t.Fatal("no background-clip:text rule in app.css — this test names a treatment that is gone")
	}
	for _, sel := range clipped {
		if !padded[sel] {
			t.Errorf("%s is painted with background-clip:text but never given padding-bottom — "+
				"its descenders are cut off", sel)
		}
		if !compensated[sel] {
			t.Errorf("%s pads for its descenders without taking it back out of the margin — "+
				"everything below it moves down", sel)
		}
	}
}
