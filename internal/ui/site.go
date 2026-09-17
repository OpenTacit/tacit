// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"html"
	"strconv"
	"strings"

	"github.com/opentacit/tacit/internal/product"
)

// The project page: what the product's own domain answers with, and the only surface in this
// codebase addressed to somebody who has never heard of Tacit.
//
// It has one job, and everything about it follows from that job being small: a
// stranger arrives, and in one screen learns what the thing does, why it would
// matter to them, and the one thing to do next. It is NOT documentation. Nothing
// here explains techniques, cohorts, retrieval or federation — the user guide does
// that, at length, to somebody who has already decided to look.
//
// The argument in the three panels is the announcement post's, compressed:
// everyone has the same models, the difference is what your own people worked
// out, and none of it is written down (docs/distribution/blog-introducing-tacit.md).
// The two examples in the middle panel are that post's own. Keeping one source
// for the pitch is the same discipline as keeping one stylesheet — and the house
// rules allow one "not X, it's Y" per piece, which this page spends on the third
// panel's title and nowhere else.
//
// The copy follows ASD-STE100 Simplified Technical English: short active
// sentences, one instruction per sentence, no figures of speech, no banned
// modals (may/should/could). The lede states the same argument the coach
// metaphor once carried, as plain fact.
//
// The word is TECHNIQUE, which is the announcement post's word too. Prose here
// must not drift into "move" or "card": nobody outside this codebase describes
// what they do with an AI agent that way. And the page explains no term of art
// at all, because it explains nothing — keep that constraint.
//
// Why it is a standalone document and not a shell page: there is no account, no
// counts, and nothing here belongs to a session. The shell exists to put a
// working surface's furniture around a view, and this page has none of that
// furniture. It shares the thing that matters — app.css, the mark, the ground,
// the theme toggle — so it cannot drift from the product it is describing.
//
// The masthead does carry a short navigation, and with the close line's contact
// address it is the whole of where this page points: three links and a repository
// icon, all of them leaving for the repository. They are reference material for
// somebody who wants to read before they ask, which is a different person from the
// one the rest of the page is written for. The one call to action goes nowhere at
// all — it opens the guide window — and a test holds the whole link budget
// (site_test.go).
//
// What it deliberately leaves out is the sign-in page's animated field. That
// page is a centred technique with a whole viewport of empty board behind it, which
// is what the field is for; this one is a scrolling column of plates, where a
// WebGL canvas would sit behind text and read as decoration. The house style is
// flat plates on the ground, and a landing page is not the place to make an
// exception to it.
//
// Every class below is either one app.css already defines for the dashboard
// (.wrap, .btn, .hint) or one of the .site-* rules added beside the sign-in
// block for this page. No colour, weight or spacing is declared here.

// SiteContactHrefFor is the close line's address as offered on a particular
// host. Empty host is the canonical address, which is what the static bundle
// publishes and what TRADEMARK.md names.
//
// The mailbox has to exist at every domain this is served from. That is a
// deployment fact this package cannot check, and the failure is silent — mail
// into a domain with no such recipient bounces to the sender, not to anybody who
// could fix it.
func SiteContactHrefFor(host string) string {
	if host == "" {
		return SiteContactHref
	}
	return "mailto:" + SiteContactMailbox + "@" + host
}

func siteNavHTML() string {
	var b strings.Builder
	for _, n := range siteNav {
		b.WriteString(`<a href="` + n.Href + `">` + html.EscapeString(n.Label) + `</a>`)
	}
	return b.String()
}

// SiteHTML renders the project page as a complete document. operator is chrome
// for a signed-in operator — SiteSignOutHotkey, or "" for the page as a visitor
// gets it. A published page carries nothing of the kind, which is what the
// empty string is for and what a test holds it to.
func SiteHTML(operator string) string { return SiteHTMLFor("", operator) }

