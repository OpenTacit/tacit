// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"fmt"
	"html"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
)

// The project page is the one document here written for somebody who has never
// seen the product, and the whole reason it can be trusted to look like the
// product is that it declares nothing of its own. A colour, a font stack or a
// pixel value in this file is a second design in the making.
func TestSitePageDeclaresNoStyleOfItsOwn(t *testing.T) {
	page := SiteHTML("")
	// The document's own <style> blocks are the shared ones (GroundCSS,
	// FrontDoorCSS), both tested elsewhere. Anything beyond them is new style
	// declared for this page alone.
	body := strings.NewReplacer(GroundCSS, "", FrontDoorCSS, "").Replace(page)
	if i := strings.Index(body, "<style"); i >= 0 {
		t.Errorf("the project page declares a stylesheet of its own:\n%.300s", body[i:])
	}
	if i := strings.Index(body, "style="); i >= 0 {
		t.Errorf("the project page carries an inline style:\n%.200s", body[i:])
	}
	// The mark's SVG is the one exception: it paints with currentColor and is
	// shared verbatim with every other surface.
	if hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindAllString(
		strings.ReplaceAll(body, MarkSVG, ""), -1); len(hex) > 0 {
		t.Errorf("the project page names colours directly (%v) — every colour resolves "+
			"from a token in app.css", hex)
	}
}

// The stylesheet is prose-heavy on purpose, which makes an unterminated — or a
// doubly terminated — comment a real hazard: CSS has no parse errors to report,
// so a stray "*/" turns the paragraph above it into declarations the browser
// drops, silently, along with the rules that follow. It has already happened
// once: an added paragraph left the old close in place and the track it
// documented stopped laying out, while every other test on this page passed
// because the selector text was all still there to grep for.
func TestStylesheetCommentsAreBalanced(t *testing.T) {
	depth, line := 0, 1
	for i := 0; i < len(cssRaw); i++ {
		switch {
		case cssRaw[i] == '\n':
			line++
		case depth == 0 && strings.HasPrefix(cssRaw[i:], "/*"):
			depth, i = depth+1, i+1
		case depth == 1 && strings.HasPrefix(cssRaw[i:], "*/"):
			depth, i = depth-1, i+1
		case depth == 0 && strings.HasPrefix(cssRaw[i:], "*/"):
			t.Fatalf("app.css line %d closes a comment that was never opened — "+
				"everything from the paragraph above it is being parsed as CSS", line)
		}
	}
	if depth != 0 {
		t.Error("app.css ends inside a comment — the rest of the file is commented out")
	}
}

// Every class the page uses has to exist in the stylesheet, because there is no
// build step that would notice otherwise and no dashboard page that would break
// first. A typo here renders as an unstyled paragraph on the product's front
// page.
func TestSitePageUsesOnlyClassesTheStylesheetDefines(t *testing.T) {
	for _, m := range regexp.MustCompile(`class="([^"]+)"`).FindAllStringSubmatch(SiteHTML(""), -1) {
		for _, cls := range strings.Fields(m[1]) {
			if cls == "mark" || cls == "booting" {
				continue // set by the shared chrome and its pre-paint script
			}
			if !strings.Contains(cssRaw, "."+cls) {
				t.Errorf("the project page uses .%s, which app.css does not define", cls)
			}
		}
	}
}

// One page, one action — in the state a visitor sees it. The page exists to
// produce a single decision, and the button that carries it appears once, at the
// foot of the column. A button to anywhere else is the change that quietly turns
// the page into a menu.
//
// The hero's own button is gone: it shows the install command instead, which is a
// thing to DO rather than a second thing to press. What must not come back is a
// second .btn — the moment there are two, a reader has to choose which way to
// start, which is the decision this page exists to make for them.
func TestSitePageOffersExactlyOneAction(t *testing.T) {
	page := SiteHTML("")
	if n := strings.Count(page, `class="btn`); n != 1 {
		t.Errorf("the project page has %d buttons, want exactly 1 — the one action, at the "+
			"foot of the column", n)
	}
	if !strings.Contains(page, SiteActionLabel) {
		t.Errorf("the button no longer carries SiteActionLabel (%q)", SiteActionLabel)
	}
	// It opens the guide window rather than going anywhere, so it is a <button>.
	// A link here would be a fourth destination and a page that navigates away
	// from its own argument.
	if !strings.Contains(page, `<button type="button" class="btn btn-primary site-cta" data-guide-open>`) {
		t.Error("the action is not a button wired to the guide window")
	}
	// Links are what a "single, obvious call to action" erodes into, so the
	// allowance is named rather than counted loosely: the close line's contact
	// address, the masthead's navigation (siteNav), and the repository the icon
	// links to. The action is no longer among them — it opens the guide window
	// and goes nowhere. The theme toggle and the copy controls are buttons, and
	// the stylesheet <link> is not an anchor.
	//
	// The count is derived from siteNav, so adding a destination to that list is
	// a deliberate act with the page's whole link budget written next to it —
	// and a link added to the template alone still fails here.
	want := 1 + len(siteNav) + 1
	if n := strings.Count(page, "<a "); n != want {
		t.Errorf("the project page carries %d links, want exactly %d (the contact "+
			"address, %d in the masthead and the repository icon)",
			n, want, len(siteNav))
	}
	if !strings.Contains(page, SiteContactHref) {
		t.Error("the close line no longer carries SiteContactHref")
	}
	for _, n := range siteNav {
		if !strings.Contains(page, `href="`+n.Href+`"`) {
			t.Errorf("the masthead does not carry %s → %s", n.Label, n.Href)
		}
	}
	if !strings.Contains(page, `href="`+SiteRepoHref+`"`) {
		t.Error("the masthead's icon no longer links to SiteRepoHref")
	}
}

