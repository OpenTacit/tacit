// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

// Proving a provider WILL work, before the registry commits to it.
//
// CheckIssuer answers one question — does this issuer serve a discovery
// document — and that question is the cheapest of the ones that matter. The
// expensive ones are the ones an operator otherwise discovers at the first
// sign-in, when the dashboard is already closed behind the provider they just
// configured: a client secret that was copied with a trailing space, a client
// that is not allowed the authorization code grant, a redirect URI that is one
// character off the one registered, a provider that will only take its secret
// in a Basic header. Every one of those produces the same symptom — sign-in
// fails, nobody can get in — and each has a different fix.
//
// Preflight runs them all, against the live provider, using exactly the
// requests this registry itself makes:
//
//	· the discovery document, and whether it agrees about its own issuer;
//	· the endpoints it names, and whether they can carry a secret;
//	· the grant, the auth method and the scopes THIS registry uses;
//	· the client ID and secret, proved at the token endpoint with a code that
//	  cannot work — a provider that recognizes the client rejects the code
//	  (invalid_grant), and one that does not rejects the client
//	  (invalid_client). That is the whole trick, and it needs no member;
//	· the redirect URI, offered to the authorization endpoint to see whether
//	  the provider accepts it or names it as the problem.
//
// A check reports what it saw, in the provider's own words where it has them,
// and which SETTING to change. Where an answer is not conclusive it says so
// rather than guessing: an inconclusive check is a warning, and warnings do not
// stand in the way — a provider whose error vocabulary this code does not know
// is not a misconfiguration.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CheckStatus is how a check came out. Only Fail stands in the way of saving:
// Warn is for what could not be established, which is not the same as wrong.
type CheckStatus string

const (
	CheckPass CheckStatus = "pass"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// CheckField names the setting a failing check is about, so a page can point at
// the box to change. It is deliberately abstract — this package does not know
// what a form field is called.
type CheckField string

const (
	FieldIssuer       CheckField = "issuer"
	FieldClientID     CheckField = "client_id"
	FieldClientSecret CheckField = "client_secret"
	FieldRedirectURI  CheckField = "redirect_uri"
	FieldScopes       CheckField = "scopes"
	FieldProvider     CheckField = "provider" // the fix is at the provider, not here
)

// Check is one question asked of the provider and the answer it gave.
type Check struct {
	Name   string
	Status CheckStatus
	Field  CheckField
	Detail string
}

// Preflight is what the registry is about to configure.
type Preflight struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       string
	// HTTP replaces both clients in tests. Preflight needs one client that
	// follows redirects and one that does not; a supplied client is used for
	// the first and copied, redirects off, for the second.
	HTTP *http.Client
}

// Run asks every question and returns the answers in the order they were asked,
// which is also the order they depend on each other: nothing after the
// discovery document is meaningful without it.
func (p Preflight) Run() []Check {
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 8 * time.Second}
	}
	noFollow := *hc
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	issuer := strings.TrimRight(strings.TrimSpace(p.Issuer), "/")
	doc, err := discover(hc, issuer)
	if err != nil {
		return []Check{{
			Name: "Discovery document", Status: CheckFail, Field: FieldIssuer,
			Detail: err.Error() + " The issuer is the base URL that serves the document, not the sign-in page: " +
				issuerExamples() + ".",
		}}
	}
	checks := []Check{{
		Name: "Discovery document", Status: CheckPass, Field: FieldIssuer,
		Detail: issuer + " serves an OpenID configuration.",
	}}
	checks = append(checks, issuerAgrees(issuer, doc))
	checks = append(checks, endpointsSecure(doc)...)
	checks = append(checks, codeFlowSupported(doc))
	checks = append(checks, secretMethodSupported(doc))
	checks = append(checks, scopesOffered(doc, p.Scopes)...)
	checks = append(checks, emailClaim(doc))
	checks = append(checks, p.clientAccepted(hc, doc))
	checks = append(checks, p.redirectAccepted(&noFollow, doc))
	return checks
}

