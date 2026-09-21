// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Signed session tokens, with nothing about who issued them.
//
// The dashboard's session is an HMAC over a JSON payload with an expiry —
// stateless, no server-side store. That machinery arrived inside the OIDC
// provider because OIDC was the only way to be signed in, and it took the
// provider's name with it. It is the wrong name: a session says a viewer was
// authenticated, not how.
//
// This package is the half that never needed an issuer, so a registry with one
// member and no identity provider can mint one too. OIDC keeps using it, and
// the tokens are byte-identical to the ones it minted before the split.
package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Claims is a signed-token payload.
type Claims map[string]any

// Signer packs and verifies tokens against one secret.
type Signer struct{ Secret []byte }

// Pack signs a claims payload into a token.
func (s Signer) Pack(data Claims) string {
	raw, _ := json.Marshal(data)
	payloadB64 := []byte(base64.URLEncoding.EncodeToString(raw))
	return string(payloadB64) + "." + s.sign(payloadB64)
}

// Unpack verifies and decodes a token; nil if invalid or expired.
func (s Signer) Unpack(token string) Claims {
	if token == "" {
		return nil
	}
	idx := strings.LastIndexByte(token, '.')
	if idx < 0 {
		return nil
	}
	payloadB64, sig := token[:idx], token[idx+1:]
	if !hmac.Equal([]byte(s.sign([]byte(payloadB64))), []byte(sig)) {
		return nil
	}
	raw, err := base64.URLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil
	}
	var data Claims
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	if exp, ok := data["exp"].(float64); ok && float64(time.Now().Unix()) > exp {
		return nil
	}
	return data
}

func (s Signer) sign(payloadB64 []byte) string {
	mac := hmac.New(sha256.New, s.Secret)
	mac.Write(payloadB64)
	return hex.EncodeToString(mac.Sum(nil))
}
