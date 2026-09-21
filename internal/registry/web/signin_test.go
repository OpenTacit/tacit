// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/oidc"
)

// fakeIDP is an identity provider that says yes: enough of the discovery,
// token and userinfo endpoints to walk a whole sign-in without a network.
func fakeIDP(t *testing.T) *httptest.Server {
	t.Helper()
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"userinfo_endpoint":      base + "/userinfo",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub": "1", "email": "member@example.com", "name": "Member"})
	})
	idp := httptest.NewServer(mux)
	base = idp.URL
	t.Cleanup(idp.Close)
	return idp
}

// signinServer wires a registry to fakeIDP with the callback on the given host.
// Passing the test server's own host is the healthy case; passing another is the
// published-behind-a-proxy case this file exists for.
func signinServer(t *testing.T, callbackBase string) (*Server, *httptest.Server) {
	t.Helper()
	srv, ts := newServer(t)
	idp := fakeIDP(t)
	if callbackBase == "" {
		callbackBase = ts.URL
	}
	srv.Cfg.OIDCRedirectURI = callbackBase + "/auth/callback"
	srv.Cfg.SessionTTLSecs = 3600
	srv.OIDC = &oidc.Provider{
		Issuer: idp.URL, ClientID: "tacit", ClientSecret: "secret",
		Scopes: "openid email profile", Secret: []byte("test-secret"), TTL: time.Hour,
	}
	srv.OIDC.RedirectURIFunc = srv.OIDCRedirectURI
	return srv, ts
}

// cookieNamed returns a named cookie from a response, or nil.
func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// A sign-in begun on a hostname other than the callback's is moved to the
// callback's host BEFORE any state is minted.
//
// This is the bug the file is named for. The txn cookie is host-only, so a flow
// that starts on one hostname and ends on another sets its state where the
// callback can never read it: the member returns to a request with no cookie,
// gets "invalid or expired sign-in attempt", and gets it again on every retry —
// nothing about starting from the same wrong hostname ever improves. A registry
// published behind a proxy while its members still reach it by its original
// name is the ordinary way to end up here.
func TestSignInMovesToTheCallbackHostBeforeMintingState(t *testing.T) {
	_, ts := signinServer(t, "https://published.example")

	resp, err := noRedirect().Get(ts.URL + "/auth/login?next=%2Ftechniques")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login on the wrong host = %d, want 302 to the callback's host", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Host != "published.example" || loc.Path != "/auth/login" {
		t.Fatalf("Location = %q, want the callback host's own /auth/login", loc)
	}
	if loc.Query().Get("next") != "/techniques" {
		t.Errorf("the page they were reaching was dropped: next=%q", loc.Query().Get("next"))
	}
	// Nothing may be minted on this host: a cookie set here is exactly the
	// unreadable one the hop exists to avoid.
	if c := cookieNamed(resp, txnCookie); c != nil && c.Value != "" {
		t.Errorf("a txn cookie was set on the wrong host: %q", c.Value)
	}
}

// The hop happens once. A proxy that rewrites Host could otherwise make the two
// hostnames disagree forever, and a sign-in loop is worse than a sign-in error.
func TestSignInHopIsOneShot(t *testing.T) {
	_, ts := signinServer(t, "https://published.example")

	resp, err := noRedirect().Get(ts.URL + "/auth/login?" + canonParam + "=1&next=%2F")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "published.example/auth/login") {
		t.Fatalf("the hop repeated: %q", loc)
	}
}

