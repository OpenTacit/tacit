// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"github.com/opentacit/tacit/internal/registry/config"
	"net/http"
	"strings"
	"testing"
)

// The sign-in front door must never be stored by a cache.
//
// It is served at whatever gated URL was asked for, and that same URL answers
// with the member's own dashboard when a session comes with it. Declaring it
// public put the signed-out page into the reader's OWN browser cache under the
// dashboard's address, and the browser then answered from that copy — for
// max-age, and then for a day of stale-while-revalidate. The member landed on
// the sign-in screen holding a valid session.
//
// The credential downgrade in cachepolicy cannot help: a private cache does not
// make the request it would be downgrading. The zone's cookie bypass cannot help
// either — it protects the shared cache, which was never the one at fault.
func TestTheSignInFrontDoorIsNeverCacheable(t *testing.T) {
	srv, ts := newServer(t)
	srv.OIDC = nil
	srv.mutateCfg(func(c *config.Config) {
		c.AuthMode = config.AuthOwner
		c.OwnerSecret = "owner-secret-for-the-test"
	})

	for _, path := range []string{"/", "/usage/work?w=30d", "/outcomes", "/techniques"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := readAll(t, resp)
		_ = resp.Body.Close()
		if !strings.Contains(body, "sign in") && !strings.Contains(body, "Sign in") {
			continue // this path is not gated on this registry; nothing to assert
		}
		cc := resp.Header.Get("Cache-Control")
		if !strings.Contains(cc, "no-store") {
			t.Errorf("GET %s served the sign-in page with Cache-Control: %q — a browser may reuse it once the reader signs in",
				path, cc)
		}
		if resp.Header.Get("ETag") != "" {
			t.Errorf("GET %s gave the sign-in page a validator, which is a promise about a URL that answers two ways", path)
		}
	}
}

// And the page the member is actually owed must not be the cached one either:
// with a session, the same URL answers privately and with different content.
func TestTheSameURLAnswersTheMemberAndTheStrangerDifferently(t *testing.T) {
	srv, ts := newServer(t)
	srv.mutateCfg(func(c *config.Config) { c.AdminEmails = []string{"ops@example.com"} })
	member := signIn(t, srv, "ops@example.com")

	anon, err := http.Get(ts.URL + "/outcomes")
	if err != nil {
		t.Fatal(err)
	}
	anonBody := readAll(t, anon)
	_ = anon.Body.Close()

	in, err := member.Get(ts.URL + "/outcomes")
	if err != nil {
		t.Fatal(err)
	}
	inBody := readAll(t, in)
	_ = in.Body.Close()

	if anonBody == inBody {
		t.Skip("this registry is ungated, so there is no second answer to protect")
	}
	if cc := in.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("the member's own page answered %q", cc)
	}
	if cc := anon.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("the stranger's copy of a URL that also answers privately is storable: %q", cc)
	}
}
