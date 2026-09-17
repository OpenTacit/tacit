// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/insights"
)

func appViews() []insights.Overview {
	return []insights.Overview{
		{Window: insights.Window{Key: "7d", Label: "Last 7 days"}},
		{Window: insights.Window{Key: "30d", Label: "Last 30 days"}},
		{Window: insights.Window{Key: "90d", Label: "Last 90 days"}},
		{Window: insights.Window{Key: "all", Label: "All time"}},
	}
}

// The app is rendered inside a host-owned frame. If it doesn't run the MCP Apps
// view handshake, the host never learns its height (the panel is clipped at the
// host default) and never offers full screen — the bug this guards against.
func TestInsightsAppSpeaksTheViewProtocol(t *testing.T) {
	app := InsightsAppHTML(appViews(), "30d")

	for _, want := range []string{
		"ui/initialize",                         // handshake: without it the host stays silent
		"ui/notifications/initialized",          // completes it; the host may not talk before this
		"ui/notifications/size-changed",         // the real height, which the host applies
		"ui/notifications/host-context-changed", // theme flips, host resizes, mode changes
		"ui/notifications/tool-input",           // the window the member actually asked for
		"ui/request-display-mode",               // the full-screen ask
		"availableDisplayModes",                 // declared, or the host may refuse the mode
		`id="mode-btn"`,                         // the control that asks for it
		`data-app="1"`,                          // scroll inside the frame once the host owns the box
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app HTML missing %s", want)
		}
	}
}

// The app wears the brand: the drawn mark (◆ core, unclosed chevrons), not the
// bare ◆ glyph that reads as a generic blue diamond.
func TestInsightsAppUsesTheBrandMark(t *testing.T) {
	app := InsightsAppHTML(appViews(), "30d")

	if !strings.Contains(app, markSVG) {
		t.Error("app HTML does not carry the Tacit mark")
	}
	if strings.Contains(app, "&#9670;") {
		t.Error("app HTML still draws the plain ◆ glyph in place of the mark")
	}
}

// Fallback path: hosts that never answer the handshake still size the frame off
// the pre-spec mcp-ui message and the document height they measure themselves.
func TestInsightsAppKeepsPreSpecSizing(t *testing.T) {
	app := InsightsAppHTML(appViews(), "30d")

	if !strings.Contains(app, "ui-size-change") {
		t.Error("app HTML dropped the mcp-ui size message")
	}
	if !strings.Contains(app, "root.style.height") {
		t.Error("app HTML no longer sets its own document height")
	}
}

// An MCP Apps host renders the argument-less template, so every window has to be
// in the document for the bridge to select the one that was asked for — with the
// requested window already up for hosts that render the tool result inline.
func TestInsightsAppCarriesEveryWindowAndSelectsTheOneAsked(t *testing.T) {
	app := InsightsAppHTML(appViews(), "7d")

	for _, key := range []string{"7d", "30d", "90d", "all"} {
		if !strings.Contains(app, `<section class="view" data-window="`+key+`"`) {
			t.Errorf("app HTML missing the %s window", key)
		}
	}
	if !strings.Contains(app, `<section class="view" data-window="7d">`) {
		t.Error("requested window 7d is not the visible one")
	}
	for _, key := range []string{"30d", "90d", "all"} {
		if !strings.Contains(app, `<section class="view" data-window="`+key+`" hidden>`) {
			t.Errorf("window %s should be hidden, not shown alongside the requested one", key)
		}
	}
	if !strings.Contains(app, `<button type="button" class="win" data-window="7d" aria-pressed="true"`) {
		t.Error("window switcher does not mark the requested window")
	}
	if !strings.Contains(app, "Last 7 days") {
		t.Error("the requested window's label is missing")
	}
}
