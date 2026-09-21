// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// ownerClient signs in the way an operator does — the link minted on the
// machine the registry runs on — and keeps the session it gets.
func ownerClient(t *testing.T, base, secret string) *http.Client {
	t.Helper()
	resp, err := noRedirects().Get(OwnerLink(base, secret))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			return &http.Client{Jar: &staticCookieJar{cookie: c}}
		}
	}
	t.Fatal("the owner link issued no session")
	return nil
}

// registryEnv gives the server a settings file to patch, and returns its path.
//
// It runs AFTER newServer on purpose. newServer points TACIT_REGISTRY_ENV at a
// scratch path of its own so the suite never reads the developer's registry,
// and config.Load has already run by then — which is also the state this
// control is about: a process that started before these settings existed.
func registryEnv(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TACIT_REGISTRY_ENV", path)
	return path
}

// ownerRegistry is a single-member registry with a settings file and an address
// of its own: the one state the Personal/Shared switch can be moved in.
//
// The address is part of that state, not scenery. Shared means colleagues, and a
// registry with no address a provider can return them to has nothing to
// configure — see TestSharedRefusesARegistryWithNoAddress.
func ownerRegistry(t *testing.T, env string) (*Server, *httptest.Server, string) {
	t.Helper()
	srv, ts := newServer(t)
	envPath := registryEnv(t, env)
	srv.Cfg.AuthMode = config.AuthOwner
	srv.Cfg.OwnerSecret = "an-owner-secret"
	srv.Cfg.ExternalURL = "https://tacit.example.com"
	return srv, ts, envPath
}

// discoveryOnlyIDP is an identity provider as far as discovery is concerned:
// the one thing the switch proves before it writes anything. (The full flow has
// its own fake in signin_test.go; this needs only the document.)
func discoveryOnlyIDP(t *testing.T) string {
	t.Helper()
	var base string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + base + `",` +
			`"authorization_endpoint":"` + base + `/auth",` +
			`"token_endpoint":"` + base + `/token",` +
			`"userinfo_endpoint":"` + base + `/userinfo"}`))
	}))
	t.Cleanup(ts.Close)
	base = ts.URL
	return ts.URL
}

