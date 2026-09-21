// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// MCP-client OAuth: the registry acts as an OAuth 2.1 Authorization Server for
// MCP clients (the remote /mcp endpoint's clients), brokering the actual human
// login to the configured upstream OIDC provider — the same flow the dashboard
// uses. This gives any MCP client the standard discover -> register -> authorize
// -> token dance and a Bearer token it presents to /mcp, without requiring the
// upstream IdP to know anything about MCP.
//
// The whole surface is gated on OIDC being configured; with OIDC off, /mcp keeps
// its X-Tacit-Key auth and these endpoints report "not configured".
package web

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/cachepolicy"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// oauthScope is the single scope granted to MCP clients (read the registry).
const oauthScope = "tacit.read"

// oauthBaseURL is the externally-reachable base of the registry: origin plus
// the mount prefix when one is configured. It prefers the explicit
// TACIT_EXTERNAL_URL when set, and otherwise falls back to the configured OIDC
// redirect URI (which the admin already sets to a real, reachable callback).
// All issuer/metadata/resource URLs are built from it.
func (s *Server) oauthBaseURL() string {
	c := s.cfg()
	if base := s.PublishedBase(); base != "" {
		return base + c.BasePath
	}
	if origin := c.ExternalOrigin(); origin != "" {
		return origin + c.BasePath
	}
	u, err := url.Parse(c.OIDCRedirectURI)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + c.BasePath
}

func (s *Server) mcpResourceURL() string { return s.oauthBaseURL() + "/mcp" }
func (s *Server) prmURL() string {
	return s.oauthBaseURL() + "/.well-known/oauth-protected-resource"
}

// oauthReady reports whether the OAuth AS surface can serve: OIDC configured and
// a usable base URL.
func (s *Server) oauthReady() bool { return s.OIDC != nil && s.oauthBaseURL() != "" }

// handleOAuthPRM serves the Protected Resource Metadata (RFC 9728): it points a
// client at this server's Authorization Server. Public (pre-auth discovery).
func (s *Server) handleOAuthPRM(w http.ResponseWriter, r *http.Request) {
	if !s.oauthReady() {
		s.sendError(w, 404, "oauth not configured")
		return
	}
	base := s.oauthBaseURL()
	s.sendJSON(w, 200, map[string]any{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{oauthScope},
	})
}

// handleOAuthASMeta serves the Authorization Server Metadata (RFC 8414). Public.
func (s *Server) handleOAuthASMeta(w http.ResponseWriter, r *http.Request) {
	if !s.oauthReady() {
		s.sendError(w, 404, "oauth not configured")
		return
	}
	base := s.oauthBaseURL()
	s.sendJSON(w, 200, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{oauthScope},
	})
}

// handleOAuthRegister is Dynamic Client Registration (RFC 7591) for public
// clients: it records the client's redirect URIs in a self-describing client_id.
func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if !s.oauthReady() {
		s.sendError(w, 404, "oauth not configured")
		return
	}
	var body struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := readJSONBody(r, &body); err != nil || len(body.RedirectURIs) == 0 {
		s.sendJSON(w, 400, map[string]string{
			"error": "invalid_client_metadata", "error_description": "redirect_uris is required"})
		return
	}
	for _, u := range body.RedirectURIs {
		if !validRedirectURI(u) {
			s.sendJSON(w, 400, map[string]string{
				"error": "invalid_redirect_uri", "error_description": "redirect URIs must be absolute URIs with a scheme"})
			return
		}
	}
	clientID := s.OIDC.RegisterClient(body.RedirectURIs, body.ClientName)
	s.sendJSON(w, 201, map[string]any{
		"client_id":                  clientID,
		"redirect_uris":              body.RedirectURIs,
		"client_name":                body.ClientName,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"client_id_issued_at":        time.Now().Unix(),
	})
}

// handleOAuthAuthorize validates the client + PKCE request, then delegates the
// human login to the upstream OIDC provider, carrying the client context through
// a signed txn cookie. The callback (handleAuthCallback) completes the grant.
func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !s.oauthReady() {
		s.sendError(w, 404, "oauth not configured")
		return
	}
	q := r.URL.Query()
	clientID, redirectURI := q.Get("client_id"), q.Get("redirect_uri")

	// The client + redirect must match a registration before we may redirect any
	// error back to the client; until then, an invalid request is shown inline.
	allowed := s.OIDC.ClientRedirects(clientID)
	if allowed == nil || !slices.Contains(allowed, redirectURI) {
		s.sendHTML(w, 400, "<p>Invalid OAuth client or redirect URI.</p>")
		return
	}
	redirectErr := func(code, desc string) {
		u, _ := url.Parse(redirectURI)
		v := u.Query()
		v.Set("error", code)
		if desc != "" {
			v.Set("error_description", desc)
		}
		if st := q.Get("state"); st != "" {
			v.Set("state", st)
		}
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	}
	if q.Get("response_type") != "code" {
		redirectErr("unsupported_response_type", "only response_type=code is supported")
		return
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		redirectErr("invalid_request", "PKCE with S256 is required")
		return
	}
	resource := q.Get("resource")
	if resource != "" && resource != s.mcpResourceURL() {
		redirectErr("invalid_target", "resource does not match this server")
		return
	}
	if resource == "" {
		resource = s.mcpResourceURL()
	}
	scope := q.Get("scope")
	if scope == "" {
		scope = oauthScope
	}

	// Same hazard as the dashboard sign-in: the txn cookie set here is host-only,
	// so an authorization begun on a hostname other than the callback's would
	// come back to a request that cannot read it. Get onto that host first,
	// carrying the client's request untouched.
	if s.beginOnCallbackHost(w, r, "/oauth/authorize", r.URL.RawQuery) {
		return
	}

	state, txn := s.OIDC.CreateMCPTxn(clientID, redirectURI, q.Get("state"), q.Get("code_challenge"), resource, scope)
	up, err := s.OIDC.AuthorizeURL(state, "")
	if err != nil {
		redirectErr("server_error", "upstream login unavailable")
		return
	}
	s.setCookie(w, txnCookie, txn, 600)
	http.Redirect(w, r, up, http.StatusFound)
}