// The hero's install command is the page's own first instruction, and the copy
// control has to hand over exactly what is on the screen. A copy button whose
// payload has drifted from the text beside it is the worst of both: the reader
// sees one command and runs another, and nothing on the page looks wrong.
func TestHeroInstallCommandIsCopiedVerbatim(t *testing.T) {
	page := SiteHTML("")
	cmd := html.EscapeString(InstallCommand)
	if !strings.Contains(page, `<code class="site-install-cmd">`+cmd+`</code>`) {
		t.Errorf("the hero does not show InstallCommand (%q)", InstallCommand)
	}
	// The copy control lives in the guide window now, and this is the assertion
	// that says so rather than one that happens to find it there. It used to read
	// `Contains(page, data-copy=cmd)` and went on passing after the hero's button
	// became an info glyph — the guide's own copy control matched it, so a test
	// named for the hero was checking a different window.
	hero := between(t, page, `<div class="site-install"`, `<p class="site-note"`)
	if strings.Contains(hero, "data-copy=") {
		t.Error("the hero window still carries a copy control; it is one click target now")
	}
	if !strings.Contains(hero, `class="site-install-info"`) {
		t.Error("the hero window has no info indicator where the copy control was")
	}
	if !strings.Contains(hero, InfoIcon) {
		t.Error("the info indicator is not InfoIcon")
	}
	guide := between(t, page, `<div class="site-guide-cmd">`, "</div>")
	if !strings.Contains(guide, `data-copy="`+cmd+`"`) {
		t.Error("the guide window does not offer the command to copy, so nothing on the page does")
	}
	// The control is wired by the shared script, which the console splices into
	// its own. Without it the button is a decoration that does nothing.
	if !strings.Contains(page, CopyButtonJS) {
		t.Error("the page carries a copy button and not the script that wires it")
	}
}

// The whole install window opens the guide, not only its title bar. The green
// light stays the one control a keyboard can reach; the window is the target a
// pointer gets.
func TestTheWholeInstallWindowOpensTheGuide(t *testing.T) {
	page := SiteHTML("")
	if !strings.Contains(page, `<div class="site-install" data-guide-surface>`) {
		t.Error("the install window is not a guide surface, so only its title bar opens the guide")
	}
	if strings.Contains(page, `<div class="site-term-bar" data-guide-surface>`) {
		t.Error("the title bar is still a surface of its own; the window around it is the surface now")
	}
	if !strings.Contains(page, `class="site-zoom" data-guide-open`) {
		t.Error("the green light is no longer the control, so there is no keyboard route into the guide")
	}
}

// Every place a reader meets this command has to show the same command. The
// constant covers the surfaces that can import it — the project page and the
// console's member panel — and everything below is prose in a repository that
// cannot, which is what makes this the join. Change the constant and this fails,
// which is the only warning any of these files gets.
func TestInstallCommandMatchesEveryPlaceThatQuotesIt(t *testing.T) {
	// A missing file FAILS rather than skips. It used to skip, and two of the five
	// entries then left for the internal archive — so this test reported SKIP, the
	// three files still here went unchecked, and the only warning those files get
	// was silently off. A guard that turns itself off when its subject moves is
	// worse than no guard: it still looks like coverage.
	for _, path := range []string{
		"../../README.md",
		"../../install.sh", // the script's own header, which names how to run it
		"../../docs/user-guide/10-get-started/00-start-on-your-own.md",
		"../../docs/user-guide/10-get-started/02-set-up-a-registry.md",
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s quotes the install command and is no longer in this tree. "+
				"If it moved on purpose, take it off this list in the same change: %v", path, err)
		}
		if !strings.Contains(string(b), InstallCommand) {
			t.Errorf("%s does not contain InstallCommand (%q) — this file and the product "+
				"are telling a first-time reader to run different things", path, InstallCommand)
		}
	}
	// The short address is a redirect, and the flag that follows it is the reason
	// the command works at all. Dropping -L from the constant turns every one of
	// those quotations into a command that fetches an empty 302 body and pipes it
	// to a shell.
	if !strings.Contains(InstallCommand, "-fsSL") {
		t.Errorf("InstallCommand is %q — it must keep -L, because InstallURL redirects "+
			"to InstallScriptURL", InstallCommand)
	}
}

