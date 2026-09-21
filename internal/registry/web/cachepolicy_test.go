// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
	"github.com/opentacit/tacit/pkg/webpaths"
)

// The registry's side of docs/distribution/cloudflare-caching.md. What is checked
// here is the origin's half of the contract: that a private response says so
// itself, that a public one carries the exact policy the guidance names and none
// of the directives it forbids, and that the two are decided by the application
// rather than by a Cloudflare rule.

// header fetches one URL and returns the response, body read and closed.
func header(t *testing.T, url string, prep func(*http.Request)) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if prep != nil {
		prep(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp
}

// signedIn puts a valid session on a request, so the "authenticated reader" half
// of every case is the real thing rather than a plausible-looking cookie.
func signedIn(srv *Server) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{
			Name:  sessionCookie,
			Value: srv.OIDC.CreateSession(oidc.Claims{"email": "member@example.com"}),
		})
	}
}

// Every route the dashboard and the API expose is private unless it is on the
// short reviewed list of public ones. The test walks the surface rather than
// naming the private routes, because the failure that matters is a route added
// later without anyone thinking about caching — and that route will be in this
// sweep on the day it lands.
func TestEveryDashboardRouteIsPrivate(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	// Reached with a session: the working dashboard, every page of it private.
	for _, path := range []string{
		"/", "/outcomes", "/outcomes/events", "/outcomes/cohorts", "/outcomes/helped-rate",
		"/learning", "/learning/workflows", "/usage", "/techniques", "/techniques/retired",
		"/techniques/map", "/techniques/tags", "/review", "/members", "/federation",
		"/settings", "/settings/models", "/setup", "/docs",
		"/v1/health", "/.well-known/tacit.json", "/usage/data", "/outcomes/events/data",
	} {
		resp := header(t, ts.URL+path, signedIn(srv))
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
			t.Errorf("%s (signed in) Cache-Control = %q, want %q", path, got, cachepolicy.PrivateControl)
		}
	}

	// And the key-authed API, which must not become cacheable merely because the
	// caller authenticated successfully.
	for _, path := range []string{
		"/v1/techniques", "/v1/events", "/v1/cohorts", "/v1/insights/app",
		"/v1/map/app", "/v1/organization/app", "/v1/review/app", "/v1/admin/usage-profile",
		"/v1/admin/subscriptions",
	} {
		resp := header(t, ts.URL+path, func(r *http.Request) { r.Header.Set("X-Tacit-Key", "test-key") })
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
			t.Errorf("%s (keyed) Cache-Control = %q, want %q", path, got, cachepolicy.PrivateControl)
		}
		if resp.StatusCode == 401 {
			t.Errorf("%s refused the test key, so this case proved nothing", path)
		}
	}
}

// The front door is not storable, and it used to be.
//
// It was the one page worth caching on a wildcard zone: what every crawler,
// scanner and cold visitor gets, the same for all of them. What that reasoning
// missed is that it is served at whatever gated URL was asked for, and those
// URLs answer with the member's own dashboard when a session comes with them.
// The credential downgrade below handles a request that carries a cookie — but a
// private cache does not make the request it would be downgrading. A browser
// that stored this page while signed out went on serving it after the reader
// signed in, and they got the sign-in screen while holding a valid session.
//
// The zone's cookie bypass never came into it: the shared cache was not the one
// at fault.
func TestSignInFrontDoorIsNotStorable(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	resp := header(t, ts.URL+"/", nil)
	if cc := resp.Header.Get("Cache-Control"); cc != cachepolicy.PrivateControl {
		t.Errorf("front door Cache-Control %q, want %q", cc, cachepolicy.PrivateControl)
	}
	if len(resp.Header.Values("Set-Cookie")) > 0 {
		t.Errorf("the front door set a cookie: %v — it hands out no identity",
			resp.Header.Values("Set-Cookie"))
	}
	if resp.Header.Get("ETag") != "" {
		t.Error("the front door carries a validator, which is a promise about a URL that answers two ways")
	}

	// The same URL with a session is the dashboard, and private.
	resp = header(t, ts.URL+"/", signedIn(srv))
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("signed-in / = %q, want %q", got, cachepolicy.PrivateControl)
	}
}