// SiteHTMLFor is the same page as served from a particular host. host is the
// apex the request arrived on, and it decides only the install address the page
// quotes (InstallCommandFor); everything else about the document is the same
// wherever it is served, which is what keeps one apex from becoming a different
// page rather than the same page at a second address.
//
// Empty host is the canonical rendering, and it is what the static bundle
// publishes — that host serves one zone and has no request to read.
func SiteHTMLFor(host, operator string) string {
	install := InstallCommandFor(host)
	page := strings.NewReplacer(
		"«CSSVER»", CSSHash(),
		"«MARK»", MarkSVG,
		"«HEADLINE»", html.EscapeString(SiteHeadline),
		"«HEADLINEWHAT»", html.EscapeString(siteHeadlineWhat),
		"«HEADLINEHOW»", html.EscapeString(siteHeadlineHow),
		"«ACTIONLABEL»", SiteActionLabel,
		"«CONTACTHREF»", SiteContactHrefFor(host),
		"«NAV»", siteNavHTML(),
		"«INSTALLCMD»", html.EscapeString(install),
		"«INSTALLTITLE»", html.EscapeString(SiteInstallTitle),
		"«GUIDETITLE»", html.EscapeString(SiteGuideTitle),
		"«GUIDE»", siteGuideHTML(install),
		"«REPOHREF»", SiteRepoHref,
		"«GITHUBMARK»", githubMarkSVG,
		"«HARNESSES»", siteHarnesses,
		"«FLOW»", siteFlowHTML(),
		"«PRIV»", sitePrivHTML(),
		"«SEE»", siteSeeHTML(),
		"«OPERATOR»", operator,
	).Replace(siteTemplate)
	return siteProduct(page)
}

// siteProduct resolves «PRODUCT» in rendered markup.
//
// It is a second pass over the whole page, and it has to be: a Replacer walks
// its input once and does not re-scan what it substituted, so a «PRODUCT» inside
// the flow, the privacy panel or the guide — all of them substituted above —
// would survive the first pass untouched.
//
// Because it runs last, the name it inserts is escaped for HTML and nothing
// escapes it again. That is also why the page's own prose keeps the placeholder
// through its usual escaping: html.EscapeString leaves guillemets alone. The
// page's scripts name no product, so HTML is the only context to escape for.
func siteProduct(rendered string) string {
	return strings.ReplaceAll(rendered, "«PRODUCT»", html.EscapeString(product.Name()))
}

// siteProductText resolves «PRODUCT» in authored text that has not been marked
// up yet, leaving the escaping to whoever does the marking up.
//
// The session frames need this rather than the pass above. They give every
// non-ASCII character a cell of its own — one <i> per rune, so a fallback font's
// wider box-drawing glyph cannot drift a table rule off its column — and the
// placeholder's own guillemets are non-ASCII, so by the time the page is built
// «PRODUCT» has become <i>«</i>PRODUCT<i>»</i> and no substitution will find it.
func siteProductText(authored string) string {
	return strings.ReplaceAll(authored, "«PRODUCT»", product.Name())
}

// siteTermBar is the window's chrome: three lights, and the scenario picker
// where a terminal puts its title. The picker is hidden until the script
// reveals it (.flow-pick) — without JavaScript it does nothing, and a dead
// control in the chrome is worse than no control.
//
// There is exactly one of these on the page now, because there is one window.
// When the stage cross-faded three of them the two transparent ones still took
// clicks and still sat in the tab order, and the picker you could see was not
// the picker you were operating.
func siteTermBar() string {
	var opts strings.Builder
	for _, sc := range siteFlowScenarios {
		opts.WriteString(`<option value="` + sc.Key + `">` + html.EscapeString(sc.Label) + `</option>`)
	}
	return `<div class="site-term-bar"><i></i><i></i><i></i>` +
		`<label class="site-term-pick">Scenario <select>` +
		opts.String() + `</select></label></div>`
}

