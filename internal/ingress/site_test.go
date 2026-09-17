// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/oidc"
	"github.com/opentacit/tacit/internal/ui"
)

// siteMarker is a structural class from the project page rather than a line of its
// copy: the headline is rewritten as the pitch is sharpened, and a test that
// fails on a better headline is a test that discourages writing one.
const siteMarker = `class="site-stage"`

// apex issues a request to the public listener as if it arrived for the zone's
// apex. Redirects are not followed: where the apex sends somebody is itself an
// assertion. cookies, when given, are sent as-is — this is how a signed-in
// operator's request is simulated without a live identity provider.
func (h *harness) apex(path string, cookies ...*http.Cookie) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest("GET", h.public.URL+path, nil)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	req.Host = h.srv.Cfg.Zone
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := *h.public.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// withIdentity configures a provider that is never dialled: everything below only
// asks whether a session verifies, and the session is minted locally. It returns a
// cookie for an admitted operator.
func (h *harness) withIdentity(email string) *http.Cookie {
	h.t.Helper()
	h.srv.Cfg.OIDCIssuer, h.srv.Cfg.OIDCClientID = "https://accounts.example.com", "console"
	h.srv.Cfg.AdminEmails = []string{"ops@example.com"}
	p := &oidc.Provider{
		Issuer: "https://accounts.example.com", ClientID: "console",
		Secret: []byte("test-secret"), TTL: time.Hour,
	}
	h.srv.OIDC, h.srv.SiteOIDC = p, p
	return &http.Cookie{Name: sessionCookie, Value: p.CreateSession(oidc.Claims{"email": email})}
}

// The three states of the apex, which are the whole of this feature. The middle
// one is the point: the page is reviewed at the address it will be published at,
// because a page reviewed at a different hostname has not been reviewed.
func TestApexServesTheDoorThenThePage(t *testing.T) {
	h := newHarness(t)
	operator := h.withIdentity("ops@example.com")

	// Unpublished, no session: the door, and nothing of the page.
	resp, body := h.apex("/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the apex answered %d, want the sign-in door", resp.StatusCode)
	}
	if strings.Contains(body, siteMarker) {
		t.Error("the unpublished page was served to a visitor with no session")
	}
	// The way in, and nothing else: the door's copy was cut back to the mark and
	// the button, so what a stranger with no session is owed here is a way to
	// sign in — the reason lives on the refusal below, which is where somebody
	// who signed in and still cannot see the page needs it.
	if !strings.Contains(body, "/auth/login") {
		t.Errorf("want the door's way in, got:\n%.400s", body)
	}

	// Unpublished, signed in: the page as it will publish, and the escape key as
	// the way out — the only operator chrome there is.
	resp, body = h.apex("/", operator)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, siteMarker) {
		t.Fatalf("an operator got %d and no page:\n%.300s", resp.StatusCode, body)
	}
	if !strings.Contains(body, ui.SiteSignOutHotkey("/auth/logout")) {
		t.Error("the operator's page carries no sign-out key — there is no way out of the session")
	}

	// Published: the page, to anybody, with none of the operator's chrome.
	h.srv.Cfg.SitePublish = true
	resp, body = h.apex("/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, siteMarker) {
		t.Fatalf("with the switch on a visitor got %d and no page", resp.StatusCode)
	}
	// "keydown" used to stand in for the hotkey here, and stopped being one when
	// the page gained keydown handlers of its own — the arrows that drive the
	// dashboard carousel. The stand-in is the hotkey itself, as in
	// ui.TestPublishedPageCarriesNoOperatorChrome.
	for _, leak := range []string{"/auth/logout", ui.SiteSignOutHotkey("/auth/logout")} {
		if strings.Contains(body, leak) {
			t.Errorf("the published page carries %q, which is the operator's business", leak)
		}
	}

	// Published, signed in: the key is still there, so the round trip — sign
	// out, ask again — still shows what a stranger gets.
	if _, body = h.apex("/", operator); !strings.Contains(body, ui.SiteSignOutHotkey("/auth/logout")) {
		t.Error("with the switch on the operator's copy loses the sign-out key")
	}
}

// The default is the guarantee. Deploying a binary must not put a page on the
// open internet, and this is the only thing standing between the two.
func TestApexPublishesNothingByDefault(t *testing.T) {
	if got := FromEnv().SitePublish; got {
		t.Error("TACIT_INGRESS_SITE defaults to on — a deploy would publish the page")
	}
	h := newHarness(t)
	h.withIdentity("ops@example.com")
	if _, body := h.apex("/"); strings.Contains(body, siteMarker) {
		t.Error("the apex served the page with the switch unset")
	}
}

