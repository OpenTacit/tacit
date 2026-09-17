// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"strings"
	"testing"
)

// The wordmark is sized from the name, so both the box the fit script measures
// and the count the stylesheet divides by have to be on the page. A front door
// that renders the name as a bare text node loses both at once, and loses them
// silently: the heading still reads correctly, at whatever size was chosen for
// somebody else's brand.
func TestFrontDoorCarriesWhatSizesTheWordmark(t *testing.T) {
	doc := SignIn{Brand: "Tacit Ingress", ActionLabel: "Sign in", ActionHref: "/auth/login"}.Render()
	for what, want := range map[string]string{
		"the measurable box":  `<span class="wordmark">Tacit Ingress</span>`,
		"the character count": `--name-len:13`,
		"the fit pass":        WordmarkFitScript,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the console's front door is missing %s", what)
		}
	}
}

// --name-len divides a width by a count of characters. Bytes would make an
// accented name a third longer than it looks and shrink its wordmark to match.
func TestWordmarkLenCountsCharactersNotBytes(t *testing.T) {
	for name, want := range map[string]string{
		"Tacit":      "5",
		"Sagesse":    "7",
		"Tacit Zone": "10",
		"Wisdom™":    "7",
		"Überblick":  "9",
	} {
		if got := WordmarkLen(name); got != want {
			t.Errorf("WordmarkLen(%q) = %s, want %s", name, got, want)
		}
	}
}
