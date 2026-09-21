// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// The sealed usage ledger: where a member's own numbers live when the registry
// they belong to runs somewhere other than their machine.
//
// The Usage page can only answer "how did my week go" when the registry and the
// agent share a host (usage.go, TACIT_LOCAL_USAGE). For everybody else the
// honest answer was a handoff to their own machine, which is no answer at all
// from a phone. This file is the other half: each of a member's machines seals
// its summaries and files them here, and the member's browser opens them.
//
// What the registry holds is ciphertext, an address it cannot invert, and a
// timestamp. It cannot read a blob, and no endpoint lists what addresses exist,
// so it cannot enumerate them either. That is the whole of the carve-out from
// principle 2 (docs/dev/01-principles.md, docs/delivery/out-of-band-plan.md):
// the row exists, and the organization still cannot see the individual.
//
// Two credentials, deliberately doing different jobs:
//
//   - The member KEY decides what can be READ. It never reaches the server, so
//     an operator with disk access, a backup, or a subpoena gets bytes.
//   - The signed-in SESSION decides what can be FOUND. A member who has bound
//     their address once is told it again on any device they sign in from, so
//     the second device needs the key and not a copied URL.
//
// The binding is stored under a hash of the subject rather than the subject, so
// the file is a set of pairs that answer "is this person bound, and to what"
// without listing who has an account here. Losing it costs a member one
// re-entry of their key; it is a convenience index, never the authority.
package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/opentacit/tacit/internal/fsx"
	"github.com/opentacit/tacit/internal/ledger"
)

const (
	// maxSealedBlob bounds one machine's sealed summaries. The payload is two
	// JSON summaries over four windows — kilobytes — so this is loose enough to
	// never bite honestly and tight enough that the ledger cannot become
	// somebody's file store.
	maxSealedBlob = 512 << 10
	// maxMachinesPerLedger bounds how many slots one address may hold. A member
	// with more machines than this has a runaway `tacit connect`, not a
	// workstation habit.
	maxMachinesPerLedger = 32
	// ledgerStale is when an unwritten slot stops being served. A machine that
	// has not published in this long is a machine the member has stopped using,
	// and folding its last week into "your week" forever would quietly inflate
	// every figure on the page.
	ledgerStale = 90 * 24 * time.Hour
)

func (s *Server) ledgerDir() string { return filepath.Join(s.cfg().DataDir, "ledger") }

// handleLedgerPut takes one machine's sealed summaries. It is key-authed, like
// the rest of /v1: any member key opens it, because a member key is exactly
// what proves the caller is one of this registry's machines. It cannot be
// narrowed to "the machine that owns this address" — the address is derived
// from a secret the registry does not have, which is the point.
//
// What stops one member writing over another's slot is that they would have to
// guess a 256-bit address first, and what stops that being catastrophic is that
// they still could not read what is there.
func (s *Server) handleLedgerPut(w http.ResponseWriter, r *http.Request) {
	id, machine := r.PathValue("id"), r.PathValue("machine")
	if !ledger.ValidID(id) || !ledger.ValidMachine(machine) {
		s.sendError(w, http.StatusBadRequest, "malformed ledger address")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSealedBlob+1))
	if err != nil {
		s.sendError(w, http.StatusBadRequest, "read error")
		return
	}
	if len(body) == 0 {
		s.sendError(w, http.StatusBadRequest, "empty blob")
		return
	}
	if len(body) > maxSealedBlob {
		s.sendError(w, http.StatusRequestEntityTooLarge, "sealed blob is too large")
		return
	}
	dir := filepath.Join(s.ledgerDir(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.sendError(w, http.StatusInternalServerError, "cannot open the ledger")
		return
	}
	// The cap counts slots that do not yet exist for this machine: a machine
	// re-publishing is always allowed, however full the ledger is, because
	// refusing it would freeze a member's own numbers at whatever they were.
	target := filepath.Join(dir, machine+".blob")
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if entries, _ := os.ReadDir(dir); len(entries) >= maxMachinesPerLedger {
			s.sendError(w, http.StatusConflict, "this ledger already holds the most machines it will")
			return
		}
	}
	if err := fsx.WriteFileAtomic(target, body, 0o600); err != nil {
		s.sendError(w, http.StatusInternalServerError, "cannot write the ledger")
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]any{"ok": true, "bytes": len(body)})
}