// siteFlowHTML renders the section's two halves: the moments on the left, and
// one terminal window on the right holding one tape per scenario.
//
// A tape is the whole session — all three moments, in order, as they came out
// of the terminal. The stage scrolls it to the moment the reader is on rather
// than swapping a screen for another screen, because that is what the session
// did: moment two happened where moment one stopped. Swapping made three
// unrelated photographs of three terminals.
//
// Everything scenario-specific carries data-scen and is hidden but for the
// first, so the page is correct before any script runs and the picker only has
// to toggle an attribute.
func siteFlowHTML() string {
	var list, tapes strings.Builder
	for i, moment := range siteFlowMoments {
		pressed := "false"
		if i == 0 {
			pressed = "true"
		}
		list.WriteString(`<li><button type="button" class="site-flow-step" aria-pressed="` +
			pressed + `"><i></i><span>` + html.EscapeString(moment) + `</span></button>`)
		for n, sc := range siteFlowScenarios {
			list.WriteString(`<p class="site-flow-sub" data-scen="` + sc.Key + `"` +
				siteHiddenAttr(n) + `>` + html.EscapeString(sc.Steps[i].Body) + `</p>`)
		}
		list.WriteString(`</li>`)
	}
	for n, sc := range siteFlowScenarios {
		tapes.WriteString(`<div class="site-term-tape" data-scen="` + sc.Key + `"` +
			siteHiddenAttr(n) + `>`)
		for _, st := range sc.Steps {
			tapes.WriteString(termMomentHTML(st.Frame, st.Alt))
		}
		tapes.WriteString(`</div>`)
	}
	// The rail sits inside the window and outside the glass, so the tape scrolling
	// behind it never moves it. aria-hidden and no role: this reports where the
	// session has got to, and it is not a control — the page scroll is the control
	// — so there is nothing here for a screen reader to operate or announce. It is
	// empty and invisible until the stage measures it, and on a page with no stage
	// it is never measured and never seen.
	return `<div class="site-flow-grid"><div class="site-flow-left">` + siteFlowHead +
		`<ol class="site-flow-steps">` + list.String() +
		`</ol></div><div class="site-flow-shots"><div class="site-term-win">` +
		siteTermBar() + `<div class="site-term-view">` + tapes.String() +
		`</div><div class="site-term-rail" aria-hidden="true"><i></i></div>` +
		`</div></div></div>`
}

// siteHiddenAttr hides every scenario but the first. The published page is the
// first scenario's, whole and correct, with the others inert beside it.
func siteHiddenAttr(i int) string {
	if i == 0 {
		return ""
	}
	return " hidden"
}

func sitePrivHTML() string {
	var cards strings.Builder
	for _, c := range sitePrivCards {
		cards.WriteString(`<li><h3>` + html.EscapeString(c.Title) + `</h3><p>` +
			html.EscapeString(c.Body) + `</p></li>`)
	}
	return `<div class="site-priv-grid"><div class="site-priv-left">` +
		`<div class="site-priv-head"><h2>Privacy and control</h2>` +
		`<p class="hint">Your organization controls its playbook and data.</p></div>
 <p class="site-priv-sub">You operate the full system yourself. You can read
 each line of its code. You decide which data leaves your site, if any. «PRODUCT»
 works inside the agents and tools that your teams already use.</p>` +
		`</div><ol class="site-priv-cards">` + cards.String() + `</ol></div>`
}