// OK reports whether the configuration can be saved: no check failed.
func OK(checks []Check) bool {
	for _, c := range checks {
		if c.Status == CheckFail {
			return false
		}
	}
	return true
}

// issuerAgrees catches the spelling that a discovery fetch alone does not: a
// host that serves the document at one URL and calls itself another. The
// registry compares the two at every sign-in, so a mismatch here is a sign-in
// that fails after the member has already typed their password.
func issuerAgrees(issuer string, doc map[string]any) Check {
	said, _ := doc["issuer"].(string)
	switch {
	case said == "":
		return Check{Name: "Issuer identity", Status: CheckWarn, Field: FieldIssuer,
			Detail: "The document names no issuer of its own, so it cannot be compared with what you entered."}
	case strings.TrimRight(said, "/") != issuer:
		return Check{Name: "Issuer identity", Status: CheckFail, Field: FieldIssuer,
			Detail: "The document says this provider's issuer is " + said + ". Use that value: providers reject a " +
				"sign-in whose issuer does not match their own, character for character."}
	}
	return Check{Name: "Issuer identity", Status: CheckPass, Field: FieldIssuer,
		Detail: "The document agrees it is " + said + "."}
}

// endpointsSecure checks what the provider will be sent over. The token
// endpoint is the one that carries the client secret and the userinfo endpoint
// carries the member's identity; neither may be plaintext.
func endpointsSecure(doc map[string]any) []Check {
	var out []Check
	for _, ep := range []string{"authorization_endpoint", "token_endpoint", "userinfo_endpoint"} {
		v, _ := doc[ep].(string)
		name := "Endpoint: " + strings.TrimSuffix(ep, "_endpoint")
		if !endpointSecure(v) {
			out = append(out, Check{Name: name, Status: CheckFail, Field: FieldProvider,
				Detail: "The provider names " + v + ", which is not https. The registry will not send a client " +
					"secret or read a member's identity in the clear. This is the provider's own configuration."})
			continue
		}
		out = append(out, Check{Name: name, Status: CheckPass, Field: FieldProvider, Detail: v})
	}
	return out
}