// The guide window is a retelling of one chapter, not a copy of it, so its prose
// is free to be shorter than the guide's. Its COMMANDS are not: a stranger runs
// what this window shows, and a command here that the chapter does not contain is
// either a typo or an instruction the documentation has since moved on from.
func TestGuideWindowTeachesWhatTheGuideTeaches(t *testing.T) {
	const guide = "../../docs/user-guide/10-get-started/02-set-up-a-registry.md"
	b, err := os.ReadFile(guide)
	if err != nil {
		t.Fatalf("%s is what this window retells, and it is not in this tree. "+
			"Point the constant at wherever the chapter went: %v", guide, err)
	}
	page := SiteHTML("")
	for _, cmd := range siteGuideCommands {
		if !strings.Contains(string(b), cmd) {
			t.Errorf("the guide window teaches %q, which %s does not contain", cmd, guide)
		}
		if !strings.Contains(page, html.EscapeString(cmd)) {
			t.Errorf("%q is in siteGuideCommands but not on the page", cmd)
		}
		// Every command in the window is copyable, for the same reason the hero's
		// is: it is there to be run, not read.
		if !strings.Contains(page, `data-copy="`+html.EscapeString(cmd)+`"`) {
			t.Errorf("%q has no copy control", cmd)
		}
	}
}

// The window has to be dismissible three ways, and two of them are the browser's
// as long as it is a real <dialog> opened with showModal(): Escape, and the
// backdrop. The third is the chrome, and a light that does not close it is a
// control that lies. This checks the markup those behaviours rest on, which is
// the part a refactor breaks silently.
func TestGuideWindowCanBeDismissed(t *testing.T) {
	page := SiteHTML("")
	if !strings.Contains(page, `<dialog class="site-guide"`) {
		t.Error("the guide window is no longer a <dialog> — Escape and the backdrop " +
			"were the browser's job, and now they are nobody's")
	}
	if !strings.Contains(siteScript, "showModal") {
		t.Error("the window is opened without showModal(), so it is not modal: Escape " +
			"does not close it and the page behind it stays focusable")
	}
	// Three lights, three close buttons, and every one of them wired. Counted in
	// the markup with the script removed: the script names both hooks in its own
	// selectors, and counting those as controls would pass a page that has none.
	markup := strings.Replace(page, siteScript, "", 1)
	if n := strings.Count(markup, "data-guide-close"); n != 3 {
		t.Errorf("the guide window has %d chrome buttons wired to close, want 3", n)
	}
	// Two controls open it: the hero window's green light, and the page's one
	// action at the foot of the column. Both are buttons, both are in the tab
	// order, and both are meant to be there — a third would be the page growing a
	// second way to start.
	if n := strings.Count(markup, "data-guide-open"); n != 2 {
		t.Errorf("%d controls open the guide window, want exactly 2 (the green light "+
			"and the call to action)", n)
	}
	// The title bar is a click surface, not a second control: it opens the window
	// for a pointer that should not have to hunt for an 11px circle, and it stays
	// out of the tab order because the light is already in it. Marked with its own
	// attribute so the count above keeps meaning "controls", not "click targets".
	if n := strings.Count(markup, "data-guide-surface"); n != 1 {
		t.Errorf("%d click surfaces open the guide window, want exactly 1 (the title bar)", n)
	}
	if strings.Contains(markup, `data-guide-surface tabindex`) ||
		strings.Contains(markup, `<button class="site-term-bar`) {
		t.Error("the title bar has become a control of its own — the green light is the " +
			"control, and two focusable things that do one job is one too many")
	}
}

// The masthead goes to two places and no others: the project's blog, and the
// repository. Anywhere else is a destination this project does not publish, and
// a landing page that sends a stranger to a host nobody here controls is how the
// navigation stops being the project's own.
//
// This was once a check that EVERY destination was the repository, which is what
// it meant while the repository was the only thing the project published. The
// blog is the second, and it is named here rather than allowed by a looser rule,
// so the third one is a deliberate edit too.
func TestMastheadDestinationsAreOursAlone(t *testing.T) {
	for _, n := range append(siteNav, struct{ Label, Href string }{"repository", SiteRepoHref}) {
		if !strings.HasPrefix(n.Href, SiteRepoHref) && !strings.HasPrefix(n.Href, SiteBlogHref) {
			t.Errorf("%s points at %s, which is neither the repository nor the blog — "+
				"the masthead publishes this project and nothing else", n.Label, n.Href)
		}
	}
	// "Docs" is the user guide's front page and not the docs tree: /docs is an
	// index of design records and plans, and a stranger following that link lands
	// in the project's internal reading rather than the guide.
	if !strings.Contains(SiteDocsHref, "user-guide") {
		t.Errorf("Docs points at %s, which is not the user guide", SiteDocsHref)
	}
}

