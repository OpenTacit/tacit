// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"fmt"
	"html"
	"strings"
)

// Shell renders a page in the house style for a secondary console — today the
// ingress (docs/distribution/ingress.md). It is deliberately a smaller
// thing than the registry dashboard's own shell: same stylesheet, same header
// geometry, same breadcrumb, none of the registry-specific furniture (search,
// technique counts, the account menu's operator destinations). A second console does
// not need those, and a shell general enough for both would be a worse shell
// for each.
//
// What it must never become is a second look. Every class here is one app.css
// already defines; if a panel needs a style that does not exist yet, add it to
// the stylesheet where both binaries get it.
type Shell struct {
	// Brand is the wordmark beside the ◆ — "OpenTacit Ingress", not "OpenTacit", so an
	// operator with both consoles open can tell the tabs apart.
	Brand string
	// Nav is the top-bar links, in order.
	Nav []NavItem
	// Meta is the quiet figure at the right of the bar (the registry puts technique
	// and event totals here).
	Meta string
	// Account is the right-hand identity slot: a Sign in link, or the signed-in
	// address with a way out.
	Account string
}

// NavItem is one top-bar destination.
type NavItem struct {
	Label  string
	Href   string
	Active bool
}

// Page is one rendered view.
type Page struct {
	Title   string // <title>; the brand is appended
	Crumbs  []Crumb
	Content string // markup, already escaped by the caller
	Status  int
}

// Crumb is one breadcrumb step. An empty Href marks the current page.
type Crumb struct {
	Label string
	Href  string
}

// Render returns a complete HTML document.
func (s Shell) Render(p Page) string {
	var nav strings.Builder
	for _, n := range s.Nav {
		cls := ""
		if n.Active {
			cls = ` class="active"`
		}
		fmt.Fprintf(&nav, `<a href="%s"%s>%s</a>`, html.EscapeString(n.Href), cls, html.EscapeString(n.Label))
	}
	title := s.Brand
	if p.Title != "" {
		title = p.Title + " · " + s.Brand
	}
	return strings.NewReplacer(
		"«TITLE»", html.EscapeString(title),
		"«BRAND»", html.EscapeString(s.Brand),
		"«CSSVER»", CSSHash(),
		"«NAV»", nav.String(),
		"«META»", html.EscapeString(s.Meta),
		"«ACCOUNT»", s.Account,
		"«CRUMBS»", crumbs(p.Crumbs),
		"«CONTENT»", p.Content,
	).Replace(shellTemplate)
}

