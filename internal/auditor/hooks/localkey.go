// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The key the member's own session log files a session under.
//
// It used to be the ORG salt, and on a machine without one — which is most
// machines, because the org salt is optional and has no default — every record
// was dropped on the floor. sessionhash.Hash returns "" for an empty salt, by
// design, and upsert refuses a record with no key. So the Usage page was empty
// and nothing anywhere said why.
//
// The two uses had been conflated. An org salt is essential for the audit
// facts that LEAVE the machine: it is what lets the registry count one member's
// sessions without learning whose, and an empty one must produce no pseudonym
// at all rather than a weak one. The session log leaves nothing. Its key has
// one job — tell two local sessions apart without naming either — and a salt
// that never leaves the machine does that job completely.
//
// So the org salt still wins where it is set, which keeps a machine's existing
// records readable and keeps one member's local keys matching the ones their
// org-scoped facts carried. Where it is not set, a salt is made once per
// machine and kept beside the log it keys.
package hooks

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// localSaltFile is where the per-machine salt lives: beside the log it keys,
// so deleting the state directory deletes both and a member who clears their
// history does not leave a key to it behind.
const localSaltFile = "session-key-salt"

// resolveLocalSalt returns the salt the session log should key on: the org's
// where there is one, and this machine's own otherwise. An unwritable state
// directory falls back to the salt held in memory for the life of the process,
// which keeps a session's own records consistent even when nothing persists.
func resolveLocalSalt(orgSalt, stateDir string) string {
	if s := strings.TrimSpace(orgSalt); s != "" {
		return s
	}
	fresh := newSalt()
	if stateDir == "" {
		return fresh
	}
	path := filepath.Join(stateDir, localSaltFile)
	if raw, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(raw)); s != "" {
			return s
		}
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fresh
	}
	// 0600 and never uploaded: it is not a secret about the member, but it is
	// the key to their own history and it has no business being readable.
	if err := os.WriteFile(path, []byte(fresh+"\n"), 0o600); err != nil {
		return fresh
	}
	return fresh
}

func newSalt() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// A machine whose randomness is broken still gets a working log. The
		// salt is not a secret about anybody; it only has to differ between
		// machines, and a fixed one costs nothing but that.
		return "tacit-local-session-key"
	}
	return hex.EncodeToString(b)
}