// The blog comes before the user guide and the releases. Those two are for
// somebody who has already decided to try this; the blog is for somebody still
// deciding, which is most of the people who ever reach this page. Reading order
// is the only thing that says so, so the order is held here rather than left to
// whoever edits the slice next.
func TestBlogLeadsTheMasthead(t *testing.T) {
	if len(siteNav) == 0 || siteNav[0].Label != "Blog" {
		t.Fatalf("the masthead's first link is %+v, want Blog", siteNav[0])
	}
	// And it reaches the page in that order: the slice is rendered in sequence,
	// and a template that reordered it would leave this test passing on a page
	// that reads the other way round.
	page := SiteHTML("")
	blog := strings.Index(page, `href="`+SiteBlogHref+`"`)
	guide := strings.Index(page, `href="`+SiteDocsHref+`"`)
	releases := strings.Index(page, `href="`+SiteReleasesHref+`"`)
	if blog < 0 || guide < 0 || releases < 0 {
		t.Fatalf("a masthead link is missing from the page: blog=%d guide=%d releases=%d",
			blog, guide, releases)
	}
	if !(blog < guide && guide < releases) {
		t.Errorf("the masthead reads blog=%d guide=%d releases=%d, want the blog first",
			blog, guide, releases)
	}
}

// The sign-out hotkey is operator chrome and must never reach a visitor: it
// names a route that is none of their business, and a page the edge caches for
// everybody cannot carry anything of one person's session.
func TestPublishedPageCarriesNoOperatorChrome(t *testing.T) {
	published := SiteHTML("")
	// The word "keydown" used to stand in for the hotkey here. It stopped being a
	// proxy the day the page grew a keyboard control of its own — the arrow keys
	// that drive the dashboard carousel — so the check is the hotkey itself and
	// the route it names, which is what was ever actually at stake. A bare
	// listener is no longer evidence of anything.
	for _, leak := range []string{"/auth/logout", SiteSignOutHotkey("/auth/logout")} {
		if strings.Contains(published, leak) {
			t.Errorf("the published page carries %q — that is the operator's chrome, "+
				"not a visitor's", leak)
		}
	}

	// The operator's copy is the visitor's page plus the key, and NOTHING else:
	// the whole point of trading the old preview bar for a keystroke is that
	// what an operator reviews is the published composition, byte for byte.
	hotkey := SiteSignOutHotkey("/auth/logout")
	preview := SiteHTML(hotkey)
	if !strings.Contains(preview, hotkey) {
		t.Fatal("the operator's copy carries no sign-out key — there is no way out of the session")
	}
	if strings.Replace(preview, hotkey, "", 1) != published {
		t.Error("the operator's copy differs from a visitor's beyond the sign-out key — " +
			"the preview is no longer reviewing the page that publishes")
	}

	// The href lands inside a script string, so it is escaped for that context —
	// a quote must not break out of the literal and become code.
	if quoted := SiteSignOutHotkey("/x'y"); !strings.Contains(quoted, `/x\'y`) {
		t.Errorf("a quote in the sign-out href is not JS-escaped: %s", quoted)
	}
}

func TestProjectPageHasNoAnimatedBackdrop(t *testing.T) {
	page := SiteHTML("")
	for _, unwanted := range []string{"<canvas", "data-backdrop", BackdropURL(), "WebGLRenderingContext"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("the project page still carries animated-backdrop code %q", unwanted)
		}
	}
}

// The headline appears twice — on the page and in the tab — and they have already
// drifted once, leaving a title that was the brand and a dash. Neither place is
// allowed its own copy.
func TestHeadlineIsTheSameOnThePageAndInTheTab(t *testing.T) {
	page := SiteHTML("")
	if strings.Contains(page, "&amp;nbsp;") || strings.Contains(page, "&nbsp;") {
		t.Error("the headline renders a non-breaking-space entity as text")
	}
	if h1 := between(t, page, `<h1 class="site-title">`, "</h1>"); h1 != html.EscapeString(SiteHeadline) {
		t.Errorf("the <h1> is %q, not SiteHeadline", h1)
	}
	title := between(t, page, "<title>", "</title>")
	if !strings.HasSuffix(title, html.EscapeString(SiteHeadline)) {
		t.Errorf("the <title> is %q, which does not end in the headline — a tab, a bookmark "+
			"and a shared link all show this and none of them show the page", title)
	}
	if strings.TrimSpace(strings.TrimSuffix(title, html.EscapeString(SiteHeadline))) == "" {
		t.Error("the <title> is the headline alone; it should carry the brand too")
	}
}