// Two anonymous visitors asking for the same tenant URL must get the same bytes.
// This is the invariant that makes bucket A safe at all, so it is checked on the
// response rather than argued about in a comment.
func TestFrontDoorIsIdenticalForEveryAnonymousVisitor(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	fetch := func(prep func(*http.Request)) (string, string) {
		req, _ := http.NewRequest("GET", ts.URL+"/", nil)
		if prep != nil {
			prep(req)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return string(body), resp.Header.Get("ETag")
	}
	first, tag1 := fetch(nil)
	// A second visitor, arriving with the header-shaped things that must not
	// change a cacheable response: a language, a user agent, a forwarded host.
	second, tag2 := fetch(func(r *http.Request) {
		r.Header.Set("Accept-Language", "fr-CA")
		r.Header.Set("User-Agent", "some-other-browser/9")
		r.Header.Set("X-Forwarded-Host", "attacker.example.com")
	})
	if first != second {
		t.Error("two anonymous visitors got different front doors; the response depends on a request header")
	}
	if tag1 != tag2 {
		t.Errorf("validators differ (%q vs %q) for the same URL", tag1, tag2)
	}
	if strings.Contains(second, "attacker.example.com") {
		t.Error("a client-controlled header reached the rendered body — cache poisoning")
	}
}

// The user guide is public and the rest of the docs tree is not, and the split
// has to show up in the cache policy and not only in the sign-in gate.
func TestUserGuideIsPublicAndTheRestIsNot(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	// The shipped tree is the user guide and nothing else, so the members-only
	// branch has no subject in the repository any more. It is still reachable —
	// an operator can point --docs at a tree of their own — so the fixture
	// supplies one rather than letting the case quietly stop testing anything.
	docs := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		full := filepath.Join(docs, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join("user-guide", "index.md"), "# Guide\n\nPublic.\n")
	mustWrite(filepath.Join("dev", "design.md"), "# Design\n\nMembers only.\n")
	srv.DocsDir = docs

	guide := header(t, ts.URL+"/docs/user-guide", nil)
	if guide.StatusCode != 200 {
		t.Fatalf("user guide status = %d", guide.StatusCode)
	}
	if !strings.Contains(guide.Header.Get("Cache-Control"), "public") {
		t.Errorf("user guide = %q, want public", guide.Header.Get("Cache-Control"))
	}
	if len(guide.Header.Values("Set-Cookie")) > 0 {
		t.Error("the public guide set a cookie")
	}

	// The development tree yields the front door, which is public — but as the
	// front door, not as the document. What must never happen is the members-only
	// page itself arriving with a public policy.
	dev := header(t, ts.URL+"/docs/dev/design.md", nil)
	if strings.Contains(dev.Header.Get("Cache-Control"), "public") {
		body := header(t, ts.URL+"/docs/dev/design.md", nil)
		if body.StatusCode == 200 && !strings.Contains(dev.Header.Get("Content-Type"), "text/html") {
			t.Error("a members-only doc was served with a public policy")
		}
	}
	// With a session it is the real page, and private.
	dev = header(t, ts.URL+"/docs/dev/design.md", signedIn(srv))
	if got := dev.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("members-only doc (signed in) = %q, want %q", got, cachepolicy.PrivateControl)
	}
}

// The commons feed is the one piece of application data that is public by design,
// and only while the commons is actually being served. Every other channel is
// somebody's, including the descriptor that lists them.
func TestFederationFeedPolicyFollowsTheChannel(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, true
	seedRanked(t, srv, "cache-proven", 40, 50)
	srv.RecomputePublicChannel()

	pub := header(t, ts.URL+"/f/public/feed.json", nil)
	if pub.StatusCode != 200 {
		t.Fatalf("commons feed status = %d", pub.StatusCode)
	}
	if !strings.Contains(pub.Header.Get("Cache-Control"), "public") {
		t.Errorf("commons feed = %q, want public", pub.Header.Get("Cache-Control"))
	}

	// Not confirmed: computed but withheld, so not cacheable either.
	srv.Cfg.GlobalAccessConfirmed = false
	staged := header(t, ts.URL+"/f/public/feed.json", nil)
	if strings.Contains(staged.Header.Get("Cache-Control"), "public") {
		t.Errorf("a staged commons must not be cacheable, got %q", staged.Header.Get("Cache-Control"))
	}
	srv.Cfg.GlobalAccessConfirmed = true

	// A gated channel answers 404 to an unauthorized reader, and the refusal is
	// private: a cached 404 would outlive the token that fixes it.
	gated := header(t, ts.URL+"/f/acme-migration/feed.json", nil)
	if got := gated.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("gated feed = %q, want %q", got, cachepolicy.PrivateControl)
	}

	// The descriptor filters channels by the caller's token, so its body depends
	// on a request header and it can never be cacheable.
	desc := header(t, ts.URL+"/.well-known/tacit.json", nil)
	if got := desc.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("descriptor = %q, want %q", got, cachepolicy.PrivateControl)
	}
}

