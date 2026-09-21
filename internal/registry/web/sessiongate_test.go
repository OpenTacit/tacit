// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// routeDecl finds a mounted route in the route-table source. The pattern is the
// literal a handler is registered under, so reading it here means the sweep
// below covers routes that do not exist yet: add a browser POST tomorrow and
// this test starts asserting on it the same day.
var routeDecl = regexp.MustCompile(`HandleFunc\("POST (/[^"]*)"`)

// wildcard matches a ServeMux path variable, e.g. {id...} or {action}.
var wildcard = regexp.MustCompile(`\{[^}]*\}`)

// browserPostRoutes reads the two route tables that mount browser-facing
// handlers and returns one concrete request path per POST route. The /v1
// prefix is left out: that is the key-authed API, gated by keyAuthed and
// rootKeyAuthed and covered by its own tests. Everything else here is a form a
// browser can submit, and a browser cannot send X-Tacit-Key — the session is
// the only thing standing in front of it.
func browserPostRoutes(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, file := range []string{"routes_service.go", "routes_pages.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range routeDecl.FindAllStringSubmatch(string(src), -1) {
			pattern := m[1]
			if strings.HasPrefix(pattern, "/v1/") {
				continue
			}
			path := wildcard.ReplaceAllString(pattern, "x")
			if seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	if len(out) < 15 {
		t.Fatalf("only %d browser POST routes found; the route tables moved", len(out))
	}
	sort.Strings(out)
	return out
}

// techniqueState is the whole store folded to a string, so the sweep can say
// "nothing changed" about every technique at once.
func techniqueState(t *testing.T, s *Server) string {
	t.Helper()
	techniques, err := s.Store.ListTechniques(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range techniques {
		lines = append(lines, fmt.Sprintf("%s|%s|%d|%s", c.ID, c.Status, c.Version, strings.Join(c.Tags, ",")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestBrowserPostRoutesRefuseWithoutSession is the structural gate on the whole
// browser mutation surface. With OIDC on and no session cookie, every POST a
// browser can reach must refuse — and refuse before it does any work.
//
// Status alone is not enough evidence: several of these handlers answer a
// refusal and a success with the same 302, so the test also asserts the store
// is byte-identical afterwards. A handler that skipped the gate and rewrote
// tags would still redirect, and would still be caught.
//
// This is the test that guards handlers nobody has written yet: the route list
// comes from the route tables, so a new POST is swept the moment it is mounted.
func TestBrowserPostRoutesRefuseWithoutSession(t *testing.T) {
	srv, ts := newServer(t)
	seedTagTechniques(t, srv)
	// OIDC on, no upstream needed: nothing here completes a sign-in, and an
	// unverifiable cookie is exactly what an anonymous caller has.
	srv.OIDC = &oidc.Provider{
		Issuer: "https://idp.example", ClientID: "tacit", ClientSecret: "secret",
		RedirectURI: ts.URL + "/auth/callback", Scopes: "openid email",
		Secret: []byte("test-secret"), TTL: time.Hour,
	}

	before := techniqueState(t, srv)

	// One form body carrying the field names every one of these handlers reads.
	// A handler that ran instead of refusing would find real work to do: `from`
	// and `tag` name a tag the seeded techniques actually carry.
	form := url.Values{
		"from": {"audit"}, "to": {"merged"}, "tag": {"audit"},
		"id": {"t1"}, "do": {"publish"}, "channels": {"public"},
		"name": {"swept"}, "feed_url": {"https://example.invalid/feed.json"},
		"label": {"swept"}, "to_dataset": {"x"}, "proposal": {"0"},
		"into_0": {"merged"}, "src_0": {"audit"},
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	routes := browserPostRoutes(t)
	for _, path := range routes {
		resp, err := client.PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.Errorf("POST %s with no session = %d; a browser mutation must refuse", path, resp.StatusCode)
		}
	}
	if after := techniqueState(t, srv); after != before {
		t.Errorf("an unsigned-in sweep of %d routes changed the store:\nbefore:\n%s\nafter:\n%s",
			len(routes), before, after)
	}
	t.Logf("swept %d browser POST routes", len(routes))
}

// TestOIDCOffLeavesBrowserPostsOpen holds the other half of the rule. A local
// pilot with no identity provider configured is deliberately open: the gate
// asks "is OIDC on and is there no session", and with OIDC off it must never
// refuse. Losing this would lock every pilot registry out of its own dashboard.
func TestOIDCOffLeavesBrowserPostsOpen(t *testing.T) {
	srv, ts := newServer(t)
	if srv.OIDC != nil {
		t.Fatal("test registry should start with OIDC off")
	}
	if err := srv.Store.UpsertTechnique(models.Technique{
		ID: "open-pilot", Name: "Open pilot", Status: "stable", Tags: []string{"pilot-tag"},
	}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/admin/tags/delete", url.Values{"tag": {"pilot-tag"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got, ok, err := srv.Store.GetTechnique("open-pilot")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("OIDC off should leave the action open; tags = %v", got.Tags)
	}
}
