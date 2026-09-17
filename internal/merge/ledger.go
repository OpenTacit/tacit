// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LedgerName is the file, kept beside the personal registry's own settings.
const LedgerName = "merged.json"

// Sent records one technique's crossing: where it went and what it became.
type Sent struct {
	Destination string `json:"destination"`
	DraftID     string `json:"draft_id"`
	SentAt      string `json:"sent_at"`
}

// Ledger is what a merge remembers between runs.
//
// It exists because of one property of the destination: UniqueID never
// overwrites. An id already taken becomes "-2", so a second contribute of the
// same technique does not fail, does not merge, and does not warn — it creates
// a second copy, and the reviewer sees two of everything with no way to tell
// which is which. Recording what went is therefore not bookkeeping; it is the
// only thing that makes running the command twice safe.
//
// Which in turn is what makes `tacit merge` resumable rather than atomic. It
// cannot be atomic: it POSTs over a network to a service that has already
// stored what it accepted. So it is made repeatable instead, and every step
// after the contributions is ordered to survive being run again.
//
// The destination's key lives here too, for the same reason. A join token is
// spent by the exchange that redeems it, so a run interrupted after the
// exchange could not otherwise be resumed without asking a colleague for a
// second invitation.
type Ledger struct {
	Destination string `json:"destination,omitempty"`
	Key         string `json:"key,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	// RetiredAt marks the instance as finished — merged, stopped, address handed
	// back. The next run sweeps the profile aside rather than starting the same
	// registry again (retire.go).
	RetiredAt   string          `json:"retired_at,omitempty"`
	ArchivePath string          `json:"archive_path,omitempty"`
	Sent        map[string]Sent `json:"sent,omitempty"`
	// Held is what the member chose NOT to contribute, by local technique id
	// (docs/design/browser-led-team-transition.md, M8). A third state, and
	// it has to be recorded: without it a technique deliberately left behind is
	// indistinguishable from one that has not gone yet, and the selection table
	// offers it again every time the member opens it.
	//
	// Held is not a refusal by the destination and not a decision that sticks
	// forever — the table shows these, unticked, so a member can change their
	// mind — it is only the reason they are not ticked by default.
	Held map[string]string `json:"held,omitempty"`

	path string
}

// LedgerPath is where a personal profile keeps its ledger: beside registry.env,
// so it belongs to that registry and not to the machine.
func LedgerPath(registryEnvPath string) string {
	return filepath.Join(filepath.Dir(registryEnvPath), LedgerName)
}

// OpenLedger reads the ledger at path, returning an empty one when there is
// none. A ledger that cannot be parsed is an error rather than a fresh start:
// treating it as empty would re-send everything it recorded.
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path, Sent: map[string]Sent{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, l); err != nil {
		return nil, err
	}
	l.path = path
	if l.Sent == nil {
		l.Sent = map[string]Sent{}
	}
	return l, nil
}

// AlreadySent reports that this technique has gone to this destination. The
// destination is part of the question: a member who merges into a second
// organization is contributing, not re-contributing.
func (l *Ledger) AlreadySent(techniqueID, destination string) (Sent, bool) {
	s, ok := l.Sent[techniqueID]
	return s, ok && s.Destination == destination
}

// Record writes one crossing and flushes immediately.
//
// Immediately, and not at the end of the run: the failure this guards against
// is the process dying mid-merge, and a ledger held in memory until a clean
// exit would be empty in exactly that case.
func (l *Ledger) Record(techniqueID, destination, draftID string) error {
	l.Sent[techniqueID] = Sent{Destination: destination, DraftID: draftID,
		SentAt: time.Now().UTC().Format(time.RFC3339)}
	return l.Save()
}

// Hold records that the member kept a technique back, and flushes. Recording a
// hold before the run rather than after it means a run interrupted half way
// still knows what it was not going to send.
func (l *Ledger) Hold(techniqueIDs []string) error {
	if l.Held == nil {
		l.Held = map[string]string{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range techniqueIDs {
		l.Held[id] = now
	}
	return l.Save()
}

// Release forgets a hold, for a technique the member has now ticked.
func (l *Ledger) Release(techniqueIDs []string) {
	for _, id := range techniqueIDs {
		delete(l.Held, id)
	}
}

// Save writes the ledger. 0600 because it holds the destination's member key.
func (l *Ledger) Save() error {
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(l.path, append(raw, '\n'), 0o600)
}

// SentTo lists what has gone to one destination, by local technique id. It is
// what the personal dashboard reads to answer "did any of my work land".
func (l *Ledger) SentTo(destination string) map[string]Sent {
	out := map[string]Sent{}
	for id, s := range l.Sent {
		if s.Destination == destination {
			out[id] = s
		}
	}
	return out
}

// IDs are the local technique ids in the ledger, sorted, for stable reporting.
func (l *Ledger) IDs() []string {
	out := make([]string, 0, len(l.Sent))
	for id := range l.Sent {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
