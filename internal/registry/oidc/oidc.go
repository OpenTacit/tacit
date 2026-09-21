// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package oidc provides sign-in for the dashboard's HTML view.
//
// Unrelated to the /v1 JSON API's X-Tacit-Key auth. Authorization Code
// flow against the provider's standard discovery document, using net/http
// only (matching the stdlib-only dependency policy). Identity is read from
// the provider's userinfo endpoint rather than by verifying the ID token's
// signature locally, which avoids needing a JWT/JWKS implementation.
//
// Sessions and the login CSRF/next-URL handshake are both stateless signed
// tokens (HMAC-SHA256 over a JSON payload) — no server-side session store.
package oidc

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/opentacit/tacit/internal/registry/session"
)

// Claims is a signed-token payload / the provider's userinfo claims.
type Claims map[string]any

// Provider runs the OIDC flow for one configured issuer.
type Provider struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// RedirectURIFunc, when set, supplies the redirect URI instead of the static
	// field. It exists because a published registry's public address is decided
	// at runtime — the shared proxy allocates it — and both legs of the flow have
	// to agree: the authorize request and the token exchange must send the SAME
	// redirect_uri or the provider rejects the exchange.
	RedirectURIFunc func() string
	Scopes          string
	Secret          []byte // HMAC key for session/txn tokens
	TTL             time.Duration
	HTTP            *http.Client

	mu        sync.Mutex
	discovery map[string]any
}

func (p *Provider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Discover fetches (and caches) /.well-known/openid-configuration.
func (p *Provider) Discover() (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovery != nil {
		return p.discovery, nil
	}
	u := strings.TrimRight(p.Issuer, "/") + "/.well-known/openid-configuration"
	resp, err := p.client().Get(u)
	if err != nil {
		return nil, fmt.Errorf("discovery failed for %s: %w", p.Issuer, err)
	}
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("discovery failed for %s: %w", p.Issuer, err)
	}
	p.discovery = doc
	return doc, nil
}

// redirectURI is the callback both legs of the flow declare.
func (p *Provider) redirectURI() string {
	if p.RedirectURIFunc != nil {
		if uri := p.RedirectURIFunc(); uri != "" {
			return uri
		}
	}
	return p.RedirectURI
}

// AuthorizeURL builds the provider redirect for one login attempt.
//
// A non-empty prompt is passed through to the provider. "select_account" is the
// one that matters here: without it a provider with a single live session signs
// the visitor straight back in as whoever that is, so someone who arrived as the
// wrong identity has no way to become the right one. Asking for it on every
// sign-in would cost everyone else a tap, so the callers ask for it only where
// choosing again is the point — see handleAuthLogin's switch.
func (p *Provider) AuthorizeURL(state, prompt string) (string, error) {
	doc, err := p.Discover()
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {p.ClientID},
		"redirect_uri":  {p.redirectURI()},
		"scope":         {p.Scopes},
		"state":         {state},
	}
	if prompt != "" {
		q.Set("prompt", prompt)
	}
	return fmt.Sprint(doc["authorization_endpoint"]) + "?" + q.Encode(), nil
}

// ExchangeCode POSTs the authorization code to the token endpoint.
func (p *Provider) ExchangeCode(code string) (map[string]any, error) {
	doc, err := p.Discover()
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.redirectURI()},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}
	resp, err := p.client().PostForm(fmt.Sprint(doc["token_endpoint"]), form)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", strings.TrimSpace(string(body)))
	}
	var tokens map[string]any
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	return tokens, nil
}

// FetchUserinfo reads the provider's userinfo claims.
func (p *Provider) FetchUserinfo(accessToken string) (Claims, error) {
	doc, err := p.Discover()
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest("GET", fmt.Sprint(doc["userinfo_endpoint"]), nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo fetch failed: %s", strings.TrimSpace(string(body)))
	}
	var claims Claims
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("userinfo fetch failed: %w", err)
	}
	return claims, nil
}

// --- stateless signed tokens (sessions + the login CSRF/next handshake) ----

// signer is the half of this that never needed an issuer. It lives in
// internal/registry/session so a registry with one member and no identity
// provider can mint the same tokens; the bytes are unchanged by the move.
func (p *Provider) signer() session.Signer { return session.Signer{Secret: p.Secret} }

// Pack signs a claims payload into a token.
func (p *Provider) Pack(data Claims) string {
	return p.signer().Pack(session.Claims(data))
}

// Unpack verifies + decodes a token; nil if invalid/expired.
func (p *Provider) Unpack(token string) Claims {
	return Claims(p.signer().Unpack(token))
}

// displayName picks the friendliest non-empty identity label from userinfo.
func displayName(user Claims) any {
	if n := user["name"]; n != nil && n != "" {
		return n
	}
	if e := user["email"]; e != nil && e != "" {
		return e
	}
	return user["sub"]
}

