// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package oidc

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func provider() *Provider {
	return &Provider{Secret: []byte("test-secret"), TTL: time.Hour}
}

func TestPackUnpackRoundTrip(t *testing.T) {
	p := provider()
	token := p.Pack(Claims{"sub": "u1", "email": "a@b.c"})
	got := p.Unpack(token)
	if got == nil || got["email"] != "a@b.c" {
		t.Fatalf("roundtrip lost claims: %v", got)
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	p := provider()
	token := p.Pack(Claims{"sub": "u1"})
	if p.Unpack(token+"x") != nil {
		t.Fatal("tampered signature accepted")
	}
	if p.Unpack("") != nil || p.Unpack("garbage") != nil {
		t.Fatal("garbage accepted")
	}
	other := &Provider{Secret: []byte("other"), TTL: time.Hour}
	if other.Unpack(token) != nil {
		t.Fatal("cross-secret token accepted")
	}
}

func TestExpiredSessionRejected(t *testing.T) {
	p := provider()
	token := p.Pack(Claims{"sub": "u1", "exp": time.Now().Add(-time.Minute).Unix()})
	if p.Unpack(token) != nil {
		t.Fatal("expired token accepted")
	}
}

func TestCreateSessionPicksName(t *testing.T) {
	p := provider()
	sess := p.CreateSession(Claims{"sub": "u1", "email": "a@b.c"})
	got := p.VerifySession(sess)
	if got == nil || got["name"] != "a@b.c" {
		t.Fatalf("name fallback wrong: %v", got)
	}
}

func TestTxnStateMustMatch(t *testing.T) {
	p := provider()
	state, token := p.CreateTxn("/techniques")
	if next := p.VerifyTxn(token, state); next != "/techniques" {
		t.Fatalf("valid txn rejected: %q", next)
	}
	if p.VerifyTxn(token, "wrong-state") != "" {
		t.Fatal("mismatched state accepted (CSRF)")
	}
	if p.VerifyTxn("", state) != "" {
		t.Fatal("missing txn cookie accepted")
	}
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestRegisterClientRoundTrip(t *testing.T) {
	p := provider()
	uris := []string{"http://127.0.0.1:9000/callback", "vscode://cb"}
	id := p.RegisterClient(uris, "My MCP Client")
	got := p.ClientRedirects(id)
	if len(got) != 2 || got[0] != uris[0] || got[1] != uris[1] {
		t.Fatalf("client redirects lost: %v", got)
	}
	// A non-client token (e.g. a session) must not pass as a client_id.
	if p.ClientRedirects(p.CreateSession(Claims{"sub": "u"})) != nil {
		t.Fatal("session token accepted as client_id")
	}
	if p.ClientRedirects("garbage") != nil {
		t.Fatal("garbage accepted as client_id")
	}
}

func TestAuthCodePKCEFlow(t *testing.T) {
	p := provider()
	const (
		verifier    = "a-high-entropy-code-verifier-1234567890"
		clientID    = "cid"
		redirectURI = "http://127.0.0.1:9000/callback"
		resource    = "https://reg.example/mcp"
	)
	user := Claims{"sub": "u1", "email": "a@b.c"}
	code := p.MintAuthCode(user, clientID, redirectURI, challengeFor(verifier), resource, "tacit.read")

	// Wrong verifier fails PKCE.
	if _, err := p.VerifyAuthCode(code, clientID, redirectURI, "wrong"); err == nil {
		t.Fatal("bad PKCE verifier accepted")
	}
	// Wrong client / redirect binding fails.
	if _, err := p.VerifyAuthCode(code, "other", redirectURI, verifier); err == nil {
		t.Fatal("code accepted for a different client")
	}
	if _, err := p.VerifyAuthCode(code, clientID, "http://evil/cb", verifier); err == nil {
		t.Fatal("code accepted for a different redirect URI")
	}
	// Correct exchange yields the identity + grant.
	claims, err := p.VerifyAuthCode(code, clientID, redirectURI, verifier)
	if err != nil {
		t.Fatalf("valid code rejected: %v", err)
	}
	if claims["email"] != "a@b.c" || claims["resource"] != resource {
		t.Fatalf("code claims wrong: %v", claims)
	}
}

func TestAuthCodeExpires(t *testing.T) {
	p := provider()
	code := p.Pack(Claims{
		"kind": KindMCPCode, "client_id": "c", "redirect_uri": "r",
		"code_challenge": challengeFor("v"), "exp": time.Now().Add(-time.Second).Unix()})
	if _, err := p.VerifyAuthCode(code, "c", "r", "v"); err == nil {
		t.Fatal("expired authorization code accepted")
	}
}

func TestAccessTokenAudienceBound(t *testing.T) {
	p := provider()
	const resource = "https://reg.example/mcp"
	tok, expiresIn := p.MintAccessToken(Claims{"sub": "u1"}, resource, "tacit.read")
	if expiresIn <= 0 {
		t.Fatalf("expires_in not set: %d", expiresIn)
	}
	if p.VerifyAccessToken(tok, resource) == nil {
		t.Fatal("valid access token rejected")
	}
	// Audience mismatch is rejected (token stolen for another resource).
	if p.VerifyAccessToken(tok, "https://reg.example/other") != nil {
		t.Fatal("token accepted for wrong audience")
	}
	// A code must not pass as an access token (kind separation).
	code := p.MintAuthCode(Claims{"sub": "u"}, "c", "r", challengeFor("v"), resource, "s")
	if p.VerifyAccessToken(code, resource) != nil {
		t.Fatal("authorization code accepted as access token")
	}
}

func TestRefreshTokenBoundAndKindSeparated(t *testing.T) {
	p := provider()
	const resource = "https://reg.example/mcp"
	user := Claims{"sub": "u1", "email": "a@b.c"}
	rt := p.MintRefreshToken(user, "client-1", resource, "tacit.read")

	claims, err := p.VerifyRefreshToken(rt, "client-1", resource)
	if err != nil {
		t.Fatalf("valid refresh token rejected: %v", err)
	}
	if claims["sub"] != "u1" || claims["scope"] != "tacit.read" {
		t.Fatalf("refresh claims wrong: %v", claims)
	}
	// Bound to the client it was granted to…
	if _, err := p.VerifyRefreshToken(rt, "other-client", resource); err == nil {
		t.Fatal("refresh token accepted for a different client")
	}
	// …and to the resource.
	if _, err := p.VerifyRefreshToken(rt, "client-1", "https://reg.example/other"); err == nil {
		t.Fatal("refresh token accepted for a different resource")
	}
	// Kind separation both ways: an access token is not a refresh token, and a
	// refresh token never opens /mcp directly.
	at, _ := p.MintAccessToken(user, resource, "tacit.read")
	if _, err := p.VerifyRefreshToken(at, "client-1", resource); err == nil {
		t.Fatal("access token accepted as refresh token")
	}
	if p.VerifyAccessToken(rt, resource) != nil {
		t.Fatal("refresh token accepted as access token")
	}
	// An expired refresh token is rejected.
	old := p.Pack(Claims{"kind": KindMCPRefresh, "client_id": "client-1", "aud": resource,
		"exp": time.Now().Add(-time.Second).Unix()})
	if _, err := p.VerifyRefreshToken(old, "client-1", resource); err == nil {
		t.Fatal("expired refresh token accepted")
	}
}