// An account the identity provider accepts but this ingress does not is a
// different answer from "you are not signed in", and sending them round the
// sign-in loop again would only return them here.
func TestApexTellsAnUnlistedAccountWhatIsWrong(t *testing.T) {
	h := newHarness(t)
	stranger := h.withIdentity("someone@example.com")

	_, body := h.apex("/", stranger)
	if strings.Contains(body, siteMarker) {
		t.Fatal("an address outside the operator list was served the unpublished page")
	}
	if !strings.Contains(body, "does not list as an operator") {
		t.Errorf("want an explanation, got:\n%.400s", body)
	}
	if strings.Contains(body, `class="btn"`) {
		t.Error("the refusal still offers a sign-in button, which would return the same answer")
	}
	// Which account, and the way back out of it. Without the address the reader
	// cannot tell which of the several they are signed into arrived here; without
	// the link the only exit from a page with no navigation is clearing a cookie
	// by hand, and the browser will sign them straight back in as the same person.
	if !strings.Contains(body, "someone@example.com") {
		t.Errorf("the refusal does not name the account it is refusing:\n%.400s", body)
	}
	if !strings.Contains(body, `href="/auth/logout"`) {
		t.Errorf("the refusal offers no way to sign out — a locked room, not a closed "+
			"door:\n%.400s", body)
	}
}

// Signing in and out both have to happen on the apex, because a session cookie is
// host-only: one set on the console's hostname is simply absent here. That is why
// the apex has a provider of its own, and why its callback address is the apex's.
func TestApexSignsPeopleInOnItsOwnHostname(t *testing.T) {
	h := newHarness(t)
	h.withIdentity("ops@example.com")

	if got, want := h.srv.Cfg.SiteRedirectURI(), h.srv.Cfg.SiteURL()+"/auth/callback"; got != want {
		t.Errorf("SiteRedirectURI() = %q, want %q", got, want)
	}
	if strings.Contains(h.srv.Cfg.SiteRedirectURI(), h.srv.Cfg.AdminHost) {
		t.Errorf("the apex's callback points at the console host (%q) — the session would be "+
			"set on a hostname that never serves the page", h.srv.Cfg.SiteRedirectURI())
	}
	for _, path := range []string{"/auth/login", "/auth/logout"} {
		if resp, _ := h.apex(path); resp.StatusCode != http.StatusFound {
			t.Errorf("%s on the apex answered %d, want a redirect", path, resp.StatusCode)
		}
	}
	// Signing out returns to the apex's own root, which is the page — so the round
	// trip shows what a stranger gets rather than some other page's door.
	resp, _ := h.apex("/auth/logout")
	if got := resp.Header.Get("Location"); got != "/" {
		t.Errorf("signing out went to %q, want \"/\"", got)
	}
	for _, evil := range []string{"//evil.example", "https://evil.example", "javascript:alert(1)"} {
		if got := localPath(evil); got != "/" {
			t.Errorf("localPath(%q) = %q, want \"/\" — the return path is an open redirect", evil, got)
		}
	}
}

// The apex's callback is derived, and the two settings it is derived from are the
// two an ingress behind a TLS terminator gets wrong. Both mistakes produce an
// address no provider accepts, and neither is visible until a sign-in fails at
// Google naming a URI the operator never typed.
func TestDerivedCallbackWarnsWhenItCannotBeRegistered(t *testing.T) {
	good := Config{Zone: "tacit.zone", Scheme: "https", PublicPort: "443", PublicAddr: ":8443"}
	if got := SiteRedirectProblems(good); len(got) != 0 {
		t.Errorf("a correctly configured ingress warned anyway: %v", got)
	}
	if got, want := good.SiteRedirectURI(), "https://tacit.zone/auth/callback"; got != want {
		t.Errorf("SiteRedirectURI() = %q, want %q", got, want)
	}

	// TLS terminated in front, and nobody told the ingress.
	plain := Config{Zone: "tacit.zone", Scheme: "http", PublicPort: "443"}
	if got := SiteRedirectProblems(plain); len(got) != 1 || !strings.Contains(got[0], "TACIT_INGRESS_SCHEME") {
		t.Errorf("an http:// callback on a public host did not name the fix: %v", got)
	}

	// Listening on a loopback port behind that terminator.
	ported := Config{Zone: "tacit.zone", Scheme: "https", PublicAddr: "127.0.0.1:8443"}
	if got := SiteRedirectProblems(ported); len(got) != 1 || !strings.Contains(got[0], "TACIT_INGRESS_PUBLIC_PORT") {
		t.Errorf("a callback carrying the listener's port did not name the fix: %v", got)
	}

	// A laptop is neither, and must not be nagged about either.
	laptop := Config{Zone: "localhost", Scheme: "http", PublicAddr: ":8443"}
	if got := SiteRedirectProblems(laptop); len(got) != 0 {
		t.Errorf("a laptop was warned about being a laptop: %v", got)
	}
}