// A technique in the commons is a published document; a technique in any other channel is
// not, and the canonical-technique path is where that distinction is easiest to lose.
func TestCanonicalTechniquePolicyFollowsItsChannels(t *testing.T) {
	srv, ts := newServer(t)
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, true
	// Membership of the commons is computed from measured outcomes, not set by
	// hand, so the public case is seeded the way the ranking actually admits a
	// technique. The gated case is an explicit channel, which is how an operator
	// publishes to one.
	seedRanked(t, srv, "cache-open", 40, 50)
	closed := models.Technique{ID: "cache-closed", Name: "Closed", Status: "stable",
		Channels: []string{"acme-private"}}
	if err := srv.Store.UpsertTechnique(closed); err != nil {
		t.Fatal(err)
	}
	srv.RecomputePublicChannel()

	resp := header(t, ts.URL+"/f/techniques/cache-open.md", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("a technique in the commons is not being served: %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
		t.Errorf("a technique in the commons = %q, want public", resp.Header.Get("Cache-Control"))
	}
	resp = header(t, ts.URL+"/f/techniques/cache-closed.md", nil)
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("a technique in a gated channel = %q, want %q", got, cachepolicy.PrivateControl)
	}
}

// Fingerprinted URLs are cached for a year; the same bytes at a bare URL are not.
// The difference is the promise the URL makes, and it is the only thing that
// earns bucket C.
func TestAssetPolicies(t *testing.T) {
	_, ts := newServer(t)

	for _, path := range []string{
		"/assets/app.css?v=" + appCSSHash[:12],
		ui.BackdropURL(), // the scene is shared with the ingress and carries its own hash
		"/assets/techniquemap.js?v=" + techniqueMapJSHash[:12],
	} {
		resp := header(t, ts.URL+path, nil)
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.ImmutableControl {
			t.Errorf("%s = %q, want %q", path, got, cachepolicy.ImmutableControl)
		}
	}
	for _, path := range []string{"/assets/app.css", "/assets/backdrop.js", "/assets/techniquemap.js"} {
		cc := header(t, ts.URL+path, nil).Header.Get("Cache-Control")
		if strings.Contains(cc, "immutable") {
			t.Errorf("%s claims immutability without a fingerprint: %q", path, cc)
		}
		if strings.Contains(cc, "no-cache") {
			t.Errorf("%s answers no-cache, which is forbidden on a public response: %q", path, cc)
		}
		if !strings.Contains(cc, "public") {
			t.Errorf("%s = %q, want the short public policy", path, cc)
		}
	}

	// The chrome every tenant shares: public, and the same on all of them.
	for _, path := range []string{"/favicon.ico", "/apple-touch-icon.png", "/manifest.webmanifest", "/robots.txt"} {
		resp := header(t, ts.URL+path, nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s status = %d", path, resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
			t.Errorf("%s = %q, want public", path, resp.Header.Get("Cache-Control"))
		}
	}

	// A signed-in member keeps the immutable assets — the fingerprint makes them
	// reader-independent, and re-fetching the typefaces on every page would buy
	// nobody any privacy.
	srv, ts2 := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}
	resp := header(t, ts2.URL+"/assets/app.css?v="+appCSSHash[:12], signedIn(srv))
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.ImmutableControl {
		t.Errorf("a fingerprinted asset with a session = %q, want %q", got, cachepolicy.ImmutableControl)
	}
}

