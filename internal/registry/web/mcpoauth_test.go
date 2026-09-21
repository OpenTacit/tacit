// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/internal/registry/config"
	"github.com/opentacit/tacit/internal/registry/oidc"
)

// challengeS256 is the PKCE S256 transform of a verifier (RFC 7636).
func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthServer builds a registry with OIDC wired (no real upstream needed for the
// endpoints under test) and a stub MCP handler behind the /mcp guard.
func oauthServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	// Hermetic: never read the developer machine's real registry.env — its
	// embedder/key choices must not leak into the suite.
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "no-such.env"))
	cfg := config.Load()
	cfg.APIKey = "test-key"
	cfg.OIDCRedirectURI = "https://reg.example/auth/callback"
	cfg.OIDCIssuer = "https://idp.example"
	cfg.OIDCClientID = "tacit"
	cfg.OIDCClientSecret = "secret"
	srv := &Server{
		Cfg: cfg,
		OIDC: &oidc.Provider{
			Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret, RedirectURI: cfg.OIDCRedirectURI,
			Scopes: "openid email", Secret: []byte("test-secret"), TTL: time.Hour,
		},
		MCP: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}),
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// noRedirect is a client that surfaces 3xx responses instead of following them.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestOAuthDiscoveryMetadata(t *testing.T) {
	_, ts := oauthServer(t)

	resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	var prm map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&prm)
	resp.Body.Close()
	if prm["resource"] != "https://reg.example/mcp" {
		t.Fatalf("PRM resource wrong: %v", prm["resource"])
	}
	if as, _ := prm["authorization_servers"].([]any); len(as) != 1 || as[0] != "https://reg.example" {
		t.Fatalf("PRM authorization_servers wrong: %v", prm["authorization_servers"])
	}

	resp, _ = http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	var as map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&as)
	resp.Body.Close()
	if as["authorization_endpoint"] != "https://reg.example/oauth/authorize" ||
		as["token_endpoint"] != "https://reg.example/oauth/token" ||
		as["registration_endpoint"] != "https://reg.example/oauth/register" {
		t.Fatalf("AS metadata endpoints wrong: %v", as)
	}
	if m, _ := as["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Fatalf("PKCE S256 not advertised: %v", as["code_challenge_methods_supported"])
	}
}

