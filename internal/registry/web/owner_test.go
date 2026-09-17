// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// ownerServer is a registry with one member and no identity provider.
func ownerServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, ts := newServer(t)
	srv.Cfg.AuthMode = config.AuthOwner
	srv.Cfg.OwnerSecret = "an-owner-secret"
	return srv, ts.URL
}

func noRedirects() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestAnOwnerLinkSignsTheOwnerIn(t *testing.T) {
	_, base := ownerServer(t)
	resp, err := noRedirects().Get(OwnerLink(base, "an-owner-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want a redirect into the dashboard", resp.StatusCode)
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			got = c
		}
	}
	if got == nil || got.Value == "" {
		t.Fatalf("cookies = %v, want a session", resp.Cookies())
	}
	if !got.HttpOnly {
		t.Error("the session cookie is readable by script")
	}
}

func TestALinkSignedWithAnotherSecretIsRefused(t *testing.T) {
	_, base := ownerServer(t)
	resp, err := noRedirects().Get(OwnerLink(base, "not-the-owner-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want a refusal", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("a bad link minted a session")
		}
	}
}

func TestOwnerModeGatesTheDashboard(t *testing.T) {
	srv, base := ownerServer(t)
	if !srv.authRequired() {
		t.Fatal("owner mode does not require a signed-in viewer")
	}
	// A browser mutation with no session must refuse, exactly as it does with
	// an identity provider configured.
	resp, err := noRedirects().PostForm(base+"/settings", url.Values{"name": {"swept"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		t.Errorf("status = %d, want a refusal without a session", resp.StatusCode)
	}
}

func TestWithNoOwnerSecretThereIsNoOwnerRoute(t *testing.T) {
	// A mode with no secret cannot let anyone in, and a secret left behind by
	// an earlier run must not turn a gate on by itself.
	srv, ts := newServer(t)
	srv.Cfg.AuthMode = config.AuthOwner
	if srv.authRequired() {
		t.Error("owner mode with no secret claims to be a gate")
	}
	resp, err := noRedirects().Get(ts.URL + "/auth/owner?t=anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want the route absent", resp.StatusCode)
	}
}

func TestAnOwnerLinkStopsWorking(t *testing.T) {
	// It is printed to a console, and consoles are scrolled, copied and kept.
	if ownerLinkTTL > 30*60*1000*1000*1000 {
		t.Errorf("ownerLinkTTL = %s, too long for something printed to a terminal", ownerLinkTTL)
	}
	if !strings.Contains(OwnerLink("https://x.example", "s"), "/auth/owner?t=") {
		t.Error("the link does not carry its token")
	}
}

func TestOwnerModeGatesThePagesAndNotJustTheForms(t *testing.T) {
	// The page gate keyed on "is OIDC configured" while the action gates keyed
	// on "is a viewer required", so owner mode refused every form and served
	// every page — the settings page, with the registry's own configuration on
	// it, to anyone who found the address.
	_, base := ownerServer(t)
	resp, err := noRedirects().Get(base + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := readAll(t, resp)
	if strings.Contains(body, "Global Access") {
		t.Error("the settings page rendered for a visitor with no session")
	}
	if !strings.Contains(body, "tacit dashboard") {
		t.Errorf("the front door does not say how to get in:\n%s", firstKB(body))
	}
	if strings.Contains(body, `href="/auth/login`) {
		t.Error("the door offers an identity provider this registry does not have")
	}
}

func TestASignedInOwnerSeesTheDashboard(t *testing.T) {
	_, base := ownerServer(t)
	jar := noRedirects()
	resp, err := jar.Get(OwnerLink(base, "an-owner-secret"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session to carry")
	}
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.AddCookie(cookie)
	got, err := jar.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	if body := readAll(t, got); strings.Contains(body, "tacit dashboard") {
		t.Error("a signed-in owner was shown the front door")
	}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func firstKB(s string) string {
	if len(s) > 1024 {
		return s[:1024]
	}
	return s
}

func TestASignInLinkKeepsWorkingUntilItExpires(t *testing.T) {
	// Not a wish — a property. The token is a signed payload and nothing
	// records that one has been spent, so a second use succeeds. The test
	// exists so that if anyone ever makes these single-use, they change the
	// words in the same commit rather than leaving a comment that lies.
	_, base := ownerServer(t)
	link := OwnerLink(base, "an-owner-secret")
	for i := 1; i <= 2; i++ {
		resp, err := noRedirects().Get(link)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("use %d: status = %d, want the same answer both times", i, resp.StatusCode)
		}
	}
}

func TestTheOwnerIsTheAdminOfTheirOwnRegistry(t *testing.T) {
	// A single-member registry whose operator cannot open its settings is a
	// dashboard that shows them their configuration and refuses to let them
	// touch it. isAdmin used to return false whenever OIDC was absent, which
	// is always, in owner mode.
	srv, _ := ownerServer(t)
	owner := oidc.Claims{"sub": "owner", "owner": true}
	if !srv.isAdmin(owner) {
		t.Error("the owner is not an admin of their own registry")
	}
	// A session without the owner claim is not one — a forged cookie carrying
	// only a subject must not become an operator.
	if srv.isAdmin(oidc.Claims{"sub": "someone"}) {
		t.Error("a session with no owner claim was treated as an admin")
	}
	if srv.isAdmin(nil) {
		t.Error("no session is an admin")
	}
}

func TestAnOpenRegistryHasNoOwnerAdmin(t *testing.T) {
	// Owner mode off: the claim means nothing, however it got into a cookie.
	srv, _ := newServer(t)
	if srv.isAdmin(oidc.Claims{"sub": "owner", "owner": true}) {
		t.Error("an owner claim made somebody an admin of a registry with no owner mode")
	}
}

func TestSettingsFormsAreProtectedWithoutAnIdentityProvider(t *testing.T) {
	// The CSRF token hung off the OIDC secret, so in owner mode there was none
	// and every form was refused as stale. It hangs off whichever secret signs
	// this registry's sessions.
	srv, base := ownerServer(t)
	resp, err := noRedirects().Get(OwnerLink(base, "an-owner-secret"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session")
	}
	req, _ := http.NewRequest("GET", base+"/settings", nil)
	req.AddCookie(cookie)
	if got := srv.csrfToken(req); got == "" {
		t.Error("no CSRF token for an owner session; every settings form would be refused")
	}
}

func TestSettingsDoNotTellTheOwnerThereIsNoSignIn(t *testing.T) {
	// The page had three more OIDC-shaped assumptions after isAdmin: the
	// read-only banner, the Administrators chip, and the members-page caution.
	// All three told the one person who had just signed in that nobody could.
	_, base := ownerServer(t)
	resp, err := noRedirects().Get(OwnerLink(base, "an-owner-secret"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session")
	}
	req, _ := http.NewRequest("GET", base+"/settings", nil)
	req.AddCookie(cookie)
	got, err := noRedirects().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Body.Close()
	body := readAll(t, got)

	if !strings.Contains(body, `name="llm_key"`) {
		t.Error("the model key is not editable by the owner")
	}
	if strings.Contains(body, "Read-only") {
		t.Error("the owner is told the page is read-only")
	}
	if strings.Contains(body, "no sign-in") {
		t.Errorf("the Administrators chip says there is no sign-in on a registry that has one")
	}
	if !strings.Contains(body, ">owner<") {
		t.Error("the Administrators chip does not name the owner")
	}
}