// The headline wears no colour, and neither does anything else in the chrome.
//
// It used to carry two marker strokes, painted by a script that measured the
// phrases and laid <i> elements behind them. That was the loudest thing on the
// first screen, and the house rules ration the spectrum to the data. Removing the
// attributes alone would have left the script, the stylesheet rules and the ~2KB
// it costs every reader in place, waiting to be reattached by somebody who did
// not know why they went; this is the assertion that says all of it left.
func TestTheHeadlineWearsNoMarkerStroke(t *testing.T) {
	page := SiteHTML("")
	for _, gone := range []string{
		"data-highlight-orange", "data-highlight-green",
		"site-highlight-host", "site-highlight-stroke",
	} {
		if strings.Contains(page, gone) {
			t.Errorf("the page still carries %q — the headline's marker strokes are back", gone)
		}
		if strings.Contains(cssRaw, "."+gone) {
			t.Errorf("app.css still defines .%s, which nothing uses", gone)
		}
	}
	if strings.Contains(page, "createTreeWalker") {
		t.Error("the highlight script is still on the page")
	}
}

// The guide window is an animation away from the hero's little one — it grows out
// of it — so the two title bars are one title. Give them separate copy and the
// FLIP has a word change in the middle of it, which reads as the illusion breaking
// rather than as a window opening.
func TestBothTerminalsWearTheSameTitle(t *testing.T) {
	page := SiteHTML("")
	const bar = `<span class="site-install-title" aria-hidden="true">`
	var titles []string
	for _, after := range strings.Split(page, bar)[1:] {
		j := strings.Index(after, "</span>")
		if j < 0 {
			t.Fatal("a title bar's <span> is never closed")
		}
		titles = append(titles, after[:j])
	}
	if len(titles) != 2 {
		t.Fatalf("the page has %d title bars, not the hero's and the guide's — a third "+
			"window is a decision, not a detail", len(titles))
	}
	for _, got := range titles {
		if got != html.EscapeString(SiteInstallTitle) {
			t.Errorf("a title bar reads %q, not SiteInstallTitle %q", got, SiteInstallTitle)
		}
	}
}

// The member-flow section is one story told twice from one slice: the
// step list the stage shows on a wide screen, and the captions the stacked
// fallback shows everywhere else. If either telling loses a moment, or a frame
// loses its description, the section is quietly lying to one audience.
func TestSiteFlowTellsOneStoryTwice(t *testing.T) {
	page := SiteHTML("")
	for _, moment := range siteFlowMoments {
		title := html.EscapeString(moment)
		if strings.Count(page, title) != 1 {
			t.Errorf("moment %q appears %d times, want 1 — the list names it, and the "+
				"window shows it", moment, strings.Count(page, title))
		}
	}
	for _, sc := range siteFlowScenarios {
		for i, st := range sc.Steps {
			if !strings.Contains(page, termMomentHTML(st.Frame, st.Alt)) {
				t.Errorf("the page does not carry %s moment %d's frame", sc.Key, i+1)
			}
			// role="img" plus the description is the frame's whole accessible name:
			// without it a screen reader reads two dozen lines of box drawing, or
			// nothing at all.
			if !strings.Contains(page, `aria-label="`+html.EscapeString(siteProductText(st.Alt))+`"`) {
				t.Errorf("%s moment %d's frame carries no description — the story goes "+
					"silent for a reader who cannot see it", sc.Key, i+1)
			}
			if n := strings.Count(page, siteProduct(html.EscapeString(st.Body))); n != 1 {
				t.Errorf("%s moment %d's body appears %d times, want 1", sc.Key, i+1, n)
			}
		}
	}
	// The stage is opt-in by script: the section's markup must not presume it,
	// or the no-JavaScript reading starts hidden. (The script that ADDS the
	// class is elsewhere on the page, which is exactly the point.)
	section := between(t, page, `<section class="site-flow"`, "</section>")
	if strings.Contains(section, "flow-live") || strings.Contains(section, "is-active") {
		t.Error("the flow markup pre-sets a stage class — without JavaScript that " +
			"state never changes and the fallback breaks")
	}
}