// The console no longer carries the page. One address in every state was the whole
// reason for giving the apex its own sign-in, and a second copy on the console
// would quietly become the one people linked to.
func TestTheConsoleDoesNotServeTheProjectPage(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SitePublish = true

	for _, path := range []string{"/project", "/site"} {
		req, _ := http.NewRequest("GET", h.public.URL+path, nil)
		req.Host = h.srv.Cfg.AdminHost
		resp, err := h.public.Client().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), siteMarker) {
			t.Errorf("the console still serves the project page at %s", path)
		}
	}
}

// The page is one document, and the assets it links have to answer on the same
// hostname it was served from — the console mux that normally serves them is on
// another host and is never reached here. This failed silently the first time: the
// page rendered unstyled, which no test of the HTML alone would notice.
func TestApexServesTheAssetsItsOwnPageLinks(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SitePublish = true

	_, body := h.apex("/")
	for _, want := range []string{`href="/assets/app.css`, `id="theme-toggle"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the project page no longer carries %q, so this test is checking the wrong asset", want)
		}
	}
	// The member-flow frames need no request of their own: they are text in the
	// document, which is the point of them being text.

	resp, css := h.apex("/assets/app.css?v=x")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the stylesheet answered %d on the apex — the page would render unstyled", resp.StatusCode)
	}
	if !strings.Contains(css, "--ring:") {
		t.Errorf("the apex served something that is not the stylesheet")
	}
	if resp, _ := h.apex("/favicon.ico"); resp.StatusCode != http.StatusOK {
		t.Errorf("the favicon answered %d on the apex", resp.StatusCode)
	}
}

// tacit.zone/install.sh is the address printed on the project page, handed to
// every new member by the console, and quoted in the README, the guide and the
// script's own header. It has to answer wherever those are read, which means two
// things this checks: it redirects to where the bytes are, and it does so whether
// or not the project page is published — the console hands the command out for
// registries that have nothing to do with the apex being live.
func TestApexRedirectsTheInstallScript(t *testing.T) {
	h := newHarness(t)

	for _, published := range []bool{false, true} {
		h.srv.Cfg.SitePublish = published
		resp, _ := h.apex("/install.sh")
		if resp.StatusCode != http.StatusFound {
			t.Errorf("SitePublish=%v: /install.sh answered %d, want 302 — the command on the "+
				"project page and in the console does not work", published, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != ui.InstallScriptURL {
			t.Errorf("SitePublish=%v: /install.sh redirects to %q, want %q",
				published, loc, ui.InstallScriptURL)
		}
	}
	// The address people are given must be the one this route answers on. They are
	// two constants and nothing else joins them.
	if want := "/install.sh"; !strings.HasSuffix(ui.InstallURL, want) {
		t.Errorf("ui.InstallURL is %q, which does not end in %q — the quoted command and "+
			"the route the apex serves have come apart", ui.InstallURL, want)
	}
}

// The apex is a page, not a tree. A path sweep against it must not reach the route
// table or the tunnel, and the answer must be cheap enough to absorb.
func TestApexAnswersOnlyItsOwnPaths(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SitePublish = true

	resp, _ := h.apex("/outcomes")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a dashboard path on the apex answered %d, want 404", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Errorf("the apex refusal is %q; a fixed sentence with nothing in it should be "+
			"cacheable so a sweep never reaches the origin twice", cc)
	}
}

// Only the visitor's copy may be cached. The door and the operator's copy are
// answered to one person and say more than a visitor's does.
func TestOnlyTheVisitorsCopyIsCacheable(t *testing.T) {
	h := newHarness(t)
	operator := h.withIdentity("ops@example.com")

	resp, _ := h.apex("/")
	if cc := resp.Header.Get("Cache-Control"); strings.Contains(cc, "public") {
		t.Errorf("the sign-in door answered %q — it disappears when the switch is thrown, "+
			"and a cached copy would outlive it", cc)
	}

	h.srv.Cfg.SitePublish = true
	resp, _ = h.apex("/", operator)
	if cc := resp.Header.Get("Cache-Control"); strings.Contains(cc, "public") {
		t.Errorf("the operator's copy answered %q — it carries their sign-out key, and the "+
			"edge must not hand that to everybody", cc)
	}
	resp, _ = h.apex("/")
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Errorf("the published page answered %q, want a public policy", cc)
	}
}

// Adding a page to the apex must not have taken a hostname away from a tenant.
func TestPublishedInstancesStillAnswerBesideTheApex(t *testing.T) {
	h := newHarness(t)
	h.srv.Cfg.SitePublish = true
	h.publish(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("the registry answered"))
	}))

	resp, body := h.get(h.publishedName, "/outcomes")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "the registry answered") {
		t.Fatalf("the instance answered %d %q", resp.StatusCode, body)
	}
}
