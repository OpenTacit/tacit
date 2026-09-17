// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/ui"
)

// The ground is worth nothing if it arrives after the stylesheet it is meant to
// stand in for, or if a document forgets it.
func TestGroundCSSPrecedesStylesheetEverywhere(t *testing.T) {
	for name, doc := range map[string]string{
		"shell":   shellTemplate,
		"sign-in": signinTemplate,
	} {
		g, c := strings.Index(doc, groundCSS), strings.Index(doc, cssLink)
		if g < 0 {
			t.Errorf("%s: no inline ground, so its first paint is the browser default", name)
			continue
		}
		if c >= 0 && g > c {
			t.Errorf("%s: inline ground comes after the stylesheet link; it must come first to be any use", name)
		}
	}
}

// The boot hide is only safe because two independent things take it off again.
// Lose either and the failure is severe: without the stylesheet's onload the
// page waits out the timer on every navigation; without the timer a stylesheet
// that never arrives leaves a permanently blank page.
func TestBootHideAlwaysComesOff(t *testing.T) {
	if !strings.Contains(groundCSS, "html.booting body{visibility:hidden}") {
		t.Fatal("groundCSS no longer hides content while booting")
	}
	if !strings.Contains(bootHideScript, "classList.add('booting')") {
		t.Fatal("bootHideScript no longer arms the boot hide")
	}
	if !strings.Contains(bootHideScript, "setTimeout") ||
		!strings.Contains(bootHideScript, "classList.remove('booting')") {
		t.Fatal("bootHideScript lost its timed reveal — a missing stylesheet would leave the page blank")
	}
	if !strings.Contains(cssLink, "classList.remove('booting')") {
		t.Fatal("the stylesheet no longer reveals the page on load — every navigation would wait out the timer")
	}
	// The class is added by script only, so a reader without JavaScript never
	// has anything hidden from them.
	if strings.Contains(shellTemplate, `class="booting"`) ||
		strings.Contains(shellTemplate, "html class='booting'") {
		t.Fatal("booting is baked into the markup; with JavaScript off the page would never reveal")
	}
	// Every document that hides must also carry a way to un-hide.
	for name, doc := range map[string]string{"shell": shellTemplate, "sign-in": signinTemplate} {
		if strings.Contains(doc, groundCSS) && !strings.Contains(doc, bootHideScript) {
			t.Errorf("%s: carries the hide rule but not the script that reveals it", name)
		}
	}
}

// Click-to-navigate rows are one behaviour, so they are one implementation:
// ui.RowLinkScript, which both consoles include. This dashboard had its own
// copy while the ingress console had none, and the ingress table's rows were
// dead as a result. The assertion is that the copy is gone, not merely that the
// shared one is present — two live copies is how they drift apart again.
func TestRowLinksComeFromTheSharedChrome(t *testing.T) {
	if !strings.Contains(shellTemplate, ui.RowLinkScript) {
		t.Fatal("the shell no longer includes ui.RowLinkScript, so no row navigates")
	}
	body := strings.Replace(shellTemplate, ui.RowLinkScript, "", 1)
	if strings.Contains(body, "row-link") || strings.Contains(body, "getAttribute('data-href')") {
		t.Error("the shell has grown a second row-link implementation beside the shared one")
	}
}

// bloomStep exists twice — once in Go for the server-rendered charts, once in
// JavaScript for the Usage chart, which is assembled in the browser. Two copies
// of one curve in two languages is exactly the shape of thing that drifts, and a
// drift here is silent: the charts would simply disagree about how bright a value
// is. This pins the thresholds so a change to one has to be a change to both.
func TestUsageBloomStepMatchesGo(t *testing.T) {
	for _, want := range []string{
		"v*(0.5+0.5*v)",     // the same curve
		"b>=0.72?' bloom4'", // and the same four thresholds
		"b>=0.44?' bloom3'",
		"b>=0.20?' bloom2'",
		"b>=0.06?' bloom1'",
	} {
		if !strings.Contains(usageJS, want) {
			t.Errorf("the Usage chart's bloomStep no longer carries %q — it has drifted from viz.go's, "+
				"so the same value will glow differently on the two views", want)
		}
	}
	// And the Go side still says what the JS is pinned to.
	for _, want := range []string{"0.5 + 0.5*v", "0.72", "0.44", "0.20", "0.06"} {
		if !strings.Contains(vizGoBloomSource, want) {
			t.Errorf("viz.go bloomStep no longer contains %q; update the Usage copy and this test together", want)
		}
	}
}

// Absolute times are the reader's, not the server's. The mechanism only works
// if the localiser is actually in the document, so this pins it into the shell
// rather than trusting that whoever adds the next page remembers. (The ingress
// console's half of the same assertion is in internal/ui, where its shell is.)
func TestDashboardLocalisesItsTimes(t *testing.T) {
	if !strings.Contains(shellTemplate, ui.LocalTimeScript) {
		t.Fatal("the shell no longer includes ui.LocalTimeScript; every date on every page reverts to UTC")
	}
}

// The chart axis is where the two halves have to agree: the server writes a UTC
// fallback label and the browser overwrites it from the instant beside it. If a
// bucket ever ships without its instant the label silently stays UTC, which
// looks exactly like a label that worked.
func TestChartBucketsCarryTheirInstants(t *testing.T) {
	buckets := []insights.Bucket{{Start: time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)}}
	for _, tc := range []struct {
		name   string
		bucket time.Duration
		kind   string
		label  string
	}{
		{"daily", 24 * time.Hour, ui.LTDate, "Jul 28"},
		{"weekly", 7 * 24 * time.Hour, ui.LTWeek, "wk Jul 28"},
	} {
		a := timeAxis(buckets, tc.bucket)
		if a.Kind != tc.kind {
			t.Errorf("%s: axis kind = %q, want %q", tc.name, a.Kind, tc.kind)
		}
		if a.Labels[0] != tc.label {
			t.Errorf("%s: fallback label = %q, want the UTC %q", tc.name, a.Labels[0], tc.label)
		}
		if a.ISO[0] != "2026-07-28T22:00:00Z" {
			t.Errorf("%s: bucket start did not travel: %q", tc.name, a.ISO[0])
		}
	}
}
