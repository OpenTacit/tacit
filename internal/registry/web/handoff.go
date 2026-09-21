// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

// Device handoff: moving a member key from the browser that accepted an
// invitation to the machine the member actually works on.
//
// The invitation page used to print the key itself, inside a copyable
// `tacit connect --registry … --key <secret>`. That is a long-lived
// credential displayed in a browser, meant to be carried to another machine —
// through a paste buffer, a chat window to oneself, or an agent transcript,
// all of which keep it. On a phone it could not be used at all: the member was
// holding a secret for a computer they were not sitting at.
//
// So the page prints a code instead. The code is short enough to retype,
// dies in fifteen minutes, and works once. The key it stands for never leaves
// this process until the machine asks for it.
//
// The parked secret lives in memory and not in the store, deliberately. A
// member key at rest is a member key to protect; this one is only ever minutes
// old, and a registry restart inside the window costs the member one click on
// a page they are already signed into. Nothing in the store means nothing to
// migrate across the file, SQLite and Postgres backends.

import (
	"crypto/rand"
	"strings"
	"time"
)

// handoffTTL is how long a code stands. Long enough to walk to the other desk,
// short enough that a code read over someone's shoulder is worth little.
const handoffTTL = 15 * time.Minute

// handoffAlphabet omits I, O, 0 and 1: a code gets retyped from a phone screen
// onto a keyboard, and those four are where that goes wrong.
const handoffAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// handoffCodeLen is twelve characters over a 32-symbol alphabet: sixty bits,
// which no one is guessing inside fifteen minutes.
const handoffCodeLen = 12

type handoffEntry struct {
	secret  string
	base    string
	expires time.Time
}

// newHandoff parks a freshly minted member key under a code and returns it.
func (s *Server) newHandoff(secret, base string) string {
	code := newHandoffCode()
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	if s.handoffs == nil {
		s.handoffs = map[string]handoffEntry{}
	}
	s.sweepHandoffsLocked()
	s.handoffs[code] = handoffEntry{secret: secret, base: base, expires: time.Now().Add(handoffTTL)}
	return code
}

// redeemHandoff hands the key over exactly once. The second caller with the
// same code gets nothing, which is the point of a code rather than a key.
func (s *Server) redeemHandoff(code string) (secret, base string, ok bool) {
	code = normalizeHandoffCode(code)
	s.handoffMu.Lock()
	defer s.handoffMu.Unlock()
	s.sweepHandoffsLocked()
	e, found := s.handoffs[code]
	if !found {
		return "", "", false
	}
	delete(s.handoffs, code)
	return e.secret, e.base, true
}

// sweepHandoffsLocked drops what has expired. Callers hold s.handoffMu.
//
// Swept on every access rather than on a timer: the map holds one entry per
// invitation accepted in the last quarter hour, so it is small by construction
// and a background goroutine would be more machinery than the problem.
func (s *Server) sweepHandoffsLocked() {
	now := time.Now()
	for code, e := range s.handoffs {
		if now.After(e.expires) {
			delete(s.handoffs, code)
		}
	}
}

// newHandoffCode returns a code in XXXX-XXXX-XXXX form.
func newHandoffCode() string {
	buf := make([]byte, handoffCodeLen)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail in practice, and a code that is not random
		// must never be issued, so take the process down rather than hand out
		// a guessable one.
		panic("handoff code: " + err.Error())
	}
	var b strings.Builder
	for i, v := range buf {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(handoffAlphabet[int(v)%len(handoffAlphabet)])
	}
	return b.String()
}

// normalizeHandoffCode accepts what a member actually types: any case, with or
// without the dashes that only exist to make the thing readable.
func normalizeHandoffCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if strings.ContainsRune(handoffAlphabet, r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) != handoffCodeLen {
		return out
	}
	return out[:4] + "-" + out[4:8] + "-" + out[8:]
}