// The protocol description is the release's, identical on every tenant.
func TestSchemasArePublic(t *testing.T) {
	_, ts := newServer(t)
	for _, path := range []string{"/v1/openapi.yaml", "/v1/schemas/technique.schema.json"} {
		resp := header(t, ts.URL+path, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
			t.Errorf("%s = %q, want public", path, resp.Header.Get("Cache-Control"))
		}
	}
	// An unknown schema is a real 404, and private.
	resp := header(t, ts.URL+"/v1/schemas/not-a-schema", nil)
	if resp.StatusCode != 404 {
		t.Errorf("unknown schema status = %d, want 404", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("unknown schema = %q, want %q", got, cachepolicy.PrivateControl)
	}
}

// Cache deception: a path wearing an image extension must not come back as an
// application page with a public policy. The registry answers a real 404, and
// the ingress refuses the path before the tunnel (TestCacheDeceptionPathsAreRefused
// there); Cloudflare's Cache Deception Armor is the third layer, not the first.
func TestMisleadingExtensionsDoNotYieldCacheableAppHTML(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	for _, path := range []string{
		"/techniques/foo.jpg", "/outcomes/foo.jpg", "/drafts/foo.css",
		"/techniques/map/foo.jpg", "/review/foo.png",
	} {
		// Signed in, where the page would otherwise carry the reader's own shell.
		resp := header(t, ts.URL+path, signedIn(srv))
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
			t.Errorf("%s (signed in) = %q, want %q", path, got, cachepolicy.PrivateControl)
		}
		if resp.StatusCode == 200 {
			t.Errorf("%s returned 200; an unknown child path must 404", path)
		}
	}
}

// An unknown top-level path is a real 404 and never the parent page.
func TestUnknownPathsAre404(t *testing.T) {
	_, ts := newServer(t)
	for _, path := range []string{"/dashboard/foo.jpg", "/nope", "/nope/deeper", "/v1/nope"} {
		resp := header(t, ts.URL+path, nil)
		if resp.StatusCode != 404 {
			t.Errorf("%s status = %d, want 404", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
			t.Errorf("%s = %q, want %q", path, got, cachepolicy.PrivateControl)
		}
	}
}

// The session cookie is host-only. A Domain=.tacit.zone cookie would be sent to
// every tenant in the fleet, which would make one member's login bypass the cache
// on every sibling hostname and quietly weaken the isolation the wildcard depends
// on.
func TestSessionCookieIsHostOnly(t *testing.T) {
	srv, _ := newServer(t)
	rec := httptest.NewRecorder()
	srv.setCookie(rec, sessionCookie, "value", 3600)
	raw := rec.Header().Get("Set-Cookie")
	if raw == "" {
		t.Fatal("no cookie written")
	}
	if strings.Contains(strings.ToLower(raw), "domain=") {
		t.Fatalf("session cookie carries a Domain: %q", raw)
	}
	for _, want := range []string{"HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(raw, want) {
			t.Errorf("session cookie %q missing %s", raw, want)
		}
	}
	if !strings.Contains(raw, "tacit_session=") {
		t.Errorf("the cookie name must stay tacit_session so one zone rule can bypass every tenant: %q", raw)
	}
}

// Anonymous visitors are not given sessions. A cookie handed to a crawler would
// make every one of its requests uncacheable and every one of ours an origin hit.
func TestAnonymousRequestsGetNoCookie(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	for _, path := range []string{
		"/", "/outcomes", "/techniques", "/docs/user-guide", "/robots.txt", "/favicon.ico",
		"/assets/app.css?v=" + appCSSHash[:12], "/v1/openapi.yaml", "/f/public/feed.json",
	} {
		resp := header(t, ts.URL+path, nil)
		if got := resp.Header.Values("Set-Cookie"); len(got) > 0 {
			t.Errorf("%s handed an anonymous visitor %v", path, got)
		}
	}
}

