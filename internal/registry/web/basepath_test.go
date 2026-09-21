// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// Serving the registry under a mount prefix (TACIT_BASE_PATH, config.BasePath):
// inbound stripping, outbound rebasing of markup and redirects, and the
// root-level OAuth discovery passthrough. docs/user-guide covers the operator
// side (proxy config).

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/embed"
	"github.com/opentacit/tacit/internal/registry/jobs"
	"github.com/opentacit/tacit/internal/registry/store"
)

const testBase = "/apps/tacit"

// newBaseServer is newServer with a mount prefix, and optionally the OIDC +
// external-URL settings that light up the OAuth discovery surface.
func newBaseServer(t *testing.T, withOIDC bool) (*Server, *httptest.Server) {
	t.Helper()
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "no-such.env"))
	cfg := config.Load()
	cfg.APIKey = "test-key"
	cfg.DataDir = t.TempDir()
	cfg.BasePath = testBase
	if withOIDC {
		cfg.ExternalURL = "https://demo.example.com" + testBase
		cfg.OIDCIssuer = "https://idp.example.com"
		cfg.OIDCClientID = "cid"
		cfg.OIDCClientSecret = "secret"
		cfg.OIDCRedirectURI = "https://demo.example.com" + testBase + "/auth/callback"
	}
	techniquesDir, _ := filepath.Abs("../../../techniques")
	docsDir, _ := filepath.Abs("../../../docs")
	cfg.TechniquesDir = techniquesDir
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	embedder, _ := embed.New(cfg.EmbedModel, cfg.EmbedDim)
	if _, _, err := jobs.Startup(st, techniquesDir, embedder); err != nil {
		t.Fatal(err)
	}
	srv := New(cfg, st, embedder, docsDir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestNormalizeBasePath(t *testing.T) {
	for in, want := range map[string]string{
		"":             "",
		"/":            "",
		"//":           "",
		"/apps/tacit":  "/apps/tacit",
		"/apps/tacit/": "/apps/tacit",
		"apps/tacit":   "/apps/tacit",
		" /x ":         "/x",
	} {
		if got := config.NormalizeBasePath(in); got != want {
			t.Errorf("NormalizeBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every URL a page emits must carry the prefix: one unprefixed href is a
// broken link on a sub-path deploy. Sweeps the main pages for any
// root-absolute attribute value that escaped the rebase.
func TestBasePathPagesRebased(t *testing.T) {
	_, ts := newBaseServer(t, false)
	attr := regexp.MustCompile(`(?:href|src|action|data-href)="(/[^"]*)"`)
	for _, p := range []string{
		"/", "/outcomes", "/techniques", "/techniques/map", "/techniques/tags", "/review",
		"/members", "/learning", "/federation", "/docs", "/settings",
	} {
		resp, err := http.Get(ts.URL + testBase + p)
		if err != nil {
			t.Fatal(err)
		}
		body := readBody(t, resp)
		if resp.StatusCode != 200 {
			t.Errorf("GET %s%s = %d", testBase, p, resp.StatusCode)
			continue
		}
		for _, m := range attr.FindAllStringSubmatch(body, -1) {
			if u := m[1]; !strings.HasPrefix(u, testBase+"/") && u != testBase {
				t.Errorf("page %s emits unprefixed URL %q", p, m[0])
			}
		}
		if !strings.Contains(body, `data-base="`+testBase+`"`) {
			t.Errorf("page %s: shell missing data-base=%q for the init scripts", p, testBase)
		}
	}
}

func TestBasePathRoutingAndRedirects(t *testing.T) {
	_, ts := newBaseServer(t, false)
	c := noRedirectClient()
	for _, tc := range []struct {
		path, wantLocation string
		wantStatus         int
	}{
		{testBase, testBase + "/", http.StatusFound},      // bare prefix -> home
		{"/", testBase + "/", http.StatusFound},           // proxy-root stray -> home
		{"/elsewhere", "", http.StatusNotFound},           // outside the mount
		{testBase + "/insights", "", http.StatusNotFound}, // legacy URLs are gone, not redirected
		{testBase + "/techniques", "", http.StatusOK},     // a real page under the mount
	} {
		resp, err := c.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		readBody(t, resp)
		if resp.StatusCode != tc.wantStatus {
			t.Errorf("GET %s = %d, want %d", tc.path, resp.StatusCode, tc.wantStatus)
		}
		if got := resp.Header.Get("Location"); got != tc.wantLocation {
			t.Errorf("GET %s Location = %q, want %q", tc.path, got, tc.wantLocation)
		}
	}
}

func TestBasePathAPIAndManifest(t *testing.T) {
	_, ts := newBaseServer(t, false)
	resp, _ := request(t, "GET", ts.URL+testBase+"/v1/health", "test-key", "")
	if resp == nil {
		t.Fatal("no health response")
	}

	mresp, err := http.Get(ts.URL + testBase + "/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	manifest := readBody(t, mresp)
	for _, want := range []string{
		`"scope": "` + testBase + `/"`,
		`"start_url": "` + testBase + `/"`,
		`"src": "` + testBase + `/apple-touch-icon.png"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %s", want)
		}
	}
}

// RFC 8414/9728: with a path-bearing resource/issuer, discovery lives at the
// ORIGIN root with the path inserted after the well-known segment. The
// operator's proxy forwards those two root paths; the handler maps them onto
// the internal discovery routes.
func TestBasePathWellKnownAtRoot(t *testing.T) {
	_, ts := newBaseServer(t, true)
	for path, want := range map[string]string{
		"/.well-known/oauth-protected-resource" + testBase + "/mcp": `"resource":"https://demo.example.com` + testBase + `/mcp"`,
		"/.well-known/oauth-protected-resource" + testBase:          `"resource":"https://demo.example.com` + testBase + `/mcp"`,
		"/.well-known/oauth-authorization-server" + testBase:        `"issuer":"https://demo.example.com` + testBase + `"`,
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readBody(t, resp)
		if resp.StatusCode != 200 {
			t.Errorf("GET %s = %d", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("GET %s: body %s missing %s", path, body, want)
		}
	}
	// The bare root form (no inserted path) belongs to whatever else the proxy
	// serves at the origin root — not this mount.
	resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("root well-known without inserted path = %d, want 404", resp.StatusCode)
	}
}

// With OIDC on, a dashboard URL renders the sign-in page: its login link must
// carry the prefix, and the session cookie the callback would set must be
// scoped to the mount.
func TestBasePathSigninAndCookieScope(t *testing.T) {
	srv, ts := newBaseServer(t, true)
	resp, err := http.Get(ts.URL + testBase + "/techniques")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `href="`+testBase+`/auth/login?next=`) {
		t.Errorf("sign-in link not rebased: %s", body)
	}

	rec := httptest.NewRecorder()
	srv.setCookie(rec, "tacit_probe", "v", 60)
	if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "Path="+testBase) {
		t.Errorf("cookie not scoped to base path: %q", c)
	}
}