// No invented numbers is a house rule, and a landing page is where it is most
// tempting to break: this is the one place a figure could be printed with nobody
// to check it.
//
// Since the ambient-suggestion block came out, the session frames are the ONLY
// rates on the page, and they are the demonstration org's as the registry
// reports them (siteframes.go). So the rule here is now the strong form: strike
// the frames, and the page states no rate anywhere in its own voice — not in a
// caption, not in a panel, not in the close.
func TestSiteStatesNoRateInItsOwnVoice(t *testing.T) {
	page := SiteHTML("")
	// The flow section sits outside <main>, so it is scanned separately; its
	// caption has to name the staging rather than quote the figures.
	flow := between(t, page, `<section class="site-flow"`, "</section>")
	for _, sc := range siteFlowScenarios {
		for _, st := range sc.Steps {
			flow = strings.Replace(flow, termMomentHTML(st.Frame, st.Alt), "", 1)
		}
	}
	// So does the hero, and it now holds the offer too — as a form rather than as
	// a frame. It was the one region of this page nothing scanned, on the
	// reasonable grounds that it held no figures; the moment a percentage moved
	// into the first screen that stopped being true, and a rate in a hero is the
	// worst place on the page for one.
	//
	// What is exempt is named rather than cut out by markup: the three strings the
	// suggestion is made of. Anything else in that header quoting a rate is the
	// page speaking, which is what this forbids.
	hero := between(t, page, `<header class="site-stage">`, "</header>")
	for _, quoted := range []string{offerName, offerEvidence, offerFit} {
		hero = strings.Replace(hero, html.EscapeString(quoted), "", 1)
	}
	rest := visibleProse(t, page) + flow + hero
	if pct := regexp.MustCompile(`\b\d+(\.\d+)?%`).FindAllString(rest, -1); len(pct) > 0 {
		t.Errorf("the page states rates of its own (%v) — nothing here is measured", pct)
	}
	// The flow section names its own staging. The hero does NOT, and that is a
	// decision rather than an oversight: its caption was taken out deliberately,
	// so the first screen shows a measured evidence line inside a depiction of a
	// client with nothing next to it saying whose figures they are. The page's
	// only such statement is the one below, and a reader who leaves from the
	// first screen never reaches it. hack/sitestills/README.md still records the
	// older rule — "the page caption identifies them as staged" — so if this ever
	// reads as the page's own claim, a caption here is the fix.
	if !strings.Contains(flow, "example") && !strings.Contains(flow, "staged") {
		t.Error("the flow caption no longer says its session is an example — the " +
			"frames' figures would read as measured results")
	}
}

// The hero shows the dashboard's Outcomes view, in both schemes, and it is the
// carousel's own file rather than a second copy of it.
//
// One picture twice is the arrangement: the claim at the top of the page, and the
// same screen with its explanation beside it further down. Two files would be two
// things to regenerate, and the one nobody regenerates is the one that shows a
// stranger a dashboard the product no longer draws.
func TestTheHeroBorrowsTheCarouselsOutcomesShot(t *testing.T) {
	shot, dark, alt, w, h := siteHeroShot()
	if shot == "" || dark == "" || alt == "" || w == 0 || h == 0 {
		t.Fatalf("siteHeroViewKey %q resolves to nothing in siteSeeViews — the hero "+
			"has no picture", siteHeroViewKey)
	}
	page := SiteHTML("")
	region := between(t, page, `<div class="site-hero-shot">`, "</header>")
	for _, want := range []string{
		`<img class="theme-light" src="` + SiteShotURL(shot) + `"`,
		`<img class="theme-dark" src="` + SiteShotURL(dark) + `"`,
	} {
		if !strings.Contains(region, want) {
			t.Errorf("the hero does not carry %s", want)
		}
	}
	// Both dimensions on both images, so the first screen reserves the space
	// instead of reflowing the headline when the picture arrives.
	size := `width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `"`
	if n := strings.Count(region, size); n != 2 {
		t.Errorf("%d of the hero's two images carry %s", n, size)
	}
	// Not lazy. Everything in the carousel below is; this one is above the fold,
	// and a lazy image there is a hero that arrives after the reader has read it.
	if strings.Contains(region, `loading="lazy"`) {
		t.Error("the hero's image is lazy-loaded")
	}
	// Its description is the slide's, because it is the same picture.
	if !strings.Contains(region, siteProduct(html.EscapeString(alt))) {
		t.Error("the hero's images do not carry the view's own alt text")
	}
	// And the carousel still shows it, which is the half of "one picture twice"
	// that this test would otherwise let go missing.
	slide := between(t, page, `<li class="site-see-slide" data-see="`+siteHeroViewKey+`"`, "</li>")
	if !strings.Contains(slide, SiteShotURL(shot)) {
		t.Errorf("the %s slide no longer shows %s", siteHeroViewKey, shot)
	}
}

// The hero is the dashboard and the session below it is a terminal.
//
// Both claims are load-bearing. A terminal in the hero says this is a CLI tool,
// which is the thing the prose beside it spends two sentences denying; the
// dashboard in the flow section would lose the one rendering that shows a
// suggestion arriving inside somebody's work.
func TestTheHeroShowsTheDashboardAndTheSessionTheTerminal(t *testing.T) {
	page := SiteHTML("")
	region := between(t, page, `<div class="site-hero-shot">`, "</header>")
	if strings.Contains(region, `class="site-term`) {
		t.Error("the hero is drawing a terminal again; the first screen shows the dashboard")
	}
	flow := between(t, page, `<section class="site-flow"`, "</section>")
	if !strings.Contains(flow, `class="site-term-win"`) {
		t.Error("the flow section is no longer a terminal")
	}
}