func crumbs(cs []Crumb) string {
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<nav class="crumbs" aria-label="Breadcrumb">`)
	for i, c := range cs {
		if i > 0 {
			b.WriteString(`<span class="crumb-sep">/</span>`)
		}
		if c.Href == "" {
			fmt.Fprintf(&b, `<span class="crumb-current">%s</span>`, html.EscapeString(c.Label))
			continue
		}
		fmt.Fprintf(&b, `<a href="%s">%s</a>`, html.EscapeString(c.Href), html.EscapeString(c.Label))
	}
	b.WriteString(`</nav>`)
	return b.String()
}

var shellTemplate = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>«TITLE»</title>
` + FaviconLink + ThemeInitScript + BootHideScript + GroundCSS + `
<link rel="stylesheet" href="/assets/app.css?v=«CSSVER»" onload="document.documentElement.classList.remove('booting')">
</head><body>
<header class="top">
 <a class="brand" href="/" aria-label="«BRAND» home">` + MarkSVG + `«BRAND»</a>
 <input type="checkbox" id="nav-toggle" class="nav-toggle" aria-label="Menu">
 <span class="account">«ACCOUNT»</span>
 <label for="nav-toggle" class="nav-burger" aria-hidden="true">☰</label>
 <div class="bar-collapse">
  <nav>«NAV»</nav>
  <span class="meta">«META»</span>
  <button id="theme-toggle" class="theme-toggle" type="button" aria-label="Color theme" title="Color theme">` + AutoSVG + `</button>
 </div>
</header>
<main class="wrap">«CRUMBS»«CONTENT»</main>
<div id="tip" role="status"></div>
` + ThemeToggleScript + AccountMenuScript + RowLinkScript + VizHoverScript + LocalTimeScript + StylesheetGuardScript + `
<script>(function(){` + SortableTableJS + BulkSelectJS + BusyFormJS + `})();</script>
</body></html>`

// Tile renders one stat: the label above, the figure below. Rule 2 of the house
// style — the figure is the hero, loud through scale rather than colour.
func Tile(label, value, note string) string {
	var b strings.Builder
	b.WriteString(`<div class="tile"><div class="tile-label">` + html.EscapeString(label) + `</div>`)
	b.WriteString(`<div class="tile-row"><span class="tile-value">` + html.EscapeString(value) + `</span>`)
	if note != "" {
		b.WriteString(`<span class="tile-delta">` + html.EscapeString(note) + `</span>`)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

// SparkTile is a Tile with its recent shape under the figure. The number says
// where a thing is; the line says where it has been going, which is the question
// a total on an overview usually raises next.
func SparkTile(label, value, note, spark string) string {
	t := Tile(label, value, note)
	if spark == "" {
		return t
	}
	return strings.TrimSuffix(t, `</div>`) + spark + `</div>`
}

// Tiles wraps a row of tiles.
func Tiles(tiles ...string) string {
	return `<div class="tiles">` + strings.Join(tiles, "") + `</div>`
}

// Panel is a flat plate with an optional heading and hint.
func Panel(heading, hint, body string) string {
	var b strings.Builder
	b.WriteString(`<section class="panel">`)
	if heading != "" {
		b.WriteString(`<h2>` + html.EscapeString(heading) + `</h2>`)
	}
	if hint != "" {
		b.WriteString(`<p class="hint">` + hint + `</p>`)
	}
	b.WriteString(body)
	b.WriteString(`</section>`)
	return b.String()
}

// UH is a column header carrying its own unit on a second line.
//
// This is what retired the paragraph above nearly every table on the dashboard.
// A table whose numbers are "per session", or "of shown", or "adoptions this
// window", was announcing that in prose above itself, where a reader scanning
// the column never looked. The unit belongs at the column, because that is
// where the question is asked. Two or three words, never a sentence.
func UH(name, unit string) string {
	if unit == "" {
		return `<th class="num">` + html.EscapeString(name) + `</th>`
	}
	// The name is wrapped so the sort caret can attach to the NAME line rather
	// than to the end of the cell, which on a two-line header is under the unit.
	return `<th class="num has-u"><span class="hd">` + html.EscapeString(name) +
		`</span><span class="u">` + html.EscapeString(unit) + `</span></th>`
}

// TH is UH for a column of words rather than figures.
func TH(name, unit string) string {
	if unit == "" {
		return `<th>` + html.EscapeString(name) + `</th>`
	}
	return `<th class="has-u"><span class="hd">` + html.EscapeString(name) +
		`</span><span class="u">` + html.EscapeString(unit) + `</span></th>`
}

// Sub is a sub-heading carrying its own unit or sample beside it — the h3 twin
// of UH, for the panels whose sections are bars and charts rather than tables.
func Sub(title, unit string) string {
	// A unit with no title of its own is a caption, not a heading: the panel
	// title has already named the thing, and an <h3> holding only a muted unit
	// would put a near-empty entry in the document outline a screen reader
	// navigates by.
	if title == "" {
		if unit == "" {
			return ""
		}
		return `<p class="chart-sub unit-only"><span class="u">` +
			html.EscapeString(unit) + `</span></p>`
	}
	out := `<h3 class="chart-sub">` + html.EscapeString(title)
	if unit != "" {
		out += `<span class="u">` + html.EscapeString(unit) + `</span>`
	}
	return out + `</h3>`
}

// Fine is everything a panel must not be read as, in one closed disclosure.
//
// Closed on purpose. The marks above it carry the meaning — headings name their
// units, legends name their categories, denominators precede the rates that
// divide by them — so a reader who never opens one of these is not misled by
// the default view. That is the test every panel on this dashboard is held to.
// Nothing was deleted to reach it: the claims the product must not overstate
// all still stand, one click from the figure they qualify.
//
// Items may carry markup; empty ones are dropped so a caller can build the list
// conditionally without counting.
func Fine(items ...string) string {
	var kept []string
	for _, it := range items {
		if strings.TrimSpace(it) != "" {
			kept = append(kept, `<li>`+it+`</li>`)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return `<details class="fineprint"><summary>How this is measured</summary><ul>` +
		strings.Join(kept, "") + `</ul></details>`
}

// Esc is html.EscapeString under a shorter name, for page builders that reach
// for it on nearly every value.
func Esc(s string) string { return html.EscapeString(s) }