func TestOAuthRegisterThenAuthorizeValidation(t *testing.T) {
	srv, ts := oauthServer(t)
	client := noRedirect()

	// Dynamic registration returns a usable client_id.
	body := `{"redirect_uris":["http://127.0.0.1:9000/callback"],"client_name":"cli"}`
	resp, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	var reg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatal("no client_id issued")
	}

	// Unknown client / redirect is rejected inline (never redirected).
	resp, _ = client.Get(ts.URL + "/oauth/authorize?client_id=bogus&redirect_uri=http://x/cb&response_type=code")
	if resp.StatusCode != 400 {
		t.Fatalf("unknown client should be 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Registered client but a bad redirect_uri (not the one registered) → 400.
	q := url.Values{"client_id": {clientID}, "redirect_uri": {"http://evil/cb"}, "response_type": {"code"}}
	resp, _ = client.Get(ts.URL + "/oauth/authorize?" + q.Encode())
	if resp.StatusCode != 400 {
		t.Fatalf("wrong redirect_uri should be 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Registered client + valid redirect but missing PKCE → error redirected back.
	q = url.Values{
		"client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:9000/callback"},
		"response_type": {"code"}, "state": {"xyz"}}
	resp, _ = client.Get(ts.URL + "/oauth/authorize?" + q.Encode())
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("missing PKCE should redirect error, got %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	if loc.Query().Get("error") != "invalid_request" || loc.Query().Get("state") != "xyz" {
		t.Fatalf("error redirect wrong: %s", resp.Header.Get("Location"))
	}
	_ = srv
}

func TestOAuthTokenAndBearerAccess(t *testing.T) {
	srv, ts := oauthServer(t)

	const (
		verifier    = "verifier-with-enough-entropy-0123456789"
		clientID    = "cid"
		redirectURI = "http://127.0.0.1:9000/callback"
	)
	challenge := challengeS256(verifier)
	resource := srv.mcpResourceURL()
	// Simulate the post-login state: a minted authorization code.
	code := srv.OIDC.MintAuthCode(oidc.Claims{"sub": "u1", "email": "a@b.c"},
		clientID, redirectURI, challenge, resource, oauthScope)

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	resp, err := http.Post(ts.URL+"/oauth/token", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var tok map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if resp.StatusCode != 200 || tok["token_type"] != "Bearer" || tok["access_token"] == nil {
		t.Fatalf("token response wrong: %d %v", resp.StatusCode, tok)
	}
	access, _ := tok["access_token"].(string)

	// The Bearer token opens /mcp.
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+access)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("Bearer access to /mcp failed: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// No credential → 401 with the discovery challenge.
	req, _ = http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated /mcp should be 401, got %d", resp.StatusCode)
	}
	if wa := resp.Header.Get("WWW-Authenticate"); !strings.Contains(wa, "resource_metadata=") {
		t.Fatalf("missing discovery challenge: %q", wa)
	}
	resp.Body.Close()

	// The shared key still works (plugin / loopback path).
	req, _ = http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("X-Tacit-Key", "test-key")
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("X-Tacit-Key access to /mcp failed: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// A bad grant is an OAuth error.
	form.Set("code_verifier", "wrong")
	resp, _ = http.Post(ts.URL+"/oauth/token", "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()))
	var e map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&e)
	resp.Body.Close()
	if resp.StatusCode != 400 || e["error"] != "invalid_grant" {
		t.Fatalf("bad grant should be invalid_grant, got %d %v", resp.StatusCode, e)
	}
}

// The refresh grant: the code exchange hands back a refresh token; presenting
// it re-mints a working access token (and a fresh refresh token) with no human
// round-trip. The token is client-bound and kind-separated.
func TestOAuthRefreshGrant(t *testing.T) {
	srv, ts := oauthServer(t)

	const (
		verifier    = "verifier-with-enough-entropy-0123456789"
		clientID    = "cid"
		redirectURI = "http://127.0.0.1:9000/callback"
	)
	resource := srv.mcpResourceURL()
	code := srv.OIDC.MintAuthCode(oidc.Claims{"sub": "u1", "email": "a@b.c"},
		clientID, redirectURI, challengeS256(verifier), resource, oauthScope)

	post := func(form url.Values) (int, map[string]any) {
		t.Helper()
		resp, err := http.Post(ts.URL+"/oauth/token", "application/x-www-form-urlencoded",
			strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		return resp.StatusCode, body
	}

	status, tok := post(url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}})
	if status != 200 || tok["refresh_token"] == nil {
		t.Fatalf("code exchange should include a refresh token: %d %v", status, tok)
	}
	refresh, _ := tok["refresh_token"].(string)

	// Redeem it: a new access token that opens /mcp, and a rotated refresh token.
	status, tok2 := post(url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if status != 200 || tok2["access_token"] == nil || tok2["refresh_token"] == nil {
		t.Fatalf("refresh grant failed: %d %v", status, tok2)
	}
	access, _ := tok2["access_token"].(string)
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+access)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("refreshed Bearer access to /mcp failed: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Another client cannot redeem a stolen refresh token.
	if status, body := post(url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {"other"}}); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("foreign client redeeming a refresh token should be invalid_grant, got %d %v", status, body)
	}
	// An access token is not a refresh token.
	if status, body := post(url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {access}, "client_id": {clientID}}); status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("access token replayed as refresh should be invalid_grant, got %d %v", status, body)
	}
	// A refresh token never opens /mcp directly.
	req, _ = http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+refresh)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("refresh token used as Bearer should be 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Discovery advertises the grant so clients know to use it.
	resp, _ = http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	var meta map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	grants, _ := json.Marshal(meta["grant_types_supported"])
	if !strings.Contains(string(grants), "refresh_token") {
		t.Fatalf("metadata should advertise refresh_token: %s", grants)
	}
}