// workingIDP is a provider that answers every question the pre-flight asks the
// way a correctly configured one does.
func workingIDP(t *testing.T) string {
	t.Helper()
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"userinfo_endpoint":                     base + "/userinfo",
			"response_types_supported":              []string{"code"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
			"scopes_supported":                      []string{"openid", "email", "profile"},
			"claims_supported":                      []string{"sub", "email", "name"},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// The client is known; only the made-up code is refused — which is what
		// a correct client ID and secret look like to this probe.
		out := map[string]any{"error": "invalid_grant"}
		if r.PostFormValue("client_secret") != "shhh" {
			out = map[string]any{"error": "invalid_client", "error_description": "unknown client"}
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/login", http.StatusFound)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	base = ts.URL
	return ts.URL
}

// settingsForm is every field the settings page posts, so a test can save the
// way a browser does: one form, every tab, one Save.
func settingsForm(csrf string) url.Values {
	return url.Values{"csrf": {csrf}}
}

// The whole point of the control: an owner moves one switch, fills in what
// Shared needs, and saves. One post writes the four settings AS A SET plus the
// mode, the session secret and the cookie flag — the three an operator forgets
// and then debugs — and the registry is restarted, because sign-in is read at
// startup.
func TestSavingTheSharedSwitchTurnsSignInOn(t *testing.T) {
	srv, ts, envPath := ownerRegistry(t, "# operator's own comment\nTACIT_API_KEY=k\n")
	restarted := make(chan struct{}, 1)
	srv.RestartRegistry = func() (bool, error) { restarted <- struct{}{}; return true, nil }
	signInRestartDelay = 0
	t.Cleanup(func() { signInRestartDelay = time.Second })

	owner := ownerClient(t, ts.URL, "an-owner-secret")
	page := ""
	if resp, err := owner.Get(ts.URL + "/settings"); err == nil {
		page = readBody(t, resp)
	} else {
		t.Fatal(err)
	}
	// The switch is the same control as Global Access, and it names both answers.
	if !strings.Contains(page, `data-switch="signin_shared"`) {
		t.Fatal("the sign-in control is not the two-position switch")
	}
	for _, want := range []string{">Personal<", ">Shared<"} {
		if !strings.Contains(page, want) {
			t.Errorf("the switch does not name the position %q", want)
		}
	}
	// What Shared needs is on the same page, staged hidden for the switch to
	// reveal — no second URL, and no round trip to see it.
	if !strings.Contains(page, `data-switch-on="signin_shared" hidden`) {
		t.Error("the Shared details are not staged hidden beneath the switch")
	}
	if !strings.Contains(page, "https://tacit.example.com/auth/callback") {
		t.Error("the derived callback to register is not shown")
	}

	form := settingsForm(extractCSRF(t, page))
	form.Set("signin_shared", "on")
	issuer := discoveryOnlyIDP(t)
	form.Set("signin_issuer", issuer)
	form.Set("signin_client_id", "client-abc")
	form.Set("signin_client_secret", "shhh")
	form.Set("signin_scopes", defaultScopes)
	form.Set("admin_emails", "ops@example.com")
	resp, err := owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	// This save does not redirect: the process is about to go away.
	if !strings.Contains(body, "Sign-in is configured") {
		t.Errorf("the save does not report what happened: %s", body)
	}

	got := config.ReadEnv(envPath)
	want := map[string]string{
		"TACIT_OIDC_ISSUER":        issuer,
		"TACIT_OIDC_CLIENT_ID":     "client-abc",
		"TACIT_OIDC_CLIENT_SECRET": "shhh",
		"TACIT_OIDC_REDIRECT_URI":  "https://tacit.example.com/auth/callback",
		// Without the mode the registry would go on calling itself one person's,
		// and the owner link would sign its holder in past the new provider.
		"TACIT_AUTH_MODE": config.AuthOIDC,
		// An https callback means the session cookie must never travel in clear.
		"TACIT_COOKIE_SECURE": "1",
		"TACIT_ADMIN_EMAILS":  "ops@example.com",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// Without a stored session secret every restart signs every member out.
	if len(got["TACIT_SESSION_SECRET"]) < 32 {
		t.Errorf("no durable session secret written (got %q)", got["TACIT_SESSION_SECRET"])
	}
	// The scopes were the default, so the file does not carry a line that says
	// nothing.
	if _, ok := got["TACIT_OIDC_SCOPES"]; ok {
		t.Error("default scopes were written as an explicit setting")
	}
	raw, _ := os.ReadFile(envPath)
	if !strings.Contains(string(raw), "# operator's own comment") {
		t.Error("the patch lost the operator's own lines")
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Error("nothing restarted the registry, so the settings never come into force")
	}
}

// A save from the Personal position is every other setting on the page, and
// nothing to do with sign-in.
func TestSavingWithoutTheSwitchLeavesSignInAlone(t *testing.T) {
	srv, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	srv.RestartRegistry = func() (bool, error) { t.Error("an ordinary save restarted the registry"); return false, nil }
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	// This save redirects, so the client must stop and let the test see it.
	owner.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, _ := owner.Get(ts.URL + "/settings")
	page := readBody(t, resp)

	form := settingsForm(extractCSRF(t, page))
	form.Set("admin_emails", "ops@example.com")
	// The fields are in the form even in the Personal position, because they are
	// hidden rather than removed. An unmoved switch must make them inert.
	form.Set("signin_issuer", discoveryOnlyIDP(t))
	form.Set("signin_client_id", "client-abc")
	form.Set("signin_client_secret", "shhh")
	resp, err := owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("an ordinary save = %d, want the usual redirect: %s", resp.StatusCode, body)
	}
	got := config.ReadEnv(envPath)
	if got["TACIT_OIDC_ISSUER"] != "" || got["TACIT_AUTH_MODE"] != "" {
		t.Errorf("a save from the Personal position turned sign-in on: %v", got)
	}
	if got["TACIT_ADMIN_EMAILS"] != "ops@example.com" {
		t.Error("the rest of the save did not happen")
	}
}

// The issuer is proved BEFORE anything is written. A wrong issuer discovered at
// the first sign-in is a locked dashboard — and a refused save must not write
// the other settings either, since the page rejects the whole form.
func TestSharedRefusesAnIssuerItCannotProve(t *testing.T) {
	srv, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	srv.RestartRegistry = func() (bool, error) { t.Error("restarted on a refused issuer"); return false, nil }
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, _ := owner.Get(ts.URL + "/settings")
	csrf := extractCSRF(t, readBody(t, resp))

	// A server that answers, but not with a discovery document.
	notAnIssuer := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notAnIssuer.Close)

	form := settingsForm(csrf)
	form.Set("signin_shared", "on")
	form.Set("signin_issuer", notAnIssuer.URL)
	form.Set("signin_client_id", "client-abc")
	form.Set("signin_client_secret", "shhh")
	resp, err := owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "not an OIDC issuer") {
		t.Errorf("the refusal does not say what was wrong: %s", body)
	}
	if got := config.ReadEnv(envPath); got["TACIT_OIDC_ISSUER"] != "" || got["TACIT_AUTH_MODE"] != "" {
		t.Errorf("a refused issuer still wrote settings: %v", got)
	}
	// The form comes back on the Shared position with what was typed, so the fix
	// is one field away rather than the whole thing again.
	if !strings.Contains(body, `name="signin_shared" checked`) {
		t.Error("the switch snapped back to Personal, hiding the fields with the error in them")
	}
	if !strings.Contains(body, `value="`+notAnIssuer.URL+`"`) {
		t.Error("the rejected form lost the issuer that was typed")
	}
}