// The whole round trip on one host: login mints the state, the callback spends
// it, and the member lands where they were going with a session.
func TestSignInRoundTrip(t *testing.T) {
	srv, ts := signinServer(t, "")
	client := noRedirect()

	resp, err := client.Get(ts.URL + "/auth/login?next=%2Ftechniques")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login = %d, want 302 to the provider", resp.StatusCode)
	}
	up, _ := url.Parse(resp.Header.Get("Location"))
	state := up.Query().Get("state")
	if state == "" {
		t.Fatal("no state on the authorize URL")
	}
	// An ordinary sign-in does not make everyone re-pick their account.
	if p := up.Query().Get("prompt"); p != "" {
		t.Errorf("prompt=%q on an ordinary sign-in; that is a tap for every member", p)
	}
	txn := cookieNamed(resp, txnCookie)
	if txn == nil || txn.Value == "" {
		t.Fatal("login set no txn cookie, so the callback has nothing to verify")
	}

	req, _ := http.NewRequest("GET", ts.URL+"/auth/callback?code=abc&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: txnCookie, Value: txn.Value})
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/techniques" {
		t.Fatalf("callback = %d -> %q, want 302 -> /techniques", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c := cookieNamed(resp, sessionCookie); c == nil || srv.OIDC.VerifySession(c.Value) == nil {
		t.Fatal("no valid session came out of a completed sign-in")
	}
	// The txn is single-use and must not outlive the attempt that spent it.
	if c := cookieNamed(resp, txnCookie); c == nil || c.MaxAge >= 0 {
		t.Error("the spent txn cookie was left in the jar")
	}
}

// ?switch=1 asks the provider for its account chooser. Without it, a provider
// with one live session signs the visitor straight back in as whoever that is —
// which is no way out for someone who arrived as the wrong identity.
func TestSwitchAsksForTheAccountChooser(t *testing.T) {
	_, ts := signinServer(t, "")

	resp, err := noRedirect().Get(ts.URL + "/auth/login?" + switchParam + "=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	up, _ := url.Parse(resp.Header.Get("Location"))
	if got := up.Query().Get("prompt"); got != "select_account" {
		t.Fatalf("prompt = %q, want select_account", got)
	}
}

// A callback that cannot be verified answers with a way forward, not a JSON dead
// end. What the member used to get was {"error":"invalid or expired sign-in
// attempt"} — no button, no explanation, and nothing to distinguish it from a
// broken registry.
func TestFailedCallbackOffersARetry(t *testing.T) {
	_, ts := signinServer(t, "")

	// No txn cookie: the shape every cross-host sign-in used to arrive in.
	resp, err := noRedirect().Get(ts.URL + "/auth/callback?code=abc&state=whatever")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("failed callback = %d, want 400", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want HTML — a person is reading this", ct)
	}
	if strings.Contains(body, `{"error"`) {
		t.Error("the raw JSON dead end is still being served")
	}
	// The retry has to be able to change identity, or it is not a retry for the
	// member who signed in as the wrong person.
	if !strings.Contains(body, "/auth/login?next=") || !strings.Contains(body, switchParam+"=1") {
		t.Error("no sign-in link offering the account chooser on the failure page")
	}
	// And it must not send them back into the callback they just failed at.
	if strings.Contains(body, "next=%2Fauth%2Fcallback&"+switchParam) {
		t.Error("the retry link returns to the bare callback URL, which fails again")
	}
	if c := cookieNamed(resp, txnCookie); c == nil || c.MaxAge >= 0 {
		t.Error("a failed attempt left its txn cookie behind")
	}
}

// A provider that refuses (a declined consent, a blocked account) gets the same
// treatment: cleared state, and a page with a way back.
func TestProviderErrorIsRecoverable(t *testing.T) {
	_, ts := signinServer(t, "")

	resp, err := noRedirect().Get(ts.URL + "/auth/callback?error=access_denied")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("provider error = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "/auth/login?next=") {
		t.Error("no way back from a refused sign-in")
	}
	if c := cookieNamed(resp, txnCookie); c == nil || c.MaxAge >= 0 {
		t.Error("a refused attempt left its txn cookie behind")
	}
}

// next never re-enters the flow: a sign-in offered on a failed callback page
// would otherwise return the member to that same bare callback URL and fail a
// second time, which reads exactly like the loop this all came from.
func TestNextNeverReEntersTheFlow(t *testing.T) {
	_, ts := signinServer(t, "")

	resp, err := noRedirect().Get(ts.URL + "/auth/login?next=%2Fauth%2Fcallback")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	txn := cookieNamed(resp, txnCookie)
	if txn == nil {
		t.Fatal("no txn cookie")
	}
	claims := oidc.Claims(nil)
	p := &oidc.Provider{Secret: []byte("test-secret")}
	claims = p.Unpack(txn.Value)
	if claims == nil {
		t.Fatal("txn cookie did not verify")
	}
	if claims["next"] != "/" {
		t.Fatalf("next = %v, want / — the flow must not send anyone back into itself", claims["next"])
	}
}

// The front door asks for a member key, which is the same act as every other
// field in the registry: paste a machine string into a box. It wore browser
// chrome — a default input with a default border, on the one page a member sees
// before anything else — so it borrows the shared field instead.
func TestTheMemberKeyDoorUsesTheRegistrysOwnField(t *testing.T) {
	form := memberKeyForm()
	if !strings.Contains(form, `<div class="inline-form"><input id="member-key"`) {
		t.Error("the member key field does not use the shared inline form")
	}
	if !strings.Contains(form, `<button type="submit">Sign in</button></div>`) {
		t.Error("the button is not in the row with the field it submits")
	}
	// The caption and the hint stay outside the row: a flex row would lay them
	// out beside the field rather than above and below it.
	row := strings.Index(form, `<div class="inline-form">`)
	if i := strings.Index(form, `<label for="member-key">`); i < 0 || i > row {
		t.Error("the caption was pulled into the field's row")
	}
	if i := strings.Index(form, `<p class="signin-hint">`); i < strings.Index(form, `</div>`) {
		t.Errorf("the hint sits inside the field's row (at %d)", i)
	}
	if !strings.Contains(appCSS, ".signin-key{display:flex;flex-direction:column;") {
		t.Error("the door's own stacking is gone, and the caption sits on the field")
	}
}
