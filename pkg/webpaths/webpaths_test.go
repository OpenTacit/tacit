// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package webpaths

import (
	"strings"
	"testing"
)

// The public and private lists must not overlap. An overlap means a path matches
// both a bypass and the allowlist, and which one wins becomes a question about
// rule order rather than about the path.
func TestNoPathIsBothPublicAndPrivate(t *testing.T) {
	for _, p := range PublicPaths {
		if IsPrivate(p.Path) {
			t.Errorf("%q is on the public allowlist and also private", p.Path)
		}
	}
}

// Every reviewed public path carries a note saying why it is safe. The note is the
// artefact of the review, and a missing one means nobody did it.
func TestEveryPublicPathIsAnnotated(t *testing.T) {
	for _, p := range PublicPaths {
		if strings.TrimSpace(p.Note) == "" {
			t.Errorf("public path %q has no note explaining why it is public", p.Path)
		}
		if !strings.HasPrefix(p.Path, "/") {
			t.Errorf("public path %q is not absolute", p.Path)
		}
		if !p.Exact && !strings.HasSuffix(p.Path, "/") && !strings.Contains(p.Path, "user-guide") {
			t.Errorf("prefix %q does not end in a slash; it will match sibling routes by accident", p.Path)
		}
	}
}

// A routing cookie that never reached the personal set would be bypassed by
// nothing, and a cached page would outlive the switch that chose it.
func TestPersonalCookiesCoverEveryRoutingCookie(t *testing.T) {
	personal := PersonalCookies()
	for _, c := range append([]string{SessionCookieName, IngressSessionCookieName}, RoutingCookies...) {
		found := false
		for _, p := range personal {
			if p == c {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q makes a response unshareable but is not in PersonalCookies()", c)
		}
	}
}

// PersonalCookies returns a fresh slice each call. It appends to a literal, and
// an append that reallocated only sometimes would let one caller's edit reach
// the next — the kind of aliasing bug that shows up as a cookie mysteriously
// present in one configuration and absent in another.
func TestPersonalCookiesDoesNotAliasRoutingCookies(t *testing.T) {
	first := PersonalCookies()
	first[len(first)-1] = "clobbered"
	if RoutingCookies[len(RoutingCookies)-1] == "clobbered" {
		t.Fatal("PersonalCookies() aliases RoutingCookies; a caller can edit the package's own list")
	}
	if got := PersonalCookies(); got[len(got)-1] == "clobbered" {
		t.Fatal("a second call returned the clobbered slice")
	}
}

// Exactness is the difference between serving one document from the cache and
// serving a whole tree. A prefix entry has to match its children; an exact one
// must not.
func TestExactAndPrefixEntriesMatchAsDeclared(t *testing.T) {
	if !IsPublic("/assets/app.css") {
		t.Error("/assets/ is a prefix entry but does not match a file under it")
	}
	if IsPublic("/robots.txt.bak") {
		t.Error("/robots.txt is exact but matched a longer path")
	}
	if !IsPrivate("/v1/techniques") {
		t.Error("/v1/ is a private prefix but did not match a path under it")
	}
}