// Sign-in must move together. Three of the four settings leave the dashboard
// open with nothing saying so, which is why the save refuses a partial set.
func TestSharedNeedsBothHalvesOfTheClient(t *testing.T) {
	_, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, _ := owner.Get(ts.URL + "/settings")

	form := settingsForm(extractCSRF(t, readBody(t, resp)))
	form.Set("signin_shared", "on")
	form.Set("signin_issuer", discoveryOnlyIDP(t))
	form.Set("signin_client_id", "client-abc")
	resp, err := owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "client secret") {
		t.Fatalf("a client with no secret was accepted (%d): %s", resp.StatusCode, body)
	}
	if got := config.ReadEnv(envPath); got["TACIT_OIDC_CLIENT_ID"] != "" {
		t.Error("a partial client was written")
	}
}

// The switch closes the registry's door, so a visitor who cannot prove they run
// this machine must not be able to move it.
func TestOnlyTheOwnerCanMoveTheSignInSwitch(t *testing.T) {
	srv, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	srv.RestartRegistry = func() (bool, error) { t.Error("restarted for a stranger"); return false, nil }

	form := url.Values{"signin_shared": {"on"}, "signin_issuer": {discoveryOnlyIDP(t)},
		"signin_client_id": {"a"}, "signin_client_secret": {"b"}}
	resp, err := http.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a session-less save = %d, want 403", resp.StatusCode)
	}

	// And the owner's own session still needs the form's token, so a link on
	// another site cannot post this for them.
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, err = owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a save with no CSRF token = %d, want 403", resp.StatusCode)
	}
	if got := config.ReadEnv(envPath); got["TACIT_OIDC_ISSUER"] != "" {
		t.Error("an unauthenticated post wrote settings")
	}
}