// CreateSession signs a session token from the provider's userinfo claims. The
// profile picture rides along (when the provider returned one under the
// "profile" scope) so the dashboard's top bar can show the member's avatar
// without a second call to userinfo on every page.
func (p *Provider) CreateSession(user Claims) string {
	sess := Claims{
		"sub":   user["sub"],
		"email": user["email"],
		"name":  displayName(user),
		"exp":   time.Now().Add(p.TTL).Unix(),
	}
	if pic, _ := user["picture"].(string); pic != "" {
		sess["picture"] = pic
	}
	return p.Pack(sess)
}

// VerifySession returns the session claims, or nil.
func (p *Provider) VerifySession(token string) Claims { return p.Unpack(token) }

// CreateTxn starts a login attempt: an unguessable state paired (signed) with
// the post-login redirect target — no server-side state between /auth/login
// and /auth/callback.
func (p *Provider) CreateTxn(nextPath string) (state, token string) {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	state = base64.RawURLEncoding.EncodeToString(b)
	token = p.Pack(Claims{"state": state, "next": nextPath,
		"exp": time.Now().Add(10 * time.Minute).Unix()})
	return state, token
}

// VerifyTxn returns the validated next path, or "" if the txn is
// missing/expired/mismatched.
func (p *Provider) VerifyTxn(token, state string) string {
	data := p.Unpack(token)
	if data == nil || state == "" {
		return ""
	}
	got, _ := data["state"].(string)
	if !hmac.Equal([]byte(got), []byte(state)) {
		return ""
	}
	if next, _ := data["next"].(string); next != "" {
		return next
	}
	return "/"
}

// VerifyTxnClaims returns the full, validated txn claims when the signed token
// is intact and its embedded CSRF state matches; nil otherwise. It lets the
// login callback tell a dashboard sign-in from an MCP authorization (by the
// "kind" claim) and recover the client context carried through the round-trip.
func (p *Provider) VerifyTxnClaims(token, state string) Claims {
	data := p.Unpack(token)
	if data == nil || state == "" {
		return nil
	}
	got, _ := data["state"].(string)
	if !hmac.Equal([]byte(got), []byte(state)) {
		return nil
	}
	return data
}

// --- OAuth 2.1 Authorization Server for MCP clients -------------------------
//
// The registry brokers MCP-client authorization: to the client it presents an
// OAuth 2.1 AS surface (dynamic registration + PKCE authorization code), while
// the actual human login is delegated to the upstream OIDC provider — the same
// flow the dashboard uses. Every artifact here (client_id, authorization code,
// access token) is a stateless signed token (Pack), so no server-side store is
// needed; replay of the short-TTL codes is bounded by their expiry and PKCE.

// Token kinds distinguish the signed artifacts so one can never be presented in
// another's place.
const (
	KindMCPClient  = "mcp_client"  // a dynamically registered client_id
	KindMCPTxn     = "mcp_txn"     // the in-flight authorization (txn cookie)
	KindMCPCode    = "mcp_code"    // an authorization code
	KindMCPToken   = "mcp_token"   // an access token
	KindMCPRefresh = "mcp_refresh" // a refresh token
)

// RefreshTTL is how long a refresh token stays redeemable. A year: the access
// token stays short-lived (the session TTL), but the CLIENT's grant — a
// connector an operator wired up once — must not demand a human re-login every
// working day. Each refresh hands back a fresh refresh token, so an in-use
// connection renews indefinitely while any single token still hard-expires.
const RefreshTTL = 365 * 24 * time.Hour

// RegisterClient issues a self-describing client_id for a dynamically
// registered public client: a signed token carrying its allowed redirect URIs.
// It does not expire (a registration is durable) but is tamper-evident.
func (p *Provider) RegisterClient(redirectURIs []string, name string) string {
	return p.Pack(Claims{
		"kind":          KindMCPClient,
		"redirect_uris": redirectURIs,
		"client_name":   name,
		"iat":           time.Now().Unix(),
	})
}

// ClientRedirects returns the redirect URIs a client_id was registered with, or
// nil if the id is invalid, tampered, or not one of ours.
func (p *Provider) ClientRedirects(clientID string) []string {
	data := p.Unpack(clientID)
	if data == nil || data["kind"] != KindMCPClient {
		return nil
	}
	raw, _ := data["redirect_uris"].([]any)
	uris := make([]string, 0, len(raw))
	for _, u := range raw {
		if s, ok := u.(string); ok {
			uris = append(uris, s)
		}
	}
	return uris
}