// siteSeeHTML renders the section: one description column, and a track of the
// dashboard's three screens that the reader swipes between.
//
// Everything past the first view starts hidden, and the script reveals it — the
// scenario picker's rule, for the scenario picker's reason. Swiping a track is a
// control like any other, and here it is one the page cannot offer honestly
// without script: the dots that say a second screen exists need somewhere to
// report the position back to, and a description column that stayed on Outcomes
// while the reader swiped to Review would be the section quietly lying. Without
// JavaScript this is the page it has always been — one screenshot, one paragraph
// — rather than a carousel with the machinery missing.
func siteSeeHTML() string {
	var slides, dots, column strings.Builder
	for i, v := range siteSeeViews {
		hidden := ""
		if i > 0 {
			hidden = " hidden"
		}
		img := func(theme, name string) string {
			return `<img class="theme-` + theme + `" src="` + SiteShotURL(name) +
				`" alt="` + html.EscapeString(v.Alt) +
				`" width="` + strconv.Itoa(v.W) + `" height="` + strconv.Itoa(v.H) +
				`" loading="lazy">`
		}
		slides.WriteString(`<li class="site-see-slide" data-see="` + v.Key + `"` + hidden + `>` +
			img("light", v.Shot) + img("dark", v.Dark) + `</li>`)
		// The dot's name is the destination's own word, so the control announces
		// where it goes rather than "slide 2 of 3", which names the carousel and
		// not the dashboard.
		dots.WriteString(`<button type="button" class="site-see-dot" data-see-to="` + v.Key +
			`" aria-label="` + html.EscapeString(v.Label) + `"></button>`)
		// One block per view, carrying all three of its lines. The script swaps
		// whole blocks and never individual sentences, so a view cannot end up
		// showing its own title over somebody else's paragraph — and data-see-text
		// rather than data-see, because the slides wear data-see and they are the
		// one thing here that must never be hidden.
		column.WriteString(`<div class="site-see-view" data-see-text="` + v.Key + `"` + hidden + `>` +
			`<div class="site-see-head"><h2>` + html.EscapeString(v.Title) + `</h2>` +
			`<p class="hint">` + html.EscapeString(v.Subtitle) + `</p></div>` +
			`<p class="site-see-sub">` + html.EscapeString(v.Desc) + `</p></div>`)
	}
	return `<div class="site-see-grid" data-see-carousel>` +
		`<div class="site-see-left">` + column.String() + `</div>` +
		`<figure class="site-see-shot">` +
		`<ul class="site-see-track">` + slides.String() + `</ul>` +
		`<div class="site-see-dots" role="group" aria-label="Dashboard views">` +
		dots.String() + `</div>` +
		`</figure></div>`
}

var siteTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>«PRODUCT»: «HEADLINE»</title>
<meta name="description" content="«PRODUCT» records which AI techniques help your organization and recommends them in supported tools with measured evidence.">
` + FaviconLink + ThemeInitScript + BootHideScript + GroundCSS + FrontDoorCSS + `
<link rel="stylesheet" href="/assets/app.css?v=«CSSVER»" onload="document.documentElement.classList.remove('booting')">
</head><body class="site">
«OPERATOR»

<header class="site-stage">
 <div class="site-masthead">
  <div class="site-wordmark" aria-hidden="true">«MARK»<span>«PRODUCT»</span></div>
  <nav class="site-nav" aria-label="Project">«NAV»</nav>
  <a class="site-gh" href="«REPOHREF»" aria-label="«PRODUCT» on GitHub" title="«PRODUCT» on GitHub">«GITHUBMARK»</a>
  <button id="theme-toggle" class="theme-toggle site-theme" type="button" aria-label="Color theme" title="Color theme">` + AutoSVG + `</button>
 </div>
 <h1 class="site-title" data-highlight-orange="«HEADLINEWHAT»" data-highlight-green="«HEADLINEHOW»">«HEADLINE»</h1>
 <div class="site-pitch">
  <p class="site-lede">«PRODUCT» builds an organization playbook from session data.
   It recommends relevant techniques in supported tools and records whether users
   adopted them and found them helpful.</p>
  <div class="site-install" data-guide-surface>
   <div class="site-term-bar"><i aria-hidden="true"></i><i aria-hidden="true"></i>
    <button type="button" class="site-zoom" data-guide-open aria-label="«GUIDETITLE»">` + zoomGlyphSVG + `</button>
    <span class="site-install-title" aria-hidden="true">«INSTALLTITLE»</span></div>
   <div class="site-install-row">
    <code class="site-install-cmd">«INSTALLCMD»</code>
    <span class="site-install-info" aria-hidden="true">` + InfoIcon + `</span>
   </div>
  </div>
  <p class="site-note">«PRODUCT» is open source and self-hosted.</p>
 </div>
