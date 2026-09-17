// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/product"
	"github.com/opentacit/tacit/internal/registry/insights"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// The product name is one setting, and every surface the registry renders takes
// it. A hard-coded name left behind anywhere shows up here as the old name
// surviving a rename that changed everything around it — which is exactly the
// failure the setting exists to prevent.
func TestProductNameReachesEveryRenderedSurface(t *testing.T) {
	t.Setenv(product.EnvKey, "Renamed")
	_, ts := newServer(t)
	seedOneEvent(t, ts)

	// Each page, and what on it carries the name.
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/", []string{
			"<title>Renamed</title>",
			`aria-label="Renamed home"`,
			`apple-mobile-web-app-title" content="Renamed"`,
			"How Renamed helps", // the funnel panel
		}},
		{"/outcomes/events", []string{`aria-label="Renamed home"`}},
		{"/review", []string{`aria-label="Renamed home"`}},
		{"/learning", []string{`aria-label="Renamed home"`}},
		{"/learning/workflows", []string{`aria-label="Renamed home"`}},
		// The usage page is rendered in the browser, so the name reaches it
		// through the shell script rather than the server-rendered head.
		{"/usage", []string{`"product":"Renamed"`}},
		// Federation's public-pool note names the product too, but it renders
		// only with Global Access on, which this server does not have.
		{"/federation", nil},
		{"/manifest.webmanifest", []string{`"name": "Renamed"`, `"short_name": "Renamed"`}},
		{"/robots.txt", []string{"# Renamed registry."}},
	} {
		body := get(t, ts.URL+tc.path)
		for _, want := range tc.want {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not carry the configured name: missing %q", tc.path, want)
			}
		}
		if strings.Contains(body, product.Default) {
			t.Errorf("%s still holds the compiled-in name %q", tc.path, product.Default)
		}
	}
}

// The self-contained apps and the digest are rendered outside the HTTP handler,
// so they take the name through their own path.
func TestProductNameReachesTheStandalonePages(t *testing.T) {
	t.Setenv(product.EnvKey, "Renamed")
	for name, got := range map[string]string{
		"review app":   ReviewAppHTML(nil),
		"insights app": InsightsAppHTML([]insights.Overview{{}}, ""),
		"setup shell":  setupShell(""),
	} {
		if !strings.Contains(got, "Renamed") {
			t.Errorf("%s does not carry the configured name", name)
		}
		if strings.Contains(withoutLicenceNotice(got), product.Default) {
			t.Errorf("%s still holds the compiled-in name %q", name, product.Default)
		}
	}
}

// These pages inline the stylesheet, which carries the project's copyright
// notice — and the holder there is who owns the code, not a label on the
// product. An operator who renames their deployment does not become the author
// of the software, so the notice stays exactly as NOTICE words it and is the one
// place the compiled-in name may appear. Everything else on the page is a label
// and has to follow the setting.
func withoutLicenceNotice(page string) string {
	for _, notice := range []string{
		"/* Copyright 2026 The OpenTacit Authors */",
		"// Copyright 2026 The OpenTacit Authors",
	} {
		page = strings.ReplaceAll(page, notice, "")
	}
	return page
}

// The front door sizes its wordmark from the name it was given, so the rendered
// page has to carry both halves of that: the span the fit pass measures, and the
// character count the stylesheet falls back on. The count is of the CONFIGURED
// name — bake it into the template and it silently describes the old brand.
func TestFrontDoorSizesTheWordmarkForTheConfiguredName(t *testing.T) {
	t.Setenv(product.EnvKey, "Überblick") // 9 characters, 10 bytes
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{} // enabled, no session presented — so a gated page is the front door
	door := get(t, ts.URL+"/outcomes")

	for what, want := range map[string]string{
		"the measurable box":  `<span class="wordmark">Überblick</span>`,
		"the character count": `--name-len:9`,
		"the fit pass":        ui.WordmarkFitScript,
	} {
		if !strings.Contains(door, want) {
			t.Errorf("the front door is missing %s", what)
		}
	}
	// The lede used to carry a <br> placed for a desktop column. On a phone the
	// line had already wrapped by the time the break arrived, so it landed
	// mid-sentence; balanced wrapping puts the turn where the width is.
	start := strings.Index(door, `class="signin-lede"`)
	if start < 0 {
		t.Fatal("no lede on the front door")
	}
	lede := door[start:]
	if i := strings.Index(lede, "</p>"); i > 0 && strings.Contains(lede[:i], "<br") {
		t.Error("the lede has a hard line break again; it cannot know the width it will be read at")
	}
}

// A name with a quote and an angle bracket must not escape its context — the
// wordmark is HTML, the empty-state copy is a single-quoted JavaScript literal,
// and the manifest is JSON. Each needs its own escaping, so each is checked.
func TestProductNameIsEscapedForItsContext(t *testing.T) {
	t.Setenv(product.EnvKey, `A'<b>"c`)
	_, ts := newServer(t)

	home := get(t, ts.URL+"/")
	if !strings.Contains(home, "<title>A&#39;&lt;b&gt;&#34;c</title>") {
		t.Error("the title did not HTML-escape the name")
	}
	// The Usage page no longer pastes the name into a script literal: it rides
	// in a JSON configuration block and reaches the document through the
	// renderer's esc(). json.Marshal escapes <, > and &, so the name cannot
	// close the element it sits in whatever it says.
	usage := get(t, ts.URL+"/usage")
	if !strings.Contains(usage, `"product":"A'\u003cb\u003e\"c"`) {
		t.Error("the usage configuration did not JSON-escape the name")
	}
	if strings.Contains(usage, `A'<b>"c`) {
		t.Error("the name reached the document unescaped, markup and all")
	}
	if !strings.Contains(usageJS, "esc(PRODUCT)") {
		t.Error("the renderer no longer escapes the name for the document")
	}
	manifest := get(t, ts.URL+"/manifest.webmanifest")
	if !strings.Contains(manifest, `"name": "A'\u003cb\u003e\"c"`) {
		t.Errorf("the manifest is not valid JSON for this name:\n%s", manifest)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d", url, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// The same rule the project page is held to (ui.TestSiteWritesNoIndefiniteArticle…),
// applied to what the registry renders: no "a" or "an" immediately before the
// product name. The article follows the sound of the word after it, so copy that
// reads correctly under one name reads as a typo under the next.
func TestNoIndefiniteArticleBeforeTheName(t *testing.T) {
	for _, name := range []string{"Aardvark", "Tacit"} {
		t.Setenv(product.EnvKey, name)
		_, ts := newServer(t)
		for _, path := range []string{"/", "/outcomes/events", "/review", "/usage", "/learning"} {
			page := get(t, ts.URL+path)
			for _, article := range []string{"a", "an", "A", "An"} {
				if bad := " " + article + " " + name; strings.Contains(page, bad) {
					t.Errorf("%s writes %q — rewrite the line so no article precedes the name", path, bad)
				}
			}
		}
	}
}
