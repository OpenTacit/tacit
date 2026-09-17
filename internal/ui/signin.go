// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"fmt"
	"html"
	"strings"
)

// The front door: the page a console shows someone who has not signed in.
//
// It is the same panel the registry's own sign-in wears — centred technique on the
// board, the mark at full size, one accent button — because a visitor who lands
// on either should be in no doubt they are looking at the same product. What it
// leaves out is the registry's animated field: that page is a first impression
// for a whole organization, and this one is a gate an operator passes through on
// the way to work. A WebGL canvas here would be decoration nobody asked for in
// front of a task somebody is mid-way through.
//
// Every class is one app.css already defines for the registry's front door, so
// the two cannot drift apart on colour, weight or spacing — and the legibility
// floor (FrontDoorCSS) applies here for the same reason it applies there: this
// is the one page a stranger may meet before the stylesheet arrives.
//
// The registry keeps its own document rather than being folded in here. It
// carries things this has no use for — the canvas, the launch transition, the
// home-screen manifest, the connect-your-harness disclosure — and a template
// with a slot for each would be a worse page than either.

// SignIn is a console's front door.
type SignIn struct {
	// Brand is the wordmark beside the mark — "OpenTacit Ingress". Any length: the
	// heading is measured and scaled to the panel (WordmarkFitScript), up to the
	// 3.5rem the design wants, and never wraps.
	Brand string
	// Eyebrow names which console this is ("Ingress console"), in small text
	// under the wordmark, since Brand alone cannot carry both.
	Eyebrow string
	// Lede is one sentence on what lies behind the door.
	Lede string
	// Action is the primary button. Omit the href to render no button — the
	// case where signing in again is not the answer.
	ActionLabel, ActionHref string
	// Note is a second paragraph under the lede: why the visitor is here when
	// it is not simply "you are signed out".
	Note string
	// Links are the chips under the button, each a label and a target.
	Links [][2]string
	// Title is the <title>; the brand is appended.
	Title string
}

// Render returns a complete HTML document.
func (s SignIn) Render() string {
	var technique strings.Builder
	fmt.Fprintf(&technique, `<h1 style="--name-len:%s"%s>%s%s</h1>`,
		WordmarkLen(s.Brand), WordmarkAttr("3.5"), MarkSVG, Wordmark(html.EscapeString(s.Brand)))
	if s.Eyebrow != "" {
		fmt.Fprintf(&technique, `<p class="eyebrow">%s</p>`, html.EscapeString(s.Eyebrow))
	}
	if s.Lede != "" {
		fmt.Fprintf(&technique, `<p class="signin-lede">%s</p>`, html.EscapeString(s.Lede))
	}
	if s.ActionHref != "" {
		fmt.Fprintf(&technique, `<a class="btn" href="%s">%s</a>`,
			html.EscapeString(s.ActionHref), html.EscapeString(s.ActionLabel))
	}
	if s.Note != "" {
		fmt.Fprintf(&technique, `<p class="signin-hint">%s</p>`, html.EscapeString(s.Note))
	}
	if len(s.Links) > 0 {
		technique.WriteString(`<nav class="signin-links" aria-label="More">`)
		for _, l := range s.Links {
			fmt.Fprintf(&technique, `<a href="%s">%s</a>`, html.EscapeString(l[1]), html.EscapeString(l[0]))
		}
		technique.WriteString(`</nav>`)
	}
	title := s.Brand
	if s.Title != "" {
		title = s.Title + " · " + s.Brand
	}
	return strings.NewReplacer(
		"«TITLE»", html.EscapeString(title),
		"«CSSVER»", CSSHash(),
		"«TECHNIQUE»", technique.String(),
	).Replace(signInTemplate)
}

var signInTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>«TITLE»</title>
` + FaviconLink + ThemeInitScript + BootHideScript + GroundCSS + FrontDoorCSS + `
<link rel="stylesheet" href="/assets/app.css?v=«CSSVER»" onload="document.documentElement.classList.remove('booting')">
</head><body class="signin">
<button id="theme-toggle" class="theme-toggle signin-theme" type="button" aria-label="Color theme" title="Color theme">` + AutoSVG + `</button>
<main class="technique">«TECHNIQUE»</main>
` + ThemeToggleScript + WordmarkFitScript + StylesheetGuardScript + `
</body></html>`
