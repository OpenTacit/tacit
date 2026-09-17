// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// registryRouteGlob finds the files where the registry groups its HTTP surface.
// This test reads them as text rather than importing the package: internal/registry/
// web imports THIS package (for the publish tunnel), so importing it back — even
// from a test — would be a cycle. Reading the source is also the honest scope:
// what matters is the route table as written, not what one build happens to hold.
const registryRouteGlob = "../registry/web/routes_*.go"

// muxRoute matches one registration in that file: mux.HandleFunc("GET /path", …)
// or the method-less form.
var muxRoute = regexp.MustCompile(`mux\.Handle(?:Func)?\("(?:(?:GET|POST|PUT|DELETE|PATCH|HEAD) )?(/[^"]*)"`)

// wildcardSeg matches a ServeMux path wildcard ({id}, {id...}, {$}).
var wildcardSeg = regexp.MustCompile(`\{[^}]*\}`)

// The ingress forwards only paths on an allowlist, and that allowlist is a copy
// of the registry's route table kept in another program. Copies drift, and this
// one did: every /admin/ form — "Accept", editing a technique, renaming a
// tag — and the demo dataset switch were missing, so a registry published through
// the ingress had a dashboard that looked complete and could not act on anything.
// The 404 came from the ingress, before the tunnel, which is why nothing in the
// registry's own logs showed it.
//
// Whoever adds a route to the registry next will not think about this file. So
// the check is here instead: every route the registry registers must be one the
// proxy will forward.
func TestEveryRegistryRouteReachesTheTunnel(t *testing.T) {
	paths, err := filepath.Glob(registryRouteGlob)
	if err != nil {
		t.Fatalf("find registry route files: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no registry route files match %s", registryRouteGlob)
	}
	s := &Server{Cfg: Config{AllowPrefixes: DefaultAllowPrefixes}}

	found, refused := 0, map[string]bool{}
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read registry routes at %s: %v", path, err)
		}
		for _, m := range muxRoute.FindAllStringSubmatch(string(src), -1) {
			found++
			if !s.pathAllowed(probePath(m[1])) {
				refused[m[1]] = true
			}
		}
	}
	// A regex that matched nothing would pass this test while checking nothing.
	if found < 50 {
		t.Fatalf("found only %d routes in %s; the registration form must have changed", found, registryRouteGlob)
	}
	if len(refused) > 0 {
		var names []string
		for p := range refused {
			names = append(names, p)
		}
		sort.Strings(names)
		t.Fatalf("the proxy will not forward %d of the registry's %d routes:\n  %s\n"+
			"Each would 404 at the ingress with the registry never asked. Add the prefix "+
			"to DefaultAllowPrefixes.", len(names), found, strings.Join(names, "\n  "))
	}
}

// probePath turns a route pattern into a concrete path to test the allowlist
// with: wildcards become an ordinary segment, and the root pattern becomes "/".
func probePath(pattern string) string {
	if pattern == "/{$}" {
		return "/"
	}
	return wildcardSeg.ReplaceAllString(pattern, "x")
}

// The three the operator actually hit, named so a regression reads as itself
// rather than as a count in the test above.
func TestTheFormsAndTheFaviconAreForwardable(t *testing.T) {
	s := &Server{Cfg: Config{AllowPrefixes: DefaultAllowPrefixes}}
	for _, p := range []string{
		"/admin/techniques/promote/some-technique-id", // "Accept" on a draft
		"/demo/switch", // choosing a demo dataset
		"/favicon.ico", // asked for on a cold visit whatever a page declares
	} {
		if !s.pathAllowed(p) {
			t.Errorf("the proxy refuses %s, so it 404s before the registry sees it", p)
		}
	}
	// The allowlist still has a job: it is not a general-purpose proxy.
	for _, p := range []string{"/etc/passwd", "/http://elsewhere.example", "/wp-login.php"} {
		if s.pathAllowed(p) {
			t.Errorf("the proxy forwards %s; the allowlist has stopped bounding anything", p)
		}
	}
}
