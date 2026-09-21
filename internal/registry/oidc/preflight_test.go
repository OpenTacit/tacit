// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// idp is a provider that can be told to behave like each of the ways a real one
// goes wrong. The defaults are a correct provider.
type idp struct {
	base string
	// doc overrides, applied over the correct document.
	doc map[string]any
	// tokenError and authorize decide the two probes.
	tokenError   string // OAuth error code at the token endpoint
	tokenStatus  int
	authorize    func(w http.ResponseWriter, r *http.Request)
	sawTokenForm url.Values
}

func newIDP(t *testing.T, cfg *idp) string {
	t.Helper()
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"userinfo_endpoint":                     base + "/userinfo",
			"response_types_supported":              []string{"code"},
			"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
			"scopes_supported":                      []string{"openid", "email", "profile"},
			"claims_supported":                      []string{"sub", "email", "name"},
		}
		for k, v := range cfg.doc {
			if v == nil {
				delete(doc, k)
				continue
			}
			doc[k] = v
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cfg.sawTokenForm = r.PostForm
		code, status := cfg.tokenError, cfg.tokenStatus
		if code == "" {
			code = "invalid_grant" // the correct answer to a code nobody issued
		}
		if status == 0 {
			status = http.StatusBadRequest
			if code == "invalid_client" {
				status = http.StatusUnauthorized
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": code, "error_description": "from the test provider"})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		if cfg.authorize != nil {
			cfg.authorize(w, r)
			return
		}
		http.Redirect(w, r, base+"/login", http.StatusFound) // its own sign-in screen
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL
	cfg.base = base
	return base
}

func run(t *testing.T, issuer string, edit func(*Preflight)) []Check {
	t.Helper()
	pf := Preflight{Issuer: issuer, ClientID: "client-abc", ClientSecret: "shhh",
		RedirectURI: "https://tacit.example.com/auth/callback", Scopes: DefaultScopes}
	if edit != nil {
		edit(&pf)
	}
	return pf.Run()
}

// find is the check by name, so a test says which question it is asserting on.
func find(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %v", name, names(checks))
	return Check{}
}

func names(checks []Check) []string {
	var out []string
	for _, c := range checks {
		out = append(out, c.Name+"="+string(c.Status))
	}
	return out
}

// A provider that is set up correctly passes everything, and the credential
// probe is the request the registry itself would make.
func TestPreflightPassesACorrectProvider(t *testing.T) {
	cfg := &idp{}
	issuer := newIDP(t, cfg)
	checks := run(t, issuer, nil)
	if !OK(checks) {
		t.Fatalf("a correct provider did not pass: %v", names(checks))
	}
	for _, c := range checks {
		if c.Status == CheckWarn {
			t.Errorf("a correct provider produced a warning: %s — %s", c.Name, c.Detail)
		}
	}
	// The probe sends the secret the way ExchangeCode does, so what it proves is
	// what sign-in will do.
	if got := cfg.sawTokenForm.Get("client_secret"); got != "shhh" {
		t.Errorf("the credential probe sent client_secret=%q, so it did not test the real exchange", got)
	}
	if got := cfg.sawTokenForm.Get("grant_type"); got != "authorization_code" {
		t.Errorf("the credential probe used grant_type=%q", got)
	}
}

// The check that earns the button: a wrong secret is invalid_client, and the
// failure names the field to change rather than the flow that broke.
func TestPreflightCatchesAWrongClientSecret(t *testing.T) {
	issuer := newIDP(t, &idp{tokenError: "invalid_client"})
	checks := run(t, issuer, nil)
	if OK(checks) {
		t.Fatal("a rejected client passed")
	}
	c := find(t, checks, "Client ID and secret")
	if c.Status != CheckFail || c.Field != FieldClientSecret {
		t.Errorf("wrong verdict: %+v", c)
	}
	if !strings.Contains(c.Detail, "does not recognize") {
		t.Errorf("the detail does not say what the provider answered: %q", c.Detail)
	}
}

// A client that exists but is the wrong KIND — a single-page app, a
// machine-to-machine client — fails differently and needs a different fix.
func TestPreflightCatchesAClientWithoutTheCodeGrant(t *testing.T) {
	issuer := newIDP(t, &idp{tokenError: "unauthorized_client"})
	c := find(t, run(t, issuer, nil), "Client ID and secret")
	if c.Status != CheckFail || c.Field != FieldClientID {
		t.Errorf("wrong verdict: %+v", c)
	}
	if !strings.Contains(c.Detail, "web application") {
		t.Errorf("the detail does not say what to change at the provider: %q", c.Detail)
	}
}

// The most common misconfiguration of all, in each of the two shapes providers
// answer it with: an OAuth error, and an HTML error page.
func TestPreflightCatchesAnUnregisteredRedirectURI(t *testing.T) {
	oauth := newIDP(t, &idp{authorize: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "invalid_request", "error_description": "redirect_uri did not match a registered value"})
	}})
	c := find(t, run(t, oauth, nil), "Redirect URI")
	if c.Status != CheckFail || c.Field != FieldRedirectURI {
		t.Errorf("an OAuth refusal was not caught: %+v", c)
	}
	if !strings.Contains(c.Detail, "https://tacit.example.com/auth/callback") {
		t.Errorf("the detail does not name the URI to register: %q", c.Detail)
	}

	page := newIDP(t, &idp{authorize: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<html><body>Error 400: redirect_uri_mismatch</body></html>`))
	}})
	c = find(t, run(t, page, nil), "Redirect URI")
	if c.Status != CheckFail || c.Field != FieldRedirectURI {
		t.Errorf("an HTML error page was not caught: %+v", c)
	}
}

// A provider that sends the visitor to its own sign-in screen has accepted the
// request — that is what acceptance looks like from here.
func TestPreflightReadsASignInScreenAsAcceptance(t *testing.T) {
	issuer := newIDP(t, &idp{})
	c := find(t, run(t, issuer, nil), "Redirect URI")
	if c.Status != CheckPass {
		t.Errorf("a sign-in redirect was not read as acceptance: %+v", c)
	}
}

// The issuer that serves the document and calls itself something else — the
// Okta and Entra trailing-path mistake. The registry compares the two at every
// sign-in, so this is a failure, and the fix is the value the document gave.
func TestPreflightCatchesAnIssuerThatNamesItselfDifferently(t *testing.T) {
	cfg := &idp{doc: map[string]any{"issuer": "https://example.okta.com/oauth2/default"}}
	issuer := newIDP(t, cfg)
	c := find(t, run(t, issuer, nil), "Issuer identity")
	if c.Status != CheckFail || c.Field != FieldIssuer {
		t.Errorf("wrong verdict: %+v", c)
	}
	if !strings.Contains(c.Detail, "https://example.okta.com/oauth2/default") {
		t.Errorf("the detail does not offer the value to use: %q", c.Detail)
	}
}

// What the registry needs of the provider's own configuration: the code flow,
// a secret in the body, and the scopes it asks for.
func TestPreflightCatchesAProviderThatCannotServeThisRegistry(t *testing.T) {
	noCode := newIDP(t, &idp{doc: map[string]any{"response_types_supported": []string{"id_token", "token"}}})
	if c := find(t, run(t, noCode, nil), "Authorization code flow"); c.Status != CheckFail {
		t.Errorf("a provider with no code flow passed: %+v", c)
	}
	basicOnly := newIDP(t, &idp{doc: map[string]any{
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic"}}})
	c := find(t, run(t, basicOnly, nil), "Secret in the request body")
	if c.Status != CheckFail || !strings.Contains(c.Detail, "client_secret_post") {
		t.Errorf("a provider that refuses the registry's auth method passed: %+v", c)
	}
	noEmail := newIDP(t, &idp{doc: map[string]any{"scopes_supported": []string{"openid", "profile"}}})
	c = find(t, run(t, noEmail, nil), "Scope: email")
	if c.Status != CheckFail || c.Field != FieldScopes {
		t.Errorf("a provider with no email scope passed: %+v", c)
	}
	if !strings.Contains(c.Detail, "administrators list") {
		t.Errorf("the detail does not say why the registry needs it: %q", c.Detail)
	}
}

// What cannot be established is a warning, not a refusal: a provider whose
// document is sparse, or whose error vocabulary this code does not know, is not
// a misconfiguration.
func TestPreflightWarnsRatherThanRefusesWhenItCannotTell(t *testing.T) {
	sparse := newIDP(t, &idp{
		doc:        map[string]any{"scopes_supported": nil, "claims_supported": nil, "response_types_supported": nil},
		tokenError: "something_unheard_of",
	})
	checks := run(t, sparse, nil)
	if !OK(checks) {
		t.Errorf("an unreadable answer was treated as a failure: %v", names(checks))
	}
	if c := find(t, checks, "Client ID and secret"); c.Status != CheckWarn {
		t.Errorf("an unknown token error should be inconclusive: %+v", c)
	}
	if c := find(t, checks, "Scopes"); c.Status != CheckWarn {
		t.Errorf("an absent scope list should be inconclusive: %+v", c)
	}
}

// Nothing else is meaningful without the document, so a bad issuer is the only
// thing reported.
func TestPreflightStopsAtAnIssuerItCannotRead(t *testing.T) {
	notAnIssuer := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notAnIssuer.Close)
	checks := run(t, notAnIssuer.URL, nil)
	if len(checks) != 1 || checks[0].Status != CheckFail || checks[0].Field != FieldIssuer {
		t.Fatalf("want one issuer failure, got %v", names(checks))
	}
	if !strings.Contains(checks[0].Detail, "accounts.google.com") {
		t.Errorf("the detail does not show what an issuer looks like: %q", checks[0].Detail)
	}
}
