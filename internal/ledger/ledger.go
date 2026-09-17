// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package ledger is the sealing layer under a member's own usage: the one
// place a machine's summaries are encrypted, and the one place the address they
// are filed under is computed.
//
// It exists because the goal — a member reads their own usage through the
// registry UI, from any device, wherever the usage happened — cannot be met
// member-side. Two of a member's machines never meet, and a chat client on
// /mcp has no machine at all, so the record has to pass through the registry.
// Principle 2 says the registry stores no member identity and no prose
// (docs/dev/01-principles.md), and the narrow way to keep that guarantee while
// letting the row exist is to make the row unreadable to the server
// (docs/delivery/out-of-band-plan.md).
//
// Two derived values, from one secret the member already has:
//
//   - The KEY seals the payload. Only something holding the member's own
//     credential can open a blob, which means not the registry, not its
//     operator, and not a backup of its disk.
//   - The ID addresses it. It is derived rather than chosen so that every
//     machine a member joined files to the same place without being told where,
//     and so the registry can group a member's blobs without learning whose
//     they are.
//
// Both come out of HKDF over the member key, which is high-entropy random
// (web.NewAPIKey) rather than a human-chosen password — so an extract-and-expand
// KDF is the right tool and there is no iteration count to defend. Different
// info strings keep the two outputs independent: the id is published in a URL,
// and it must say nothing about the key that opens what it points at.
//
// The sealed bytes are AES-256-GCM with a random nonce prepended. Everything
// here is standard library, and everything here has a WebCrypto equivalent,
// because the other implementation of this file is thirty lines of JavaScript
// in the browser that has to agree with it exactly.
package ledger

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

// Version is the derivation domain. It rides in both info strings, so changing
// it changes the key AND the address together — a member whose blobs were
// written under an older version reads nothing rather than garbage, and the
// upgrade is a re-publish rather than a migration.
const Version = "tacit-ledger-v1"

// KeySize is the AES-256 key length. IDSize is the address length in bytes
// before hex, wide enough that the address is unguessable on its own: it is the
// only thing standing in front of an unreadable blob, and the whole point of
// this design is that layers below it do not depend on that.
const (
	KeySize = 32
	IDSize  = 32
)

// ErrKeyRequired is what an empty member secret produces. It is a distinct
// error because the caller's remedy is specific — connect this machine to a
// registry — and "invalid key" would send them looking at the key they have
// rather than at the one they do not.
var ErrKeyRequired = errors.New("no member key on this machine: run tacit connect")

// Derive turns a member's credential into the pair of values everything else
// here takes. The secret never leaves the caller.
func Derive(memberSecret string) (key []byte, id string, err error) {
	if memberSecret == "" {
		return nil, "", ErrKeyRequired
	}
	// No salt. HKDF's salt defends against low-entropy input, and the input
	// here is a 256-bit random credential; a fixed salt would be theatre and a
	// random one could not be recomputed on another machine, which is the
	// property that makes a member's laptops agree without coordinating.
	key, err = hkdf.Key(sha256.New, []byte(memberSecret), nil, Version+" key", KeySize)
	if err != nil {
		return nil, "", fmt.Errorf("derive key: %w", err)
	}
	raw, err := hkdf.Key(sha256.New, []byte(memberSecret), nil, Version+" id", IDSize)
	if err != nil {
		return nil, "", fmt.Errorf("derive id: %w", err)
	}
	return key, hex.EncodeToString(raw), nil
}

// idPattern is what a URL path segment must look like to be an address here.
// The registry validates against it before touching disk, so a path segment can
// never become a directory traversal or an unbounded filename.
var idPattern = regexp.MustCompile(`^[0-9a-f]{` + fmt.Sprint(IDSize*2) + `}$`)

// ValidID reports whether s is a well-formed ledger address. It says nothing
// about whether anything is filed there.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// machinePattern bounds the second path segment: a machine label the member
// chose, reduced to something safe to be a filename. Labels come from
// `tacit connect`, so they are already tame; this is the check that keeps them
// that way when they arrive over HTTP.
var machinePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidMachine reports whether s is usable as the per-machine slot within a
// ledger. One slot per machine is what makes the merge in the browser additive:
// a laptop re-publishing replaces its own slot and touches nobody else's.
func ValidMachine(s string) bool { return machinePattern.MatchString(s) }

// Seal encrypts plaintext under key. The nonce is random per call and prepended
// to the ciphertext, which is the layout the browser's open() expects.
func Seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. It exists for the CLI and for tests; the browser is the
// caller that matters and it has its own implementation.
func Open(key, blob []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("sealed blob is too short to carry a nonce")
	}
	nonce, body := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	out, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		// Deliberately unspecific: a caller cannot tell a wrong key from a
		// corrupt blob, and neither can anybody probing with a guessed one.
		return nil, errors.New("cannot open: wrong key or damaged blob")
	}
	return out, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cipher: %w", err)
	}
	return cipher.NewGCM(block)
}