// CreateMCPTxn binds the upstream-login CSRF state to the client's request
// (redirect, state, PKCE challenge, resource, scope) in a signed cookie token,
// so the callback can mint the client's authorization code with no stored state.
func (p *Provider) CreateMCPTxn(clientID, redirectURI, clientState, codeChallenge, resource, scope string) (state, token string) {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	state = base64.RawURLEncoding.EncodeToString(b)
	token = p.Pack(Claims{
		"kind":           KindMCPTxn,
		"state":          state,
		"client_id":      clientID,
		"redirect_uri":   redirectURI,
		"client_state":   clientState,
		"code_challenge": codeChallenge,
		"resource":       resource,
		"scope":          scope,
		"exp":            time.Now().Add(10 * time.Minute).Unix(),
	})
	return state, token
}

// MintAuthCode issues a short-lived, PKCE-bound authorization code carrying the
// authenticated user's identity and the client/resource it was granted for.
func (p *Provider) MintAuthCode(user Claims, clientID, redirectURI, codeChallenge, resource, scope string) string {
	return p.Pack(Claims{
		"kind":           KindMCPCode,
		"sub":            user["sub"],
		"email":          user["email"],
		"name":           displayName(user),
		"client_id":      clientID,
		"redirect_uri":   redirectURI,
		"code_challenge": codeChallenge,
		"resource":       resource,
		"scope":          scope,
		"exp":            time.Now().Add(60 * time.Second).Unix(),
	})
}

// VerifyAuthCode validates an authorization code against the presenting client,
// its redirect URI, and the PKCE verifier, returning the identity+grant claims
// or an error. Codes are short-TTL signed tokens: replay is bounded by the 60s
// expiry (this stateless design keeps no single-use store).
func (p *Provider) VerifyAuthCode(code, clientID, redirectURI, codeVerifier string) (Claims, error) {
	data := p.Unpack(code)
	if data == nil || data["kind"] != KindMCPCode {
		return nil, errors.New("invalid or expired authorization code")
	}
	if data["client_id"] != clientID || data["redirect_uri"] != redirectURI {
		return nil, errors.New("code issued to a different client or redirect URI")
	}
	challenge, _ := data["code_challenge"].(string)
	if !verifyPKCE(codeVerifier, challenge) {
		return nil, errors.New("PKCE verification failed")
	}
	return data, nil
}

// verifyPKCE checks a code_verifier against an S256 code_challenge (RFC 7636).
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return hmac.Equal([]byte(want), []byte(challenge))
}

// MintAccessToken issues a Bearer access token bound to the MCP resource
// (audience), returning the token and its lifetime in seconds.
func (p *Provider) MintAccessToken(user Claims, resource, scope string) (token string, expiresIn int) {
	ttl := p.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	token = p.Pack(Claims{
		"kind":  KindMCPToken,
		"sub":   user["sub"],
		"email": user["email"],
		"name":  displayName(user),
		"aud":   resource,
		"scope": scope,
		"exp":   time.Now().Add(ttl).Unix(),
	})
	return token, int(ttl.Seconds())
}

// VerifyAccessToken returns an access token's claims when it is valid, unexpired,
// and minted for the given resource (audience); nil otherwise.
func (p *Provider) VerifyAccessToken(token, resource string) Claims {
	data := p.Unpack(token)
	if data == nil || data["kind"] != KindMCPToken {
		return nil
	}
	if aud, _ := data["aud"].(string); aud != resource {
		return nil
	}
	return data
}

// MintRefreshToken issues a refresh token bound to the client it was granted
// to and the MCP resource, carrying the identity needed to re-mint access
// tokens. Stateless like every artifact here: expiry is the only revocation
// short of rotating the signing secret.
func (p *Provider) MintRefreshToken(user Claims, clientID, resource, scope string) string {
	return p.Pack(Claims{
		"kind":      KindMCPRefresh,
		"sub":       user["sub"],
		"email":     user["email"],
		"name":      displayName(user),
		"client_id": clientID,
		"aud":       resource,
		"scope":     scope,
		"exp":       time.Now().Add(RefreshTTL).Unix(),
	})
}

// VerifyRefreshToken validates a refresh token against the presenting client
// and the resource it was granted for, returning the identity+grant claims or
// an error. The kind check keeps an access token (same signer, longer-lived
// claims otherwise) from ever being replayed as a refresh token.
func (p *Provider) VerifyRefreshToken(token, clientID, resource string) (Claims, error) {
	data := p.Unpack(token)
	if data == nil || data["kind"] != KindMCPRefresh {
		return nil, errors.New("invalid or expired refresh token")
	}
	if data["client_id"] != clientID {
		return nil, errors.New("refresh token issued to a different client")
	}
	if aud, _ := data["aud"].(string); aud != resource {
		return nil, errors.New("refresh token issued for a different resource")
	}
	return data, nil
}