// The frames are text now, which is what makes them editable — and an editable
// frame is one somebody can make too wide or too tall by typing. Both failures
// are invisible in a diff and obvious on the front page: a long line wraps where
// the session did not wrap it, and a tall frame either overflows the stage or
// changes its shape mid-crossfade.
func TestSiteFramesFitTheTerminal(t *testing.T) {
	for _, sc := range siteFlowScenarios {
		for m, st := range sc.Steps {
			where := fmt.Sprintf("%s moment %d", sc.Key, m+1)
			lines := strings.Split(termPlain(st.Frame), "\n")
			if len(lines) > termRows {
				t.Errorf("%s is %d lines, and the stage is cut for %d",
					where, len(lines), termRows)
			}
			for i, line := range lines {
				if n := len([]rune(line)); n > termCols {
					t.Errorf("%s line %d is %d columns, and the terminal was %d:\n%s",
						where, i+1, n, termCols, line)
				}
				if strings.ContainsAny(line, "\t") {
					t.Errorf("%s line %d contains a tab — a frame is placed by spaces, "+
						"or it is placed by whatever the browser feels", where, i+1)
				}
			}
			// A moment renders at its own length now — the tape is one session,
			// not three padded screens. What still has to hold is that it fits
			// the glass: the stage scrolls the tape to a moment's first line and
			// stops, so anything past termRows could never be read.
			if n := strings.Count(termMomentHTML(st.Frame, st.Alt), "\n") + 1; n > termRows {
				t.Errorf("%s renders %d rows, and the window is %d — the tail would "+
					"never come into view", where, n, termRows)
			}
		}
	}
}

// The band behind a member's own prompt is a rectangle, because that is what a
// terminal paints: every line of the prompt carries it, and they all end at the
// same column. Marking only the first line of a wrapped prompt — the obvious way
// to write it — renders a highlight that stops mid-sentence with the rest of the
// sentence hanging outside it, which is what this test exists to catch.
func TestPromptBandIsARectangle(t *testing.T) {
	for _, sc := range siteFlowScenarios {
		for m, st := range sc.Steps {
			where := fmt.Sprintf("%s moment %d", sc.Key, m+1)
			raw := strings.Split(st.Frame, "\n")
			width := -1
			for i, line := range raw {
				if !strings.Contains(line, "{u ") {
					continue
				}
				// Every banded line ends where the first one did.
				if n := len([]rune(termPlain(line))); width < 0 {
					width = n
				} else if n != width {
					t.Errorf("%s line %d ends at column %d and the band above it ends at %d "+
						"— pad it with spaces inside the braces so the band is a rectangle",
						where, i+1, n, width)
				}
				// And the prompt does not continue past the band. What a wrapped
				// prompt leaves behind is a line of bare prose under the band —
				// bare being the test: a line the harness painted carries an ink
				// marker, and termPlain leaves an unmarked line alone.
				if i+1 < len(raw) {
					if next := raw[i+1]; strings.TrimSpace(next) != "" && termPlain(next) == next {
						t.Errorf("%s line %d follows the prompt band but is outside it:\n%s\n"+
							"a wrapped prompt carries the band on every line", where, i+2, next)
					}
				}
			}
		}
	}
}