// Once sign-in is on, the switch reports the position and cannot be moved back:
// the way out of a wrong provider is the console, which stays reachable when the
// dashboard does not. A forged post of the off direction changes nothing.
func TestSharedCannotBeSwitchedBackFromTheBrowser(t *testing.T) {
	envPath := registryEnv(t, strings.Join([]string{
		"TACIT_API_KEY=k",
		"TACIT_OIDC_ISSUER=https://accounts.google.com",
		"TACIT_OIDC_CLIENT_ID=abc",
		"TACIT_OIDC_CLIENT_SECRET=shhh",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
	}, "\n")+"\n")
	srv, ts := newServer(t)
	srv.Cfg.AdminEmails = []string{"ops@example.com"}
	admin := signIn(t, srv, "ops@example.com")
	srv.Cfg.OIDCIssuer = "https://accounts.google.com"

	resp, err := admin.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	if !strings.Contains(page, `name="signin_shared" checked disabled`) {
		t.Error("a secured registry does not show the switch locked in the Shared position")
	}
	if !strings.Contains(page, "tacit secure") {
		t.Error("the page does not say where turning it back off is done")
	}

	form := settingsForm(extractCSRF(t, page)) // no signin_shared: the off direction
	form.Set("admin_emails", "ops@example.com")
	resp, err = admin.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	got := config.ReadEnv(envPath)
	if got["TACIT_OIDC_ISSUER"] == "" || got["TACIT_OIDC_CLIENT_SECRET"] == "" {
		t.Errorf("a save from a secured registry removed its sign-in: %v", got)
	}
}

// Between the write and the restart the file says sign-in is on and the process
// still has it off. The page has to say which, because the dashboard is open in
// the meantime.
func TestSettingsSaysSignInIsWrittenButNotInForce(t *testing.T) {
	// The four settings land in the file while the process that is serving this
	// page is already running without them.
	_, ts, _ := ownerRegistry(t, strings.Join([]string{
		"TACIT_API_KEY=k",
		"TACIT_OIDC_ISSUER=https://accounts.google.com",
		"TACIT_OIDC_CLIENT_ID=abc",
		"TACIT_OIDC_CLIENT_SECRET=shhh",
		"TACIT_OIDC_REDIRECT_URI=https://tacit.example.com/auth/callback",
	}, "\n")+"\n")
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, err := owner.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	if !strings.Contains(page, "not in force") {
		t.Error("the page does not report settings that are written and not in force")
	}
	if !strings.Contains(page, `set-chip set-chip-warn">restart<`) {
		t.Error("the tab strip does not carry the pending restart")
	}
	if !strings.Contains(page, `name="signin_shared" checked disabled`) {
		t.Error("the switch does not hold the Shared position it has already been moved to")
	}
}

// A callback the provider will refuse is a lockout, so the page says so before
// the operator types a secret rather than after.
func TestSharedWarnsWhenTheCallbackCannotWork(t *testing.T) {
	srv, ts, _ := ownerRegistry(t, "TACIT_API_KEY=k\n")
	srv.Cfg.ExternalURL = "http://tacit.example.com" // plaintext: no provider accepts it
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, err := owner.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	if !strings.Contains(page, "must be https") {
		t.Errorf("no warning about a plaintext callback: %s", page)
	}
	form := settingsForm(extractCSRF(t, page))
	form.Set("signin_shared", "on")
	form.Set("signin_issuer", discoveryOnlyIDP(t))
	form.Set("signin_client_id", "a")
	form.Set("signin_client_secret", "b")
	resp, err = owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	if body := readBody(t, resp); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a plaintext callback was accepted (%d): %s", resp.StatusCode, body)
	}
}

// The redirect URI is the one value that has to be right, and a registry with no
// address of its own has no candidate for it. It used to offer the bind address
// dressed as a hostname — http://localhost:8080/auth/callback — which a provider
// will happily accept and which then returns every member to their own machine.
// The page asks for the address instead, and refuses the save until there is one.
func TestSharedRefusesARegistryWithNoAddress(t *testing.T) {
	srv, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	srv.Cfg.ExternalURL = "" // nothing but the port it is bound to
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, err := owner.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	if strings.Contains(page, "localhost") {
		t.Error("the page still offers a loopback address as the redirect URI to register")
	}
	if !strings.Contains(page, "needs an address to return members to") {
		t.Errorf("the page does not say why sign-in cannot be configured yet: %s", page)
	}
	// No client fields either: pasting a client for a callback that cannot be
	// registered is work spent on nothing.
	if strings.Contains(page, `name="signin_client_secret"`) {
		t.Error("the page asks for a client it could not use")
	}
	if strings.Contains(page, "the owner link stops working") {
		t.Error("the page promises what Save will do when Save can do nothing here")
	}

	form := settingsForm(extractCSRF(t, page))
	form.Set("signin_shared", "on")
	form.Set("signin_issuer", discoveryOnlyIDP(t))
	form.Set("signin_client_id", "a")
	form.Set("signin_client_secret", "b")
	resp, err = owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a save with no address = %d, want 400", resp.StatusCode)
	}
	if !strings.Contains(body, "Set Own address above") {
		t.Errorf("the refusal does not name the setting to change: %s", body)
	}
	if got := config.ReadEnv(envPath); got["TACIT_OIDC_ISSUER"] != "" {
		t.Error("sign-in was written for an address that cannot carry it")
	}
}

