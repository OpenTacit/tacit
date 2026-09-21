// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The instance key is the registry's own identity, and it is the whole of the
// enrollment story.
//
// A registry generates it once — at `tacit init`, or the first time publishing
// is switched on — and keeps it for the rest of its life. The ingress stores
// only its hash, so the file on the registry's disk is the sole copy: whoever
// holds it is that instance, and nobody, including the ingress operator, can
// issue or re-issue one. That is what makes connecting self-service. There is
// no name to request, no token to be vended, and no conversation to have.
//
// The first time a key is presented, the ingress enrols it and allocates a
// hostname. Every time after, the same key resolves to the same hostname —
// which is what makes the address stable across restarts, reconnects and
// address changes, and stable is what OAuth clients require.

// KeyPrefix marks an instance key in a log or a config file, so one that leaks
// into either is recognisable as a secret.
const KeyPrefix = "tik_"

// KeyFileName is the file the key lives in, beside registry.env.
const KeyFileName = "instance.key"

// NewKey returns a fresh instance key.
func NewKey() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// KeyPath is where a registry keeps its key, given its config directory.
func KeyPath(configDir string) string { return filepath.Join(configDir, KeyFileName) }

// LoadKey reads an existing key. It returns os.ErrNotExist when there is none,
// which callers use to decide whether this registry has ever had an identity.
func LoadKey(configDir string) (string, error) {
	b, err := os.ReadFile(KeyPath(configDir))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if !strings.HasPrefix(key, KeyPrefix) {
		return "", errors.New("instance key file does not contain an instance key")
	}
	return key, nil
}

// LoadOrCreateKey returns this registry's key, generating and saving one the
// first time. Writing it 0600 matters: it is a bearer credential for the
// registry's public name.
func LoadOrCreateKey(configDir string) (string, error) {
	if key, err := LoadKey(configDir); err == nil {
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", err
	}
	key := NewKey()
	if err := os.WriteFile(KeyPath(configDir), []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return key, nil
}

// FingerprintOf is the short, non-secret form of a key — what a console or a
// log line can show to identify an instance without being able to become it.
func FingerprintOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// newSessionSecret is the HMAC key for console sessions when the operator has
// not set one. Same entropy as an instance key; different purpose, so it does
// not carry the instance-key prefix.
func newSessionSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
