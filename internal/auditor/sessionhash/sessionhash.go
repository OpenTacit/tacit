// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package sessionhash creates unlinkable, org-scoped session pseudonyms.
package sessionhash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the first 16 bytes of SHA-256(salt NUL key), hex encoded.
// Missing configuration or identity deliberately produces no pseudonym.
func Hash(salt, key string) string {
	if salt == "" || key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(salt + "\x00" + key))
	return hex.EncodeToString(sum[:16])
}