// Global Access exists so a registry with no domain of its own gets a public
// https address — which is exactly the operator who could not otherwise answer
// the redirect-URI question. That address is the callback, and the page says it
// is on loan.
func TestSharedUsesThePublishedAddressWhenItIsAllThereIs(t *testing.T) {
	srv, ts, _ := ownerRegistry(t, "TACIT_API_KEY=k\n")
	srv.Cfg.ExternalURL = ""
	srv.Cfg.GlobalAccess = true
	srv.pub.state = PublishState{Enabled: true, Connected: true, URL: "https://cedar-hollow.tacit.zone"}

	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, err := owner.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	if !strings.Contains(page, "https://cedar-hollow.tacit.zone/auth/callback") {
		t.Errorf("the published address is not offered as the callback: %s", page)
	}
	if !strings.Contains(page, "Global Access lends this address") {
		t.Error("the page does not say the callback is on loan from Global Access")
	}
	if !strings.Contains(page, `name="signin_client_secret"`) {
		t.Error("a publishable registry is not offered the client fields")
	}
}

// Moving the switch to Shared turns the page's one button into Test
// configuration, so the first press asks the provider rather than writing
// anything. The script settles the label while the page is live; this is what a
// browser without one is given, and the two have to agree.
func TestTheButtonAsksBeforeItSaves(t *testing.T) {
	_, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, err := owner.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	page := readBody(t, resp)
	// At rest — the switch on Personal — it is the ordinary save button.
	if !strings.Contains(page, `value="save">Save changes<`) {
		t.Error("the settings page does not render its usual save button")
	}
	if !strings.Contains(page, "Test configuration") {
		t.Error("the page carries no test state for the script to switch to")
	}

	// A press with the switch on Shared: the checks run and nothing is written.
	form := settingsForm(extractCSRF(t, page))
	form.Set("do", "test")
	form.Set("signin_shared", "on")
	form.Set("signin_issuer", workingIDP(t))
	form.Set("signin_client_id", "client-abc")
	form.Set("signin_client_secret", "shhh")
	resp, err = owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if got := config.ReadEnv(envPath); got["TACIT_OIDC_ISSUER"] != "" {
		t.Fatal("a test wrote the configuration it was asked to test")
	}
	if !strings.Contains(body, "Every check passed") {
		t.Errorf("a working provider was not reported as working: %s", body)
	}
	// Having passed, the button is Save changes — the operator's next press
	// commits it.
	if !strings.Contains(body, `value="save">Save changes<`) {
		t.Error("a passing test did not turn the button back into Save changes")
	}
}