// The frame markup exists so the transcripts can be edited as text; what it must
// never do is let that text out as markup. The frames carry real shell
// redirections, and one unescaped ampersand or angle bracket on the front page
// is a broken document.
func TestSiteFramesEscapeTheirText(t *testing.T) {
	rendered := termMomentHTML(`{d a & b}<i>c</i> {b d}`, `x & y`)
	for _, want := range []string{
		`<span class="term-dim">a &amp; b</span>`,
		`&lt;i&gt;c&lt;/i&gt;`,
		`<span class="term-lit">d</span>`,
		`aria-label="x &amp; y"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the frame renderer did not produce %q:\n%s", want, rendered)
		}
	}
	// A brace that opens nothing is text, so prose never has to be escaped.
	if plain := termPlain(`{not ink} {d ink}`); plain != `{not ink} ink` {
		t.Errorf("stripping markup gave %q, want %q", plain, `{not ink} ink`)
	}
	// Every ink the frames use has to resolve to a class the stylesheet paints,
	// or a span renders as unstyled default text and nobody notices.
	for _, cls := range termInk {
		if !strings.Contains(cssRaw, "."+cls+"{") {
			t.Errorf("the frames paint with .%s, which app.css does not define", cls)
		}
	}
}

// visibleProse is the page's <main>: what a reader actually reads, with none of
// the head's percent-encoded icon or the shared chrome's inline CSS, both of
// which are full of digits and per-cent signs that mean nothing here.
func visibleProse(t *testing.T, page string) string {
	t.Helper()
	return between(t, page, "<main", "</main>")
}

func between(t *testing.T, s, open, end string) string {
	t.Helper()
	i := strings.Index(s, open)
	if i < 0 {
		t.Fatalf("the page no longer contains %q, so this test is reading the wrong region", open)
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("no %q after %q", end, open)
	}
	return rest[:j]
}

// No indefinite article may touch the product name, anywhere on this page.
//
// The name is a setting now, and "a" or "an" is chosen by the sound of the word
// after it. Copy written for one name reads as a typo under another — "a Tacit
// user" became "an OpenTacit user" the moment the setting moved, in a heading set
// in 48px. There is no article rule worth encoding either: it follows sound, not
// spelling, so "an hour" and "a university" both break the obvious one.
//
// So the rule is the simple one — write around it. "What NAME users see", not
// "What a NAME user sees". This renders the whole page under a vowel and a
// consonant and holds it to that.
func TestSiteWritesNoIndefiniteArticleBeforeTheName(t *testing.T) {
	for _, name := range []string{"Aardvark", "Tacit"} {
		t.Setenv(product.EnvKey, name)
		page := SiteHTML("")
		for _, article := range []string{"a", "an", "A", "An"} {
			if bad := " " + article + " " + name; strings.Contains(page, bad) {
				t.Errorf("the page writes %q — rewrite the line so no article precedes "+
					"the name, which is a setting and cannot be relied on to start with "+
					"a consonant", bad)
			}
		}
	}
}

// The install address follows the host the page was served from. An ingress
// answering for several zones serves /install.sh at each apex, so the address a
// reader is handed should be the one they are already looking at.
func TestInstallAddressFollowsTheServingHost(t *testing.T) {
	if got := InstallCommandFor(""); got != InstallCommand {
		t.Errorf("no host should give the canonical command, got %q", got)
	}
	if got, want := InstallURLFor("example.test"), "https://example.test/install.sh"; got != want {
		t.Errorf("InstallURLFor = %q, want %q", got, want)
	}

	page := SiteHTMLFor("example.test", "")
	if want := html.EscapeString(InstallCommandFor("example.test")); !strings.Contains(page, want) {
		t.Errorf("the page served from example.test does not quote %q", want)
	}
	// Not once, anywhere: the hero, the copy button and the guide all quote it,
	// and one of them left on the canonical address is the bug this catches.
	if strings.Contains(page, InstallURL) {
		t.Errorf("the page served from example.test still quotes %q", InstallURL)
	}
	// The default rendering is unchanged, which is what the static bundle
	// publishes and what every existing test reads.
	if SiteHTML("") != SiteHTMLFor("", "") {
		t.Error("SiteHTML disagrees with SiteHTMLFor on the canonical rendering")
	}
}

// The contact address follows the host too, for the same reason the install
// address does: a reader who arrived at one of the project's domains is given a
// way to reach it at that domain, not at another one they have no reason to
// trust yet. The mailbox has to exist at every domain the page is served from —
// see SiteContactHrefFor.
func TestContactAddressFollowsTheServingHost(t *testing.T) {
	if got := SiteContactHrefFor(""); got != SiteContactHref {
		t.Errorf("no host should give the canonical address, got %q", got)
	}
	if got, want := SiteContactHrefFor("example.test"), "mailto:team@example.test"; got != want {
		t.Errorf("SiteContactHrefFor = %q, want %q", got, want)
	}
	if page := SiteHTMLFor("example.test", ""); !strings.Contains(page, "mailto:team@example.test") {
		t.Error("the page served from example.test does not offer that address")
	}
}

// The whole reason this exists: a page served from one of the project's domains
// must not quote another of them anywhere a reader could act on it. Both the
// product's own domain and the tenant zone count as "another" here — the split
// between them (Config.SiteHost) is exactly what makes it possible to name the
// wrong one.
func TestPageServedFromAHostNamesNoOtherDomain(t *testing.T) {
	page := SiteHTMLFor("example.test", "")
	for _, foreign := range []string{"opentacit.com", "tacit.zone"} {
		if !strings.Contains(page, foreign) {
			continue
		}
		for _, line := range strings.Split(page, ">") {
			if !strings.Contains(line, foreign) {
				continue
			}
			// The blog is the one exception, and it is an exception because of
			// what the rule is for. The install command and the contact address
			// follow the serving host because a reader ACTS on them there, and
			// being sent to another of the project's domains to do it is the
			// fault. The blog is a publication with one address, the same way the
			// repository is: a tenant's page linking to it is right, and
			// rewriting it per host would name a site that does not exist.
			bare := strings.ReplaceAll(line, SiteBlogHref, "")
			if strings.Contains(line, SiteBlogHref) && !strings.Contains(bare, foreign) {
				continue
			}
			t.Errorf("page served from example.test still names %s: %q", foreign, line)
		}
	}
}
