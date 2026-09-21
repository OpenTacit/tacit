// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

// Proving a provider BEFORE anything is written.
//
// A wrong issuer spelling (Okta's custom authorization server, the Entra tenant
// path, the Keycloak realm) or a callback the provider will refuse is otherwise
// discovered at the first sign-in — by which time the dashboard is already
// closed and the operator is locked out of the registry they were securing.
// Both checks therefore run before the settings are written, and they live here
// rather than in either caller because `tacit secure` on the console and the
// Settings page in the browser must refuse exactly the same things.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// IssuerFormat is one provider's issuer shape, for an operator who has pasted
// the wrong URL. These are the four that are habitually got wrong: the issuer
// is not the sign-in page, the console URL or the tenant home.
type IssuerFormat struct{ Provider, Example string }

// IssuerFormats is the list both callers show when discovery fails.
var IssuerFormats = []IssuerFormat{
	{"Google", "https://accounts.google.com"},
	{"Okta", "https://<org>.okta.com  (or .../oauth2/default)"},
	{"Entra ID", "https://login.microsoftonline.com/<tenant-id>/v2.0"},
	{"Keycloak", "https://<host>/realms/<realm>"},
}

// CheckIssuer fetches the issuer's discovery document and reports whether it
// can carry this registry's flow. The three endpoints are all required: the
// registry reads identity from userinfo, so an issuer that names only the first
// two would authenticate members it then could not name.
func CheckIssuer(issuer string) (map[string]any, error) {
	return discover(&http.Client{Timeout: 10 * time.Second}, issuer)
}

// DefaultScopes is what the registry asks a provider for: email identifies the
// member and matches the administrators list, profile gives the name and the
// avatar in the top bar.
const DefaultScopes = "openid email profile"

// discover is the fetch itself, with the client the caller wants — Preflight
// substitutes one in tests, and needs the same reading of the document.
func discover(hc *http.Client, issuer string) (map[string]any, error) {
	u := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	resp, err := hc.Get(u)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s — this is not an OIDC issuer", u, resp.Status)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s did not return a discovery document: %v", u, err)
	}
	for _, ep := range []string{"authorization_endpoint", "token_endpoint", "userinfo_endpoint"} {
		if s, _ := doc[ep].(string); s == "" {
			return nil, fmt.Errorf("%s names no %s — the registry needs all three (it reads identity from userinfo)", u, ep)
		}
	}
	return doc, nil
}

// CheckCallback returns "" when the URI can carry the sign-in flow, or the
// problem in the operator's terms. The https rule is the one that prevents a
// lockout: providers refuse a plaintext redirect URI, so a callback written
// today would fail at the first sign-in, with the dashboard already closed.
func CheckCallback(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Sprintf("the callback must be an absolute URL (got %q)", raw)
	}
	if !strings.HasSuffix(u.Path, "/auth/callback") {
		return fmt.Sprintf("the callback must end in /auth/callback (got %q) — it is a route this registry serves", raw)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return fmt.Sprintf("the callback must be https (got %q) — identity providers refuse a plaintext redirect URI. "+
			"Give this registry an https address of its own (TACIT_EXTERNAL_URL, or Global Access) and try again", raw)
	}
	return ""
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
