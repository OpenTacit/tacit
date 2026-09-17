// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"crypto/rand"
	"encoding/hex"
)

// newID is the same five lines as models.NewID on the registry side, and stays
// separate on purpose: the auditor imports nothing from internal/registry, so
// sharing it would mean the hook agent linking the registry to format an id.
func newID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