// endpointSecure allows a provider on this machine, the same exception
// CheckCallback makes for the callback: a Keycloak or dex on localhost is a test
// rig, and there is no network between it and the registry to protect.
func endpointSecure(raw string) bool {
	if strings.HasPrefix(raw, "https://") {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "http" && isLoopbackHost(u.Hostname())
}

// codeFlowSupported: the registry exchanges an authorization code from the
// server. A provider offering only implicit or device flows cannot serve it.
func codeFlowSupported(doc map[string]any) Check {
	types := stringsIn(doc["response_types_supported"])
	if len(types) == 0 {
		return Check{Name: "Authorization code flow", Status: CheckWarn, Field: FieldProvider,
			Detail: "The document does not list its response types, so this could not be established."}
	}
	for _, t := range types {
		if t == "code" {
			return Check{Name: "Authorization code flow", Status: CheckPass, Field: FieldProvider,
				Detail: "The provider offers the code flow, which is the one the registry uses."}
		}
	}
	return Check{Name: "Authorization code flow", Status: CheckFail, Field: FieldProvider,
		Detail: "The provider offers " + strings.Join(types, ", ") + " and not code. The registry signs members " +
			"in by exchanging an authorization code from the server; it cannot use these."}
}

// secretMethodSupported: the registry sends its secret in the form body
// (client_secret_post), the way ExchangeCode builds the request. A provider
// that takes it only in a Basic header refuses every exchange, and the symptom
// — invalid_client at sign-in — reads as a wrong secret rather than a wrong
// method.
func secretMethodSupported(doc map[string]any) Check {
	methods := stringsIn(doc["token_endpoint_auth_methods_supported"])
	if len(methods) == 0 {
		// The spec's default when the list is absent is client_secret_basic,
		// but providers that omit the list almost always accept both, so this
		// is not a refusal — the credential probe below tests it for real.
		return Check{Name: "Secret in the request body", Status: CheckWarn, Field: FieldProvider,
			Detail: "The document does not list how it accepts a client secret. The credential check below tests it directly."}
	}
	for _, m := range methods {
		if m == "client_secret_post" {
			return Check{Name: "Secret in the request body", Status: CheckPass, Field: FieldProvider,
				Detail: "The provider accepts client_secret_post, which is how the registry sends it."}
		}
	}
	return Check{Name: "Secret in the request body", Status: CheckFail, Field: FieldProvider,
		Detail: "The provider accepts its client secret only as " + strings.Join(methods, ", ") +
			". The registry sends client_secret_post. In your provider, allow that method for this client."}
}

// scopesOffered: openid and email are load-bearing — email is the member's
// identity and what the administrators list is matched against.
func scopesOffered(doc map[string]any, scopes string) []Check {
	want := strings.Fields(scopes)
	if len(want) == 0 {
		want = strings.Fields(DefaultScopes)
	}
	offered := stringsIn(doc["scopes_supported"])
	if len(offered) == 0 {
		return []Check{{Name: "Scopes", Status: CheckWarn, Field: FieldScopes,
			Detail: "The document does not list the scopes it offers, so " + strings.Join(want, " ") +
				" could not be confirmed."}}
	}
	has := map[string]bool{}
	for _, s := range offered {
		has[s] = true
	}
	var out []Check
	for _, w := range want {
		switch {
		case has[w]:
			continue
		case w == "openid" || w == "email":
			out = append(out, Check{Name: "Scope: " + w, Status: CheckFail, Field: FieldScopes,
				Detail: "The provider does not offer " + w + ". The registry needs it: " + whyScope(w)})
		default:
			out = append(out, Check{Name: "Scope: " + w, Status: CheckWarn, Field: FieldScopes,
				Detail: "The provider does not list " + w + ". Sign-in will work without it; " + whyScope(w)})
		}
	}
	if len(out) == 0 {
		return []Check{{Name: "Scopes", Status: CheckPass, Field: FieldScopes,
			Detail: "The provider offers " + strings.Join(want, " ") + "."}}
	}
	return out
}

func whyScope(s string) string {
	switch s {
	case "openid":
		return "it is what makes this an OpenID sign-in at all."
	case "email":
		return "a member's verified email is their identity here, and what the administrators list is matched against."
	case "profile":
		return "it supplies the name and picture shown in the top bar."
	}
	return "it was asked for in the Scopes field."
}

// emailClaim: a provider can offer the email SCOPE and still not return the
// claim. Where the document lists its claims, this is worth knowing before
// nobody can be an administrator.
func emailClaim(doc map[string]any) Check {
	claims := stringsIn(doc["claims_supported"])
	if len(claims) == 0 {
		return Check{Name: "Email claim", Status: CheckWarn, Field: FieldProvider,
			Detail: "The document does not list its claims, so this could not be established. " +
				"If members sign in but no one is an administrator, this is the first thing to check."}
	}
	for _, c := range claims {
		if c == "email" {
			return Check{Name: "Email claim", Status: CheckPass, Field: FieldProvider,
				Detail: "The provider returns an email claim, which is how the registry names a member."}
		}
	}
	return Check{Name: "Email claim", Status: CheckWarn, Field: FieldProvider,
		Detail: "The document does not list an email claim. The registry identifies members by email; " +
			"without one, sign-in succeeds and the administrators list matches nobody."}
}

// clientAccepted proves the client ID and secret with a request that cannot
// succeed: an authorization code that was never issued. A provider that knows
// the client rejects the CODE (invalid_grant); one that does not rejects the
// CLIENT (invalid_client). No member, no browser, no side effects.
func (p Preflight) clientAccepted(hc *http.Client, doc map[string]any) Check {
	const name = "Client ID and secret"
	endpoint, _ := doc["token_endpoint"].(string)
	if endpoint == "" {
		return Check{Name: name, Status: CheckWarn, Field: FieldClientID,
			Detail: "The provider names no token endpoint, so the credentials could not be tested."}
	}
	resp, err := hc.PostForm(endpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"tacit-preflight-not-a-real-code"},
		"redirect_uri":  {p.RedirectURI},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	})
	if err != nil {
		return Check{Name: name, Status: CheckWarn, Field: FieldClientID,
			Detail: "Could not reach the token endpoint: " + err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	code, desc := oauthError(body)
	switch {
	case code == "invalid_grant":
		// The provider recognized the client and rejected only the made-up code,
		// which is exactly what a correct client and secret look like here.
		return Check{Name: name, Status: CheckPass, Field: FieldClientID,
			Detail: "The provider accepted this client and rejected only the test code, which is what should happen."}
	case code == "invalid_client" || resp.StatusCode == http.StatusUnauthorized:
		return Check{Name: name, Status: CheckFail, Field: FieldClientSecret,
			Detail: "The provider does not recognize this client ID and secret" + saidThat(desc) +
				" Check both against the client in your provider — a secret copied with a space, or " +
				"from a different client, fails exactly like this."}
	case code == "unauthorized_client":
		return Check{Name: name, Status: CheckFail, Field: FieldClientID,
			Detail: "The provider knows this client but does not allow it the authorization code grant" +
				saidThat(desc) + " In your provider it has to be a web application, not a single-page or " +
				"machine-to-machine client."}
	case code == "invalid_request" && strings.Contains(strings.ToLower(string(body)), "redirect"):
		return Check{Name: name, Status: CheckFail, Field: FieldRedirectURI,
			Detail: "The provider rejected the redirect URI in the exchange" + saidThat(desc)}
	case resp.StatusCode == http.StatusOK:
		// A provider that issues tokens for a code nobody minted is not a
		// provider this can reason about; say so rather than pass it.
		return Check{Name: name, Status: CheckWarn, Field: FieldClientID,
			Detail: "The token endpoint answered 200 to a code that was never issued, which no provider should. " +
				"The credentials could not be established from that."}
	}
	return Check{Name: name, Status: CheckWarn, Field: FieldClientID,
		Detail: fmt.Sprintf("The token endpoint answered %s%s, which is not an error this check knows how to read. "+
			"The credentials could not be confirmed either way.", resp.Status, saidThat(firstNonEmpty(code, desc))),
	}
}

// redirectAccepted offers the callback to the authorization endpoint the way a
// sign-in does, and stops at the provider's first answer. A registered URI gets
// the member sent to a sign-in screen; an unregistered one gets the provider's
// own error, which is the single most common way this configuration is wrong.
func (p Preflight) redirectAccepted(noFollow *http.Client, doc map[string]any) Check {
	const name = "Redirect URI"
	endpoint, _ := doc["authorization_endpoint"].(string)
	if endpoint == "" {
		return Check{Name: name, Status: CheckWarn, Field: FieldRedirectURI,
			Detail: "The provider names no authorization endpoint, so the redirect URI could not be tested."}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return Check{Name: name, Status: CheckWarn, Field: FieldRedirectURI,
			Detail: "The provider's authorization endpoint is not a URL: " + endpoint}
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", firstNonEmpty(p.Scopes, DefaultScopes))
	q.Set("state", "tacit-preflight")
	u.RawQuery = q.Encode()

	resp, err := noFollow.Get(u.String())
	if err != nil {
		return Check{Name: name, Status: CheckWarn, Field: FieldRedirectURI,
			Detail: "Could not reach the authorization endpoint: " + err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	// A redirect BACK to the callback carries the provider's verdict in the
	// query; anywhere else is the provider's own sign-in screen, which it only
	// shows once it has accepted the request.
	if loc := resp.Header.Get("Location"); loc != "" {
		if code, desc := redirectError(loc, p.RedirectURI); code != "" {
			return authorizeVerdict(code, desc, p.RedirectURI)
		}
		return Check{Name: name, Status: CheckPass, Field: FieldRedirectURI,
			Detail: "The provider accepted " + p.RedirectURI + " and asked for a sign-in."}
	}
	if code, desc := oauthError(body); code != "" {
		return authorizeVerdict(code, desc, p.RedirectURI)
	}
	if marker := mismatchMarker(body); marker != "" {
		return Check{Name: name, Status: CheckFail, Field: FieldRedirectURI,
			Detail: "The provider will not return members to " + p.RedirectURI + " — it answered with " + marker +
				". Register that exact URI with this client: the scheme, host, port and path must match, with no trailing slash."}
	}
	if resp.StatusCode == http.StatusOK {
		// The sign-in page itself, served rather than redirected to.
		return Check{Name: name, Status: CheckPass, Field: FieldRedirectURI,
			Detail: "The provider accepted " + p.RedirectURI + " and answered with a sign-in page."}
	}
	return Check{Name: name, Status: CheckWarn, Field: FieldRedirectURI,
		Detail: "The authorization endpoint answered " + resp.Status + ", which this check cannot read as either " +
			"an acceptance or a refusal. If sign-in fails with redirect_uri_mismatch, this is the value to register: " +
			p.RedirectURI}
}

func authorizeVerdict(code, desc, redirectURI string) Check {
	const name = "Redirect URI"
	switch code {
	case "invalid_client", "unauthorized_client":
		return Check{Name: name, Status: CheckFail, Field: FieldClientID,
			Detail: "The provider rejected the client ID before it looked at anything else" + saidThat(desc)}
	case "invalid_request", "invalid_redirect_uri", "redirect_uri_mismatch", "access_denied":
		return Check{Name: name, Status: CheckFail, Field: FieldRedirectURI,
			Detail: "The provider refused the request" + saidThat(desc) + " Register " + redirectURI +
				" with this client, exactly as written."}
	case "invalid_scope":
		return Check{Name: name, Status: CheckFail, Field: FieldScopes,
			Detail: "The provider refused the scopes" + saidThat(desc)}
	}
	return Check{Name: name, Status: CheckWarn, Field: FieldRedirectURI,
		Detail: "The provider answered " + code + saidThat(desc)}
}

// mismatchMarker finds a provider's own words for an unregistered callback in
// an HTML error page — the answer Google, Entra and Keycloak give instead of an
// OAuth error object.
func mismatchMarker(body []byte) string {
	low := strings.ToLower(string(body))
	for _, m := range []string{"redirect_uri_mismatch", "redirect uri mismatch", "aadsts50011",
		"invalid redirect", "redirect_uri is not", "invalid_redirect_uri", "redirect uri included is not valid"} {
		if strings.Contains(low, m) {
			return `"` + m + `"`
		}
	}
	return ""
}

// oauthError reads an OAuth error object out of a response body, JSON or form
// encoded — providers answer with both.
func oauthError(body []byte) (code, desc string) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(trimmed), &obj) == nil {
			code, _ = obj["error"].(string)
			desc, _ = obj["error_description"].(string)
			return code, desc
		}
	}
	if v, err := url.ParseQuery(trimmed); err == nil && v.Get("error") != "" {
		return v.Get("error"), v.Get("error_description")
	}
	return "", ""
}

// redirectError reads the verdict out of a redirect back to the callback.
func redirectError(location, redirectURI string) (code, desc string) {
	if !strings.HasPrefix(strings.ToLower(location), strings.ToLower(redirectURI)) {
		return "", ""
	}
	u, err := url.Parse(location)
	if err != nil {
		return "", ""
	}
	q := u.Query()
	return q.Get("error"), q.Get("error_description")
}

func stringsIn(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		if s, ok := it.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func saidThat(desc string) string {
	if strings.TrimSpace(desc) == "" {
		return "."
	}
	return `: "` + strings.TrimSpace(desc) + `".`
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func issuerExamples() string {
	parts := make([]string, 0, len(IssuerFormats))
	for _, f := range IssuerFormats {
		parts = append(parts, f.Provider+" "+f.Example)
	}
	return strings.Join(parts, "; ")
}
