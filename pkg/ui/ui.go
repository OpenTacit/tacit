// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package ui exports the part of the interface a console built outside this
// module needs: the shell, the plates, the charts, the time formatting and the
// static assets. It is a facade over internal/ui, which stays the single source
// of truth — one CSS file, one shell, one set of tokens, whoever renders the
// page (internal/ui/assets/app.css).
//
// It exists because the ingress operator console is not in this repository. That
// console serves the project's own instance and nothing a reader of the open
// source would run, so it was moved out; what it renders, though, has to be the
// same product as the registry dashboard. A page built from a second copy of
// these helpers would drift on the first edit to a token.
//
// The set below is exactly what that console uses, and nothing is here to round
// the list out. Adding a symbol is a deliberate act: it is one more piece of the
// interface that something outside this module can pin.
package ui

import (
	"html/template"
	"net/http"
	"time"

	internal "github.com/opentacit/tacit/internal/ui"
)

// The page frame: a shell holding nav and account, rendering one page inside it.
type (
	Shell   = internal.Shell
	Page    = internal.Page
	NavItem = internal.NavItem
	Crumb   = internal.Crumb
	SignIn  = internal.SignIn
	Series  = internal.Series
	Axis    = internal.Axis
)

// Account renders the avatar control the registry dashboard uses, so an operator
// moving between the two consoles sees one identity control rather than two.
func Account(user map[string]any, oidcOn bool, menuItems string) string {
	return internal.Account(user, oidcOn, menuItems)
}

// Esc escapes text for HTML. Every value a console puts on a page goes through
// it; the registry's own views are held to the same rule.
func Esc(s string) string { return internal.Esc(s) }

// FmtCount renders a count the way the house style asks for it.
func FmtCount(v int) string { return internal.FmtCount(v) }

// The flat plates: a panel with a heading, a tile carrying one figure, and the
// row that holds tiles.
func Panel(heading, hint, body string) string { return internal.Panel(heading, hint, body) }
func Tile(label, value, note string) string   { return internal.Tile(label, value, note) }
func Tiles(tiles ...string) string            { return internal.Tiles(tiles...) }
func SparkTile(label, value, note, spark string) string {
	return internal.SparkTile(label, value, note, spark)
}

// The charts, server-rendered against the tokens. Series carry a key, never a
// colour, so both themes follow from one edit to the token block.
func LineChart(axis Axis, series []Series, height int) template.HTML {
	return internal.LineChart(axis, series, height)
}
func Sparkline(values []int) template.HTML { return internal.Sparkline(values) }

// LineSeriesKeys is the fixed series vocabulary. A chart picks a key from it; it
// never picks a colour.
var LineSeriesKeys = internal.LineSeriesKeys

// LocalTime renders a timestamp in the reader's zone, with LTFallback's text
// standing in until the script runs. The kinds are the LT constants below.
func LocalTime(t time.Time, kind string) string  { return internal.LocalTime(t, kind) }
func LTFallback(t time.Time, kind string) string { return internal.LTFallback(t, kind) }

// The timestamp kinds LocalTime accepts.
const (
	LTDate   = internal.LTDate
	LTStamp  = internal.LTStamp
	LTDateHM = internal.LTDateHM
	LTHM     = internal.LTHM
	LTHMS    = internal.LTHMS
)

// The two scripts a page includes: one turns the stamps above into local time,
// the other makes a table row follow its link.
const (
	LocalTimeScript = internal.LocalTimeScript
	RowLinkScript   = internal.RowLinkScript
)

// The static assets, served from the binary. Nothing is fetched at runtime, so
// these are the whole of what a page loads (internal/ui/assets).
func ServeCSS(w http.ResponseWriter, r *http.Request)      { internal.ServeCSS(w, r) }
func ServeFavicon(w http.ResponseWriter, r *http.Request)  { internal.ServeFavicon(w, r) }
func ServeFont(w http.ResponseWriter, r *http.Request)     { internal.ServeFont(w, r) }
func ServeShot(w http.ResponseWriter, r *http.Request)     { internal.ServeShot(w, r) }
func ServeBackdrop(w http.ResponseWriter, r *http.Request) { internal.ServeBackdrop(w, r) }

// The project page served on the product's own domain, and the hotkey that signs
// an operator out of the copy they are reviewing. SiteMaxAge is how long a
// shared cache may keep the published page.
func SiteHTML(operator string) string             { return internal.SiteHTML(operator) }
func SiteHTMLFor(host, operator string) string    { return internal.SiteHTMLFor(host, operator) }
func SiteSignOutHotkey(signOutHref string) string { return internal.SiteSignOutHotkey(signOutHref) }

const SiteMaxAge = internal.SiteMaxAge

// The two installer addresses, quoted from one place so a redirect configured
// elsewhere and the page cannot disagree (pkg/installaddr).
const (
	InstallURL       = internal.InstallURL
	InstallScriptURL = internal.InstallScriptURL
)
