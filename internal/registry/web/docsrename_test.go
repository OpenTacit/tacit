// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
)

// The HTML walk has one job the plain-text rename cannot do: tell prose from
// everything else a document is made of.
func TestRenameProductHTMLLeavesMarkupAndCodeAlone(t *testing.T) {
	t.Setenv(product.EnvKey, "Sagesse")
	for what, tc := range map[string]struct{ in, want string }{
		"prose": {
			`<p>OpenTacit records what helped.</p>`,
			`<p>Sagesse records what helped.</p>`},
		"an id and the link that points at it": {
			`<h2 id="what-tacit-does">What OpenTacit does</h2><a href="#what-tacit-does">above</a>`,
			`<h2 id="what-tacit-does">What Sagesse does</h2><a href="#what-tacit-does">above</a>`},
		"a command in a code span": {
			`<p>Run <code>tacit connect</code> to join OpenTacit.</p>`,
			`<p>Run <code>tacit connect</code> to join Sagesse.</p>`},
		"a header name in a code span": {
			`<p>OpenTacit authenticates with <code>X-Tacit-Key</code>.</p>`,
			`<p>Sagesse authenticates with <code>X-Tacit-Key</code>.</p>`},
		"a fenced block": {
			`<pre><code class="lang-bash">TACIT_API_KEY=… # OpenTacit</code></pre><p>OpenTacit.</p>`,
			`<pre><code class="lang-bash">TACIT_API_KEY=… # OpenTacit</code></pre><p>Sagesse.</p>`},
		"an element whose name merely starts with pre": {
			`<precise>OpenTacit</precise>`,
			`<precise>Sagesse</precise>`},
		"an attribute that reads like prose": {
			`<img src="/a.png" alt="OpenTacit at work">`,
			`<img src="/a.png" alt="OpenTacit at work">`},
	} {
		if got := renameProductHTML(tc.in); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", what, got, tc.want)
		}
	}
}

// Serving the guide is the whole point, so the invariants are checked on a real
// response rather than on a hand-written fragment: the heading renamed, the id
// it was derived from unmoved, the link still pointing at it, and the commands
// still runnable.
//
// The id carries the AUTHORED name, not the configured one, and that is the
// invariant rather than an accident of this fixture: the renamer skips markup,
// so an anchor is stable across every deployment whatever each one calls the
// product. A link written in the guide works in all of them. Changing the
// authored word does move the anchor — TestGuideAnchorsResolve in internal/ui
// is what catches the links when it does.
func TestServedGuideRenamesProseAndNothingElse(t *testing.T) {
	t.Setenv(product.EnvKey, "Sagesse")
	_, ts := newServer(t)
	page := get(t, ts.URL+"/docs/user-guide/40-administration/14-configure-the-registry.md")

	for what, want := range map[string]string{
		"the renamed heading":           `Publish through the Sagesse proxy`,
		"the id it was derived from":    `id="publish-through-the-opentacit-proxy"`,
		"the link that points at it":    `href="#publish-through-the-opentacit-proxy"`,
		"the header a client sends":     `X-Tacit-Key`,
		"the variable an operator sets": `TACIT_API_KEY`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the served guide is missing %s (%q)", what, want)
		}
	}
	if strings.Contains(page, "X-Sagesse-Key") || strings.Contains(page, "SAGESSE_") {
		t.Error("the rename reached an identifier; the guide now documents a system nobody is running")
	}

}

// The guide opened with a picture of the wordmark, which no rename could reach —
// the name was typeset into the SVG at a hand-placed coordinate. It is markup
// now, so it takes the setting like everything else on the page.
func TestGuideMastheadIsTheLiveWordmark(t *testing.T) {
	t.Setenv(product.EnvKey, "Sagesse")
	_, ts := newServer(t)
	page := get(t, ts.URL+"/docs/user-guide")

	if strings.Contains(page, "hero.svg") {
		t.Error("the masthead is still an image, so the name in it cannot follow the setting")
	}
	for what, want := range map[string]string{
		"the lockup":             `<p class="doc-masthead"`,
		"the configured name":    `<span class="wordmark">Sagesse</span>`,
		"the character count":    `--name-len:7`,
		"the fit pass's ceiling": `data-wordmark="4.5"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the guide masthead is missing %s (%q)", what, want)
		}
	}
}

// stripShell drops the page chrome, which legitimately carries the configured
// name, so a test about document CONTENT is not answered by the wordmark.
func stripShell(page string) string {
	i := strings.Index(page, `<main`)
	if i < 0 {
		return page
	}
	return page[i:]
}