// sealedSlot is one machine's contribution, as the browser receives it.
type sealedSlot struct {
	Machine string `json:"machine"`
	Sealed  string `json:"sealed"` // base64; opaque here and everywhere on this side
	Updated string `json:"updated"`
}

// handleLedgerGet returns every slot at one address. It needs a signed-in
// reader where there is sign-in at all — not because the bytes need protecting,
// which is what the sealing is for, but because an open endpoint that answers
// "does anything exist at this address" is a probe worth denying for free.
func (s *Server) handleLedgerGet(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrJSON(w, r) {
		return
	}
	id := r.PathValue("id")
	if !ledger.ValidID(id) {
		s.sendError(w, http.StatusBadRequest, "malformed ledger address")
		return
	}
	entries, err := os.ReadDir(filepath.Join(s.ledgerDir(), id))
	if err != nil {
		// An address with nothing at it is not an error and must not read as
		// one: a member who has just entered their key on a new device is in
		// exactly this state until a machine of theirs next publishes.
		s.sendJSON(w, http.StatusOK, map[string]any{"machines": []sealedSlot{}})
		return
	}
	cutoff := time.Now().Add(-ledgerStale)
	slots := make([]sealedSlot, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".blob" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.ledgerDir(), id, e.Name()))
		if err != nil {
			continue
		}
		slots = append(slots, sealedSlot{
			Machine: e.Name()[:len(e.Name())-len(".blob")],
			Sealed:  base64.StdEncoding.EncodeToString(raw),
			Updated: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	// Newest first, so a browser that decides to render only one machine
	// renders the one the member was last working on.
	sort.Slice(slots, func(i, j int) bool { return slots[i].Updated > slots[j].Updated })
	s.sendJSON(w, http.StatusOK, map[string]any{"machines": slots})
}

// bindingsPath is the convenience index: hashed subject -> address.
func (s *Server) bindingsPath() string { return filepath.Join(s.ledgerDir(), "bindings.json") }

// subjectKey is how a signed-in reader is written down here. Hashing the
// subject means this file can answer "is this person bound" for the person in
// front of it and nothing else — it is not a member list, and it cannot be read
// as one.
func subjectKey(user map[string]any) string {
	sub, _ := user["sub"].(string)
	if sub == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("tacit-ledger-binding\x00" + sub))
	return hex.EncodeToString(sum[:])
}

func (s *Server) readBindings() map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(s.bindingsPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// handleLedgerBind records which address the signed-in member reads, so their
// next device asks instead of being told. The key itself is never sent here and
// this endpoint could not use it if it were.
func (s *Server) handleLedgerBind(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrJSON(w, r) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&body); err != nil {
		s.sendError(w, http.StatusBadRequest, "expected {\"id\": \"...\"}")
		return
	}
	if !ledger.ValidID(body.ID) {
		s.sendError(w, http.StatusBadRequest, "malformed ledger address")
		return
	}
	key := subjectKey(s.sessionUser(r))
	if key == "" {
		// No subject to file under. On a registry with no sign-in there is one
		// reader and nothing to disambiguate, so the browser keeps its own
		// address locally and this is not a failure.
		s.sendJSON(w, http.StatusOK, map[string]any{"bound": false, "reason": "no signed-in subject"})
		return
	}
	if err := os.MkdirAll(s.ledgerDir(), 0o700); err != nil {
		s.sendError(w, http.StatusInternalServerError, "cannot open the ledger")
		return
	}
	bindings := s.readBindings()
	bindings[key] = body.ID
	if err := fsx.WriteJSONAtomic(s.bindingsPath(), bindings, 0o600); err != nil {
		s.sendError(w, http.StatusInternalServerError, "cannot record the binding")
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]any{"bound": true})
}

// handleLedgerMine answers "which address is mine" for a signed-in reader, and
// says nothing about anybody else's.
func (s *Server) handleLedgerMine(w http.ResponseWriter, r *http.Request) {
	if !s.signedInOrJSON(w, r) {
		return
	}
	key := subjectKey(s.sessionUser(r))
	if key == "" {
		s.sendJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if id, ok := s.readBindings()[key]; ok {
		s.sendJSON(w, http.StatusOK, map[string]any{"id": id})
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]any{})
}
