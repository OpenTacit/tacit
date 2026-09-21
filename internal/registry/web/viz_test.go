// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"
)

func TestOrganizationWindowStateLivesInTheURL(t *testing.T) {
	if strings.Contains(shellTemplate, "tacit-window") {
		t.Fatal("period state must not use localStorage")
	}
	// The styling lives in one served stylesheet now, not in the shell string.
	for _, want := range []string{`max-height:min(480px,55vh)`, `overflow:auto`, `touch-action:pan-x pan-y`, `position:sticky`, `@media (forced-colors:active)`, `@media (prefers-reduced-motion:reduce)`} {
		if !strings.Contains(appCSS, want) {
			t.Fatalf("organization heatmap CSS missing %q", want)
		}
	}
	if strings.Contains(appCSS, `.org-map`) || strings.Contains(appCSS, `.org-matrix`) {
		t.Fatal("obsolete organization visualization CSS remains")
	}
	// The shell carries no stylesheet of its own — it links the one. There is
	// exactly one deliberate exception: groundCSS, three tokens inlined so the
	// first paint is the right colour before the stylesheet lands (see shell.go).
	// Allowing it by identity rather than relaxing the check keeps the original
	// point of this guard — that ad-hoc styles must not creep back into the shell
	// string, which is how the registry ended up with four drifting palettes.
	if !strings.Contains(shellTemplate, cssLink) {
		t.Fatal("the shell no longer links the stylesheet")
	}
	if n := strings.Count(shellTemplate, "<style>"); n != 1 {
		t.Fatalf("shell has %d inline <style> blocks, want exactly 1 (groundCSS)", n)
	}
	if !strings.Contains(shellTemplate, groundCSS) {
		t.Fatal("the shell regrew an inline stylesheet that is not groundCSS")
	}
}
