// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"io/fs"
	"strings"
	"testing"
)

// Both typefaces are licensed under the SIL Open Font License, which requires
// the licence text to travel with the font files wherever they are
// redistributed — and `//go:embed assets/fonts` means every binary
// redistributes them.
//
// This is a compliance test, not a rendering one. It exists because the
// failure it guards is invisible: an asset pipeline that learned to embed only
// *.woff2, or a tidy-up that moved the .txt files somewhere "neater", would
// keep every page looking correct while quietly making each binary an OFL
// violation. Nothing else in the suite would notice.
//
// THIRD-PARTY-NOTICES.md points at these paths, so they are also the thing
// that keeps that document honest.
func TestFontLicensesAreEmbeddedBesideTheFonts(t *testing.T) {
	want := map[string]string{
		"assets/fonts/LICENSE-IBMPlex.txt":  "SIL OPEN FONT LICENSE",
		"assets/fonts/LICENSE-MonaSans.txt": "SIL OPEN FONT LICENSE",
	}
	for path, marker := range want {
		b, err := fs.ReadFile(fontFS, path)
		if err != nil {
			t.Errorf("%s is not embedded: %v — the OFL requires it to ship with the fonts", path, err)
			continue
		}
		if !strings.Contains(strings.ToUpper(string(b)), marker) {
			t.Errorf("%s does not contain %q; is it the real licence text?", path, marker)
		}
	}
}

// A licence file with no font beside it would pass the test above while
// meaning nothing. This asserts the pairing the OFL actually cares about.
func TestEveryEmbeddedFontHasLicenceTextWithIt(t *testing.T) {
	entries, err := fs.ReadDir(fontFS, "assets/fonts")
	if err != nil {
		t.Fatalf("no embedded font directory: %v", err)
	}
	var fonts, licences int
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name(), ".woff2"), strings.HasSuffix(e.Name(), ".woff"),
			strings.HasSuffix(e.Name(), ".ttf"), strings.HasSuffix(e.Name(), ".otf"):
			fonts++
		case strings.HasPrefix(e.Name(), "LICENSE"):
			licences++
		}
	}
	if fonts == 0 {
		t.Fatal("no fonts embedded; this test is checking nothing")
	}
	if licences == 0 {
		t.Errorf("%d font files embedded with no licence text beside them", fonts)
	}
}
