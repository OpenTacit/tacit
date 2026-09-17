// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
	"time"
)

// The instant is the thing that has to survive the trip. If the element loses
// its datetime attribute or its kind, the browser has nothing to re-format
// against and the reader is silently left on the server's clock — which is the
// bug this whole mechanism exists to close.
func TestLocalTimeCarriesTheInstantAndTheKind(t *testing.T) {
	at := time.Date(2026, 7, 28, 22, 5, 0, 0, time.UTC)
	got := LocalTime(at, LTYMD)
	for _, want := range []string{`datetime="2026-07-28T22:05:00Z"`, `data-lt="ymd"`, `>2026-07-28</time>`} {
		if !strings.Contains(got, want) {
			t.Errorf("LocalTime(%s) = %q, missing %q", LTYMD, got, want)
		}
	}
}

// The rendered text is a FALLBACK, not the answer: it is what a reader without
// script sees, and it must be UTC rather than whatever zone the server happens
// to sit in. A server in Los Angeles rendering its own afternoon into a page
// read in Tokyo is the failure mode; UTC is at least a defensible default and
// the only one the format can honestly label.
func TestFallbackTextIsUTCNotServerLocal(t *testing.T) {
	// 22:05 UTC is the previous afternoon in Los Angeles and the next morning
	// in Tokyo — any zone leaking in moves the date, not just the clock.
	at := time.Date(2026, 7, 28, 22, 5, 0, 0, time.UTC)
	for _, loc := range []string{"America/Los_Angeles", "Asia/Tokyo"} {
		z, err := time.LoadLocation(loc)
		if err != nil {
			t.Skipf("no zone data for %s", loc)
		}
		if got := LTFallback(at.In(z), LTYMD); got != "2026-07-28" {
			t.Errorf("LTFallback in %s = %q, want the UTC date 2026-07-28", loc, got)
		}
		if got := LTFallback(at.In(z), LTHM); got != "22:05" {
			t.Errorf("LTFallback in %s = %q, want the UTC clock 22:05", loc, got)
		}
	}
}

// A zero time is the ABSENCE of a timestamp. Rendering it would put "Jan 1" in
// a cell that means "never" — and, worse, a <time> the browser would happily
// re-date into a confident-looking year one.
func TestZeroTimeRendersNothing(t *testing.T) {
	if got := LocalTime(time.Time{}, LTStamp); got != "" {
		t.Errorf("LocalTime(zero) = %q, want empty", got)
	}
}

// A stored value in an unexpected shape is still what the record says. Dropping
// it would hide the problem; passing it through unescaped would be an injection
// the store could carry.
func TestUnparseableTimestampIsPassedThroughEscaped(t *testing.T) {
	if got := LocalTimeISO("not a time", LTYMD); got != "not a time" {
		t.Errorf("LocalTimeISO(garbage) = %q, want it passed through", got)
	}
	if got := LocalTimeISO(`<script>x</script>`, LTYMD); strings.Contains(got, "<script>") {
		t.Errorf("LocalTimeISO escaped nothing: %q", got)
	}
}

// Every kind the Go side can render must be one the browser also knows, and the
// other way round. A kind that only one half understands is a cell that renders
// in UTC forever without anyone noticing.
func TestEveryKindIsHandledOnBothSides(t *testing.T) {
	kinds := []string{LTDate, LTWeek, LTYMD, LTStamp, LTDateHM, LTHM, LTHMS}
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("two kinds share the name %q; the browser cannot tell them apart", k)
		}
		seen[k] = true
		if !strings.Contains(LocalTimeScript, `'`+k+`'`) && k != LTDate {
			// LTDate is the script's default branch and so is never named.
			t.Errorf("kind %q is never tested for in LocalTimeScript, so it falls through to a date", k)
		}
	}
}

// The localiser has to be reachable by name for markup that arrives after load
// — the ingress rate chart replaces its own plot on every zoom. Without the
// handle, the one part of the console a reader interacts with most is the one
// part still showing UTC.
func TestLocaliserIsExposedForLateMarkup(t *testing.T) {
	if !strings.Contains(LocalTimeScript, "window.tacitLocalTime=run") {
		t.Fatal("LocalTimeScript no longer exposes tacitLocalTime; swapped-in markup would keep the server's zone")
	}
	if !strings.Contains(LocalTimeScript, "run(document)") {
		t.Fatal("LocalTimeScript no longer runs on load")
	}
}

// An axis without instants is left exactly as it was given — the ingress
// console's day-key chart depends on it, because a boundary someone else
// already fixed cannot honestly be re-dated by moving its label.
func TestAxisWithoutInstantsIsNotLocalised(t *testing.T) {
	a := Axis{Labels: []string{"Jul 28"}}
	if got := a.Tick(0); got != "Jul 28" {
		t.Errorf("Tick without ISO = %q, want the label verbatim", got)
	}
	if got := a.HoverAttrs(0); strings.Contains(got, "data-label-iso") {
		t.Errorf("HoverAttrs without ISO = %q, want no instant", got)
	}
}

// With instants, both the visible tick and the tooltip's attribute have to
// carry them. The tooltip is read at hover time, long after the localiser has
// run, so it is the one that fails quietly if the instant is missing.
func TestAxisWithInstantsLocalisesTickAndTooltip(t *testing.T) {
	a := Axis{
		Labels: []string{"Jul 28"},
		ISO:    []string{"2026-07-28T22:00:00Z"},
		Kind:   LTDate,
	}
	if got := a.Tick(0); !strings.Contains(got, `datetime="2026-07-28T22:00:00Z"`) {
		t.Errorf("Tick = %q, want the instant", got)
	}
	got := a.HoverAttrs(0)
	for _, want := range []string{`data-label="Jul 28"`, `data-label-iso="2026-07-28T22:00:00Z"`, `data-lt="d"`} {
		if !strings.Contains(got, want) {
			t.Errorf("HoverAttrs = %q, missing %q", got, want)
		}
	}
}

// The ingress console shares the localiser with the registry dashboard
// deliberately: an operator moving between the two should not find that one of
// them dates things in UTC. (The dashboard's half of this is in
// internal/registry/web.)
func TestIngressShellLocalisesItsTimes(t *testing.T) {
	if !strings.Contains(shellTemplate, LocalTimeScript) {
		t.Fatal("the ingress shell dropped LocalTimeScript, so the two consoles now disagree about what time it is")
	}
}

func TestIngressShellHasMobileNavigation(t *testing.T) {
	page := (Shell{Brand: "Tacit Ingress", Nav: []NavItem{{Label: "Overview", Href: "/"}},
		Account: `<a href="/auth/login">Sign in</a>`}).Render(Page{})
	for _, want := range []string{
		`<input type="checkbox" id="nav-toggle" class="nav-toggle" aria-label="Menu">`,
		`<label for="nav-toggle" class="nav-burger" aria-hidden="true">☰</label>`,
		`<div class="bar-collapse">`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("ingress shell is missing mobile navigation control %q", want)
		}
	}
	account := strings.Index(page, `<span class="account">`)
	collapse := strings.Index(page, `<div class="bar-collapse">`)
	if account < 0 || collapse < 0 || account > collapse {
		t.Fatal("the ingress account control must remain visible outside the collapsed phone menu")
	}
}