// The point of testing first: a wrong secret is caught before it is written, and
// the answer says which box to change.
func TestATestThatFailsNamesTheFieldToChange(t *testing.T) {
	_, ts, envPath := ownerRegistry(t, "TACIT_API_KEY=k\n")
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, _ := owner.Get(ts.URL + "/settings")
	page := readBody(t, resp)

	form := settingsForm(extractCSRF(t, page))
	form.Set("do", "test")
	form.Set("signin_shared", "on")
	form.Set("signin_issuer", workingIDP(t))
	form.Set("signin_client_id", "client-abc")
	form.Set("signin_client_secret", "wrong-secret")
	resp, err := owner.PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "1 check failed") {
		t.Errorf("the failure is not reported: %s", body)
	}
	if !strings.Contains(body, `set-badge">Client secret</span>`) {
		t.Error("the failed check does not name the setting to change")
	}
	if !strings.Contains(body, "unknown client") {
		t.Error("the failed check does not quote what the provider said")
	}
	// Still Test configuration: nothing has been proved yet.
	if !strings.Contains(body, `value="test">Test configuration<`) {
		t.Error("a failed test offered Save changes anyway")
	}
	if got := config.ReadEnv(envPath); got["TACIT_OIDC_ISSUER"] != "" {
		t.Fatal("a failed test wrote settings")
	}
}

// The script's endpoint: the same checks, as a fragment, with the verdict in a
// header so the button can follow it without a reload.
func TestTheTestEndpointAnswersWithTheVerdict(t *testing.T) {
	_, ts, _ := ownerRegistry(t, "TACIT_API_KEY=k\n")
	owner := ownerClient(t, ts.URL, "an-owner-secret")
	resp, _ := owner.Get(ts.URL + "/settings")
	csrf := extractCSRF(t, readBody(t, resp))

	pass := url.Values{"csrf": {csrf}, "signin_issuer": {workingIDP(t)},
		"signin_client_id": {"client-abc"}, "signin_client_secret": {"shhh"}}
	resp, err := owner.PostForm(ts.URL+"/settings/signin/test", pass)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if got := resp.Header.Get("X-Tacit-Checks"); got != "pass" {
		t.Errorf("verdict header = %q, want pass", got)
	}
	if !strings.Contains(body, "chk-pass") {
		t.Errorf("the fragment carries no results: %s", body)
	}

	bad := url.Values{"csrf": {csrf}, "signin_issuer": {workingIDP(t)},
		"signin_client_id": {"client-abc"}, "signin_client_secret": {"nope"}}
	resp, err = owner.PostForm(ts.URL+"/settings/signin/test", bad)
	if err != nil {
		t.Fatal(err)
	}
	body = readBody(t, resp)
	if got := resp.Header.Get("X-Tacit-Checks"); got != "fail" {
		t.Errorf("verdict header = %q, want fail", got)
	}
	if !strings.Contains(body, "chk-fail") {
		t.Error("the fragment does not mark the failure")
	}

	// Half a configuration is not a test: say what is missing rather than asking
	// the provider a question with a blank in it.
	resp, err = owner.PostForm(ts.URL+"/settings/signin/test", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	if body := readBody(t, resp); !strings.Contains(body, "Fill in the issuer") {
		t.Errorf("an empty form was tested anyway: %s", body)
	}

	// It is the owner's endpoint, like the switch it serves.
	stranger, err := http.Post(ts.URL+"/settings/signin/test", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatal(err)
	}
	stranger.Body.Close()
	if stranger.StatusCode != http.StatusForbidden {
		t.Errorf("a stranger could run the test: %d", stranger.StatusCode)
	}
}

// The claims a session carries are the only difference between the owner and a
// stranger here, so the gate is asserted directly too.
func TestCanConfigureSignInIsOwnerModeOnly(t *testing.T) {
	srv, _ := newServer(t)
	srv.Cfg.AuthMode = config.AuthOwner
	srv.Cfg.OwnerSecret = "an-owner-secret"
	ownerSession := oidc.Claims{"owner": true}
	if !srv.canConfigureSignIn(ownerSession) {
		t.Error("the owner of a single-member registry cannot configure sign-in")
	}
	if srv.canConfigureSignIn(nil) {
		t.Error("a visitor with no session can configure sign-in")
	}
	if srv.canConfigureSignIn(oidc.Claims{"email": "someone@example.com"}) {
		t.Error("a session that is not the owner's can configure sign-in")
	}
	srv.OIDC = &oidc.Provider{Secret: []byte("s")}
	if srv.canConfigureSignIn(ownerSession) {
		t.Error("a registry that already has a provider still offers the switch")
	}
}