// finishMCPAuthorize is the MCP branch of the OIDC callback: the human is now
// authenticated upstream, so mint the client's authorization code and redirect
// back to the client's registered redirect URI.
func (s *Server) finishMCPAuthorize(w http.ResponseWriter, r *http.Request, txn, user oidc.Claims) {
	clientID, _ := txn["client_id"].(string)
	redirectURI, _ := txn["redirect_uri"].(string)
	challenge, _ := txn["code_challenge"].(string)
	resource, _ := txn["resource"].(string)
	scope, _ := txn["scope"].(string)
	clientState, _ := txn["client_state"].(string)

	u, err := url.Parse(redirectURI)
	if err != nil {
		s.sendHTML(w, 400, "<p>Invalid client redirect URI.</p>")
		return
	}
	code := s.OIDC.MintAuthCode(user, clientID, redirectURI, challenge, resource, scope)
	v := u.Query()
	v.Set("code", code)
	if clientState != "" {
		v.Set("state", clientState)
	}
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// handleOAuthToken is the token endpoint: it exchanges a PKCE-verified
// authorization code — or a previously issued refresh token — for a Bearer
// access token bound to the MCP resource. Both grants also return a fresh
// refresh token, so an in-use connection renews silently instead of asking
// the human to re-authorize every session-TTL.
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if !s.oauthReady() {
		s.sendError(w, 404, "oauth not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.oauthTokenErr(w, "invalid_request", "malformed form body")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		claims, err := s.OIDC.VerifyAuthCode(
			r.PostForm.Get("code"), r.PostForm.Get("client_id"),
			r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
		if err != nil {
			s.oauthTokenErr(w, "invalid_grant", err.Error())
			return
		}
		resource, _ := claims["resource"].(string)
		scope, _ := claims["scope"].(string)
		s.sendOAuthTokens(w, claims, r.PostForm.Get("client_id"), resource, scope)
	case "refresh_token":
		claims, err := s.OIDC.VerifyRefreshToken(
			r.PostForm.Get("refresh_token"), r.PostForm.Get("client_id"), s.mcpResourceURL())
		if err != nil {
			s.oauthTokenErr(w, "invalid_grant", err.Error())
			return
		}
		scope, _ := claims["scope"].(string)
		s.sendOAuthTokens(w, claims, r.PostForm.Get("client_id"), s.mcpResourceURL(), scope)
	default:
		s.oauthTokenErr(w, "unsupported_grant_type", "only authorization_code and refresh_token are supported")
	}
}

// sendOAuthTokens writes a token response: a session-TTL access token plus a
// year-TTL refresh token (oidc.RefreshTTL). The refresh token rotates on every
// use with a fresh expiry — stateless tokens cannot be revoked individually,
// so the prior one stays valid until its own exp, but every token is bounded.
func (s *Server) sendOAuthTokens(w http.ResponseWriter, user oidc.Claims, clientID, resource, scope string) {
	access, expiresIn := s.OIDC.MintAccessToken(user, resource, scope)
	refresh := s.OIDC.MintRefreshToken(user, clientID, resource, scope)
	w.Header().Set("Cache-Control", cachepolicy.PrivateControl)
	s.sendJSON(w, 200, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    expiresIn,
		"refresh_token": refresh,
		"scope":         scope,
	})
}

func (s *Server) oauthTokenErr(w http.ResponseWriter, code, desc string) {
	w.Header().Set("Cache-Control", cachepolicy.PrivateControl)
	m := map[string]string{"error": code}
	if desc != "" {
		m["error_description"] = desc
	}
	s.sendJSON(w, 400, m)
}

// mcpAuthed guards the /mcp endpoint. It accepts either the shared registry key
// (the local plugin + in-process loopback path) or, when OIDC is configured, a
// Bearer access token minted for this resource. A missing/invalid Bearer under
// OIDC returns a 401 with the RFC 9728 WWW-Authenticate challenge so the client
// can discover the Authorization Server and start the OAuth flow.
func (s *Server) mcpAuthed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A session endpoint, and a streaming one. Both make it bucket B, and the
		// classification is recorded here rather than left to the default so the
		// log distinguishes "we decided" from "nobody said".
		cachepolicy.MarkPrivate(w, r, "MCP session")
		if s.keyOK(r.Header.Get("X-Tacit-Key")) {
			h(w, r)
			return
		}
		if s.oauthReady() {
			tok := bearerToken(r)
			if tok != "" && s.OIDC.VerifyAccessToken(tok, s.mcpResourceURL()) != nil {
				h(w, r)
				return
			}
			errCode := ""
			if tok != "" {
				errCode = "invalid_token"
			}
			s.mcpChallenge(w, errCode)
			return
		}
		s.sendError(w, 401, "unauthorized")
	}
}

// mcpChallenge emits the 401 + WWW-Authenticate pointing at the resource metadata.
func (s *Server) mcpChallenge(w http.ResponseWriter, errCode string) {
	challenge := `Bearer resource_metadata="` + s.prmURL() + `"`
	if errCode != "" {
		challenge += `, error="` + errCode + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	s.sendError(w, 401, "unauthorized")
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// validRedirectURI accepts absolute http(s) URLs (with a host) and native/custom
// scheme callbacks; exact-match at /authorize is what actually binds security.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.Host != ""
	}
	return u.Host != "" || u.Opaque != "" || u.Path != ""
}
