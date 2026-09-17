// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package slug turns free text into a kebab-case identifier.
//
// It is a leaf — no imports beyond the standard library — because the three
// sides that need it do not import one another on purpose: the registry, the
// auditor's hook agent, and the demo loader (which speaks the registry's public
// API rather than linking it). Each had grown its own copy, and the copies had
// drifted: one trimmed the input first, one capped the result at eighty
// characters, and they disagreed about what an empty result should be.
//
// A slug is durable. Technique ids and federation subscription ids are derived
// from one, and a subscription's id is re-derived from its feed URL whenever
// the subscription is saved — so changing what this produces renames things
// that already exist. Treat the output as a wire surface.
package slug

import (
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// Make lowercases text, replaces each run of anything that is not a letter or
// digit with a single hyphen, and trims hyphens from both ends. Text with
// nothing usable in it yields the empty string; a caller that needs a
// non-empty id supplies its own fallback.
func Make(text string) string {
	return strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(text), "-"), "-")
}

// MakeMax is Make, capped at max characters, with any hyphen the cut exposed
// trimmed off so the result never ends mid-separator. A max of zero or less
// means no cap.
//
// The cap is not part of Make because not every caller can afford one: a
// federation subscription id is re-derived from its feed URL on every save, so
// shortening one would orphan a subscription that already exists.
func MakeMax(text string, max int) string {
	s := Make(text)
	if max > 0 && len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	return s
}