// /v1/health reports the classification tally, so an operator can see from the
// origin whether anything is being offered to the edge at all.
func TestHealthReportsCacheCounters(t *testing.T) {
	_, ts := newServer(t)
	header(t, ts.URL+"/robots.txt", nil) // one bucket-A response to count

	resp, err := http.Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Cache map[string]int64 `json:"cache"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Cache == nil {
		t.Fatal("health reports no cache counters")
	}
	if body.Cache["public"] < 1 {
		t.Errorf("public counter = %d after a public response (all: %v)", body.Cache["public"], body.Cache)
	}
	for _, key := range []string{"private", "immutable", "undeclared", "downgraded", "etags"} {
		if _, ok := body.Cache[key]; !ok {
			t.Errorf("health cache counters missing %q", key)
		}
	}
}

// Nothing on the surface answers with a Vary that pretends to separate cache
// entries, and nothing compresses at the origin — Cloudflare does that, and two
// compressors would only complicate the validators.
func TestNoMisleadingVaryAndNoOriginCompression(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	for _, path := range []string{"/", "/docs/user-guide", "/robots.txt", "/v1/openapi.yaml"} {
		resp := header(t, ts.URL+path, func(r *http.Request) {
			r.Header.Set("Accept-Encoding", "gzip, br")
		})
		vary := resp.Header.Get("Vary")
		for _, bad := range []string{"Cookie", "Authorization", "User-Agent"} {
			if strings.Contains(vary, bad) {
				t.Errorf("%s answers Vary: %s — Cloudflare does not key on it", path, vary)
			}
		}
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Errorf("%s compressed at the origin (%s); the edge does that", path, enc)
		}
		if cc := resp.Header.Get("Cache-Control"); strings.Contains(cc, "no-transform") {
			t.Errorf("%s sends no-transform, which disables edge compression: %q", path, cc)
		}
	}
}

// The Cloudflare allowlist and the handlers have to agree, and nothing in either
// place makes them. A path on the allowlist that the origin answers private is a
// rule that matches and never caches; a handler made public whose path is not on
// the list is a cacheable response the edge never sees. Both are silent, so both
// are checked here — and every entry needs a probe URL, so adding one to the list
// without adding it here fails.
func TestZoneRulesMatchTheApplication(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}
	srv.Cfg.GlobalAccess, srv.Cfg.GlobalAccessConfirmed = true, true
	seedRanked(t, srv, "zone-proven", 40, 50)
	srv.RecomputePublicChannel()

	// One URL per allowlist entry, because a prefix is not always a route:
	// /assets/ alone is a 404 and proves nothing.
	probes := map[string]string{
		"/robots.txt":                       "/robots.txt",
		"/favicon.ico":                      "/favicon.ico",
		"/apple-touch-icon.png":             "/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png": "/apple-touch-icon-precomposed.png",
		"/manifest.webmanifest":             "/manifest.webmanifest",
		"/assets/":                          "/assets/app.css?v=" + appCSSHash[:12],
		"/docs/user-guide":                  "/docs/user-guide",
		"/f/public/feed.json":               "/f/public/feed.json",
		"/f/techniques/":                    "/f/techniques/zone-proven.md",
	}
	for _, entry := range webpaths.PublicPaths {
		probe, ok := probes[entry.Path]
		if !ok {
			t.Errorf("allowlist entry %q has no probe URL in this test; add one", entry.Path)
			continue
		}
		resp := header(t, ts.URL+probe, nil)
		if resp.StatusCode != 200 {
			t.Errorf("%s (for allowlist entry %q) status = %d", probe, entry.Path, resp.StatusCode)
			continue
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
			t.Errorf("%s is on the Cloudflare allowlist but the origin answers %q",
				probe, resp.Header.Get("Cache-Control"))
		}
	}

	// And the other direction: nothing the allowlist covers may be a bypassed
	// path, and every path the bypass rule names must answer private.
	bypassProbes := map[string]string{
		"/v1/":          "/v1/techniques",
		"/mcp":          "/mcp",
		"/oauth/":       "/oauth/authorize",
		"/.well-known/": "/.well-known/tacit.json",
		"/auth/":        "/auth/login",
		"/admin/":       "/admin/poll-feeds",
		"/settings":     "/settings",
		"/setup":        "/setup",
		"/members":      "/members",
		"/join/":        "/join/not-a-token",
		"/usage":        "/usage",
		"/learning":     "/learning",
		"/review":       "/review",
		"/drafts":       "/drafts/not-a-draft",
		"/federation":   "/federation",
		"/demo/":        "/demo/switch",
		"/outcomes":     "/outcomes",
		"/techniques":   "/techniques",
	}
	for _, prefix := range webpaths.PrivatePathPrefixes {
		probe, ok := bypassProbes[prefix]
		if !ok {
			t.Errorf("bypass prefix %q has no probe URL in this test; add one", prefix)
			continue
		}
		if webpaths.IsPublic(probe) {
			t.Errorf("%q is bypassed and also matched by the public allowlist", probe)
		}
		resp := header(t, ts.URL+probe, signedIn(srv))
		if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
			t.Errorf("%s is on the bypass list but the origin answers %q", probe, got)
		}
	}
}

// A per-session CSRF token must never reach a cacheable document: cached once, it
// would be handed to every subsequent visitor, and it is derived from one member's
// session cookie. Here it cannot, because the derivation returns nothing without a
// session and the pages that carry it are private — but "cannot by construction"
// is worth a test, since the construction is two files apart.
func TestNoPerSessionTokenInCacheableHTML(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	for _, path := range []string{"/", "/docs/user-guide", "/techniques", "/settings"} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(resp.Header.Get("Cache-Control"), "public") {
			continue // private pages may carry one; that is what private is for
		}
		if strings.Contains(string(body), `name="csrf"`) {
			t.Errorf("%s is cacheable and embeds a CSRF token", path)
		}
	}

	// And the token itself is empty without a session, so there is nothing to
	// embed even if a page tried.
	req, _ := http.NewRequest("GET", ts.URL+"/settings", nil)
	if got := srv.csrfToken(req); got != "" {
		t.Errorf("csrfToken for a session-less request = %q, want empty", got)
	}
}

// Every HTML response announces the stylesheet it is about to need, which is what
// gives Cloudflare Early Hints something to replay. The URL has to be the
// fingerprinted one the document itself uses, or the browser fetches twice.
func TestHTMLAnnouncesItsStylesheet(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	resp := header(t, ts.URL+"/", nil)
	link := resp.Header.Get("Link")
	if !strings.Contains(link, "rel=preload") || !strings.Contains(link, "as=style") {
		t.Fatalf("Link = %q, want a stylesheet preload", link)
	}
	if !strings.Contains(link, appCSSHash[:12]) {
		t.Errorf("Link %q does not carry the content fingerprint the document uses", link)
	}
}

// Demonstration mode routes by a cookie: tacit_demo chooses which dataset answers,
// so the same URL renders different content with and without it. That makes it a
// cache-correctness problem rather than a feature flag, and it is handled in both
// halves — the origin answers such a request private, and the zone bypasses it,
// because only a bypass can stop an already-cached production page being served to
// a demo reader.
func TestDemoCookieMakesTheAnswerPrivate(t *testing.T) {
	if !slices.Contains(webpaths.RoutingCookies, DemoCookie) {
		t.Fatalf("%s routes requests but is not in webpaths.RoutingCookies %v; the zone will not bypass it",
			DemoCookie, webpaths.RoutingCookies)
	}
	// On a registry that HAS demonstration mode. Elsewhere the cookie routes
	// nothing and is ignored — TestWithoutADemoDirTheDemoCookieRoutesNothing.
	t.Setenv("TACIT_DEMO_DIR", t.TempDir())
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	// The user guide rather than the front door, which is not cacheable for
	// reasons of its own (TestSignInFrontDoorIsNotStorable).
	if got := header(t, ts.URL+"/docs/user-guide", nil).Header.Get("Cache-Control"); !strings.Contains(got, "public") {
		t.Fatalf("user guide = %q, want public", got)
	}
	// With it, the same URL is not.
	resp := header(t, ts.URL+"/docs/user-guide", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: DemoCookie, Value: "some-scenario"})
	})
	if got := resp.Header.Get("Cache-Control"); got != cachepolicy.PrivateControl {
		t.Errorf("a request carrying %s = %q, want %q", DemoCookie, got, cachepolicy.PrivateControl)
	}
}

// A public response revalidates cheaply: the second fetch with the validator is a
// 304 with no body.
func TestPublicResponsesRevalidate(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = &oidc.Provider{Secret: []byte("test-secret"), TTL: time.Hour}

	first := header(t, ts.URL+"/docs/user-guide", nil)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no validator on the user guide")
	}
	second := header(t, ts.URL+"/docs/user-guide", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match got %d, want 304", second.StatusCode)
	}
}