</header>

<div class="site-marquee" aria-label="Works inside these tools">
 <div class="site-marquee-track">«HARNESSES»<i>◆</i>«HARNESSES»<i>◆</i></div>
</div>

<section class="site-flow" data-flow>
 <div class="site-flow-pin">
  «FLOW»
 </div>
</section>

<main class="wrap wrap-narrow site-body">

<section class="site-see">
«SEE»
</section>

<section class="site-priv">
«PRIV»
</section>

<div class="site-cta-end">
 <button type="button" class="btn btn-primary site-cta" data-guide-open>«ACTIONLABEL»</button>
</div>

<p class="site-close">Built in Berkeley, California.
 <a href="«CONTACTHREF»">Contact us.</a></p>

</main>

<dialog class="site-guide" aria-label="«GUIDETITLE»">
 <div class="site-guide-win">
  <div class="site-term-bar">
   <button type="button" class="site-guide-light" data-guide-close aria-label="Close"></button>
   <button type="button" class="site-guide-light" data-guide-close aria-label="Close"></button>
   <button type="button" class="site-guide-light" data-guide-close aria-label="Close"></button>
   <span class="site-install-title" aria-hidden="true">«INSTALLTITLE»</span>
  </div>
  <div class="site-guide-body">«GUIDE»</div>
 </div>
</dialog>
	` + ThemeToggleScript + StylesheetGuardScript + siteHighlightScript +
	`<script>` + CopyButtonJS + `</script>` + siteScript + `
</body></html>`

// siteGuideStep renders one numbered step: a heading, a sentence, and the command
// with the same copy control the hero's window carries.
func siteGuideStep(n, title, body, cmd string) string {
	esc := html.EscapeString(cmd)
	return `<section class="site-guide-step">` +
		`<h3><span class="site-guide-num">` + n + `</span>` + html.EscapeString(title) + `</h3>` +
		`<p>` + body + `</p>` +
		`<div class="site-guide-cmd"><code>` + esc + `</code>` +
		`<button type="button" class="copy-btn" data-copy="` + esc +
		`" aria-label="Copy: ` + esc + `">` + CopyIcon + `</button></div>` +
		`</section>`
}

func siteGuideHTML(install string) string {
	return `<h2>` + html.EscapeString(SiteGuideHeading) + `</h2>` +
		`<p class="site-guide-lede">The registry is the service that holds your
		 organization's playbook. It is one binary. It stores the techniques, measures
		 the outcomes, serves the dashboard, and answers your colleagues' harnesses.
		 Four commands set it up.</p>` +

		siteGuideStep("1", "Install «PRODUCT»",
			`The installer verifies the checksum before it changes anything. It puts one
			 binary in <code>~/.local/bin</code>.`, install) +

		siteGuideStep("2", "Bootstrap the registry",
			`This writes the configuration, creates the API key, adds a starter set of
			 techniques, downloads the search model, and starts the registry in the current
			 terminal until you press Ctrl-C. You can run it again; it adds missing items only.
			 A registry your
			 colleagues sign in to needs to survive a reboot; turning on sign-in with
			 <code>tacit secure</code> installs the service that does that.`, siteGuideInit) +

		siteGuideStep("3", "Invite your first members",
			`This creates a join link. The link installs «PRODUCT», joins the registry and wires
			 the member's harnesses in one step. Give the link the protection you give a
			 password-reset link.`, siteGuideInvite) +

		siteGuideStep("4", "Check the health",
			`This checks the service, the permissions on the configuration file, the API
			 key and the embedder. It reports each check as ok, a warning, or a failure
			 with the fix.`, siteGuideDoctor) +

		`<p class="site-guide-note">The dashboard has no sign-in by default. Keep it on a
		 loopback address until you configure OIDC. The user guide covers containers,
		 sub-paths, and storage backends.</p>`
}
